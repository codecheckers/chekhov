package command

import (
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/build"
)

// Deployment is what a reply says about the bot answering it.
//
// The register is in there deliberately: a development bot and the real one
// look alike in a comment, and a codechecker has to be able to tell which one
// just answered them.
//
// How much else a reply says depends on the Environment. A development
// deployment is being watched by whoever is developing it, and every detail -
// the build, the commit, the register - saves a question. A production
// deployment is read by codecheckers who want an answer, not a fingerprint of
// the machine that gave it.
type Deployment struct {
	Version     string
	Commit      string
	Register    string
	Bot         string
	Environment string
}

// Development reports whether this deployment may talk about itself.
//
// Only "development" does. Anything else - production, a future staging
// environment, or a Deployment somebody forgot to fill in - says less than it
// could rather than more than it should.
func (d Deployment) Development() bool {
	return d.Environment == "development"
}

// Facts are what this deployment may say about itself, for the health
// endpoint. The rule about who may know what lives here, with the replies,
// rather than being decided again at every place that reports something.
func (d Deployment) Facts() map[string]any {
	facts := map[string]any{
		"version":     d.Version,
		"bot":         d.Bot,
		"register":    d.Register,
		"environment": d.Environment,
	}
	if d.Development() {
		facts["commit"] = d.Commit
	}
	return facts
}

// Signature goes under every comment in development: which bot, which build,
// which register. In production a reply is signed by the account that posts
// it, which is all a reader needs.
func (d Deployment) Signature() string {
	if !d.Development() {
		return ""
	}
	return fmt.Sprintf("`@%s` %s · working on `%s`", d.Bot, d.describeVersion(), d.Register)
}

// Listing is the reply to "@chekhovbot commands", generated from the registry
// and filtered by what the asker may run.
func Listing(roles Roles) string {
	var out strings.Builder
	out.WriteString("**Commands I understand**\n")

	visible := Visible(roles)
	for _, group := range groups() {
		inGroup := make([]Definition, 0, len(visible))
		for _, definition := range visible {
			if definition.Group == group {
				inGroup = append(inGroup, definition)
			}
		}
		if len(inGroup) == 0 {
			continue
		}

		fmt.Fprintf(&out, "\n**%s**\n\n| Command | What it does |\n|---|---|\n", group)
		for _, definition := range inGroup {
			fmt.Fprintf(&out, "| `%s %s` | %s |\n",
				Bot, escapePipes(definition.Usage), escapePipes(definition.Summary))
		}
	}

	out.WriteString("\nWrite the command on the **first line** of a comment, one command per comment.\n")
	return out.String()
}

// HelloReply is the answer to the liveness check: short, and specific enough
// that it says which deployment is awake. Which register it works on is said
// either way - that is the point of the command - and the build only where
// someone is watching the build.
func (d Deployment) HelloReply() string {
	running := ""
	if d.Development() {
		running = ", running " + d.describeVersion()
	}
	return fmt.Sprintf("Hello! I am awake%s and working on `%s`.\n\n"+
		"Type `%s commands` to see what I can do.\n", running, d.Register, Bot)
}

// ThanksReply acknowledges being thanked, in the words of the bot's namesake.
//
// pick chooses which quote, and is the caller's: the listener passes a random
// one, so that the same pleasantry twice reads differently, and a test passes a
// fixed one, so that it can say what the answer should be.
func ThanksReply(pick func(n int) int) string {
	collection, err := quotes()
	if err != nil || len(collection.Quotes) == 0 {
		// The quotes are embedded, so this cannot happen in a built binary -
		// and if it somehow does, being thanked is not the moment to complain.
		return "You are welcome.\n"
	}

	quote := collection.Quotes[pick(len(collection.Quotes))]
	return fmt.Sprintf("> %s\n\n<sub>Anton Chekhov, %s · [Wikiquote](%s)</sub>\n",
		quote.Text, quote.Cite(), collection.Source)
}

