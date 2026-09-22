package bot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/announce"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/mastodon"
	"github.com/codecheckers/chekhov/internal/people"
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

// Assigner is the part of the reply path that keeps the issue's assignee in
// step with the assigned codechecker. A Poster that cannot assign - the test
// recorder, or a preview - simply does not, and the reply says so.
type Assigner interface {
	Assign(ctx context.Context, repository string, issue int, handle string) ([]string, error)
	Unassign(ctx context.Context, repository string, issue int, handle string) error
}

// Toots is the part of the Mastodon client announcing and following need, so
// a test can watch what would be tooted, followed or listed without an
// instance.
type Toots interface {
	Post(ctx context.Context, status mastodon.Status) (mastodon.Posted, error)
	PostDirect(ctx context.Context, text, idempotencyKey string) (mastodon.Posted, error)
	UploadMedia(ctx context.Context, name, mimeType string, content []byte, description string) (string, error)
	RecentStatuses(ctx context.Context, limit int) ([]mastodon.Posted, error)
	Limits(ctx context.Context) (mastodon.Limits, error)
	VerifyAccount(ctx context.Context) error
	ResolveAccount(ctx context.Context, handle string) (mastodon.Account, error)
	Relationships(ctx context.Context, accountIDs []string) ([]mastodon.Relationship, error)
	Follow(ctx context.Context, accountID string) error
	Collections(ctx context.Context) ([]mastodon.Collection, error)
	GetCollection(ctx context.Context, id string) (mastodon.Collection, error)
	CreateCollection(ctx context.Context, name, description string) (mastodon.Collection, error)
	AddCollectionItem(ctx context.Context, collectionID, accountID string) (mastodon.CollectionItem, error)
	RemoveCollectionItem(ctx context.Context, collectionID, itemID string) error
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

	// Teams is who holds the standing roles, as the organisation maintains
	// them. Nil means nobody does: a deployment that cannot read the teams
	// refuses the editor commands rather than opening them to everyone.
	Teams *people.Teams

	// Checks is the per-check roles, kept in each issue's own bot-owned
	// comment. Nil means the bot cannot read or record them, and says so
	// rather than pretending a check has none.
	Checks *people.Checks

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
//
// A panic here is a bug in one command, not a reason to take down every other
// command in flight: without recover, Go crashes the whole process, and a
// deployment shared by every codechecker loses whatever else was running.
// A kill from the platform's own memory limit is a different thing entirely -
// an OS signal ends the process before any Go code, recover included, runs -
// and no amount of recovering here changes that; see docs/deployment.md ->
// Memory and codecheckers/chekhov#32.
func (s *Server) act(event mention, parsed command.Command) {
	defer func() {
		if s.done != nil {
			s.done <- struct{}{}
		}
	}()
	defer s.recoverCommand(event, parsed)

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	started := time.Now()
	// Everything the bot says goes out through here, and everything it says
	// may quote somebody: a command it did not understand, a report built from
	// a stranger's codecheck.yml, a toot composed from a published record. The
	// roles of a check are kept in a comment by the bot, so nothing the bot
	// posts may look like one. Defusing here rather than in each reply makes
	// that structural instead of something every new reply has to remember.
	body := command.Defuse(s.answer(ctx, event, parsed))
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

// recoverCommand catches a panic from one command, so it costs that command's
// answer rather than the process. It logs the full trace and tries to leave a
// word on the issue; either can fail without making things worse - and must
// not panic in turn, or reporting the first panic costs the process the
// second one was meant to save.
func (s *Server) recoverCommand(event mention, parsed command.Command) {
	r := recover()
	if r == nil {
		return
	}
	defer func() { recover() }()

	trace := string(debug.Stack())
	s.Logger.Error("command panicked", "command", parsed.Name, "repository", event.Repository,
		"issue", event.Issue, "author", event.Author, "panic", r, "stack", trace)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.Replies.Comment(ctx, event.Repository, event.Issue,
		fmt.Sprintf("Something went wrong answering `%s`. It has been logged.\n", parsed.Name))
}

// answer produces the reply to one command.
func (s *Server) answer(ctx context.Context, event mention, parsed command.Command) string {
	roles := s.standingRoles(ctx, event)
	definition, found := command.Lookup(string(parsed.Name))
	// The per-check roles cost a read of the issue's comments, so they are
	// resolved only for a command that is gated on one. Nothing else - hello,
	// version, a check - is about who somebody is on this check.
	if found && definition.Role != command.RoleAnyone && !definition.Role.Standing() {
		roles = s.checkRoles(ctx, event, roles)
	}

	if found && !definition.Permits(roles) {
		return fmt.Sprintf("`%s %s` is for %s.\n", command.Bot, parsed.Name, definition.Role.Description())
	}

	// Every command reads the world afresh. A deployment keeps one Services for
	// its whole life, and a response cached for it would answer a check, or
	// compose an announcement, from what was published hours ago.
	services := s.Services.Fresh()

	switch parsed.Name {
	case command.Commands:
		return command.Listing(roles)
	case command.Hello:
		return s.Deployment.HelloReply()
	case command.Thanks:
		return command.ThanksReply(rand.IntN)
	case command.Version:
		return s.version()
	case command.Check:
		return s.check(parsed, services)
	case command.Rules:
		return s.rules(services)
	case command.Announce:
		return s.announce(ctx, parsed, services)
	case command.Follow:
		return s.follow(ctx, parsed, services)
	case command.Refresh:
		return s.refresh(ctx, parsed)
	case command.Assign:
		return s.assign(ctx, event, parsed)
	case command.Remove:
		return s.remove(ctx, event, parsed)
	case command.ListRoles:
		return s.listRoles(ctx, event, roles)
	case command.ListCodecheckers:
		return s.codecheckers(services)
	case command.SuggestCodecheckers:
		return s.suggestCodecheckers(ctx, event, parsed, services)
	case command.Accept:
		return s.accept(ctx, event, parsed)
	default:
		return command.UnknownReply(parsed)
	}
}

// TeamsFor is the membership cache, wired the one way. The deployment and the
// command line preview both go through it, so that what a preview answers
// cannot drift from what would be posted.
func TeamsFor(settings *config.Settings, reader people.Reader, logger *slog.Logger) *people.Teams {
	return &people.Teams{
		Reader:       reader,
		Organisation: settings.TeamOrganisation(),
		Logger:       logger,
	}
}

// teamStates is what the cache holds, in the terms a reply speaks: the reply
// bodies know nothing about how membership is fetched.
func teamStates(states []people.State) []command.TeamState {
	reply := make([]command.TeamState, 0, len(states))
	for _, state := range states {
		shown := command.TeamState{Team: state.Team, Members: state.Members,
			Read: state.Fetched, Previous: state.Previous}
		if state.Err != nil {
			shown.Problem = state.Err.Error()
		}
		reply = append(reply, shown)
	}
	return reply
}

// teamState is how old each membership list is, for /healthz in development.
// A cache that stopped refreshing looks exactly like one that is working,
// until somebody is refused.
func (s *Server) teamState() map[string]any {
	now := time.Now()
	state := map[string]any{}
	for _, team := range s.Teams.States(s.Settings.Teams()...) {
		entry := map[string]any{"members": team.Members, "read": "never"}
		if !team.Fetched.IsZero() {
			entry["read"] = team.Fetched.UTC().Format(time.RFC3339)
			entry["age_seconds"] = int(now.Sub(team.Fetched).Seconds())
		}
		if team.Err != nil {
			entry["error"] = team.Err.Error()
		}
		state[team.Team] = entry
	}
	return state
}

// rolesOf is everything the person writing the comment is, on this check.
//
// The standing roles come from the organisation's teams, read through the
// cache, which fails closed: a team that cannot be read has no members, so the
// commands that need it refuse rather than open.
//
// The per-check roles - the assigned codechecker, the authors - are a property
// of this issue and are read from it, which is why this takes the whole
// mention. That store is the next part of codecheckers/chekhov#18; until it
// exists, an editor is an editor and everybody else is whatever the teams say.
func (s *Server) standingRoles(ctx context.Context, event mention) command.Roles {
	var roles command.Roles
	for _, standing := range []command.Role{command.RoleEditor, command.RoleCodechecker} {
		if s.Teams.Has(ctx, s.team(standing), event.Author) {
			roles = roles.With(standing)
		}
	}
	return roles
}

// checkRoles adds what this check gave the author to what the organisation
// already says they are.
//
// A record that cannot be read is not a reason to refuse everything: the
// standing roles still stand, and the command that needs the record says what
// went wrong when it reads it for itself.
func (s *Server) checkRoles(ctx context.Context, event mention, roles command.Roles) command.Roles {
	if event.Issue <= 0 || s.Checks == nil {
		return roles
	}
	reading, err := s.Checks.Read(ctx, event.Repository, event.Issue)
	if err != nil || reading.Tampered != nil {
		// A record somebody edited grants nothing: the roles it names may be
		// theirs rather than an editor's. The command that needs it says so.
		s.Logger.Warn("the roles of this check could not be trusted",
			"repository", event.Repository, "issue", event.Issue,
			"error", errors.Join(err, reading.Tampered))
		return roles
	}
	for _, role := range reading.Record.RolesOf(event.Author) {
		roles = roles.With(role)
	}
	return roles
}

// assign gives somebody a role on this check, and records it in the issue.
func (s *Server) assign(ctx context.Context, event mention, parsed command.Command) string {
	handle, role, team, err := command.ParseAssignment(parsed.Args)
	if reply, ok := s.roleCommand(event, err); !ok {
		return reply
	}
	// An editor may name the team themselves, in either direction; the bot
	// works it out from the check when they do not. Refused here rather than
	// at the request, so that the reply can say what may be named.
	if team != "" {
		// Handed on in the settings' own spelling: the guard at the request
		// compares exactly, so "Codecheckers" accepted here would be refused
		// there, after the role had been recorded.
		canonical, ok := s.Settings.ManagedTeam(team)
		if !ok {
			return command.UnmanagedTeamReply(team, s.Settings.ManagedTeams())
		}
		team = canonical
	}

	// A role a check gives out may still need the person to be something
	// already: only an editor can be the handling editor.
	if required, needs := role.Requires(); needs && !s.Teams.Has(ctx, s.team(required), handle) {
		return fmt.Sprintf("`@%s` is not one of the %s, so I cannot make them the %s of this check.\n",
			handle, required.Description(), role)
	}

	// Somebody outside the organisation cannot hold a role on a check, and
	// only an owner can invite them in - so the role is not recorded and the
	// owners are asked instead. A question that could not be answered records
	// the role and touches no team; see Server.membership.
	inside, known := s.membership(ctx, handle)
	if known && !inside {
		return s.outsideReply(ctx, event, handle, role, team)
	}

	replaced := ""
	_, err = s.Checks.Update(ctx, event.Repository, event.Issue, func(record people.Record) (people.Record, error) {
		var err error
		record, replaced, err = record.Grant(role, handle)
		return record, err
	})
	switch {
	case errors.Is(err, people.ErrUnchanged):
		return fmt.Sprintf("`@%s` is already the %s of this check.\n", handle, role)
	case errors.Is(err, people.ErrTampered):
		return tamperedReply(err)
	case err != nil:
		return fmt.Sprintf("%s\n", err)
	}

	// The assigned codechecker is also the issue's assignee, so that the role
	// is visible where people look for it rather than only in a comment.
	assigned := false
	if role == command.RoleAssignedCodechecker {
		assigned = s.assignee(ctx, event, handle, replaced)
	}
	reply := command.AssignedReply(role, handle, replaced, assigned, event.Author, time.Now())

	// A codechecker belongs in the team the check implies. Only when they are
	// known to be in the organisation: the write is a PUT on a team
	// membership, which GitHub turns into an organisation invitation for
	// anybody else, and inviting is the owners' half.
	if role.Checks() && inside {
		if note := s.intoTheTeam(ctx, handle, s.teamFor(ctx, event, team)); note != "" {
			reply += "\n" + note + "\n"
		}
	}
	return reply
}

// remove takes a role away again.
func (s *Server) remove(ctx context.Context, event mention, parsed command.Command) string {
	handle, role, _, err := command.ParseAssignment(parsed.Args)
	if reply, ok := s.roleCommand(event, err); !ok {
		return reply
	}

	_, err = s.Checks.Update(ctx, event.Repository, event.Issue, func(record people.Record) (people.Record, error) {
		updated, held := record.Revoke(role, handle)
		if !held {
			return record, people.ErrUnchanged
		}
		return updated, nil
	})
	switch {
	case errors.Is(err, people.ErrUnchanged):
		return command.RemovedReply(role, handle, false, event.Author, time.Now())
	case errors.Is(err, people.ErrTampered):
		return tamperedReply(err)
	case err != nil:
		return fmt.Sprintf("%s\n", err)
	}

	if role == command.RoleAssignedCodechecker {
		s.unassign(ctx, event, handle)
	}
	return command.RemovedReply(role, handle, true, event.Author, time.Now())
}

// roleCommand is the two things every role command needs before it starts:
// somewhere to keep the roles, and arguments that made sense.
func (s *Server) roleCommand(event mention, err error) (string, bool) {
	if reply, ok := s.noCheck(event); !ok {
		return reply, false
	}
	if err != nil {
		return fmt.Sprintf("%s\n", err), false
	}
	return "", true
}

// listRoles says who holds which role on this check.
func (s *Server) listRoles(ctx context.Context, event mention, roles command.Roles) string {
	if reply, ok := s.noCheck(event); !ok {
		return reply
	}
	reading, err := s.Checks.Read(ctx, event.Repository, event.Issue)
	if err != nil {
		return fmt.Sprintf("I could not read the roles of this check: %s\n", err)
	}
	for _, role := range reading.Record.RolesOf(event.Author) {
		roles = roles.With(role)
	}
	// Shown even when the record cannot be trusted: hiding what it says helps
	// nobody, and this reply is where an editor finds out it was edited.
	return command.RolesReply(reading.Record.Holders(), command.Teams{
		Editors:      s.Settings.EditorsTeam(),
		Codecheckers: s.Settings.CodecheckersTeam(),
	}, roles, provenanceOf(reading))
}

// accept adopts a record the bot did not write, so that work can go on.
func (s *Server) accept(ctx context.Context, event mention, parsed command.Command) string {
	if reply, ok := s.noCheck(event); !ok {
		return reply
	}
	if what := strings.ToLower(strings.Join(parsed.Args, " ")); what != "" && what != "roles" {
		return fmt.Sprintf("I can accept `roles`, not %q.\n", what)
	}

	reading, err := s.Checks.Accept(ctx, event.Repository, event.Issue, event.Author, time.Now())
	switch {
	case errors.Is(err, people.ErrUnchanged):
		return "This record is the one I wrote, so there is nothing to adopt.\n"
	case err != nil:
		return fmt.Sprintf("I could not adopt the roles of this check: %s\n", err)
	}
	return command.AcceptedReply(reading.Record.Holders(), event.Author, reading.Signed(), time.Now())
}

// provenanceOf is what a reply says about where a record came from.
func provenanceOf(reading people.Reading) command.Provenance {
	told := command.Provenance{
		Unsigned:   reading.Unsigned,
		AcceptedBy: reading.AcceptedBy, AcceptedAt: reading.AcceptedAt,
	}
	told.Why = people.Why(reading.Tampered)
	return told
}

// tamperedReply says what the bot will not do, and how to get past it.
func tamperedReply(err error) string {
	return command.TamperedNote(people.Why(err))
}

// noCheck refuses the commands that are about one check when there is no
// check to be about: the command line preview has no issue, and a deployment
// without a store cannot record anything.
func (s *Server) noCheck(event mention) (string, bool) {
	switch {
	case event.Issue <= 0:
		return "Roles belong to a check: ask me on a checks issue.\n", false
	case s.Checks == nil:
		return "I cannot read or record the roles of a check here.\n", false
	default:
		return "", true
	}
}

// team is the GitHub team a standing role comes from.
func (s *Server) team(role command.Role) string {
	switch role {
	case command.RoleEditor:
		return s.Settings.EditorsTeam()
	case command.RoleCodechecker:
		return s.Settings.CodecheckersTeam()
	default:
		return ""
	}
}

// assignee keeps the issue's assignee in step with the assigned codechecker.
//
// A failure is logged and reported as "not done" rather than undoing the
// assignment: the record is the roles, and the assignee is a convenience on
// top of it.
func (s *Server) assignee(ctx context.Context, event mention, handle, replaced string) bool {
	assigner, ok := s.Replies.(Assigner)
	if !ok {
		return false
	}
	// Assign before unassigning: a failure between the two would otherwise
	// leave the issue with nobody assigned at all, which is worse than the
	// state the command started from.
	assigned, err := assigner.Assign(ctx, event.Repository, event.Issue, handle)
	if err != nil {
		s.Logger.Warn("the codechecker could not be made the issue's assignee",
			"issue", event.Issue, "handle", handle, "error", err)
		return false
	}
	if replaced != "" {
		s.unassign(ctx, event, replaced)
	}
	for _, who := range assigned {
		if strings.EqualFold(who, handle) {
			return true
		}
	}
	return false
}

func (s *Server) unassign(ctx context.Context, event mention, handle string) {
	if assigner, ok := s.Replies.(Assigner); ok {
		if err := assigner.Unassign(ctx, event.Repository, event.Issue, handle); err != nil {
			s.Logger.Warn("the codechecker could not be unassigned",
				"issue", event.Issue, "handle", handle, "error", err)
		}
	}
}

// refresh reads the teams again, so that somebody just added to one does not
// wait for the day's expiry.
//
// The runtime cache is the transient copy of something the organisation owns;
// this and the reload at startup are the two ways it is rebuilt from it.
func (s *Server) refresh(ctx context.Context, parsed command.Command) string {
	if what := strings.ToLower(strings.Join(parsed.Args, " ")); what != "" && what != "teams" {
		return fmt.Sprintf("I can refresh `teams`, not %q.\n", what)
	}
	return command.TeamsReply(teamStates(s.Teams.Refresh(ctx, s.Settings.Teams()...)))
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
		if check.IsWord(argument) {
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

	// Not a part of the catalogue but a description of the repository the
	// configuration would come from, which is asked before there is one to
	// check. See check.AboutRepository.
	if part == check.AboutRepository {
		description, err := check.DescribeRepository(target, s.LocalPaths, services)
		if err != nil {
			return fmt.Sprintf("I could not look at `%s`: %s\n", target, err)
		}
		return description.Markdown()
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

// rules says which catalogue this build judges by, and whether the register
// has moved on since it was bundled (chekhov#43).
func (s *Server) rules(services *check.Services) string {
	drift, err := check.RulesDrift(services)
	if err != nil {
		return fmt.Sprintf("I could not read my own rules: %s\n", err)
	}
	return drift.Markdown()
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

// followCollections are the public Mastodon collections follow curates, in
// the order the reply reports them, each with how to read its members out of
// a certificate's resolved mentions and what the collection is described as.
var followCollections = []struct {
	title       string
	description string
	handles     func(announce.Mentions) []string
}{
	{command.CodecheckersList, "Accounts that codechecked a CODECHECK certificate", func(m announce.Mentions) []string { return m.Codecheckers }},
	{command.AuthorsList, "Accounts whose paper was CODECHECKed", func(m announce.Mentions) []string { return m.Authors }},
	{command.VenuesList, "Venues whose paper was CODECHECKed", func(m announce.Mentions) []string {
		if m.Venue == "" {
			return nil
		}
		return []string{m.Venue}
	}},
}

// follow previews, or with "confirm" performs, following a certificate's
// accounts and curating the Codecheckers/Authors/Venues collections. Like
// announce, the bot keeps no state: every run resolves the certificate and
// the collections afresh.
//
// A collection is public and federated, unlike a private List, which is why
// it is the right shape for "who has codechecked, who has been CODECHECKed" -
// but membership needs the account's consent (an item starts "pending" until
// accepted) and Mastodon caps a collection at mastodon.MaxCollectionItems.
// Past the cap, the oldest member is evicted to make room: these collections
// are a curated, rotating sample, not an exhaustive membership record - that
// record is the codechecker lists themselves (codecheckers/codecheckers).
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

	existing, err := s.Toots.Collections(ctx)
	if err != nil {
		return fmt.Sprintf("I could not read the account's collections: %s\n", err)
	}
	byTitle := make(map[string]mastodon.Collection, len(existing))
	for _, collection := range existing {
		byTitle[collection.Name] = collection
	}

	reply.Requested = map[string][]string{}
	reply.Evicted = map[string]int{}
	reply.NotEligible = map[string][]string{}
	collectionURL := map[string]string{} // title -> its public page, for the follow-back DM
	for _, group := range followCollections {
		handles := group.handles(mentions)
		if !anyResolved(handles, accounts) {
			// Nothing in this group resolved to an account, so there is
			// nothing to request - and no reason to create the collection yet.
			continue
		}

		collection, existed := byTitle[group.title]
		var items []mastodon.CollectionItem
		if !existed {
			if collection, err = s.Toots.CreateCollection(ctx, group.title, group.description); err != nil {
				return fmt.Sprintf("I could not prepare the %s collection: %s\n", group.title, err)
			}
			byTitle[group.title] = collection
			// A freshly created collection has no items by construction - no
			// need to ask for what we already know.
		} else {
			full, err := s.Toots.GetCollection(ctx, collection.ID)
			if err != nil {
				return fmt.Sprintf("I could not read who is in the %s collection already: %s\n", group.title, err)
			}
			items = full.Items
		}
		collectionURL[group.title] = collection.URL

		// handled is every account this collection has ever asked, in any
		// state: pending, accepted, but also rejected and revoked, which
		// activeItems (below) deliberately leaves out because they don't
		// count towards the cap. A rejected or revoked account still must
		// not be asked again every run - that's a declined request, not an
		// absent one.
		handled := make(map[string]bool, len(items))
		for _, item := range items {
			handled[item.AccountID] = true
		}
		active := activeItems(items)

		for _, handle := range handles {
			account, ok := accounts[handle]
			if !ok || handled[account.ID] {
				continue
			}
			for len(active) >= mastodon.MaxCollectionItems {
				oldest := active[0]
				if err := s.Toots.RemoveCollectionItem(ctx, collection.ID, oldest.ID); err != nil {
					return fmt.Sprintf("I could not make room in the %s collection: %s\n", group.title, err)
				}
				active = active[1:]
				reply.Evicted[group.title]++
			}
			item, err := s.Toots.AddCollectionItem(ctx, collection.ID, account.ID)
			if err != nil {
				var status *mastodon.StatusError
				if errors.As(err, &status) && (status.Status == http.StatusForbidden || status.Status == http.StatusNotFound) {
					// Mastodon's own consent model, not a failure of ours: the
					// account has not (yet) declared a feature-approval policy
					// that allows it, typically because it does not follow
					// @codecheck back. A private message below asks for that;
					// nothing more to do here but say so and move on to the
					// next handle. A slot possibly freed by eviction above
					// stays unused this run rather than the confirm aborting
					// outright.
					handled[account.ID] = true
					reply.NotEligible[group.title] = append(reply.NotEligible[group.title], handle)
					continue
				}
				return fmt.Sprintf("I could not request %s for the %s collection: %s\n", handle, group.title, err)
			}
			active = append(active, item)
			handled[account.ID] = true
			reply.Requested[group.title] = append(reply.Requested[group.title], handle)
		}
	}

	// Ask each not-yet-eligible account, once, to follow back - a private
	// message, because that is the only channel that reaches the person
	// themselves; the reply above only reaches the editor reading the issue.
	// Not fatal: a failed ask does not undo the following or requesting
	// already done, so it is logged and skipped rather than aborting.
	asked := map[string]bool{}
	for _, title := range command.FollowListTitles {
		for _, handle := range reply.NotEligible[title] {
			if asked[handle] {
				continue
			}
			asked[handle] = true

			// Named plainly rather than "collection(s)": almost always one
			// title, and a handle named on the same certificate under two
			// roles is the only way it is ever more than that.
			var links []string
			for _, t := range command.FollowListTitles {
				if url := collectionURL[t]; url != "" && slices.Contains(reply.NotEligible[t], handle) {
					links = append(links, fmt.Sprintf("- %s: %s", t, url))
				}
			}
			text := fmt.Sprintf(
				"Hi %s! You're named on CODECHECK certificate %s, and could be featured in:\n%s\n\nFollow this account back to be included - thanks for being part of CODECHECK!",
				handle, certificate, strings.Join(links, "\n"))
			// Guards only against a doubled confirm within Mastodon's own
			// hour-long idempotency window, same as announce's own toot - not
			// a lasting "already asked" record. See PostDirect's own doc.
			digest := sha256.Sum256([]byte(text))
			key := fmt.Sprintf("codecheck-ask-%s-%x", certificate, digest[:8])
			if _, err := s.Toots.PostDirect(ctx, text, key); err != nil {
				s.Logger.Warn("could not ask an account to follow back", "certificate", certificate, "handle", handle, "error", err)
				continue
			}
			reply.Asked = append(reply.Asked, handle)
		}
	}

	s.Logger.Info("followed", "certificate", certificate, "newly_followed", len(reply.NewlyFollowed))
	return command.FollowPosted(reply)
}

// activeItems returns a collection's pending and accepted items, oldest
// first: rejected and revoked items don't count towards
// mastodon.MaxCollectionItems and are left out, and oldest-first is the order
// a caller evicts from to make room for a new item.
func activeItems(items []mastodon.CollectionItem) []mastodon.CollectionItem {
	var active []mastodon.CollectionItem
	for _, item := range items {
		if item.State == mastodon.CollectionItemPending || item.State == mastodon.CollectionItemAccepted {
			active = append(active, item)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].CreatedAt.Before(active[j].CreatedAt) })
	return active
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
		state["teams"] = s.teamState()
		if s.Checks != nil {
			state["record_key"] = s.Checks.Signer.PublicKey()
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
func Preview(settings *config.Settings, services *check.Services, author, body string) (string, bool) {
	parsed, addressed := command.Parse(body)
	if !addressed {
		return "", false
	}

	server := New(settings, "", nil, deploymentFor(settings))
	server.Services = services
	// On the command line the path in the comment is the user's own.
	server.LocalPaths = true
	// Roles are the organisation's to say here too. With a token the preview
	// asks it, so that an editor sees what an editor would be answered; with
	// none it falls back to a stranger's reply, which is what a deployment
	// without the permission would give.
	if token := os.Getenv("CHEKHOV_GH_ACCESS_TOKEN"); token != "" {
		server.Teams = TeamsFor(settings, github.New(token, settings.TargetRepository(), ""), server.Logger)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	// The body travels with the mention: a command may be followed by prose -
	// an abstract, an availability statement - and the preview has to read the
	// same comment a deployment would.
	return server.answer(ctx, mention{Author: author, Body: body}, parsed), true
}
