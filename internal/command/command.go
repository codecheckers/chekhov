// Package command parses the commands people address to the bot in a comment
// on the checks issue.
//
// The convention is the one the Open Journals bots established, and it is
// deliberate rather than incidental: the command is on the first line of the
// comment, and only the first command in a comment is acted on. A comment that
// mentions the bot halfway through a sentence is conversation, not an
// instruction, and is ignored.
package command

import (
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
	// Version reports which build is answering, and which register it works
	// on.
	Version Name = "version"
	// Refresh reads the organisation's teams again, for an editor who has
	// just changed one. The command takes what to refresh as an argument
	// rather than being the phrase `refresh teams`, which the registry would
	// now allow: there is nothing else to refresh, so the second word gates
	// nothing. See Definition.
	Refresh Name = "refresh"
	// Assign gives somebody a role on this check.
	Assign Name = "assign"
	// Remove takes a role away again.
	Remove Name = "remove"
	// ListRoles says who holds which role on this check. Named for what it
	// does rather than for the word people type, because Roles is the set of
	// roles one person holds.
	ListRoles Name = "roles"
	// Accept adopts a record of a check the bot did not write.
	Accept Name = "accept"
	// ListCodecheckers points at the lists of codecheckers. Named for what it
	// does rather than for the word people type, as ListRoles is.
	ListCodecheckers Name = "codecheckers"
	// SuggestCodecheckers proposes who could check this paper. The word people
	// type is `suggest`, with what to suggest as an argument, as
	// `refresh teams` is.
	SuggestCodecheckers Name = "suggest"
	// Nudge walks the register for what earlier replies left outstanding, and
	// acts on it.
	Nudge Name = "nudge"
	// Check validates the codecheck.yml of the repository under check.
	Check Name = "check"
	// CheckStart begins a check: the labels move, the repository is described
	// and the codechecker gets their checklist. A phrase, because it is a
	// subcommand of Check with a role of its own; see the registry.
	CheckStart Name = "check start"
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
	// Next says which certificate identifier comes next. The word people type
	// is `next`, with what as an argument - `next certificate` - as
	// `refresh teams` is.
	Next Name = "next"
	// Set reserves a certificate identifier for this check. A phrase, as
	// `set repository` and `set venue` are: the second word decides what is
	// set and how the rest is read.
	Set Name = "set certificate"
	// SetRepository and SetVenue record what the register row says where the
	// issue does not (#21).
	SetRepository Name = "set repository"
	SetVenue      Name = "set venue"
	// CheckRegister builds the register row this check would get and runs the
	// rules on it, writing nothing; the rules on a row already in register.csv
	// are `check certificate` (#21).
	CheckRegister Name = "check register"
	// Register does what `check register` does and then opens the pull
	// request adding the row, for an editor to merge (#21).
	Register Name = "register"
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

	fields := strings.Fields(first)
	if len(fields) == 0 || !strings.EqualFold(fields[0], Bot) {
		return Command{}, false
	}

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
			return Command{Name: definition.Name, Raw: raw, Args: rest[2:]}, true
		}
	}
	if definition, found := Lookup(rest[0]); found {
		return Command{Name: definition.Name, Raw: raw, Args: rest[1:]}, true
	}
	return Command{Name: Unknown, Raw: raw, Args: rest[1:]}, true
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
