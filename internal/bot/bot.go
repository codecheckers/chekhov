package bot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/announce"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/mastodon"
	"github.com/codecheckers/chekhov/internal/rules"
)

// commandTimeout bounds one command. GitHub gives a webhook 10 seconds to be
// answered, which is why the work happens after the answer; a check that asks
// Crossref and Zenodo can take a minute, and one that hangs must not pile up.
const commandTimeout = 5 * time.Minute

// Poster is the part of the reply path the bot needs, so a test can watch what
// would be posted without a server.
type Poster interface {
	Comment(ctx context.Context, repository string, issue int, body string) (int64, error)
}

// Toots is the part of the Mastodon client announcing and following need, so
// a test can watch what would be tooted, followed or listed without an
// instance.
type Toots interface {
	Post(ctx context.Context, status mastodon.Status) (mastodon.Posted, error)
	UploadMedia(ctx context.Context, name, mimeType string, content []byte, description string) (string, error)
	RecentStatuses(ctx context.Context, limit int) ([]mastodon.Posted, error)
	Limits(ctx context.Context) (mastodon.Limits, error)
	VerifyAccount(ctx context.Context) error
	ResolveAccount(ctx context.Context, handle string) (mastodon.Account, error)
	Relationships(ctx context.Context, accountIDs []string) ([]mastodon.Relationship, error)
	Follow(ctx context.Context, accountID string) error
	Lists(ctx context.Context) ([]mastodon.List, error)
	CreateList(ctx context.Context, title string) (mastodon.List, error)
	ListAccounts(ctx context.Context, listID string) ([]mastodon.Account, error)
	AddToList(ctx context.Context, listID string, accountIDs []string) error
}

// Server is the whole bot: one webhook endpoint, one health endpoint, no state.
type Server struct {
	Settings   *config.Settings
	Secret     string
	Replies    Poster
	Deployment command.Deployment
	Logger     *slog.Logger

	// Services lets the check command reach Crossref, ORCID, Zenodo and the
	// register. Nil keeps the bot offline, which is how the tests run it.
	Services *check.Services

	// Toots is where announcements are posted. Nil means announcing is
	// switched off for this deployment: the preview still works, confirm
	// does not.
	Toots Toots

	// LocalPaths allows a command to name a file on this machine. It is off
	// for a deployment on purpose: a path in a comment is written by anyone on
	// the internet, and the bot's disk is none of their business. The command
	// line preview turns it on, because there the path is the user's own.
	LocalPaths bool

	// done is closed when a background command finishes, for the tests.
	done chan struct{}
}

// New builds a server from the settings and a reply path.
func New(settings *config.Settings, secret string, replies Poster, deployment command.Deployment) *Server {
	return &Server{
		Settings:   settings,
		Secret:     secret,
		Replies:    replies,
		Deployment: deployment,
		Logger:     slog.Default(),
	}
}

// Handler is the bot's HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /dispatch", s.dispatch)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/codecheckers/chekhov", http.StatusFound)
	})
	return mux
}

