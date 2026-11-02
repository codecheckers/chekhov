package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// LabeledAt makes the double a LabelHistory, as the deployment's reply path
// is: the queue's label went on whenever the test says.
func (o *organisation) LabeledAt(_ context.Context, _ string, _ int, label string) (time.Time, error) {
	return o.labeledAt[label], nil
}

// stalling is a test server with nudges switched on for issue 1 alone, as a
// live test would name it.
func stalling(t *testing.T, quiet, queue int) (*Server, *organisation) {
	t.Helper()
	server, replies := organised(t)
	server.Settings.Chekhov.Nudges.QuietDays = quiet
	server.Settings.Chekhov.Nudges.NeedsCodecheckerDays = queue
	server.Settings.Chekhov.Nudges.OnlyIssues = "1"
	replies.labeledAt = map[string]time.Time{}
	return server, replies
}

// quietFor moves every comment on the issue back, as though nobody had said
// anything since.
func quietFor(replies *organisation, days int) {
	for i := range replies.thread {
		replies.thread[i].Created = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	}
}

// withACodechecker gives the check its assigned codechecker, through the
// command, the label a check being worked on carries, and a comment by the
// codechecker to be quiet after.
func withACodechecker(t *testing.T, server *Server, replies *organisation) {
	t.Helper()
	answer(t, server, command.Assign, []string{"@a-codechecker", "as", "assigned", "codechecker"}, "")
	replies.labelled("work in progress")
	replies.thread = append(replies.thread, github.Comment{ID: int64(len(replies.thread) + 1),
		Author: "a-codechecker", Body: "starting now", Created: time.Now()})
}

func TestAQuietCheckNudgesItsCodechecker(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 20)

	swept := server.nudge(context.Background())
	if swept.Nudged != 1 {
		t.Fatalf("swept %+v, want the codechecker nudged", swept)
	}
	posted := replies.last()
	for _, want := range []string{"@a-codechecker, nothing has been said on this check for 20 days",
		"`@chekhovbot status`"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the nudge does not say %q:\n%s", want, posted)
		}
	}

	// The nudge is the newest comment now, so the check is not quiet.
	before := replies.count()
	if swept := server.nudge(context.Background()); swept.Nudged != 0 || replies.count() != before {
		t.Errorf("the second sweep nudged again: %+v", swept)
	}
}

func TestACheckNotQuietForLongIsLeftAlone(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 13)

	if swept := server.nudge(context.Background()); swept.Nudged != 0 || replies.count() != 0 {
		t.Errorf("a check quiet for 13 of 14 days was nudged: %+v", swept)
	}
}

// Nobody is assigned, so there is nobody to ask: that check is the queue's.
func TestAQuietCheckWithoutACodecheckerIsNotNudged(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	replies.labelled("work in progress")
	replies.thread = append(replies.thread, github.Comment{ID: 1, Author: "a-made-up-author",
		Body: "a check nobody has picked up"})
	quietFor(replies, 60)

	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("swept %+v, want nobody nudged", swept)
	}
}

// Off is the default: the shipped settings have no thresholds, and a sweep
// says nothing about a check however long it has been quiet.
func TestNudgesAreOffUntilSwitchedOn(t *testing.T) {
	server, replies := stalling(t, 0, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 365)
	replies.labelled("needs codechecker")
	replies.labeledAt["needs codechecker"] = time.Now().AddDate(-1, 0, 0)

	if swept := server.nudge(context.Background()); swept.Nudged != 0 || replies.count() != 0 {
		t.Errorf("a sweep with nudges off nudged: %+v", swept)
	}
}

// Only the issues the settings name are nudged.
func TestOnlyTheNamedIssuesAreNudged(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	server.Settings.Chekhov.Nudges.OnlyIssues = "2, 3"
	withACodechecker(t, server, replies)
	quietFor(replies, 20)

	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("swept %+v, want issue 1 left alone", swept)
	}
}

func TestAClosedCheckIsNotNudged(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 20)
	replies.issues = nil
	replies.closed = []github.Issue{{Number: 1, Closed: true}}

	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("a closed check was nudged: %+v", swept)
	}
}

// The editors are told about a check that has waited in the queue, as the
// organisation's team when the settings name no substitutes.
func TestTheEditorsHearOfACheckWaitingInTheQueue(t *testing.T) {
	server, replies := stalling(t, 0, 7)
	replies.labelled("needs codechecker")
	replies.labeledAt["needs codechecker"] = time.Now().Add(-10 * 24 * time.Hour)

	swept := server.nudge(context.Background())
	if swept.Nudged != 1 {
		t.Fatalf("swept %+v, want the editors told", swept)
	}
	posted := replies.last()
	for _, want := range []string{"@codecheckers/editors, this check has carried `needs codechecker` for 10 days",
		"`@chekhovbot codecheckers suggest`"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the nudge does not say %q:\n%s", want, posted)
		}
	}
	if strings.Contains(posted, "configured to tell") {
		t.Errorf("the nudge says it told substitutes:\n%s", posted)
	}
	record, err := followup.Parse(posted, "codecheckers/testing-dev-register#1", server.signer())
	if err != nil || record.Kind != followup.KindQueueNudge || !record.Settled {
		t.Errorf("the nudge carries %+v, %v; want a settled record", record, err)
	}

	// Within the interval, nothing more; the record says it was said.
	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("the editors were told twice in a week: %+v", swept)
	}
	// And once the interval has passed, again.
	age(t, server, replies)
	server.Settings.Chekhov.Nudges.NeedsCodecheckerDays = 3
	if swept := server.nudge(context.Background()); swept.Nudged != 1 {
		t.Errorf("swept %+v, want the editors told again after the interval", swept)
	}
}

