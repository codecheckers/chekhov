package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// remindAs runs `remind` as somebody, in a comment of theirs on the issue,
// with the lines under the command as body, and puts what the bot answered on
// the issue, as the reply path would.
func remindAs(t *testing.T, server *Server, replies *organisation, author, body string, args ...string) string {
	t.Helper()
	replies.thread = append(replies.thread, github.Comment{ID: int64(len(replies.thread) + 1),
		Author: author, Body: body})
	result := server.answer(context.Background(), mention{
		Repository: server.Settings.TargetRepository(), Issue: 1, Author: author, Body: body,
		Comment: int64(len(replies.thread)),
	}, command.Command{Name: command.Remind, Args: args})
	posted, err := server.bodyOf(result)
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(posted)
	return posted
}

// reminderDue puts a reminder on the issue that was set some days ago and is
// due today - a second earlier than the one put on before it, so that two
// put on in one test are two reminders, as two set apart would be.
func reminderDue(t *testing.T, server *Server, replies *organisation, handle, note string) {
	t.Helper()
	block, err := followup.Render(followup.Record{
		Kind: followup.KindReminder, Check: "codecheckers/testing-dev-register#1",
		Subject: handle, By: "nuest", Note: note,
		Due:   time.Now().UTC().Add(-time.Duration(len(replies.thread)) * time.Second).Format(time.RFC3339),
		Asked: time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339),
	}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(block + "I will remind them")
}

func TestRemindMeKeepsTheReminderInTheReply(t *testing.T) {
	server, replies := organised(t)

	posted := remindAs(t, server, replies, "a-codechecker",
		"@chekhovbot remind me in 2 weeks\nask whether the data is public yet", "me", "in", "2", "weeks")

	due := time.Now().UTC().AddDate(0, 0, 14)
	for _, want := range []string{"I will remind you here on " + due.Format("Monday 2 January 2006"),
		"> ask whether the data is public yet", "dropped if this issue is closed"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the reply does not say %q:\n%s", want, posted)
		}
	}
	record, err := followup.Parse(posted, "codecheckers/testing-dev-register#1", server.signer())
	if err != nil {
		t.Fatalf("the reply carries no record: %v", err)
	}
	if record.Kind != followup.KindReminder || record.Subject != "a-codechecker" ||
		!strings.HasPrefix(record.Due, due.Format(time.DateOnly)) || record.Note != "ask whether the data is public yet" {
		t.Errorf("record = %+v", record)
	}
}

// A codechecker who is not assigned to this check may remind themselves, and
// nobody else.
func TestRemindingSomebodyElseIsForEditorsAndTheAssignedCodechecker(t *testing.T) {
	server, replies := organised(t)

	refused := remindAs(t, server, replies, "a-codechecker", "", "@nuest", "in", "2", "days")
	if !strings.Contains(refused, "is for editors or the assigned codechecker") {
		t.Errorf("an unassigned codechecker reminded somebody else:\n%s", refused)
	}
	if _, err := followup.Parse(refused, "", server.signer()); err != followup.ErrNoRecord {
		t.Errorf("a refusal carries a record: %v", err)
	}

	editor := remindAs(t, server, replies, "nuest", "", "@a-codechecker", "in", "2", "days")
	if !strings.Contains(editor, "I will remind `@a-codechecker`") {
		t.Errorf("an editor could not remind the codechecker:\n%s", editor)
	}

	answer(t, server, command.Assign, []string{"@a-codechecker", "as", "assigned", "codechecker"}, "")
	assigned := remindAs(t, server, replies, "a-codechecker", "", "@nuest", "in", "2", "days")
	if !strings.Contains(assigned, "I will remind `@nuest`") {
		t.Errorf("the assigned codechecker could not remind the editor:\n%s", assigned)
	}
}

func TestARemindWithoutAnIssueIsRefused(t *testing.T) {
	server, _ := organised(t)
	reply := server.answer(context.Background(), mention{
		Repository: server.Settings.TargetRepository(), Author: "nuest",
	}, command.Command{Name: command.Remind, Args: []string{"me", "in", "2", "days"}})
	if !strings.Contains(reply.body, "ask me on a checks issue") || reply.follow != nil {
		t.Errorf("a remind outside an issue was kept: %+v", reply)
	}
}

func TestAReminderWaitsForItsDay(t *testing.T) {
	server, replies := organised(t)
	remindAs(t, server, replies, "nuest", "", "me", "in", "1", "day")
	before := replies.count()

	swept := server.nudge(context.Background())
	if swept.Acted != 0 || swept.Outstanding != 1 || replies.count() != before {
		t.Errorf("swept %+v, want the reminder left waiting", swept)
	}
}