// dispatch receives a webhook delivery.
//
// The order matters: verify, then read. A delivery that is not signed by the
// secret is not the bot's business, and its body is not parsed at all.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "could not read the delivery", http.StatusBadRequest)
		return
	}

	if err := verifySignature(s.Secret, r.Header.Get("X-Hub-Signature-256"), body); err != nil {
		s.Logger.Warn("a delivery was refused", "delivery", r.Header.Get("X-GitHub-Delivery"), "error", err)
		http.Error(w, "signature check failed", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	event, handled, err := parseEvent(eventType, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !handled {
		// Not an error: GitHub sends a ping on setup, and whatever else the
		// webhook is subscribed to.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if reason := s.refuse(event); reason != "" {
		s.Logger.Warn("a delivery was not acted on", "reason", reason,
			"repository", event.Repository, "issue", event.Issue, "author", event.Author)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	parsed, addressed := command.Parse(event.Body)
	if !addressed {
		// The common case on a busy issue, and it has to stay cheap and silent.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Answer now, work afterwards.
	w.WriteHeader(http.StatusAccepted)
	go s.act(event, parsed)
}

// refuse says why a delivery is not acted on, or returns an empty string.
func (s *Server) refuse(event mention) string {
	switch {
	case event.Repository != s.Settings.TargetRepository():
		// A misconfigured webhook must not reach the production register.
		return fmt.Sprintf("this bot works on %s", s.Settings.TargetRepository())
	case strings.EqualFold(event.Author, s.Settings.BotUser()):
		// Otherwise a reply is a comment, and a comment gets a reply.
		return "the comment is the bot's own"
	case event.Issue <= 0:
		return "the event names no issue"
	default:
		return ""
	}
}

// act runs one command and posts the answer.
func (s *Server) act(event mention, parsed command.Command) {
	defer func() {
		if s.done != nil {
			s.done <- struct{}{}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	started := time.Now()
	body := s.answer(ctx, event, parsed)
	if body == "" {
		return
	}

	id, err := s.Replies.Comment(ctx, event.Repository, event.Issue, body)
	if err != nil {
		s.Logger.Error("the answer could not be posted", "command", parsed.Name,
			"issue", event.Issue, "error", err)
		return
	}
	s.Logger.Info("answered", "command", parsed.Name, "issue", event.Issue,
		"author", event.Author, "comment", id, "took", time.Since(started))
}

// answer produces the reply to one command.
func (s *Server) answer(ctx context.Context, event mention, parsed command.Command) string {
	role := command.RoleAnyone
	if s.Settings.IsEditor(event.Author) {
		role = command.RoleEditor
	}

	if definition, found := command.Lookup(string(parsed.Name)); found && !definition.Permits(role) {
		return fmt.Sprintf("`%s %s` is for editors.\n", command.Bot, parsed.Name)
	}

	// Every command reads the world afresh. A deployment keeps one Services for
	// its whole life, and a response cached for it would answer a check, or
	// compose an announcement, from what was published hours ago.
	services := s.Services.Fresh()

	switch parsed.Name {
	case command.Commands:
		return command.Listing(role)
	case command.Hello:
		return s.Deployment.HelloReply()
	case command.Version:
		return s.version()
	case command.Check:
		return s.check(parsed, services)
	case command.Announce:
		return s.announce(ctx, parsed, services)
	case command.Follow:
		return s.follow(ctx, parsed, services)
	default:
		return command.UnknownReply(parsed)
	}
}

// version says which build is answering and which catalogue it judges by.
func (s *Server) version() string {
	commit, retrieved := "", ""
	if provenance, err := rules.Provenance(); err == nil {
		commit, retrieved = provenance.Commit(), provenance.Retrieved
	}
	return s.Deployment.VersionReply(commit, retrieved)
}

// check validates a codecheck.yml named in the comment.
//
// The target may be written however a person finds natural - a repository
// spec, a shortcut, or a certificate identifier the register knows - and
// check.ResolveTarget decides which. Working out from the issue alone which
// configuration it is about is still to come.
func (s *Server) check(parsed command.Command, services *check.Services) string {
	part, target := "", ""
	for _, argument := range parsed.Args {
		// A part name is never a target: the catalogue's own words come first,
		// and everything else is what to read.
		if argument == "" {
			continue
		}
		if check.IsPart(argument) {
			part = argument
		} else if target == "" {
			// The first target wins. A comment is prose, and "check 2020-001
			// please" must not be read as a request to check "please".
			target = argument
		}
	}
	if target == "" {
		return fmt.Sprintf("I need to be told what to check:\n\n"+
			"    %s check codecheckers/repository\n"+
			"    %s check 2020-001\n\n"+
			"A repository may also be named the way `register.csv` does, "+
			"`github::owner/repo`, with `|sub/dir` when the configuration is not at the root.\n",
			command.Bot, command.Bot)
	}

	context, err := s.read(target, services)
	if err != nil {
		return fmt.Sprintf("I could not read `%s`: %s\n", target, err)
	}
	report, err := check.RunPart(context, "", false, part)
	if err != nil {
		return fmt.Sprintf("%s\n", err)
	}
	return report.Markdown()
}

// recentToots is how many of the account's own toots, boosts left out, are
// searched for an earlier announcement: the most Mastodon returns at once.
const recentToots = 40

// announce previews, or with "confirm" posts, the toot about a published
// certificate. The bot keeps no state, so confirm composes the toot again from
// what is published now rather than posting what the preview showed.
func (s *Server) announce(ctx context.Context, parsed command.Command, services *check.Services) string {
	certificate, confirm := parseCertificateAndConfirm(parsed.Args)
	if certificate == "" {
		return fmt.Sprintf("I need the certificate to announce:\n\n    %s announce 2020-001\n", command.Bot)
	}
	if !services.Enabled() {
		return "Announcing reads the published certificate, and this bot is not online.\n"
	}
	settings := s.Settings.Mastodon()

	published, directory, err := announce.Load(services, s.Settings, certificate)
	if err != nil {
		return fmt.Sprintf("I could not read certificate %s: %s\n", certificate, err)
	}

	limits, imageLimit := announce.DefaultLimits, int64(announce.DefaultImageLimit)
	if s.Toots != nil {
		instance, err := s.Toots.Limits(ctx)
		if err != nil {
			return fmt.Sprintf("I could not ask %s what it allows: %s\n", settings.Instance, err)
		}
		limits, imageLimit = announce.LimitsFor(instance), instance.ImageSizeLimit
	}

	toot, err := announce.Compose(published, directory, limits, settings.Visibility)
	if err != nil {
		return fmt.Sprintf("I could not compose the toot for certificate %s: %s\n", certificate, err)
	}
	preview := command.Announcement{
		Certificate: certificate, Text: toot.Text, Visibility: settings.Visibility, Defused: toot.Defused,
		Mentioned: toot.Mentioned, Unmatched: toot.Unmatched, Enabled: s.Toots != nil,
	}

	if s.Toots != nil {
		statuses, err := s.Toots.RecentStatuses(ctx, recentToots)
		if err != nil {
			return fmt.Sprintf("I could not read the account's recent toots, so I cannot tell whether %s was announced: %s\n",
				certificate, err)
		}
		if preview.AnnouncedAt, _ = announce.Announced(statuses, published); preview.AnnouncedAt != "" {
			// Nothing more to build: the answer is that it was done already.
			return command.AnnouncePreview(preview)
		}
	}

	attachment, err := announce.GIF(services, published, imageLimit)
	if err != nil {
		preview.AttachmentProblem = err.Error()
	} else {
		preview.Frames, preview.Bytes = attachment.Frames, len(attachment.GIF)
	}

	if !confirm || s.Toots == nil || preview.Frames == 0 {
		// A confirm that cannot post answers with the preview, which says why.
		return command.AnnouncePreview(preview)
	}

	media, err := s.Toots.UploadMedia(ctx, "codecheck-"+certificate+".gif", "image/gif", attachment.GIF,
		fmt.Sprintf("The first pages of CODECHECK certificate %s", certificate))
	if err != nil {
		return fmt.Sprintf("The certificate could not be uploaded, nothing was posted: %s\n", err)
	}
	digest := sha256.Sum256([]byte(toot.Text))
	posted, err := s.Toots.Post(ctx, mastodon.Status{
		Text: toot.Text, MediaIDs: []string{media}, Visibility: settings.Visibility,
		// A doubled confirm returns the first toot instead of posting twice.
		// The text is part of the key, so that a corrected toot, posted after
		// the first was deleted, is not answered with the deleted one.
		IdempotencyKey: fmt.Sprintf("codecheck-%s-%x", certificate, digest[:8]),
	})
	if err != nil {
		return fmt.Sprintf("The toot could not be posted: %s\n", err)
	}
	s.Logger.Info("announced", "certificate", certificate, "toot", posted.URL, "visibility", settings.Visibility)
	return command.AnnouncePosted(certificate, posted.URL)
}

// parseCertificateAndConfirm reads "<certificate> [confirm]" out of a
// command's arguments, the shape announce and follow both take.
func parseCertificateAndConfirm(args []string) (certificate string, confirm bool) {
	for _, argument := range args {
		switch {
		case strings.EqualFold(argument, "confirm"):
			confirm = true
		case certificate == "" && check.IsCertificateID(argument):
			certificate = argument
		}
	}
	return certificate, confirm
}

// followLists are the public Mastodon lists follow keeps in sync, in the
// order the reply reports them, each with how to read its members out of a
// certificate's resolved mentions.
var followLists = []struct {
	title   string
	handles func(announce.Mentions) []string
}{
	{command.CodecheckersList, func(m announce.Mentions) []string { return m.Codecheckers }},
	{command.AuthorsList, func(m announce.Mentions) []string { return m.Authors }},
	{command.VenuesList, func(m announce.Mentions) []string {
		if m.Venue == "" {
			return nil
		}
		return []string{m.Venue}
	}},
}

// follow previews, or with "confirm" performs, following a certificate's
// accounts and keeping the Codecheckers/Authors/Venues lists in sync. Like
// announce, the bot keeps no state: every run resolves the certificate and
// the lists afresh.
func (s *Server) follow(ctx context.Context, parsed command.Command, services *check.Services) string {
	certificate, confirm := parseCertificateAndConfirm(parsed.Args)
	if certificate == "" {
		return fmt.Sprintf("I need the certificate whose accounts to follow:\n\n    %s follow 2020-001\n", command.Bot)
	}
	if !services.Enabled() {
		return "Following reads the published certificate, and this bot is not online.\n"
	}

	published, directory, err := announce.Load(services, s.Settings, certificate)
	if err != nil {
		return fmt.Sprintf("I could not read certificate %s: %s\n", certificate, err)
	}
	mentions := announce.Resolve(published, directory)

	reply := command.Following{
		Certificate:  certificate,
		Enabled:      s.followEnabled(),
		Codecheckers: mentions.Codecheckers,
		Authors:      mentions.Authors,
		Venue:        mentions.Venue,
		Unmatched:    mentions.Unmatched,
	}
	if !reply.Enabled {
		return command.FollowPreview(reply)
	}

	handles := append([]string{}, mentions.Codecheckers...)
	handles = append(handles, mentions.Authors...)
	if mentions.Venue != "" {
		handles = append(handles, mentions.Venue)
	}
	handles = dedupeHandles(handles)

	// A handle the directory matched can still fail to resolve on the
	// instance - an account since deleted, suspended or moved - and that is
	// the same kind of "known, but unreachable" outcome Unmatched already
	// reports for a person with no account on file at all, not a reason to
	// abandon the whole preview.
	accounts := map[string]mastodon.Account{}
	var resolved []string
	for _, handle := range handles {
		account, err := s.Toots.ResolveAccount(ctx, handle)
		if err != nil {
			reply.Unresolved = append(reply.Unresolved, handle)
			s.Logger.Warn("could not resolve a mentioned account", "certificate", certificate, "handle", handle, "error", err)
			continue
		}
		accounts[handle] = account
		resolved = append(resolved, handle)
	}
	handles = resolved

	ids := make([]string, 0, len(handles))
	for _, handle := range handles {
		ids = append(ids, accounts[handle].ID)
	}
	relationships, err := s.Toots.Relationships(ctx, ids)
	if err != nil {
		return fmt.Sprintf("I could not check who @%s already follows: %s\n", s.Settings.Mastodon().Account, err)
	}
	following := make(map[string]bool, len(relationships))
	for _, relationship := range relationships {
		following[relationship.ID] = relationship.Following
	}
	for _, handle := range handles {
		if following[accounts[handle].ID] {
			reply.AlreadyFollowed = append(reply.AlreadyFollowed, handle)
		} else {
			reply.NewlyFollowed = append(reply.NewlyFollowed, handle)
		}
	}

	if !confirm {
		return command.FollowPreview(reply)
	}

	// announce reaches this same check through RecentStatuses, which it always
	// calls; follow has no equivalent read to piggyback it on, so it is
	// explicit here, and before the first write - a token of the wrong
	// account must never follow or list a real person in this deployment's
	// name.
	if err := s.Toots.VerifyAccount(ctx); err != nil {
		return fmt.Sprintf("%s\n", err)
	}

	for _, handle := range reply.NewlyFollowed {
		if err := s.Toots.Follow(ctx, accounts[handle].ID); err != nil {
			return fmt.Sprintf("I could not follow %s: %s\n", handle, err)
		}
	}

	lists, err := s.Toots.Lists(ctx)
	if err != nil {
		return fmt.Sprintf("I could not read the account's lists: %s\n", err)
	}
	byTitle := make(map[string]mastodon.List, len(lists))
	for _, list := range lists {
		byTitle[list.Title] = list
	}

	reply.Added = map[string][]string{}
	for _, group := range followLists {
		handles := group.handles(mentions)
		if !anyResolved(handles, accounts) {
			// Nothing in this group resolved to an account, so there is
			// nothing to add - and no reason to create the list for it yet.
			continue
		}

		list, ok := byTitle[group.title]
		if !ok {
			if list, err = s.Toots.CreateList(ctx, group.title); err != nil {
				return fmt.Sprintf("I could not prepare the %s list: %s\n", group.title, err)
			}
			byTitle[group.title] = list
		}
		already, err := s.Toots.ListAccounts(ctx, list.ID)
		if err != nil {
			return fmt.Sprintf("I could not read who is on the %s list already: %s\n", group.title, err)
		}
		listed := make(map[string]bool, len(already))
		for _, account := range already {
			listed[account.ID] = true
		}

		var toAdd []string
		for _, handle := range handles {
			account, ok := accounts[handle]
			if !ok || listed[account.ID] {
				continue
			}
			toAdd = append(toAdd, account.ID)
			reply.Added[group.title] = append(reply.Added[group.title], handle)
		}
		if len(toAdd) > 0 {
			if err := s.Toots.AddToList(ctx, list.ID, toAdd); err != nil {
				return fmt.Sprintf("I could not add to the %s list: %s\n", group.title, err)
			}
		}
	}

	s.Logger.Info("followed", "certificate", certificate, "newly_followed", len(reply.NewlyFollowed))
	return command.FollowPosted(reply)
}

// followEnabled says whether this deployment may follow accounts and manage
// the Codecheckers/Authors/Venues lists at all.
func (s *Server) followEnabled() bool {
	return s.Toots != nil && s.Settings.Mastodon().Follow
}

// anyResolved reports whether at least one handle resolved to an account, so
// a list with nobody to add to it is not created for nothing.
func anyResolved(handles []string, accounts map[string]mastodon.Account) bool {
	for _, handle := range handles {
		if _, ok := accounts[handle]; ok {
			return true
		}
	}
	return false
}

// dedupeHandles keeps the first occurrence of each handle, so an account
// mentioned twice (a codechecker who is also named as an author, say) is
// resolved and followed once.
func dedupeHandles(handles []string) []string {
	seen := make(map[string]bool, len(handles))
	out := make([]string, 0, len(handles))
	for _, handle := range handles {
		if !seen[handle] {
			seen[handle] = true
			out = append(out, handle)
		}
	}
	return out
}

// read loads the configuration a command names. A deployment does not read its
// own disk, so there a target is always a repository; check.Load is the one
// place that decides.
func (s *Server) read(target string, services *check.Services) (check.Context, error) {
	return check.Load(target, s.LocalPaths, services)
}

// health says which bot this is, in enough detail that a development
// deployment cannot be mistaken for the real one.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated, so it says only what a stranger may know: which bot,
	// which register, which build. The rest - the commit, where the rules came
	// from, when the token expires, whether the services are reachable - is
	// for whoever is developing the thing, and is a gift to anyone else.
	state := s.Deployment.Facts()
	state["status"] = "ok"
	if s.Deployment.Development() {
		state["online"] = s.Services.Enabled()
		if provenance, err := rules.Provenance(); err == nil {
			state["rules_commit"] = provenance.Commit()
			state["rules_retrieved"] = provenance.Retrieved
		}
		if client, ok := s.Replies.(*github.Client); ok && client.TokenExpiry() != "" {
			state["token_expires"] = client.TokenExpiry()
		}
		state["announce"] = map[string]any{
			"configured": s.Toots != nil,
			"account":    s.Settings.Mastodon().Account,
			"visibility": s.Settings.Mastodon().Visibility,
			"follow":     s.followEnabled(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(state)
}

// Preview renders what the bot would reply to a comment, without posting
// anything.
//
// It exists so that `chekhov comment` and the deployed bot answer through the
// same code: a reply that can be read on the command line is only worth
// reading if it is the reply that would be posted.
func Preview(settings *config.Settings, version, commit string, services *check.Services, author, body string) (string, bool) {
	parsed, addressed := command.Parse(body)
	if !addressed {
		return "", false
	}

	server := New(settings, "", nil, deploymentFor(settings, version, commit))
	server.Services = services
	// On the command line the path in the comment is the user's own.
	server.LocalPaths = true
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return server.answer(ctx, mention{Author: author}, parsed), true
}
