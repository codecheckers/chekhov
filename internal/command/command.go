// Package command parses the commands people address to the bot in a comment
// on the checks issue.
//
// The convention is the one the Open Journals bots established, and it is
// deliberate rather than incidental: the command is on the first line of the
// comment, and every later line that begins with the mention is another (see
// Split). A comment that mentions the bot halfway through a sentence is
// conversation, not an instruction, and is ignored.
package command

import (
	"fmt"
	"strings"
)

// Name of a command the bot knows.
type Name string

const (
	// Commands lists what the bot understands.
	Commands Name = "commands"
	// Hello is the liveness check.
	Hello Name = "hello"
	// Thanks acknowledges being thanked.
	Thanks Name = "thanks"
	// Goodbye signs off when a check ends. It is manners only: `check finish`
	// is what moves the check on.
	Goodbye Name = "goodbye"
	// Version reports which build is answering, and which register it works
	// on.
	Version Name = "version"
	// TeamsRefresh reads the organisation's teams again, for an editor who
	// has just changed one.
	//
	// The phrases are subject first, then what to do with it - `teams
	// refresh`, `certificate next`, `codecheckers suggest` - so that the
	// commands about one thing sit together, in the listing and in somebody's
	// head (#56). The verb-first forms they replaced are aliases, kept so that
	// nobody who learned them is turned away.
	TeamsRefresh Name = "teams refresh"
	// Assign gives somebody a role on this check.
	Assign Name = "assign"
	// Remove takes a role away again.
	Remove Name = "remove"
	// ListRoles says who holds which role on this check. Named for what it
	// does rather than for the word people type, because Roles is the set of
	// roles one person holds.
	ListRoles Name = "roles"
	// RecordAccept adopts a record of a check the bot did not write.
	RecordAccept Name = "record accept"
	// ListCodecheckers points at the lists of codecheckers. Named for what it
	// does rather than for the word people type, as ListRoles is.
	ListCodecheckers Name = "codecheckers"
	// CodecheckersSuggest proposes who could check this paper.
	CodecheckersSuggest Name = "codecheckers suggest"
	// Nudge walks the register for what earlier replies left outstanding, and
	// acts on it.
	Nudge Name = "nudge"
	// Remind asks the bot to mention somebody on this check after some days.
	Remind Name = "remind"
	// Check validates the codecheck.yml of the repository under check.
	Check Name = "check"
	// CheckStart begins a check: the labels move, the repository is described
	// and the codechecker gets their checklist. A phrase, because it is a
	// subcommand of Check with a role of its own; see the registry.
	CheckStart Name = "check start"
	// CheckReady says whether what an author provides before a check starts
	// is there: a README, a codecheck.yml with a manifest, a licence and a
	// paper. A phrase, because it is a subcommand of Check.
	CheckReady Name = "check ready"
	// CheckZenodo audits the certificate's Zenodo record, or its draft, against
	// the curation policy of the CODECHECK community. A phrase, because it is
	// a subcommand of Check.
	CheckZenodo Name = "check zenodo"
	// CheckFinish ends one: the rules are run, the labels move, and the
	// register pull request is the next step.
	CheckFinish Name = "check finish"
	// Status says where a check stands. Its own command rather than
	// `check status`, because it reports rather than judges.
	Status Name = "status"
	// Rules says which rule catalogue this build judges by, and whether it is
	// the register's current one.
	Rules Name = "rules"
	// Announce toots about a published certificate.
	Announce Name = "announce"
	// Follow follows the fediverse accounts a certificate names, and curates
	// the Codecheckers/Authors/Venues Mastodon collections.
	Follow Name = "follow"
	// Certificate says which identifier this check holds.
	Certificate Name = "certificate"
	// CertificateNext says which certificate identifier comes next.
	CertificateNext Name = "certificate next"
	// CertificateSet reserves a certificate identifier for this check.
	CertificateSet Name = "certificate set"
	// RepositorySet and VenueSet record what the register row says where the
	// issue does not (#21).
	RepositorySet Name = "repository set"
	VenueSet      Name = "venue set"
	// CheckRegister builds the register row this check would get and runs the
	// rules on it, writing nothing; the rules on a row already in register.csv
	// are `check certificate` (#21).
	CheckRegister Name = "check register"
	// Register does what `check register` does and then opens the pull
	// request adding the row, for an editor to merge (#21).
	Register Name = "register"
	// IncludeCheck reads a registration as a codechecker on the lists
	// repository and says what it still needs (#49). A phrase, because
	// `include` itself - adding the person - is the editors' and comes next.
	IncludeCheck Name = "include check"
	// Include adds a registered codechecker: the pull request adding their
	// row to the list, and the team once they are in the organisation (#49).
	Include Name = "include"
	// IncludeOpen opens a registration for somebody, asking for their row
	// (#49): what the sweep does when somebody an `assign` asked the owners
	// for accepts, for an editor to ask by hand on the lists. For somebody
	// outside the organisation only when they have taken part in the issue
	// already (#68).
	IncludeOpen Name = "include open"
	// Invite is `include open` asked on a check: it opens the registration
	// on the lists and, for somebody outside the organisation, asks the
	// owners to invite them (#68).
	Invite Name = "invite"
	// Allowlist says who an editor has let task the bot on this check, and
	// AllowlistAdd and AllowlistRemove put somebody on that list and take them
	// off; see access.go.
	Allowlist       Name = "allowlist"
	AllowlistAdd    Name = "allowlist add"
	AllowlistRemove Name = "allowlist remove"
	// Unknown is a mention of the bot with something it does not understand.
	Unknown Name = "unknown"
)