// On its day the reminder is posted, mentioning the person, and settled: the
// next sweep says nothing.
func TestAReminderComesOnItsDayAndOnce(t *testing.T) {
	server, replies := organised(t)
	reminderDue(t, server, replies, "a-codechecker", "the data, @a-handling-editor")

	swept := server.nudge(context.Background())
	if swept.Acted != 1 || swept.Outstanding != 0 {
		t.Fatalf("swept %+v, want the reminder posted", swept)
	}
	posted := replies.last()
	for _, want := range []string{"@a-codechecker, here is the reminder `@nuest` asked for",
		"> the data, &#64;a-handling-editor"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the reminder does not say %q:\n%s", want, posted)
		}
	}

	before := replies.count()
	if swept := server.nudge(context.Background()); swept.Acted != 0 || replies.count() != before {
		t.Errorf("the second sweep reminded again: %+v\n%s", swept, replies.last())
	}
}

// Two reminders for one person are two reminders: settling the first must
// not settle the second.
func TestTwoRemindersForOnePersonAreKeptApart(t *testing.T) {
	server, replies := organised(t)
	remindAs(t, server, replies, "nuest", "", "me", "in", "3", "weeks")
	reminderDue(t, server, replies, "nuest", "")

	swept := server.nudge(context.Background())
	if swept.Acted != 1 || swept.Outstanding != 1 {
		t.Errorf("swept %+v, want one posted and one still waiting", swept)
	}
}

func TestAClosedCheckDropsItsReminders(t *testing.T) {
	server, replies := organised(t)
	reminderDue(t, server, replies, "a-codechecker", "")
	replies.issues = nil
	replies.closed = []github.Issue{{Number: 1, Closed: true}}

	if swept := server.nudge(context.Background()); swept.Acted != 0 || replies.count() != 0 {
		t.Errorf("a closed check was reminded: %+v\n%s", swept, replies.last())
	}
}

func TestStatusListsAReminderByItsDay(t *testing.T) {
	line := command.Outstanding{Kind: followup.KindReminder, Subject: "a-codechecker",
		Since: time.Now(), Due: "2026-11-16T09:30:00Z"}.Line()
	if line != "reminder for `@a-codechecker`, due 2026-11-16" {
		t.Errorf("status line %q", line)
	}
}

// Two reminders set on one day for one person are two reminders, each with
// its own reason: the second must not replace the first.
func TestTwoRemindersOnOneDayAreBothKept(t *testing.T) {
	server, replies := organised(t)
	reminderDue(t, server, replies, "nuest", "the first")
	reminderDue(t, server, replies, "nuest", "the second")

	if swept := server.nudge(context.Background()); swept.Acted != 2 {
		t.Errorf("swept %+v, want both reminders posted", swept)
	}
}

func TestNoReminderOnAClosedCheck(t *testing.T) {
	server, replies := organised(t)
	replies.issues = []github.Issue{{Number: 1, Closed: true}}

	reply := remindAs(t, server, replies, "nuest", "", "me", "in", "2", "days")
	if !strings.Contains(reply, "This check is closed") {
		t.Errorf("a reminder was promised on a closed check:\n%s", reply)
	}
}

// A reminder notifies whom it names, so it may name only somebody the check
// has already brought in: here, nobody has heard of `@a-stranger`.
func TestNoReminderForSomebodyNotOnTheCheck(t *testing.T) {
	server, replies := organised(t)

	reply := remindAs(t, server, replies, "nuest", "", "@a-stranger", "in", "2", "days")
	if !strings.Contains(reply, "I will not remind them here") {
		t.Errorf("a stranger was to be reminded:\n%s", reply)
	}

	replies.issues = []github.Issue{{Number: 1, Author: "a-stranger"}}
	reply = remindAs(t, server, replies, "nuest", "", "@a-stranger", "in", "2", "days")
	if !strings.Contains(reply, "I will remind `@a-stranger`") {
		t.Errorf("the person who opened the issue could not be reminded:\n%s", reply)
	}
}

func TestOnlySoManyRemindersWait(t *testing.T) {
	server, replies := organised(t)
	for i := 1; i <= command.MostWaitingReminders; i++ {
		remindAs(t, server, replies, "nuest", "", "me", "in", fmt.Sprint(i), "days")
	}

	reply := remindAs(t, server, replies, "nuest", "", "me", "in", "30", "days")
	if !strings.Contains(reply, "as many as I keep") {
		t.Errorf("an eleventh reminder was kept:\n%s", reply)
	}
}
