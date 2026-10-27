package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// Noticing a label that no longer describes the check, in the commands that
// already run. The table itself is tested in internal/command; what is tested
// here is the reading of the world it judges, and that the note reaches a
// reply at all.

// watching is a bot that can assign, and a register with both codechecker
// lists in it. The institutional codechecker is in the organisation and in a
// team, so that an assignment records the role rather than asking the owners.
func watching(t *testing.T) (*Server, *organisation) {
	t.Helper()
	server, replies := organised(t)
	listing(t, server)
	replies.members["an-institutional-codechecker"] = true
	replies.inTeam["an-institutional-codechecker"] = true
	return server, replies
}

func assigned(t *testing.T, server *Server, handle string) string {
	t.Helper()
	return answer(t, server, command.Assign, []string{"@" + handle, "as", "codechecker"}, "")
}

// The warning of codecheckers/chekhov#40: an institutional codechecker checks
// for their institution, under an arrangement that institution made, and a
// check nothing says is institutional is not the work it signed up for.
func TestAssigningAnInstitutionalCodecheckerWarns(t *testing.T) {
	server, _ := watching(t)

	reply := assigned(t, server, "an-institutional-codechecker")

	for _, want := range []string{"FAKE University", "`institution`",
		"Is the label missing, or the assignment wrong?"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	// A warning, not a refusal: the assignment still happens, and the reply
	// says both things at once.
	if !strings.Contains(reply, "is now the assigned codechecker") {
		t.Errorf("the assignment was not recorded:\n%s", reply)
	}
	if !strings.Contains(reply, "I have changed no labels.") {
		t.Errorf("the reply does not say that nothing was changed:\n%s", reply)
	}
	assertNobodyIsMentioned(t, reply)
}

// The label is what the arrangement is recorded by, so a check carrying it is
// exactly the case the warning exists to find the absence of.
func TestAnInstitutionalCheckIsNotWarnedAbout(t *testing.T) {
	server, replies := watching(t)
	replies.labelled("institution")

	reply := assigned(t, server, "an-institutional-codechecker")
	if strings.Contains(reply, "FAKE University") {
		t.Errorf("an institutional check was warned about:\n%s", reply)
	}
}

// Somebody on the ordinary list only is a volunteer, and there is nothing to
// say about them.
func TestAnOrdinaryCodecheckerIsNotWarnedAbout(t *testing.T) {
	server, _ := watching(t)

	reply := assigned(t, server, "a-codechecker")
	if strings.Contains(reply, "The labels may not describe this check") {
		t.Errorf("an ordinary assignment produced a label note:\n%s", reply)
	}
}

// "Could not check" must not read as "this is wrong": a list that answered 404
// names nobody, and naming nobody is not evidence that anybody is a volunteer.
func TestAnUnreadableListIsNotAWarning(t *testing.T) {
	server, _ := watching(t)
	stub := listing(t, server)
	stub.Remove(institutionPath)

	reply := assigned(t, server, "an-institutional-codechecker")
	if strings.Contains(reply, "The labels may not describe this check") {
		t.Errorf("an unreadable list produced a warning:\n%s", reply)
	}
	if !strings.Contains(reply, "is now the assigned codechecker") {
		t.Errorf("the assignment was not recorded:\n%s", reply)
	}
}

// The queue: a check somebody is doing that is still advertised as needing
// somebody. The same note carries it, whichever command noticed.
func TestACheckStillInTheQueueIsNoticed(t *testing.T) {
	server, replies := watching(t)
	replies.labelled("needs codechecker")

	if reply := assigned(t, server, "a-codechecker"); !strings.Contains(reply,
		"still carries the `needs codechecker` label") {
		t.Errorf("the reply does not notice the queue label:\n%s", reply)
	}
	// And `roles`, whose subject is the same state, says it too.
	if reply := answer(t, server, command.ListRoles, nil, ""); !strings.Contains(reply,
		"still carries the `needs codechecker` label") {
		t.Errorf("`roles` does not notice the queue label:\n%s", reply)
	}
}

// The opposite, and the one nobody notices on their own: a check nobody is
// doing that is invisible to the queue it should be in.
func TestACheckOutsideTheQueueWithNobodyOnItIsNoticed(t *testing.T) {
	server, _ := watching(t)

	reply := answer(t, server, command.SuggestCodecheckers, []string{"R"}, "")
	if !strings.Contains(reply, "Nobody is the assigned codechecker") {
		t.Errorf("`suggest codecheckers` does not notice the missing queue label:\n%s", reply)
	}
}

// Removing the last codechecker puts the check back in the queue, and the
// label says otherwise until somebody sets it.
func TestRemovingTheCodecheckerIsNoticed(t *testing.T) {
	server, _ := watching(t)
	if reply := assigned(t, server, "a-codechecker"); !strings.Contains(reply, "is now") {
		t.Fatalf("the assignment did not happen:\n%s", reply)
	}

	reply := answer(t, server, command.Remove, []string{"@a-codechecker", "as", "codechecker"}, "")
	if !strings.Contains(reply, "Nobody is the assigned codechecker") {
		t.Errorf("`remove` does not notice the missing queue label:\n%s", reply)
	}
}

// A command whose subject is not the check's state says nothing about labels,
// however wrong they are. The registry decides which is which.
func TestACommandThatIsNotAboutTheCheckSaysNothingAboutLabels(t *testing.T) {
	server, replies := watching(t)
	replies.labelled("needs codechecker")
	if reply := assigned(t, server, "a-codechecker"); !strings.Contains(reply, "needs codechecker") {
		t.Fatalf("the labels of this check do not disagree, so the test proves nothing:\n%s", reply)
	}

	for _, name := range []command.Name{command.Version, command.Rules, command.Hello} {
		if reply := answer(t, server, name, nil, ""); strings.Contains(reply, "needs codechecker") {
			t.Errorf("`%s` talked about the labels:\n%s", name, reply)
		}
	}
}

// The note is said once while the bot's replies follow one another, and again
// as soon as something else it said comes between (codecheckers/chekhov#61).
func TestTheLabelNoteIsNotRepeatedStraightAway(t *testing.T) {
	server, replies := watching(t)
	replies.labelled("needs codechecker")
	first := assigned(t, server, "a-codechecker")
	if !strings.Contains(first, "The labels may not describe this check") {
		t.Fatalf("the labels of this check do not disagree, so the test proves nothing:\n%s", first)
	}

	// The test's reply path does not keep what it posts in the thread the
	// bot reads, so each reply is put there as the deployment would have.
	posted := func(body string) {
		replies.thread = append(replies.thread, github.Comment{
			Author: server.Settings.BotUser(), Body: command.Defuse(body)})
	}
	posted(first)

	if reply := answer(t, server, command.ListRoles, nil, ""); strings.Contains(reply, "The labels may not describe") {
		t.Errorf("the note was repeated straight after the reply that carried it:\n%s", reply)
	}

	posted(command.GoodbyeReply())
	if reply := answer(t, server, command.ListRoles, nil, ""); !strings.Contains(reply, "The labels may not describe") {
		t.Errorf("the note was held back although the bot said something else in between:\n%s", reply)
	}
}

// A staged reply is on the issue before its note is decided, and its own
// earlier stages are not the last word before it.
func TestAStagedReplyIsNotItsOwnPredecessor(t *testing.T) {
	server, replies := watching(t)
	note := command.LabelNote([]string{"the check carries `needs codechecker`"})
	replies.thread = []github.Comment{
		{ID: 5, Author: server.Settings.BotUser(), Body: "**Where this check stands**\n\n" + note},
		{ID: 6, Author: server.Settings.BotUser(), Body: "**Starting this check**\n\n…"},
	}
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1}

	if !server.justSaid(context.Background(), event, note, &reply{comment: 6}) {
		t.Error("the staged reply was taken for the comment before it")
	}
	if server.justSaid(context.Background(), event, note, nil) {
		t.Error("a comment without the note was read as having said it")
	}
	if server.justSaid(context.Background(), event, command.LabelNote([]string{"something else"}), &reply{comment: 6}) {
		t.Error("a different note was held back")
	}
}

