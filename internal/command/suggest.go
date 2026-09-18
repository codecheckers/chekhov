package command

import (
	"fmt"
	"strings"
)

// Reading `suggest codecheckers ...`.
//
// `suggest` alone would be a command that could mean anything later - suggest a
// certificate identifier, suggest a venue - so what to suggest is the first
// argument, checked here rather than guessed at.

// ParseSuggestion reads the arguments of `suggest`: what to suggest, and the
// hints to suggest it from.
func ParseSuggestion(args []string) (hints []string, err error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("suggest what? I can `%s suggest codecheckers`", Bot)
	}
	switch strings.TrimSuffix(strings.ToLower(args[0]), "s") {
	case "codechecker":
		return args[1:], nil
	default:
		return nil, fmt.Errorf("I can suggest `codecheckers`, not %q", args[0])
	}
}

// BodyText is the comment under the command line: an abstract, or a data and
// software availability statement, pasted for the bot to read.
//
// The command is the first line by convention, so everything after it is the
// writer's own prose. Only the first non-empty line is the command, so the
// lines before it - a blank line, nothing else - are dropped with it.
func BodyText(comment string) string {
	lines := strings.Split(comment, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return ""
}