// A development deployment tells the people its settings name instead of the
// editors team, and says so.
func TestADevelopmentDeploymentTellsItsSubstitutes(t *testing.T) {
	server, replies := stalling(t, 0, 7)
	server.Settings.Chekhov.Teams.Owners = []string{"nuest"}
	replies.labelled("needs codechecker")
	replies.labeledAt["needs codechecker"] = time.Now().Add(-10 * 24 * time.Hour)

	server.nudge(context.Background())
	posted := replies.last()
	if !strings.Contains(posted, "@nuest, this check") || strings.Contains(posted, "@codecheckers/editors") {
		t.Errorf("the nudge went to the editors team:\n%s", posted)
	}
	if !strings.Contains(posted, "the editors have not been notified") {
		t.Errorf("the nudge does not say it told substitutes:\n%s", posted)
	}
}

func TestACheckNotLongInTheQueueIsLeftAlone(t *testing.T) {
	server, replies := stalling(t, 0, 7)
	replies.labelled("needs codechecker")
	replies.labeledAt["needs codechecker"] = time.Now().Add(-6 * 24 * time.Hour)

	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("a check six days in the queue was nudged: %+v", swept)
	}
}

// One walk posts only so many nudges, so that switching them on does not
// post one on every stalled check in one night: once it has posted as many as
// it may, a nudge that is due is held for another night.
func TestOnlySoManyNudgesInOneWalk(t *testing.T) {
	server, replies := stalling(t, 14, 7)
	server.Settings.Chekhov.Nudges.MostPerSweep = 1
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1}
	var sweep Sweep

	server.postNudge(context.Background(), event, said("the first"), nil, false, &sweep)
	server.postNudge(context.Background(), event, said("the second"), nil, true, &sweep)

	if sweep.Nudged != 1 || sweep.Held != 1 || replies.count() != 1 {
		t.Errorf("swept %+v with %d posted, want one nudged and one held", sweep, replies.count())
	}
	if reply := command.SweptReply(sweep.Swept); !strings.Contains(reply, "1 more stalled check will be nudged on a later night") {
		t.Errorf("the reply does not say one was held:\n%s", reply)
	}
}

// Once the walk is full, the queue is not looked at: telling whether a check
// has waited long enough costs a request.
func TestAFullWalkDoesNotReadTheQueue(t *testing.T) {
	server, replies := stalling(t, 0, 7)
	replies.labelled("needs codechecker")
	replies.labeledAt["needs codechecker"] = time.Now().Add(-10 * 24 * time.Hour)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1}

	result, err := server.queueNudge(context.Background(), event, replies.issues[0],
		issueThread{}, time.Now(), true)
	if err != nil || result.body != "" {
		t.Errorf("a full walk nudged the queue: %q, %v", result.body, err)
	}
}

// A finished check is not asked about: `check finish` takes the label off.
func TestAFinishedCheckIsNotNudged(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 20)
	replies.labelled("metadata pending")

	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("a finished check was nudged: %+v", swept)
	}
}

// The bot talking on the thread is not anybody answering: a reminder posted
// yesterday does not make a check quiet for three weeks any less quiet.
func TestTheBotsOwnCommentsDoNotEndTheQuiet(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 20)
	replies.onTheIssue("an archive reminder, yesterday")

	if swept := server.nudge(context.Background()); swept.Nudged != 1 {
		t.Errorf("swept %+v, want the codechecker nudged anyway", swept)
	}
}

// A codechecker who does not answer is asked three times, an interval apart,
// and then left to the editors; an answer starts the count again.
func TestTheQuietNudgeStopsAfterThreeUnanswered(t *testing.T) {
	server, replies := stalling(t, 14, 0)
	withACodechecker(t, server, replies)
	quietFor(replies, 20)

	for i := 1; i <= 3; i++ {
		swept := server.nudge(context.Background())
		if swept.Nudged != 1 {
			t.Fatalf("nudge %d: swept %+v, want the codechecker asked", i, swept)
		}
		if last := strings.Contains(replies.last(), "the last time I will ask"); last != (i == 3) {
			t.Errorf("nudge %d says it is the last: %v", i, last)
		}
		// Within the interval, nothing; then the interval passes.
		if swept := server.nudge(context.Background()); swept.Nudged != 0 {
			t.Fatalf("nudge %d came twice in an interval", i)
		}
		age(t, server, replies)
		server.Settings.Chekhov.Nudges.QuietDays = 3
	}
	if swept := server.nudge(context.Background()); swept.Nudged != 0 {
		t.Errorf("a fourth nudge came: %+v\n%s", swept, replies.last())
	}

	replies.thread = append(replies.thread, github.Comment{ID: 99, Author: "a-codechecker",
		Body: "still on it", Created: time.Now().Add(-84 * time.Hour)})
	if swept := server.nudge(context.Background()); swept.Nudged != 1 {
		t.Errorf("swept %+v, want the count started again after an answer", swept)
	}
}

func TestALabelWithNoHistoryIsSaid(t *testing.T) {
	server, replies := stalling(t, 0, 7)
	replies.labelled("needs codechecker")

	swept := server.nudge(context.Background())
	if swept.Nudged != 0 || len(swept.Problems) != 1 || !strings.Contains(swept.Problems[0], "cannot tell since when") {
		t.Errorf("swept %+v, want the missing history said", swept)
	}
}
