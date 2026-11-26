package command

import (
	"fmt"
	"strings"
	"time"
)

// Who may task the bot at all, as opposed to which commands their roles let
// them run: what it says to somebody it takes no commands from, and what it
// says about the allowlist, the one way an editor lets somebody else in. The
// decision itself is internal/bot/gate.go.

// turnedAway and notYet open a refusal, before the handle: the first when the
// bot looked at everything that could have let somebody in, the second when it
// could not. They open differently so that only the first counts as having
// told somebody. IsTurnedAway and the check that nobody is told twice both read
// them, so a refusal written by an earlier build is still known as one: change
// them and they are not.
const (
	turnedAway = "I have not run that, `@"
	notYet     = "I have not run that yet, `@"
)

// TurnedAwayOpening is how a refusal to one person begins; unread is one made
// without being able to look at everything.
func TurnedAwayOpening(handle string, unread bool) string {
	opening := turnedAway
	if unread {
		opening = notYet
	}
	return opening + Handle(handle) + "`: "
}

// IsTurnedAway says a comment of the bot's is a refusal to somebody it takes
// no commands from, which answers nothing anybody asked on the issue: it is
// not the answer to a registrant's row, nor the last word on the labels.
func IsTurnedAway(body string) bool {
	return strings.HasPrefix(body, turnedAway) || strings.HasPrefix(body, notYet)
}

// A Refused is who was turned away and where, which decides who the reply
// says may ask instead.
type Refused struct {
	Handle       string
	Organisation string
	// OnLists is a refusal on the codechecker lists, which have no record of
	// a check and no allowlist.
	OnLists bool
	// PullRequest is a refusal on a pull request, where having opened it or
	// being assigned to it counts for nothing.
	PullRequest bool
	// Unread says something that could have let them in could not be read:
	// the organisation, the record of the check, or whom a registration is for.
	Unread bool
	// Edited says the record of the check was edited, and so lets nobody in.
	Edited bool
}

// TurnedAway is the one reply to somebody the bot takes no commands from. The
// handle is in backticks, so that refusing somebody does not notify them.
func TurnedAway(r Refused) string {
	organisation := Code(r.Organisation)
	if r.Organisation == "" {
		organisation = "CODECHECK"
	}
	place, opener := "issue", ", whoever opened it or is assigned to it"
	if r.PullRequest {
		place, opener = "pull request", ""
	}
	var out strings.Builder
	out.WriteString(TurnedAwayOpening(r.Handle, r.Unread))
	fmt.Fprintf(&out, "on this %s I take commands only from members of the %s organisation%s",
		place, organisation, opener)
	switch {
	case !r.OnLists:
		out.WriteString(", the people on its record, and those an editor has added.")
		// An edited record takes no addition until it is adopted.
		if !r.Edited {
			fmt.Fprintf(&out, " An editor can add you with `%s %s %s`.", Bot, AllowlistAdd, Handle(r.Handle))
		}
		out.WriteString("\n")
	case r.PullRequest:
		out.WriteString(".\n")
	default:
		out.WriteString(", and the person a registration is for.\n")
	}
	if r.Edited {
		fmt.Fprintf(&out, "\nThe record of this check was edited after I wrote it, so it lets nobody in "+
			"until an editor runs `%s %s`.\n", Bot, RecordAccept)
	}
	if r.Unread {
		out.WriteString("\nI could not look up everything that says whether you may, so asking again later may help.\n")
	}
	return out.String()
}

// ParseAllowlisted reads the one person `allowlist add` and `allowlist
// remove` are given.
func ParseAllowlisted(name Name, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("one person at a time: `%s %s @handle`", Bot, name)
	}
	return parseHandle(args[0])
}

// AllowlistUsage answers `allowlist` given something it does not take:
// `allowlist @user` adds nobody, as a command that lets somebody in should
// say what it does.
func AllowlistUsage() string {
	return fmt.Sprintf("`%s %s` says who has been added and takes nothing after it. "+
		"An editor adds somebody with `%s %s @handle`, and takes them off with `%s %s @handle`.\n",
		Bot, Allowlist, Bot, AllowlistAdd, Bot, AllowlistRemove)
}

// AllowlistReply says who an editor let task the bot on this check, and that
// the list is not everybody who may.
func AllowlistReply(allowed []string, organisation string, pullRequest bool, told Provenance) string {
	var out strings.Builder
	if len(allowed) == 0 {
		out.WriteString("Nobody has been added to the allowlist of this check.\n")
	} else {
		fmt.Fprintf(&out, "On the allowlist of this check: %s\n", mentions(allowed))
	}
	opener := " whoever opened this issue or is assigned to it,"
	if pullRequest {
		opener = ""
	}
	fmt.Fprintf(&out, "\nMembers of %s,%s and the people on its record may ask me here without being on it.\n",
		Code(organisation), opener)
	out.WriteString(told.Sentence())
	return out.String()
}

// AllowedReply says what `allowlist add` did. The reply is the record of who
// let them in, and when.
func AllowedReply(handle string, added bool, by string, when time.Time) string {
	if !added {
		return fmt.Sprintf("%s is on the allowlist of this check already.\n", mention(handle))
	}
	return fmt.Sprintf("✔ %s may now ask me on this check. This gives them no role.\n", mention(handle)) +
		entry("allowed", by, when)
}

// DisallowedReply says what `allowlist remove` did, and that the list is not
// the only way somebody may ask.
func DisallowedReply(handle string, held, pullRequest bool, by string, when time.Time) string {
	if !held {
		return fmt.Sprintf("%s was not on the allowlist of this check.\n", mention(handle))
	}
	opener := " opened the issue or are assigned to it,"
	if pullRequest {
		opener = ""
	}
	return fmt.Sprintf("✔ %s is off the allowlist of this check. They may still ask me if they are a member,"+
		"%s or are on its record.\n", mention(handle), opener) +
		entry("disallowed", by, when)
}