// A record somebody edited grants nothing, and a warning built on it would be
// a warning about whatever they typed.
func TestAnEditedRecordSaysNothingAboutLabels(t *testing.T) {
	server, replies := watching(t)
	replies.labelled("needs codechecker")
	if reply := assigned(t, server, "a-codechecker"); !strings.Contains(reply, "needs codechecker") {
		t.Fatalf("the labels of this check do not disagree, so the test proves nothing:\n%s", reply)
	}
	// Somebody edited the roles comment, leaving the bot as its author: the
	// signature no longer matches, and the record grants nothing.
	record := server.Checks.Comments.(*issueComments)
	for i := range record.comments {
		record.comments[i].Body = strings.Replace(record.comments[i].Body,
			"a-codechecker", "an-intruder", 1)
	}

	if reply := answer(t, server, command.ListRoles, nil, ""); strings.Contains(reply,
		"The labels may not describe this check") {
		t.Errorf("an edited record produced a label note:\n%s", reply)
	}
}

// Three parts of one command want the issue - the team an assignment leads
// to, what the check is about, and whether the labels still describe it - and
// nothing the bot does changes a label, so one read serves them all.
func TestOneCommandReadsItsIssueOnce(t *testing.T) {
	server, replies := watching(t)
	replies.labelled("needs codechecker")
	replies.reads = 0

	if reply := assigned(t, server, "an-institutional-codechecker"); !strings.Contains(reply,
		"FAKE University") {
		t.Fatalf("the command did not do the work this counts the reads of:\n%s", reply)
	}
	if replies.reads != 1 {
		t.Errorf("one command read its issue %d times, want 1", replies.reads)
	}
}

// A finished check needs nobody. The queue rule is the one that fires where
// nothing has happened yet, so it is the one that has to stay quiet where
// nothing is going to happen again.
func TestAClosedCheckIsNotToldItNeedsACodechecker(t *testing.T) {
	server, replies := watching(t)
	replies.issues = []github.Issue{{Number: 1, Closed: true}}

	if reply := answer(t, server, command.ListRoles, nil, ""); strings.Contains(reply,
		"Nobody is the assigned codechecker") {
		t.Errorf("a closed check was told it needs a codechecker:\n%s", reply)
	}
}
