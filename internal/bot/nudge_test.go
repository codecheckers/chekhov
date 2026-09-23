package bot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
)

// asked puts an ask to the owners on the issue, raised however long ago.
func asked(t *testing.T, server *Server, replies *organisation, handle string, ago time.Duration) {
	t.Helper()
	block, err := followup.Render(followup.Record{
		Kind: followup.KindOrganisation, Check: "codecheckers/testing-dev-register#1",
		Subject: handle, Role: "assigned codechecker", Team: "codecheckers",
		Asked: time.Now().UTC().Add(-ago).Format(time.RFC3339),
	}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(block + "please invite them")
}

// Somebody who has not joined and was asked about recently is left alone: the
// owners were asked three days ago at most, and asking again would be nagging.
func TestASweepLeavesARecentAskAlone(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 24*time.Hour)

	swept := server.nudge(context.Background())

	if swept.Acted != 0 || swept.Outstanding != 1 {
		t.Errorf("swept %+v, want one left waiting", swept)
	}
	if replies.count() != 0 {
		t.Errorf("the sweep said something: %s", replies.last())
	}
}

// Three days on, the owners are asked again - and the reminder carries a fresh
// record, so the next one is three days from now rather than from the first.
func TestASweepRemindsTheOwnersAfterThreeDays(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 4*24*time.Hour)

	swept := server.nudge(context.Background())
	if swept.Acted != 1 {
		t.Fatalf("swept %+v, want the owners reminded", swept)
	}

	posted := replies.last()
	if !strings.Contains(posted, "Still waiting, 4 days on") {
		t.Errorf("the reminder does not say how long:\n%s", posted)
	}
	if !strings.Contains(posted, "@an-owner") {
		t.Errorf("the reminder asks nobody:\n%s", posted)
	}
	record, err := followup.Parse(posted, "codecheckers/testing-dev-register#1", server.signer())
	if err != nil {
		t.Fatalf("the reminder carries no record: %v", err)
	}
	if record.Older(remindAfter, time.Now()) {
		t.Error("the reminder's record is already old enough to remind again")
	}
}

// Once they have joined, the sweep finishes what the ask promised.
func TestASweepFinishesTheJobWhenTheyHaveJoined(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 24*time.Hour)
	replies.members["a-newcomer"] = true

	swept := server.nudge(context.Background())
	if swept.Acted != 1 {
		t.Fatalf("swept %+v, want the job finished", swept)
	}

	posted := replies.last()
	for _, want := range []string{"has joined", "codecheckers/codecheckers", "assigned codechecker"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the reply does not say %q:\n%s", want, posted)
		}
	}
	if len(replies.added) != 1 || replies.added[0] != "a-newcomer" {
		t.Errorf("the team was not changed: %v", replies.added)
	}
	// And nothing is left outstanding: the reply carries no record.
	if _, err := followup.Parse(posted, "", server.signer()); err != followup.ErrNoRecord {
		t.Errorf("the finishing reply leaves something to chase: %v", err)
	}
}

// Running twice in a day says nothing twice: the handler decides by looking at
// the world, so once the work is done there is nothing to say.
func TestASweepSaysNothingTheSecondTime(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 24*time.Hour)
	replies.members["a-newcomer"] = true

	server.nudge(context.Background())
	replies.inTeam["a-newcomer"] = true
	before := replies.count()

	swept := server.nudge(context.Background())
	if swept.Acted != 0 {
		t.Errorf("swept %+v, want nothing said the second time", swept)
	}
	if replies.count() != before {
		t.Errorf("the second sweep posted %q", replies.last())
	}
}

// A record somebody edited is not acted on, and the sweep says so rather than
// passing over it in silence.
func TestASweepRefusesAnEditedRecord(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 4*24*time.Hour)
	replies.thread[0].Body = strings.Replace(replies.thread[0].Body, "a-newcomer", "an-intruder", 1)

	swept := server.nudge(context.Background())

	if swept.Acted != 0 {
		t.Error("an edited record was acted on")
	}
	if len(swept.Problems) != 1 || !strings.Contains(swept.Problems[0], "not one I wrote") {
		t.Errorf("problems %v, want the edit reported", swept.Problems)
	}
}