// A Command is one instruction addressed to the bot.
type Command struct {
	Name Name
	// Raw is the command as written, without the bot mention, for the reply to
	// quote back when the command is not understood.
	Raw string
	// Args are the words after the command name.
	Args []string
	// Body is the part of the comment the command was given in: its own line
	// and the lines under it, up to the next command. What a command reads
	// below itself - a reminder's note, a pasted abstract - is read from
	// here. See Split.
	Body string
}

// Bot is the account the commands are addressed to.
const Bot = "@chekhovbot"

// Parse reads the first line of a comment.
//
// It returns false when the comment is not addressed to the bot at all, which
// is the common case on a busy issue and must stay cheap and silent.
func Parse(comment string) (Command, bool) {
	first := firstNonEmptyLine(comment)
	if first == "" {
		return Command{}, false
	}

	if !addressed(first) {
		return Command{}, false
	}
	fields := strings.Fields(first)

	rest := fields[1:]
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first), fields[0]))
	if len(rest) == 0 {
		return Command{Name: Unknown, Raw: raw}, true
	}

	// The registry decides what is a command, so that adding one is a single
	// entry rather than a case here and a line in the help text. "check",
	// "check codecheck.yml" and "check config" all mean the same thing; the
	// file name is the phrasing people will write, and is an argument.
	//
	// Two words before one, so that a subcommand wins over the command it
	// belongs to: `check start` is its own entry with its own role, and
	// `check config github::owner/repo` is still the `check` command with two
	// arguments, because "config" names no entry. A second word that names no
	// entry is therefore an argument as it always was - nothing the `check`
	// command used to accept has stopped being accepted.
	if len(rest) > 1 {
		if definition, found := Lookup(rest[0] + " " + rest[1]); found {
			return Command{Name: definition.Name, Raw: raw, Args: arguments(definition, rest[2:])}, true
		}
	}
	if definition, found := Lookup(rest[0]); found {
		return Command{Name: definition.Name, Raw: raw, Args: arguments(definition, rest[1:])}, true
	}
	return Command{Name: Unknown, Raw: raw, Args: rest[1:]}, true
}

// arguments are the words after the command, with the punctuation of the
// sentence the command was written in taken off the last of them:
// `assign @a-codechecker as codechecker.` names the role "codechecker"
// (register#244). The command word already loses it in Lookup; this is the
// same for whatever comes last, a role, a handle, a number of weeks.
//
// Not for a command whose last argument is a name written out in full -
// `venue set`, `register` - where the full stop may be the venue's own.
func arguments(definition Definition, args []string) []string {
	if len(args) == 0 || definition.NamesInFull {
		return args
	}
	last := len(args) - 1
	args[last] = strings.TrimRight(args[last], sentencePunctuation)
	if args[last] == "" {
		// A full stop on its own after a space: nothing to keep.
		return args[:last]
	}
	return args
}

