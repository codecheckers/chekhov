package command

import (
	"strings"
)

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