// A register that could not be read is never "nothing to do": every follow-up
// in it is still outstanding.
func TestASweepThatCouldNotReadSaysSo(t *testing.T) {
	server, replies := organised(t)
	replies.issuesErr = errors.New("GitHub answered 403")

	swept := server.nudge(context.Background())

	if swept.Issues != 0 || swept.Acted != 0 {
		t.Errorf("swept %+v", swept)
	}
	if len(swept.Problems) != 1 || !strings.Contains(swept.Problems[0], "403") {
		t.Errorf("problems %v, want the failure reported", swept.Problems)
	}
}

// The newest record for a subject is the state: a reminder sits beside the
// first ask, and only the last one is measured from.
func TestOnlyTheNewestRecordForSomebodyCounts(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 9*24*time.Hour)
	asked(t, server, replies, "a-newcomer", time.Hour)

	swept := server.nudge(context.Background())
	if swept.Acted != 0 || swept.Outstanding != 1 {
		t.Errorf("swept %+v, want the newest record to count and nothing said", swept)
	}
	if replies.count() != 0 {
		t.Errorf("the sweep reminded from the older ask: %s", replies.last())
	}
}

// The reply an editor gets says what the walk came to.
func TestTheNudgeCommandReportsTheWalk(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", time.Hour)

	reply := answer(t, server, command.Nudge, nil, "")
	if !strings.Contains(reply, "1 open issue") {
		t.Errorf("reply:\n%s", reply)
	}
	if !strings.Contains(reply, "waiting") {
		t.Errorf("reply:\n%s", reply)
	}
}

