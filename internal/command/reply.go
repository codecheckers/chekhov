package command

import (
	"fmt"
	"strings"

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
func Listing(role Role) string {
	var out strings.Builder
	out.WriteString("**Commands I understand**\n")

	visible := Visible(role)
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
		fmt.Fprintf(&out, "I do not know the command `%s`.\n", parsed.Raw)
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
	// Enabled says whether this deployment may follow accounts and manage
	// lists at all.
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

	// Added is filled by confirm: the handles newly put on each list, keyed
	// by its title.
	Added map[string][]string
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
		out.WriteString("\nFollowing is switched off for this deployment, so `confirm` will not follow anyone or change any list.\n")
	case len(f.Codecheckers)+len(f.Authors) == 0 && f.Venue == "":
		out.WriteString("\nNobody on this certificate has a fediverse account on record.\n")
	default:
		fmt.Fprintf(&out, "\nTo follow whoever is not yet followed, and keep the Codecheckers, Authors and Venues lists in sync, write:\n\n    %s follow %s confirm\n",
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
		if added := f.Added[title]; len(added) > 0 {
			fmt.Fprintf(&out, "- added to the %s list: %s\n", title, codeList(added))
			any = true
		}
	}
	if !any {
		out.WriteString("- lists: already up to date\n")
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
