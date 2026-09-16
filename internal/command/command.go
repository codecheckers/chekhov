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
	// Check validates the codecheck.yml of the repository under check.
	Check Name = "check"
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

	switch strings.ToLower(rest[0]) {
	case "check":
		// "check", "check codecheck.yml" and "check config" all mean the same
		// thing; the file name is the phrasing people will write.
		return Command{Name: Check, Raw: raw, Args: rest[1:]}, true
	default:
		return Command{Name: Unknown, Raw: raw, Args: rest[1:]}, true
	}
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
