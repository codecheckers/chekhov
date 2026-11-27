package command

import (
	"fmt"
	"strings"
	"time"
)

// The replies of `log` and `log sweep` (#73). What the bot logged goes to the
// private log repository and never here: the register is public, and a record
// names people, what a service answered, and the build. The reply on the check
// is the link, which tells nobody without access anything.

// keptLayout is how a reply says how far back the bot's records reach.
const keptLayout = "2006-01-02 15:04 UTC"

// LogWritten is the reply once the records are in the log repository: where,
// how many and from when - the newest that fit, which may not reach back to
// the bot's start - and whether the issue there is assigned to whoever asked:
// notAssigned is why not, empty when it is.
func LogWritten(repository string, issue, records int, from time.Time, notAssigned string) string {
	reply := fmt.Sprintf("The bot's records are in the private log repository: %s#%d - the newest %d, "+
		"from %s on.", repository, issue, records, from.UTC().Format(keptLayout))
	switch notAssigned {
	case "":
		return reply + " The issue there is assigned to you.\n"
	case AssigneeIgnored:
		// GitHub leaves out somebody who may not be assigned there, which is
		// somebody without access to the repository.
		return reply + " GitHub would not assign the issue there to you, which it does for an account " +
			"without access to the repository: ask the organisation's owners for it.\n"
	}
	return reply + " The issue there could not be assigned to you: " + strings.TrimSuffix(notAssigned, ".") + ".\n"
}

// LogNothing is the reply when the bot has kept nothing about it: nothing is
// written. kept is how far back what it keeps reaches.
func LogNothing(kept time.Time) string {
	return fmt.Sprintf("The bot has kept nothing about this since %s, so nothing was written to the "+
		"log repository.\n", kept.UTC().Format(keptLayout))
}

// LogUnavailable is the reply when the records cannot go anywhere safe:
// there is no log repository, GitHub does not say it is private, or writing
// failed. Nothing of the records is ever said here instead.
func LogUnavailable(why string) string {
	return "The bot's records could not be put in the private log repository: " + why +
		". They are in the deployment's log; nothing of them is written here.\n"
}