// The endpoint the weekly workflow reads: when the process started, what the
// nightly sweeps found, and when the next one is due.
func TestTheNudgesEndpointReportsTheSweeps(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 4*24*time.Hour)

	server.sweepOnce(context.Background())

	body := nudges(t, server)
	for _, want := range []string{`"started"`, `"last_sweep"`, `"next_due"`, `"every": "24h0m0s"`, `"acted": 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("/nudges does not report %s:\n%s", want, body)
		}
	}
}

// Before the first sweep it is still an answer: the process is young, which is
// what tells the weekly check to hold its fire after a deploy.
func TestTheNudgesEndpointAnswersBeforeTheFirstSweep(t *testing.T) {
	server, _ := organised(t)

	body := nudges(t, server)
	if strings.Contains(body, "last_sweep") {
		t.Errorf("a sweep is reported before any ran:\n%s", body)
	}
	if !strings.Contains(body, `"sweeps": []`) {
		t.Errorf("/nudges does not report an empty history:\n%s", body)
	}
}

// Unauthenticated, so a problem is a number. The words name the person it is
// about, and that is nobody's business outside development.
func TestTheNudgesEndpointNamesNobodyInProduction(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", time.Hour)
	replies.membersErr = errors.New("GitHub answered 403")
	server.sweepOnce(context.Background())

	if body := nudges(t, server); !strings.Contains(body, "a-newcomer") {
		t.Errorf("development does not say what went wrong:\n%s", body)
	}
	server.Deployment.Environment = "production"
	body := nudges(t, server)
	if strings.Contains(body, "a-newcomer") {
		t.Errorf("/nudges names somebody in production:\n%s", body)
	}
	if !strings.Contains(body, `"problems": 1`) {
		t.Errorf("/nudges does not count the problem:\n%s", body)
	}
}

// The history is a few weeks of nights and no more, so a long-running
// deployment does not grow a list nobody reads.
func TestTheNudgesEndpointForgetsOldSweeps(t *testing.T) {
	server, _ := organised(t)
	for range remembered + 5 {
		server.sweepOnce(context.Background())
	}

	if len(server.sweeps.done) != remembered {
		t.Errorf("history %d sweeps, want %d", len(server.sweeps.done), remembered)
	}
}

// A sweep that found a hundred problems is remembered as a hundred, not as a
// hundred strings: the count is what the endpoint reports.
func TestOnlySoManyProblemsAreKept(t *testing.T) {
	server, replies := organised(t)
	replies.issuesErr = errors.New("GitHub answered 403")
	sweep := Sweep{Started: time.Now().UTC()}
	for range detailed + 10 {
		sweep.Problems = append(sweep.Problems, "something went wrong")
	}

	server.sweeps.record(sweep)

	if kept := len(server.sweeps.done[0].Problems); kept != detailed {
		t.Errorf("kept %d problems, want %d", kept, detailed)
	}
}

// nudges is what GET /nudges answers.
func nudges(t *testing.T, server *Server) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/nudges", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	return response.Body.String()
}

// A record is a record because the bot wrote it. Anybody can comment on an
// issue of the register, and a deployment with no key signs nothing - so a
// comment by somebody else is not read, whatever it carries.
func TestASweepReadsOnlyTheBotsOwnComments(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 4*24*time.Hour)
	replies.thread[0].Author = "a-passer-by"

	swept := server.nudge(context.Background())

	if swept.Acted != 0 || swept.Outstanding != 0 || len(swept.Problems) != 0 {
		t.Errorf("swept %+v, want a stranger's comment ignored entirely", swept)
	}
	if replies.count() != 0 {
		t.Errorf("the sweep acted on somebody else's comment: %s", replies.last())
	}
}

// The role may have gone to somebody else in the three days this took. An
// editor decided that after the ask, and the record does not overrule it.
func TestASweepLeavesARoleThatHasSinceGoneToSomebodyElse(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 24*time.Hour)
	replies.members["a-newcomer"] = true
	// An editor assigned somebody else in the meantime.
	acted(t, server, "@chekhovbot assign @a-codechecker as codechecker")
	before := replies.count()

	swept := server.nudge(context.Background())

	posted := replies.last()
	if swept.Acted != 1 || replies.count() == before {
		t.Fatalf("swept %+v, want the join answered", swept)
	}
	for _, want := range []string{"has joined", "`@a-codechecker`", "left it with them"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the reply does not say %q:\n%s", want, posted)
		}
	}
	// And the role is still the other person's.
	roles := answer(t, server, command.ListRoles, nil, "")
	if !strings.Contains(roles, "a-codechecker") || strings.Contains(roles, "a-newcomer") {
		t.Errorf("the role was taken back:\n%s", roles)
	}
}

// Finishing the job hands the issue over: the new codechecker becomes the
// issue's assignee, which is where people look for who is checking.
func TestFinishingAnAssignmentSetsTheIssueAssignee(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 24*time.Hour)
	replies.members["a-newcomer"] = true

	server.nudge(context.Background())

	if len(replies.assigned) != 1 || replies.assigned[0] != "a-newcomer" {
		t.Errorf("assigned %v, want the newcomer", replies.assigned)
	}
	// Nobody is taken off: the role was granted only because it was nobody's.
	if len(replies.unassigned) != 0 {
		t.Errorf("unassigned %v, want nobody", replies.unassigned)
	}
}

// A follow-up that is finished is not waiting on anybody, so it is not counted
// - otherwise the number an editor reads would only ever grow.
func TestAFinishedFollowUpIsNotCountedAsWaiting(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 24*time.Hour)
	replies.members["a-newcomer"] = true
	server.nudge(context.Background())
	replies.inTeam["a-newcomer"] = true

	swept := server.nudge(context.Background())

	if swept.Outstanding != 0 {
		t.Errorf("swept %+v, want nothing counted as waiting", swept)
	}
}

// The owners are not notified every three days for ever: after so many
// reminders the bot says it will stop, and then it does.
func TestTheOwnersAreNotRemindedForEver(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 4*24*time.Hour)

	// Every sweep finds the newest record, reminds, and leaves a fresh one -
	// which is aged here to let the next sweep find it ready.
	for range mostReminders + 3 {
		server.nudge(context.Background())
		age(t, server, replies)
	}

	posted := replies.count()
	if posted != mostReminders+1 {
		t.Errorf("the owners were asked %d times, want %d", posted, mostReminders+1)
	}
	if last := replies.last(); !strings.Contains(last, "last time I will ask") {
		t.Errorf("the bot never says it is giving up:\n%s", last)
	}
	// And it still finishes the job if they do join, long after it stopped.
	replies.members["a-newcomer"] = true
	if swept := server.nudge(context.Background()); swept.Acted != 1 {
		t.Errorf("swept %+v, want the join answered anyway", swept)
	}
}

// Two walks at once would both find the same record ready and remind twice.
func TestOnlyOneSweepRunsAtATime(t *testing.T) {
	server, replies := organised(t)
	asked(t, server, replies, "a-newcomer", 4*24*time.Hour)
	server.sweeping.Lock()
	defer server.sweeping.Unlock()

	swept := server.nudge(context.Background())

	if swept.Acted != 0 || replies.count() != 0 {
		t.Errorf("swept %+v, want nothing done while another walk holds it", swept)
	}
	if len(swept.Problems) != 1 || !strings.Contains(swept.Problems[0], "already running") {
		t.Errorf("problems %v, want the refusal reported", swept.Problems)
	}
}

// age moves every follow-up on the issue back past the reminder interval, so
// that the next sweep finds it ready.
func age(t *testing.T, server *Server, replies *organisation) {
	t.Helper()
	for i, comment := range replies.thread {
		record, err := followup.Parse(comment.Body, "", server.signer())
		if err != nil {
			continue
		}
		// Staggered, so that the newest record is still the newest one after
		// they have all been moved back.
		record.Asked = time.Now().UTC().Add(-4*24*time.Hour + time.Duration(i)*time.Second).Format(time.RFC3339)
		block, err := followup.Render(record, server.signer())
		if err != nil {
			t.Fatal(err)
		}
		replies.thread[i].Body = block + "aged"
	}
}

// A panic in the sweep, or in a handler it calls, must cost that night's walk
// and not the process: the deployment answers commands in the morning, the
// timer fires the next night, and the admin issue is where anybody hears of
// it. See codecheckers/chekhov#50.
func TestASweepThatPanicsCostsThatSweepAndNotTheProcess(t *testing.T) {
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "99")
	server, replies := organised(t)
	replies.issuesPanic = "boom"

	server.sweepOnce(context.Background())

	// Recorded like any other walk, so that the endpoint shows a sweep that
	// keeps panicking as well as one that has stopped.
	if body := nudges(t, server); !strings.Contains(body, `"problems": 1`) {
		t.Errorf("/nudges does not count the failed sweep:\n%s", body)
	}
	if replies.count() != 1 {
		t.Fatalf("%d comments, want the report on the admin issue", replies.count())
	}
	report := replies.comments[0]
	if report.Issue != 99 {
		t.Errorf("the report went to #%d, want the admin issue", report.Issue)
	}
	for _, want := range []string{"nightly sweep", "sweepOnce", "boom"} {
		if !strings.Contains(report.Body, want) {
			t.Errorf("the report does not carry %q:\n%s", want, report.Body)
		}
	}
}

// And the timer keeps its schedule: the sweep after a bad night runs.
func TestTheSweepRunsAgainAfterAPanic(t *testing.T) {
	server, replies := organised(t)
	replies.issuesPanic = "boom"
	server.sweepOnce(context.Background())

	replies.issuesPanic = nil
	server.sweepOnce(context.Background())

	if len(server.sweeps.done) != 2 {
		t.Fatalf("%d sweeps recorded, want the failed one and the next", len(server.sweeps.done))
	}
	if problems := server.sweeps.done[1].Problems; len(problems) != 0 {
		t.Errorf("the sweep after the panic reports %v", problems)
	}
}

// The startup team load is the other timer-less goroutine with nobody to
// answer, and it is guarded the same way.
func TestABackgroundPanicIsReportedRatherThanEndingTheProcess(t *testing.T) {
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "99")
	server, replies := organised(t)

	func() {
		defer server.recoverBackground("The team load at startup panicked.")
		panic("boom")
	}()

	if replies.count() != 1 {
		t.Fatalf("%d comments, want the report on the admin issue", replies.count())
	}
	report := replies.comments[0].Body
	if !strings.Contains(report, "team load at startup") || !strings.Contains(report, "boom") {
		t.Errorf("the report does not say what panicked:\n%s", report)
	}
}