// Split reads the commands in a comment: each line that begins with
// the bot's mention starts one, and the lines under it, up to the next, are
// its own - the note of a `remind`, the text pasted under a `suggest`. So
//
//	@chekhovbot assign @a-codechecker as codechecker
//	@chekhovbot assign @a-handling-editor as handling editor
//
// is two commands, answered in order (register#244).
//
// The comment still has to begin with a command, as Parse has always asked:
// a mention further down a comment that opened with something else is
// conversation. A line inside a code fence is never a command, so that a
// command quoted as an example under another is not run.
func Split(comment string) []Command {
	if !addressed(firstNonEmptyLine(comment)) {
		return nil
	}
	var sections []string
	var current []string
	// A fence is closed by a run of its own marker at least as long as the
	// one that opened it, as Markdown reads one: a ```` fence may quote a
	// ``` one, and the command inside both is still an example.
	fence := ""
	started := false
	for _, line := range strings.Split(comment, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence == "" && fenceMarker(trimmed) != "":
			fence = fenceMarker(trimmed)
		case fence != "" && strings.HasPrefix(trimmed, fence):
			fence = ""
		case fence == "" && addressed(line) && !indentedCode(line):
			// The blank lines before the first command are its own, not a
			// command of their own.
			if started {
				sections = append(sections, strings.Join(current, "\n"))
				current = nil
			}
			started = true
		}
		current = append(current, line)
	}
	sections = append(sections, strings.Join(current, "\n"))

	commands := make([]Command, 0, len(sections))
	for _, section := range sections {
		parsed, _ := Parse(section)
		parsed.Body = section
		commands = append(commands, parsed)
	}
	return commands
}

// fenceMarker is the run of backticks or tildes that opens a code fence on
// this line, or nothing.
func fenceMarker(trimmed string) string {
	for _, mark := range []string{"`", "~"} {
		run := len(trimmed) - len(strings.TrimLeft(trimmed, mark))
		if run >= 3 {
			return strings.Repeat(mark, run)
		}
	}
	return ""
}

// indentedCode says a line is indented as Markdown's code blocks are, four
// spaces or a tab: a command written there is shown, not given.
func indentedCode(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}

// MostCommands is how many commands one comment may give. Each is a reply,
// and replies are writes on the one queue every editor shares: a comment of
// two hundred lines would hold everybody else up for ten minutes, and a burst
// of comments is what GitHub's spam detection hid the bot for once.
const MostCommands = 5

// Capped is the commands of a comment that are run, and the note naming those
// that are not when there are more than MostCommands - empty when all are.
func Capped(commands []Command) ([]Command, string) {
	if len(commands) <= MostCommands {
		return commands, ""
	}
	return commands[:MostCommands], tooManyCommands(commands[MostCommands:])
}

func tooManyCommands(skipped []Command) string {
	var out strings.Builder
	fmt.Fprintf(&out, "⚠ This comment gives %d commands, and I run at most %d from one comment. "+
		"I am running the first %d; these I have not run:\n\n", MostCommands+len(skipped), MostCommands, MostCommands)
	for _, parsed := range skipped {
		fmt.Fprintf(&out, "- %s\n", Code(strings.TrimSpace(firstNonEmptyLine(parsed.Body))))
	}
	out.WriteString("\nPut them in a comment of their own to have them run.\n")
	return out.String()
}

// addressed says a line is a command: it begins with the bot's mention. One
// test for Parse and Split, so that they cannot disagree on what starts one.
func addressed(line string) bool {
	fields := strings.Fields(line)
	return len(fields) > 0 && strings.EqualFold(fields[0], Bot)
}

// firstNonEmptyLine returns the first line with anything on it, so that a
// comment starting with a blank line still works.
func firstNonEmptyLine(comment string) string {
	for _, line := range strings.Split(comment, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
