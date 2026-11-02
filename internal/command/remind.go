package command

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Reminders (codecheckers/chekhov#25): `remind @user in 2 weeks`, and what
// the bot says when it is set and when it is due.

const (
	// mostReminderDays is how far ahead a reminder may be set. A check that
	// needs reminding in more than a year needs something other than a
	// reminder.
	mostReminderDays = 365
	// MostWaitingReminders is how many reminders may wait on one issue. Each
	// is a write on the one queue every command of every editor shares, and
	// anybody may set one for themselves, so a burst of them must not be able
	// to hold that queue up - or look, to GitHub, like a bot posting spam.
	MostWaitingReminders = 10
	// MostReminderNote is how long a reason may be. It is kept in a signed
	// record and quoted back in full, so it is a note, not a letter.
	MostReminderNote = 500
)

// A Reminder is what `remind` was asked: whom, and in how many days.
type Reminder struct {
	// Handle is who to remind, normalised as every handle the bot keeps.
	Handle string
	// Days is how far ahead, a week being seven.
	Days int
}

// ParseReminder reads `me in 10 days` and `@user in 2 weeks`, the arguments
// of remind. author is who wrote the command, which is who "me" is.
//
// "in" is optional, because half the people who write the command will leave
// it out, and "day" and "week" are read in the singular too.
func ParseReminder(args []string, author string) (Reminder, error) {
	example := fmt.Sprintf("For example `%s remind me in 2 weeks`, or `%s remind @a-codechecker in 10 days`",
		Bot, Bot)
	if len(args) == 0 {
		return Reminder{}, fmt.Errorf("whom, and when? %s", example)
	}

	written := args[0]
	if strings.EqualFold(written, "me") {
		written = author
	}
	handle, err := parseHandle(written)
	if err != nil {
		return Reminder{}, err
	}
	reminder := Reminder{Handle: handle}

	rest := args[1:]
	if len(rest) > 0 && strings.EqualFold(rest[0], "in") {
		rest = rest[1:]
	}
	if len(rest) != 2 {
		return Reminder{}, fmt.Errorf("in how long? %s", example)
	}
	n, err := strconv.Atoi(rest[0])
	if err != nil || n < 1 {
		return Reminder{}, fmt.Errorf("%s is not a number of days or weeks I can count. %s", Code(rest[0]), example)
	}
	switch strings.ToLower(rest[1]) {
	case "day", "days":
		reminder.Days = n
	case "week", "weeks":
		reminder.Days = 7 * n
	default:
		return Reminder{}, fmt.Errorf("I count in days or weeks, not in %s. %s", Code(rest[1]), example)
	}
	if reminder.Days > mostReminderDays {
		return Reminder{}, fmt.Errorf("a reminder can be at most %d days ahead", mostReminderDays)
	}
	return reminder, nil
}

// ReminderNote is the reason given on the lines under the command, or an
// error when it is too long to keep. inIssue says the command opens an issue
// description, whose lines below are the issue rather than a reason, so it
// has none.
func ReminderNote(comment string, inIssue bool) (string, error) {
	if inIssue {
		return "", nil
	}
	note := BodyText(comment)
	if length := utf8.RuneCountInString(note); length > MostReminderNote {
		return "", fmt.Errorf("the reason under the command is %d characters long, "+
			"and a reminder keeps at most %d", length, MostReminderNote)
	}
	return note, nil
}

// RemindOthers is who may set a reminder for somebody else. The registry's
// entry is for anybody, because anybody may remind themselves, and the
// registry has no way to say "and these roles for somebody else"; this is
// that second answer, as a Definition, so that the check and the refusal
// come from one union of roles. A second command with a `me` form would be
// the time to give the registry a field for it.
var RemindOthers = Definition{Name: Remind, Role: RoleEditor, Or: []Role{RoleAssignedCodechecker}}

// RemindOthersRefused is the answer to somebody setting a reminder for
// somebody else who may not.
func RemindOthersRefused() string {
	return fmt.Sprintf("A reminder for somebody else is for %s. `%s remind me in …` is for anybody.\n",
		RemindOthers.Who(), Bot)
}

// ReminderOnAClosedCheck is the answer to `remind` on an issue that is
// closed: the sweep does not read a closed check for reminders, so promising
// one would be promising nothing.
func ReminderOnAClosedCheck() string {
	return "This check is closed, and I do not remind anybody on a closed check. " +
		"Reopen it first if there is still something to do.\n"
}

// ReminderNotOnTheCheck is the answer to a reminder for somebody who has
// nothing to do with this check: the reminder notifies them, and the bot does
// not notify strangers on anybody's behalf.
func ReminderNotOnTheCheck(handle string) string {
	return fmt.Sprintf("%s has not opened, commented on, been assigned to or been given a role on "+
		"this check, so I will not remind them here.\n", mention(handle))
}

// TooManyReminders is the answer when the issue already holds as many
// waiting reminders as it may.
func TooManyReminders() string {
	return fmt.Sprintf("%d reminders are already waiting on this check, which is as many as I keep; "+
		"`%s status` lists them.\n", MostWaitingReminders, Bot)
}

// ReminderUnkept is the answer on a deployment that cannot sign a record, so
// nothing would come back to remind anybody.
func ReminderUnkept() string {
	return "I cannot keep reminders here: this deployment has no key to sign them with, " +
		"and an unsigned reminder is not one I would trust later.\n"
}

// ReminderSet confirms a reminder. The handle is in backticks: whoever was
// named has been notified by the command itself, and is notified again when
// the reminder is due, which is the time it is for.
func ReminderSet(r Reminder, author string, due time.Time, note string) string {
	who := mention(r.Handle)
	if r.Handle == Handle(author) {
		who = "you"
	}
	lead := fmt.Sprintf("I will remind %s here on %s", who, due.UTC().Format("Monday 2 January 2006"))
	return withQuote(lead, ", quoting", note) +
		"\nThe reminder is dropped if this issue is closed before then.\n"
}

// ReminderDue is the reminder itself, which mentions the person so that they
// are notified: that is the whole of what they asked for.
func ReminderDue(handle, by string, asked time.Time, note string) string {
	who := "you"
	if by != "" && by != handle {
		who = mention(by)
	}
	when := "earlier"
	if !asked.IsZero() {
		when = "on " + asked.UTC().Format("2 January 2006")
	}
	lead := fmt.Sprintf("%s, here is the reminder %s asked for %s", notifying([]string{handle}), who, when)
	return withQuote(lead, "", note)
}

// withQuote ends a sentence: with the note quoted under it, introduced by
// what comes before the colon, or with a full stop when there is none.
func withQuote(sentence, introduction, note string) string {
	if note == "" {
		return sentence + ".\n"
	}
	return sentence + introduction + ":\n\n" + quoted(note)
}

// quoted is somebody's words as a block quote, with no at sign in them able
// to notify: the note was written in a comment that notified whoever it
// named, and quoting it later must not notify them a second time. Only the
// person being reminded is mentioned so that it notifies.
//
// Every at sign, including one in a code span, where the entity is shown as
// written rather than as an @. A reason is a sentence far more often than it
// is code, and telling the two apart would mean parsing markdown here.
func quoted(note string) string {
	note = withoutMentions(note)
	lines := strings.Split(strings.TrimSpace(note), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n") + "\n"
}