// VersionReply is the answer to "@chekhovbot version": which build is
// answering, and - deliberately - which register it is configured against, so
// that a development deployment cannot be mistaken for the real one.
//
// The rules carry their own provenance: they are maintained in the register,
// and a certificate checked today should be traceable to the exact catalogue
// that judged it.
func (d Deployment) VersionReply(rulesCommit, rulesRetrieved string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "`@%s` %s\n\n", d.Bot, d.describeVersion())

	if d.Commit != "" {
		fmt.Fprintf(&out, "- build: [`%s`](https://github.com/codecheckers/chekhov/commit/%s)\n",
			build.Shorten(d.Commit), d.Commit)
	}
	fmt.Fprintf(&out, "- working on: [`%s`](https://github.com/%s)\n", d.Register, d.Register)
	if rulesCommit != "" {
		fmt.Fprintf(&out, "- validation rules: [`%s`](https://github.com/codecheckers/register/commit/%s)",
			build.Shorten(rulesCommit), rulesCommit)
		if rulesRetrieved != "" {
			fmt.Fprintf(&out, ", retrieved %s", rulesRetrieved)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// UnknownReply answers a comment addressed to the bot that it cannot read,
// quoting back what it saw so the writer can tell a typo from a
// misunderstanding.
func UnknownReply(parsed Command) string {
	var out strings.Builder
	if parsed.Raw == "" {
		out.WriteString("You called, but did not say what for.\n")
	} else {
		fmt.Fprintf(&out, "I do not know the command `%s`.\n", Defuse(parsed.Raw))
	}

	if suggestion, found := Suggest(firstWord(parsed.Raw)); found {
		fmt.Fprintf(&out, "\nDid you mean `%s %s`?\n", Bot, suggestion)
	}
	fmt.Fprintf(&out, "\nType `%s commands` for the list.\n", Bot)
	return out.String()
}

// describeVersion names the build, with the commit when there is one. Only
// called where the deployment may say so.
func (d Deployment) describeVersion() string {
	// When there is no release to name, the version is the short commit
	// already, and "version 55fb4038 (55fb4038)" says it twice.
	if d.Commit == "" || d.Version == build.Shorten(d.Commit) {
		return "version " + d.Version
	}
	return fmt.Sprintf("version %s (`%s`)", d.Version, build.Shorten(d.Commit))
}

// escapePipes keeps a usage line like "check [config|bundle]" from breaking
// the table it sits in. A code span does not protect a pipe in GitHub's
// Markdown tables.
func escapePipes(text string) string { return strings.ReplaceAll(text, "|", "\\|") }

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// An Announcement is what the preview of "@chekhovbot announce" shows.
type Announcement struct {
	Certificate string
	Text        string
	Visibility  string
	// Defused says the mentions in Text are written without their @.
	Defused   bool
	Mentioned []string
	Unmatched []string
	// Frames and Bytes describe the attachment; Frames is 0 when there is none.
	Frames int
	Bytes  int
	// AttachmentProblem says why there is no attachment.
	AttachmentProblem string
	// AnnouncedAt is the toot that already announced the certificate.
	AnnouncedAt string
	// Enabled says whether this deployment can post at all.
	Enabled bool
}

// AnnouncePreview is the answer to "@chekhovbot announce <certificate>":
// everything the toot will be, and the exact line that posts it.
func AnnouncePreview(a Announcement) string {
	var out strings.Builder
	fmt.Fprintf(&out, "**Preview of the announcement of certificate %s**\n\n", a.Certificate)
	fmt.Fprintf(&out, "```text\n%s\n```\n\n", a.Text)

	switch {
	case a.AnnouncedAt != "":
		// Not built: there is nothing to post.
	case a.Frames > 0:
		fmt.Fprintf(&out, "- attachment: the certificate as an animated GIF, %d page%s, %s\n",
			a.Frames, plural(a.Frames), humanBytes(a.Bytes))
	default:
		fmt.Fprintf(&out, "- attachment: none, %s\n", a.AttachmentProblem)
	}
	fmt.Fprintf(&out, "- visibility: `%s`", a.Visibility)
	if a.Defused {
		out.WriteString(", so the mentions are written without `@` and nobody is notified")
	}
	out.WriteString("\n")
	if len(a.Mentioned) > 0 {
		fmt.Fprintf(&out, "- mentions: %s\n", codeList(a.Mentioned))
	} else {
		out.WriteString("- mentions: nobody\n")
	}
	if len(a.Unmatched) > 0 {
		fmt.Fprintf(&out, "- no fediverse account on record for: %s\n", strings.Join(a.Unmatched, ", "))
	}

	switch {
	case !a.Enabled:
		out.WriteString("\nAnnouncing is switched off for this deployment, so I did not check whether it was announced already, " +
			"and `confirm` will not post.\n")
	case a.AnnouncedAt != "":
		fmt.Fprintf(&out, "\n⚠ Already announced: %s\n", a.AnnouncedAt)
	case a.Frames == 0:
		out.WriteString("\nIt cannot be posted without its attachment.\n")
	default:
		fmt.Fprintf(&out, "\nTo post it, write:\n\n    %s announce %s confirm\n", Bot, a.Certificate)
	}
	return out.String()
}

// AnnouncePosted is the answer to a confirm that posted.
func AnnouncePosted(certificate, url string) string {
	return fmt.Sprintf("Announced certificate %s: %s\n", certificate, url)
}

// Names of the public Mastodon lists follow keeps in sync, and the order a
// reply reports them in. The single definition both bot.go (what it builds)
// and FollowPosted (what it reports) work from, so the two cannot drift.
const (
	CodecheckersList = "Codecheckers"
	AuthorsList      = "Authors"
	VenuesList       = "Venues"
)

// FollowListTitles are CodecheckersList, AuthorsList and VenuesList, in the
// order a reply reports them.
var FollowListTitles = []string{CodecheckersList, AuthorsList, VenuesList}

// A Following is what the preview and the posted reply of "@chekhovbot
// follow" show: who a certificate names, who @codecheck already follows, and
// - after confirm - what changed.
type Following struct {
	Certificate string
	// Enabled says whether this deployment may follow accounts and curate
	// collections at all.
	Enabled bool

	Codecheckers []string
	Authors      []string
	// Venue is the matched handle, or "" when there is none.
	Venue string
	// Unmatched are the people, and the venue, with no account on record.
	Unmatched []string
	// Unresolved are handles on record that the instance could not resolve
	// to an account - deleted, suspended or moved, say - as opposed to
	// Unmatched, where there was no handle to try in the first place.
	Unresolved []string

	// NewlyFollowed and AlreadyFollowed are the matched handles, split by
	// whether @codecheck followed them already - known in the preview, and
	// unchanged by it.
	NewlyFollowed   []string
	AlreadyFollowed []string

	// Requested is filled by confirm: the handles newly asked to join each
	// collection, keyed by its title. A collection needs the account's
	// consent, so this is a request, not immediate membership.
	Requested map[string][]string
	// Evicted counts, per collection, how many older members were dropped to
	// make room under Mastodon's cap - see mastodon.MaxCollectionItems.
	Evicted map[string]int
	// NotEligible is filled by confirm: handles Mastodon refused to request
	// for a collection because the account's own feature-approval policy
	// does not allow it yet - commonly because they do not follow @codecheck
	// back. Not an error: worth another try on a later confirm, once they do.
	NotEligible map[string][]string
	// Asked is who among NotEligible was sent a private message asking them
	// to follow back, deduplicated across collections - the only channel
	// that reaches the person themselves, this reply being a GitHub comment.
	Asked []string
}

// FollowPreview is the answer to "@chekhovbot follow <certificate>": who is
// matched, who is already followed, and the exact line that acts.
func FollowPreview(f Following) string {
	var out strings.Builder
	fmt.Fprintf(&out, "**Preview of following certificate %s's accounts**\n\n", f.Certificate)
	writeMentionGroup(&out, "codecheckers", f.Codecheckers)
	writeMentionGroup(&out, "authors", f.Authors)
	if f.Venue != "" {
		writeMentionGroup(&out, "venue", []string{f.Venue})
	}
	if len(f.Unmatched) > 0 {
		fmt.Fprintf(&out, "- no fediverse account on record for: %s\n", strings.Join(f.Unmatched, ", "))
	}
	if len(f.Unresolved) > 0 {
		fmt.Fprintf(&out, "- on record, but the instance could not resolve: %s\n", codeList(f.Unresolved))
	}
	if f.Enabled {
		if len(f.AlreadyFollowed) > 0 {
			fmt.Fprintf(&out, "- already followed: %s\n", codeList(f.AlreadyFollowed))
		}
		if len(f.NewlyFollowed) > 0 {
			fmt.Fprintf(&out, "- not yet followed: %s\n", codeList(f.NewlyFollowed))
		}
	}

	switch {
	case !f.Enabled:
		out.WriteString("\nFollowing is switched off for this deployment, so `confirm` will not follow anyone or touch any collection.\n")
	case len(f.Codecheckers)+len(f.Authors) == 0 && f.Venue == "":
		out.WriteString("\nNobody on this certificate has a fediverse account on record.\n")
	default:
		fmt.Fprintf(&out, "\nTo follow whoever is not yet followed, and request the Codecheckers, Authors and Venues collections for them, write:\n\n    %s follow %s confirm\n",
			Bot, f.Certificate)
	}
	return out.String()
}

// FollowPosted is the answer to a confirm that ran.
func FollowPosted(f Following) string {
	var out strings.Builder
	fmt.Fprintf(&out, "**Followed for certificate %s**\n\n", f.Certificate)
	if len(f.NewlyFollowed) > 0 {
		fmt.Fprintf(&out, "- followed: %s\n", codeList(f.NewlyFollowed))
	} else {
		out.WriteString("- followed: nobody new, everyone matched was already followed\n")
	}
	if len(f.Unresolved) > 0 {
		fmt.Fprintf(&out, "- on record, but the instance could not resolve: %s\n", codeList(f.Unresolved))
	}
	any := false
	for _, title := range FollowListTitles {
		if requested := f.Requested[title]; len(requested) > 0 {
			fmt.Fprintf(&out, "- requested for the %s collection: %s\n", title, codeList(requested))
			any = true
		}
		if evicted := f.Evicted[title]; evicted > 0 {
			fmt.Fprintf(&out, "- made room in the %s collection by evicting %d older member%s\n", title, evicted, plural(evicted))
			any = true
		}
		if notEligible := f.NotEligible[title]; len(notEligible) > 0 {
			fmt.Fprintf(&out, "- not yet eligible for the %s collection (they do not follow @codecheck back): %s\n", title, codeList(notEligible))
			any = true
		}
	}
	if len(f.Asked) > 0 {
		fmt.Fprintf(&out, "- privately asked to follow back: %s\n", codeList(f.Asked))
		any = true
	}
	if !any {
		out.WriteString("- collections: already up to date\n")
	}
	return out.String()
}

func writeMentionGroup(out *strings.Builder, label string, handles []string) {
	if len(handles) == 0 {
		return
	}
	fmt.Fprintf(out, "- %s: %s\n", label, codeList(handles))
}

func codeList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "`" + item + "`"
	}
	return strings.Join(quoted, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d kB", n/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// Defuse is how anything somebody else wrote goes into a reply.
//
// The bot quotes people - an unknown command is echoed back, a check report
// carries strings out of a codecheck.yml somebody controls - and its own
// comments are where the per-check roles are kept. An HTML comment in quoted
// text would let a stranger write what looks like a record into a comment
// authored by the bot, so the opening sequence never survives the journey.
// internal/people/record.go has the other half of that defence.
func Defuse(text string) string {
	// The opener only: "-->" on its own starts nothing, and escaping it would
	// show up as literal "--&gt;" inside the code blocks of a check report,
	// where entities are not decoded.
	return strings.ReplaceAll(text, "<!--", "&lt;!--")
}

// oneLine puts a message in a table cell: a GitHub error carries the response
// body, and a proxy's HTML page would otherwise end the row and the table with
// it.
func oneLine(message string) string {
	return Defuse(escapePipes(strings.Join(strings.Fields(message), " ")))
}

// A TeamState is what reading one team found, as a reply shows it. The cache
// itself is internal/people; this is the handful of fields a comment says out
// loud, so that the reply bodies stay free of what fetched them.
type TeamState struct {
	Team    string
	Members int
	// Read is when the membership in hand was read, zero when it never was.
	Read time.Time
	// Problem is why the team could not be read, empty when it could.
	Problem string
}

// TeamsReply says what reading the teams found, for `refresh teams`.
//
// It names the size of each team and whether the copy it holds is current, so
// that an editor who has just added somebody can see their change arrive
// rather than trusting that it did.
func TeamsReply(states []TeamState) string {
	if len(states) == 0 {
		return "There are no teams configured, so nobody holds a standing role here.\n"
	}

	var reply strings.Builder
	refused := false
	reply.WriteString("Read the organisation's teams again:\n\n")
	reply.WriteString("| Team | Members | Read |\n|---|---|---|\n")
	for _, state := range states {
		switch {
		case state.Problem != "" && state.Members == 0:
			refused = true
			fmt.Fprintf(&reply, "| `%s` | — | could not be read: %s |\n",
				state.Team, oneLine(state.Problem))
		case state.Problem != "":
			fmt.Fprintf(&reply, "| `%s` | %d | could not be read, so the copy from %s still stands |\n",
				state.Team, state.Members, state.Read.UTC().Format(time.RFC3339))
		default:
			fmt.Fprintf(&reply, "| `%s` | %d | just now |\n", state.Team, state.Members)
		}
	}

	if refused {
		reply.WriteString("\nUntil a team can be read, nobody holds the role it carries, " +
			"so the commands that need it will refuse.\n")
	}
	return reply.String()
}

// Holders is who holds each per-check role, as a reply shows it. The record
// itself is internal/people; this is what a comment says out loud.
type Holders struct {
	HandlingEditor      string
	AssignedCodechecker string
	Authors             []string
}

// Teams are the teams the standing roles come from, for a reply that says
// where a role came from rather than only who holds it.
type Teams struct {
	Editors      string
	Codecheckers string
}

// RolesTable is the per-check roles as a table. One renderer, used by the
// `roles` reply and by the record comment the bot keeps in the issue, so that
// the two readings of the same fact cannot drift apart.
func RolesTable(holders Holders) string {
	var table strings.Builder
	table.WriteString("**Roles on this check**\n\n")
	table.WriteString("| Role | Who |\n|---|---|\n")
	fmt.Fprintf(&table, "| %s | %s |\n", RoleHandlingEditor, mention(holders.HandlingEditor))
	fmt.Fprintf(&table, "| %s | %s |\n", RoleAssignedCodechecker, mention(holders.AssignedCodechecker))
	fmt.Fprintf(&table, "| %s | %s |\n", RoleAuthor, mentions(holders.Authors))
	return table.String()
}

// RolesReply says who holds which role on this check, and where each role
// comes from - the issue, or a team the organisation maintains.
func RolesReply(holders Holders, teams Teams, asker Roles) string {
	var reply strings.Builder
	reply.WriteString(RolesTable(holders))
	fmt.Fprintf(&reply, "\nThe standing roles come from the organisation: "+
		"the `%s` team, and the `%s` team.\n", teams.Editors, teams.Codecheckers)

	if len(asker) > 0 {
		names := make([]string, 0, len(asker))
		for _, role := range asker {
			names = append(names, string(role))
		}
		fmt.Fprintf(&reply, "\nYou are %s here.\n", strings.Join(names, " and "))
	}
	reply.WriteString("\nAn editor changes the roles above with `" + Bot +
		" assign` and `" + Bot + " remove`; team membership is changed on GitHub.\n")
	return reply.String()
}

// AssignedReply says what an assignment did.
func AssignedReply(role Role, handle, replaced string, assignee bool) string {
	var reply strings.Builder
	fmt.Fprintf(&reply, "%s is now the %s of this check.\n", mention(handle), role)
	if replaced != "" {
		fmt.Fprintf(&reply, "\nThat replaces %s, who held it until now.\n", mention(replaced))
	}
	if assignee {
		fmt.Fprintf(&reply, "\nI have also set %s as the assignee of this issue, so the role is "+
			"visible where people look for it.\n", mention(handle))
	}
	return reply.String()
}

// RemovedReply says what a removal did.
func RemovedReply(role Role, handle string, held bool) string {
	if !held {
		return fmt.Sprintf("%s was not the %s of this check, so there was nothing to take away.\n",
			mention(handle), role)
	}
	return fmt.Sprintf("%s is no longer the %s of this check.\n", mention(handle), role)
}

// mention writes a handle the way GitHub renders it, or an em dash for nobody.
//
// Deliberately without the @: a role table that mentions five people notifies
// five people every time somebody asks who holds what.
func mention(handle string) string {
	if handle == "" {
		return "—"
	}
	return "`@" + handle + "`"
}

func mentions(handles []string) string {
	if len(handles) == 0 {
		return "—"
	}
	written := make([]string, 0, len(handles))
	for _, handle := range handles {
		written = append(written, mention(handle))
	}
	return strings.Join(written, ", ")
}
