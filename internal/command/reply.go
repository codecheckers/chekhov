package command

import (
	"fmt"
	"path"
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
// the build, the register - saves a question. A production deployment is read
// by codecheckers who want an answer, not a fingerprint of the machine that
// gave it.
type Deployment struct {
	// Version is build.Version, the one version this bot reports.
	Version string
	// Revision is the commit the build was made from, empty unless the
	// toolchain stamped one - see internal/build. RevisionDirty says the tree
	// had uncommitted changes, so the binary is not that commit. Only a
	// development deployment says either.
	Revision      string
	RevisionDirty bool
	Register      string
	Bot           string
	Environment   string
}

// Development reports whether this deployment may talk about itself.
//
// Only "development" does. Anything else - production, a future staging
// environment, or a Deployment somebody forgot to fill in - says less than it
// could rather than more than it should.
func (d Deployment) Development() bool {
	return d.Environment == "development"
}

// Facts are what a deployment says about itself, for the health endpoint: the
// four fields production is allowed to disclose, plus the commit the build was
// made from where a development deployment may say it. The rest of what
// development adds - whether the services answer, when the token expires, who
// holds a role - needs the server and is assembled in bot.health.
func (d Deployment) Facts() map[string]any {
	facts := map[string]any{
		"version":     d.Version,
		"bot":         d.Bot,
		"register":    d.Register,
		"environment": d.Environment,
	}
	// A tree with uncommitted changes is worth saying: the binary is then not
	// the commit it names, and a bug report that says so saves an afternoon.
	if d.Development() && d.Revision != "" {
		facts["commit"] = d.Revision
		if d.RevisionDirty {
			facts["commit_dirty"] = true
		}
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
	fmt.Fprintf(&out, "`@%s` version %s\n\n", d.Bot, d.Version)
	// Behind the development check, as every other detail of the machine is:
	// production answers a codechecker's question, not a fingerprint.
	if d.Development() && d.Revision != "" {
		fmt.Fprintf(&out, "- build: [`%s`](https://github.com/codecheckers/chekhov/commit/%s)%s\n",
			build.Shorten(d.Revision), d.Revision, d.describeDirt())
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

// describeVersion names the build: the version, and the commit after it when
// the build can prove one. Its two callers are the footer and the greeting,
// both of which only a development deployment writes, which is where somebody
// wants to know exactly what answered - a locally built binary signing a
// preview says its own hash. The version reply writes its own line, because
// there it is a link to a full hash rather than a short one in a sentence.
func (d Deployment) describeVersion() string {
	if d.Revision == "" {
		return "version " + d.Version
	}
	return fmt.Sprintf("version %s (`%s`%s)", d.Version, build.Shorten(d.Revision), d.describeDirt())
}

// describeDirt is what to add when the binary is not the commit it names.
func (d Deployment) describeDirt() string {
	if !d.RevisionDirty {
		return ""
	}
	return ", " + build.Uncommitted
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
	// Previous is when the copy this one replaced was read, zero when there
	// was none. An editor who has just changed a team wants to know whether
	// the copy being replaced already had their change in it.
	Previous time.Time
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
		case state.Previous.IsZero():
			fmt.Fprintf(&reply, "| `%s` | %d | just now, for the first time |\n",
				state.Team, state.Members)
		default:
			fmt.Fprintf(&reply, "| `%s` | %d | just now, replacing the copy from %s |\n",
				state.Team, state.Members, state.Previous.UTC().Format(time.RFC3339))
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

// Provenance is what a reply says about where a record came from: whether the
// bot wrote what it just read, and whether anybody has adopted an edit of it.
type Provenance struct {
	// Unsigned says the record carries no signature, which only a deployment
	// with no key of its own accepts.
	Unsigned bool
	// Why is what is wrong with the record, empty when nothing is.
	Why string
	// AcceptedBy and AcceptedAt are set when an editor has adopted an edited
	// record.
	AcceptedBy, AcceptedAt string
}

// Sentence is the provenance in words, for the foot of a reply. Empty when
// there is nothing worth saying: a record the bot wrote and nobody touched.
func (p Provenance) Sentence() string {
	var said strings.Builder
	switch {
	case p.Why != "":
		said.WriteString(TamperedNote(p.Why))
	case p.Unsigned:
		said.WriteString("\nThis record is unsigned: I have no key.\n")
	}
	said.WriteString(adoptionNote(p.AcceptedBy, p.AcceptedAt))
	return said.String()
}

// AcceptedReply says that an editor has adopted a record the bot did not
// write.
func AcceptedReply(holders Holders, editor string, signed bool, when time.Time) string {
	var reply strings.Builder
	reply.WriteString("Adopted.\n\n")
	reply.WriteString(RolesTable(holders))
	if signed {
		reply.WriteString("\nSigned again.\n")
	} else {
		reply.WriteString("\nStill unsigned: I have no key.\n")
	}
	reply.WriteString(entry("adopted", editor, when))
	return reply.String()
}

// RecordNote is the foot of the comment the bot keeps a check's roles in: what
// the comment is, and what editing it by hand would and would not do.
//
// Here rather than in internal/people because every other rendering of a
// record is here, and two wordings of the same fact drift.
func RecordNote(acceptedBy, acceptedAt string, signed bool) string {
	var note strings.Builder
	note.WriteString("\nI read the record at the top, not this table.")
	if signed {
		note.WriteString(" It is signed.\n")
	} else {
		note.WriteString(" It is unsigned.\n")
	}
	note.WriteString(adoptionNote(acceptedBy, acceptedAt))
	return note.String()
}

// adoptionNote says that an editor adopted a record the bot did not write. One
// wording, used in the record comment and in a reply.
func adoptionNote(by, at string) string {
	if by == "" {
		return ""
	}
	return fmt.Sprintf("\nAdopted by `@%s`, %s.\n", by, at)
}

// TamperedNote is what the bot says about a record that no longer matches its
// signature: in a reply to a command it will not carry out, and under the
// roles it still shows.
//
// It says what happened to the record, not who did it - the bot cannot tell,
// and saying so would be a guess dressed as a fact.
func TamperedNote(why string) string {
	return fmt.Sprintf("\n⚠ **The roles record was edited after I wrote it**: %s. "+
		"I will not change the roles of this check until an editor runs `%s accept roles`.\n",
		why, Bot)
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
func RolesReply(holders Holders, teams Teams, asker Roles, told Provenance) string {
	var reply strings.Builder
	reply.WriteString(RolesTable(holders))
	reply.WriteString(told.Sentence())
	fmt.Fprintf(&reply, "\nStanding roles: the `%s` and `%s` teams on GitHub.\n",
		teams.Editors, teams.Codecheckers)

	if len(asker) > 0 {
		names := make([]string, 0, len(asker))
		for _, role := range asker {
			names = append(names, string(role))
		}
		fmt.Fprintf(&reply, "\nYou are %s here.\n", strings.Join(names, " and "))
	}
	return reply.String()
}

// AssignedReply says what an assignment did, and is the record of it.
//
// One comment, not two: the confirmation carries what a separate audit entry
// would have said - who asked, what changed, and when. The roles comment above
// always shows the current state, so the thread has to be what says how it got
// there. It matters most when the record is gone: a comment somebody deleted
// leaves these behind, and a check can be reconstructed from them.
func AssignedReply(role Role, handle, replaced string, assignee bool, by string, when time.Time) string {
	var reply strings.Builder
	fmt.Fprintf(&reply, "%s is now the %s of this check.\n", mention(handle), role)
	if replaced != "" {
		fmt.Fprintf(&reply, "\nReplaces %s.\n", mention(replaced))
	}
	if assignee {
		reply.WriteString("\nAssignee of this issue set.\n")
	}
	reply.WriteString(entry("assigned", by, when))
	return reply.String()
}

// RemovedReply says what a removal did, and is the record of it.
func RemovedReply(role Role, handle string, held bool, by string, when time.Time) string {
	if !held {
		return fmt.Sprintf("%s was not the %s of this check.\n", mention(handle), role)
	}
	return fmt.Sprintf("%s is no longer the %s of this check.\n", mention(handle), role) +
		entry("removed", by, when)
}

// entry is what makes a reply the record of a change: who did it, and when.
//
// Terse on purpose. The reply is read by somebody who asked for the change and
// knows what they asked for; explaining what a comment is for belongs in the
// documentation, not in every comment.
func entry(what, by string, when time.Time) string {
	return fmt.Sprintf("\n— %s by %s, %s\n", what, mention(by), when.UTC().Format(time.RFC3339))
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

// A CodecheckerList is one of the lists people register themselves on, as the
// reply names it: where it is, and how many people are on it.
type CodecheckerList struct {
	// Page is where a person should be sent to read the list.
	Page string
	// Count is how many entries it has, negative when it was not counted -
	// an offline bot links the lists without pretending to know their length.
	Count int
	// Problem is why it could not be read, empty when it could or when it was
	// never tried.
	Problem string
}

// CodecheckerListsReply answers "@chekhovbot codecheckers" with a link to each
// list rather than a copy of it.
//
// A copy in an issue is out of date the day somebody registers, while the file
// stays current; and the lists are long enough that pasting one would bury the
// conversation it was pasted into.
func CodecheckerListsReply(lists []CodecheckerList) string {
	if len(lists) == 0 {
		return "No codechecker lists are configured.\n"
	}

	var reply strings.Builder
	reply.WriteString("Codechecker lists, current as of now - read these rather than a copy:\n\n")
	for _, list := range lists {
		page := list.Page
		switch {
		case list.Problem != "":
			fmt.Fprintf(&reply, "- [%s](%s) — could not read it: %s\n",
				nameOfList(page), page, oneLine(list.Problem))
		case list.Count < 0:
			fmt.Fprintf(&reply, "- [%s](%s)\n", nameOfList(page), page)
		default:
			fmt.Fprintf(&reply, "- [%s](%s) — %d entr%s\n",
				nameOfList(page), page, list.Count, entryPlural(list.Count))
		}
	}
	fmt.Fprintf(&reply, "\nTo be listed, register at [codecheckers/codecheckers]"+
		"(https://github.com/codecheckers/codecheckers). Editors: `%s suggest codecheckers`.\n", Bot)
	return reply.String()
}

// nameOfList is the file a list URL ends in, which is what people call it.
func nameOfList(url string) string { return path.Base(url) }

func entryPlural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// A Candidate is one suggested codechecker, as the reply shows them. The
// ranking itself is internal/suggest; this is what a comment says out loud.
type Candidate struct {
	Handle string
	Name   string
	// Why is what they share with this check, in words.
	Why string
}

// A LeftOut is somebody deliberately not suggested, and the reason.
type LeftOut struct {
	Handle string
	Why    string
}

// Suggestions is the answer to "@chekhovbot suggest codecheckers".
type Suggestions struct {
	Candidates []Candidate
	LeftOut    []LeftOut
	// Read is what the evidence was taken from, in words.
	Read []string
	// Unread is what could not be read, and why; the command answers with
	// what it has rather than failing on one source.
	Unread []string
	// Languages and Fields are what the check was matched on: what the
	// suggestions actually share with it, when there are any, and otherwise
	// what was looked for. A metadata source can say thirty things a paper is
	// about, and a reader wants the handful that decided the answer.
	Languages []string
	Fields    []string
	// NotChecked names the exclusions that could not be applied - on the
	// command line there is no issue, so this check's roles are unknown.
	NotChecked []string
	// Matched is how many codecheckers share anything with this check, of
	// which Candidates is the first few.
	Matched int
	// Considered is how many codecheckers were read from the lists.
	Considered int
	// Undistinguished says the shortlist is not in order of fitness, because
	// nothing about the check separates the people on it.
	Undistinguished bool
}

// SuggestionsReply names the candidates without mentioning them.
//
// Every handle is in backticks, deliberately: asking me who could check a paper
// must not notify half the community. The editor mentions whom they choose.
func SuggestionsReply(s Suggestions) string {
	var reply strings.Builder

	if len(s.Candidates) == 0 {
		reply.WriteString("No codechecker to suggest.\n")
	} else {
		fmt.Fprintf(&reply, "%s, of %d listed:\n\n",
			describeShortlist(len(s.Candidates), s.Matched), s.Considered)
		reply.WriteString("| Handle | Codechecker | Declares |\n|---|---|---|\n")
		for _, candidate := range s.Candidates {
			fmt.Fprintf(&reply, "| `@%s` | %s | %s |\n", candidate.Handle,
				oneLine(withoutMentions(candidate.Name)), oneLine(candidate.Why))
		}
		if s.Undistinguished {
			reply.WriteString("\nAll matches share the same terms. " +
				"Please provide work DOI or abstract for better matches.\n")
		}
		reply.WriteString("\nHandles are in backticks, so nobody is notified. " +
			"`" + Bot + " assign @user as codechecker` records your choice.\n")
	}

	if matched := describeMatch(s); matched != "" {
		fmt.Fprintf(&reply, "\nMatched on %s.\n", matched)
	}
	if len(s.Read) > 0 {
		fmt.Fprintf(&reply, "\nRead: %s.\n", strings.Join(s.Read, "; "))
	}
	if len(s.LeftOut) > 0 {
		left := make([]string, 0, len(s.LeftOut))
		for _, out := range s.LeftOut {
			left = append(left, fmt.Sprintf("`@%s` (%s)", out.Handle, out.Why))
		}
		fmt.Fprintf(&reply, "\nLeft out: %s.\n", strings.Join(left, ", "))
	}
	for _, note := range s.NotChecked {
		fmt.Fprintf(&reply, "\n%s\n", oneLine(note))
	}
	for _, problem := range s.Unread {
		fmt.Fprintf(&reply, "\nCould not read %s.\n", oneLine(problem))
	}
	return reply.String()
}

// withoutMentions writes an at sign so that it cannot notify anybody.
//
// A name comes out of a list people fill in about themselves, and a name with
// an @ in it would mention whoever owns that handle from a reply whose whole
// promise is that asking me notifies nobody. The entity renders as an @ and
// links to nothing.
func withoutMentions(text string) string { return strings.ReplaceAll(text, "@", "&#64;") }

// describeShortlist says whether the reply is the whole answer or a slice of
// it. Five out of forty is a different answer from five out of five, and an
// editor choosing between them should be told which they are reading.
func describeShortlist(shown, matched int) string {
	if matched > shown {
		return fmt.Sprintf("%d of %d matching codecheckers", shown, matched)
	}
	return fmt.Sprintf("%d matching codechecker%s", shown, plural(shown))
}

func describeMatch(s Suggestions) string {
	var matched []string
	if len(s.Languages) > 0 {
		matched = append(matched, "languages "+codeListUpTo(s.Languages, mostTermsShown))
	}
	if len(s.Fields) > 0 {
		matched = append(matched, "fields "+codeListUpTo(s.Fields, mostTermsShown))
	}
	return strings.Join(matched, " and ")
}

// mostTermsShown is how many matched terms a line names before it stops being
// a sentence and becomes a dump.
const mostTermsShown = 8

func codeListUpTo(items []string, most int) string {
	if len(items) <= most {
		return codeList(items)
	}
	return fmt.Sprintf("%s and %d more", codeList(items[:most]), len(items)-most)
}

// An OutsideRequest is somebody an editor tried to give a role to who is not
// in the organisation yet, as the reply says it.
//
// The one reply in this package that writes a plain @mention. Everywhere else
// a handle is in backticks, because a reply is not the place to notify a
// person who has not asked to be in the thread - but this reply exists to
// reach the owners, who are the only people who can invite somebody in, and a
// notification is what it is for. The handles are the organisation's current
// owners, read from the organisation; nothing here writes one down. See
// CLAUDE.md -> Committing and publishing.
type OutsideRequest struct {
	// Handle is the person who is not in the organisation. In backticks, as
	// everywhere: they are being discussed, not summoned.
	Handle string
	// Role is what the editor tried to give them.
	Role Role
	// Organisation and Team are where they need to end up.
	Organisation string
	Team         string
	// Owners are the handles this reply mentions. Empty when they could not
	// be read, which the reply says rather than asking nobody.
	Owners []string
	// Unreadable is why the owners could not be read, empty when they could.
	Unreadable string
	// Configured says the handles came from the settings rather than from the
	// organisation. The reply says so: otherwise it reads as though the
	// organisation's owners had been asked, when in a test they have not
	// been.
	Configured bool
}

// OutsideReply asks the owners to invite somebody, and says what the bot will
// do once they have.
//
// Only an organisation owner can invite somebody from outside into a team -
// GitHub's rule, not this bot's - so the division of labour is the point of
// the wording: the owners invite, the bot does the rest.
func OutsideReply(request OutsideRequest) string {
	var out strings.Builder
	fmt.Fprintf(&out, "`@%s` is not in the `%s` organisation yet, so I have not made them "+
		"the %s of this check: somebody outside the organisation cannot hold a role on one.\n",
		request.Handle, request.Organisation, request.Role)

	if request.Unreadable != "" {
		fmt.Fprintf(&out, "\nAn organisation owner has to invite them, and I could not read "+
			"who the owners are: %s\n", oneLine(request.Unreadable))
		return out.String()
	}
	if len(request.Owners) == 0 {
		out.WriteString("\nAn organisation owner has to invite them, and I could not find one.\n")
		return out.String()
	}

	fmt.Fprintf(&out, "\n%s — please invite `@%s` to the organisation. "+
		"I will handle the rest: once they accept, %s\n",
		notifying(request.Owners), request.Handle, request.rest())
	if request.Configured {
		// Said plainly: a reader must not take this for the organisation's
		// owners having been asked.
		out.WriteString("\n*This deployment is configured to ask the people above rather than " +
			"the organisation's owners, so the owners have not been notified.*\n")
	}
	// What to do next, said once and honestly: the nightly sweep picks this
	// up (codecheckers/chekhov#48), so nobody has to come back to the thread,
	// and the command is offered only as a way of not waiting for it.
	fmt.Fprintf(&out, "\nOnly an owner can invite somebody from outside the organisation, "+
		"which is why this is not something I can do. I will check every night and finish "+
		"this once they have joined; `%s assign @%s as %s` does it sooner, and asks the "+
		"owners again if they still have not.\n", Bot, request.Handle, request.Role.Typed())
	return out.String()
}

// UnmanagedTeamReply refuses a team the settings do not allow the bot to add
// to, and says which it does - a refusal that names what it would have
// accepted saves the next command.
func UnmanagedTeamReply(team string, managed []string) string {
	if len(managed) == 0 {
		return "I am not set up to put anybody in a team.\n"
	}
	return fmt.Sprintf("I can only put somebody in %s, not in `%s`.\n", codeList(managed), team)
}

// rest is what the bot promises to do once the person has joined, said from
// what is actually true of this role: a team only for somebody who checks, and
// the issue's assignee only for the one role the issue shows.
func (request OutsideRequest) rest() string {
	promised := []string{"I record the role"}
	if request.Team != "" {
		promised = []string{fmt.Sprintf("I put them in `%s/%s`", request.Organisation, request.Team),
			"record the role"}
	}
	if request.Role == RoleAssignedCodechecker {
		promised = append(promised, "assign this issue to them")
	}
	switch len(promised) {
	case 1:
		return promised[0] + "."
	default:
		return strings.Join(promised[:len(promised)-1], ", ") + " and " + promised[len(promised)-1] + "."
	}
}

// A Swept is what one walk of the register came to, as the reply says it.
type Swept struct {
	Issues      int
	Acted       int
	Outstanding int
	Problems    []string
}

// SweptReply says what the walk found. Short when there was nothing to do,
// which is the usual answer and should read as reassurance rather than as a
// shrug.
// mostProblemsListed bounds the problems one reply prints; see SweptReply.
const mostProblemsListed = 20

func SweptReply(swept Swept) string {
	var out strings.Builder
	fmt.Fprintf(&out, "I walked %d open issue%s.\n", swept.Issues, plural(swept.Issues))

	switch {
	case swept.Acted > 0:
		fmt.Fprintf(&out, "\nAnswered %d follow-up%s", swept.Acted, plural(swept.Acted))
		if swept.Outstanding > 0 {
			fmt.Fprintf(&out, ", and left %d waiting", swept.Outstanding)
		}
		out.WriteString(".\n")
	case swept.Outstanding > 0:
		fmt.Fprintf(&out, "\n%d follow-up%s waiting, none of them ready for anything yet.\n",
			swept.Outstanding, plural(swept.Outstanding))
	default:
		out.WriteString("\nNothing outstanding.\n")
	}

	// Bounded, because a register-wide failure - an expired token, a walk
	// that ran out of time - is one problem per open issue, and a comment
	// GitHub refuses for its length is no answer at all.
	listed := swept.Problems
	if len(listed) > mostProblemsListed {
		listed = listed[:mostProblemsListed]
	}
	for _, problem := range listed {
		fmt.Fprintf(&out, "\nCould not check: %s\n", oneLine(problem))
	}
	if left := len(swept.Problems) - len(listed); left > 0 {
		fmt.Fprintf(&out, "\nAnd %d more like that, which I have left out.\n", left)
	}
	return out.String()
}

// Joined is somebody who has accepted the organisation's invitation, and what
// was done about it.
type Joined struct {
	// Handle is who joined. In backticks, as everywhere: the reply is about
	// them, and the invitation already reached them.
	Handle string
	Role   Role
	// Taken is who holds the role now, when it went to somebody else while
	// the invitation was outstanding. The role is left with them: an editor
	// decided that after the ask, and a three-day-old record does not
	// overrule it.
	Taken string
	// Granted says the role was recorded now rather than by somebody who got
	// there first, and Assigned that the issue's assignee was set.
	Granted  bool
	Assigned bool
	// TeamNote is what the team change came to, empty when there was nothing
	// to change.
	TeamNote string
	// Lists is where a codechecker's row is kept, for the reply to point at.
	Lists []string
	// Organisation is the one they joined, named from the settings rather
	// than written down here.
	Organisation string
}

// JoinedReply says that the wait is over and what was done, for the nightly
// sweep that noticed.
func JoinedReply(joined Joined) string {
	var out strings.Builder
	fmt.Fprintf(&out, "`@%s` has joined the `%s` organisation.\n", joined.Handle, joined.Organisation)

	var did []string
	if joined.TeamNote != "" {
		did = append(did, joined.TeamNote)
	}
	if joined.Granted {
		did = append(did, fmt.Sprintf("They are now the %s of this check.", joined.Role))
	}
	if joined.Taken != "" {
		did = append(did, fmt.Sprintf("The %s of this check is `@%s`, who was given the role "+
			"after I asked for the invitation, so I have left it with them.", joined.Role, joined.Taken))
	}
	if joined.Assigned {
		did = append(did, "Assignee of this issue set.")
	}
	for _, done := range did {
		fmt.Fprintf(&out, "\n%s\n", done)
	}

	if joined.Role.Checks() && len(joined.Lists) > 0 {
		out.WriteString("\nThe row in the codechecker list is still a person's job - " +
			"it carries their name, ORCID, fields and languages, which I have no way to know:\n\n")
		for _, list := range joined.Lists {
			fmt.Fprintf(&out, "- [%s](%s)\n", nameOfList(list), list)
		}
	}
	return out.String()
}

// mostOwnersMentioned is how many owners one reply will notify. Asking three
// people to do a thing gets it done; asking thirty is a broadcast.
const mostOwnersMentioned = 3

// notifying writes handles so that GitHub notifies their owners: the one
// place in this package that does, see OutsideRequest. Not to be confused
// with mentions above, which writes a handle in backticks precisely so that
// it does not.
func notifying(handles []string) string {
	// Bounded, because a large organisation would otherwise make one
	// paragraph that notifies everybody holding owner rights.
	if len(handles) > mostOwnersMentioned {
		handles = handles[:mostOwnersMentioned]
	}
	named := make([]string, 0, len(handles))
	for _, handle := range handles {
		named = append(named, "@"+handle)
	}
	switch len(named) {
	case 0:
		// Guarded against by every caller, and guarded here too: the last
		// branch indexes named[:len-1], which would panic.
		return ""
	case 1:
		return named[0]
	case 2:
		return named[0] + " and " + named[1]
	default:
		return strings.Join(named[:len(named)-1], ", ") + " and " + named[len(named)-1]
	}
}
