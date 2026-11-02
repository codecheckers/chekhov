package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// The archive reminder (codecheckers/chekhov#27): `check finish` warns about a
// copy that is not archived, and the sweep reminds the handling editor once the
// certificate is in the register.

const archiveCheck = "codecheckers/testing-dev-register#1"

// copyArchived serves whether codecheckers/demo is archived.
func copyArchived(stub *testserver.Server, archived bool) {
	stub.JSON("/github/repos/codecheckers/demo",
		fmt.Sprintf(`{"full_name": "codecheckers/demo", "archived": %v}`, archived))
}

// inTheRegister serves a register.csv, with a row for issue #1 when asked.
func inTheRegister(stub *testserver.Server, server *Server, row bool) {
	csv := "Certificate,Repository,Type,Venue,Issue\n1970-001,github::a-made-up-author/other,journal,FAKE,7\n"
	if row {
		csv += "1970-021,github::codecheckers/demo,journal,FAKE,1\n"
	}
	stub.Text("/raw/"+server.Settings.TargetRepository()+"/HEAD/register.csv", csv)
}

// withServices points a server at a stub for GitHub and the register.
func withServices(t *testing.T, server *Server) *testserver.Server {
	t.Helper()
	stub := testserver.New(t)
	server.Services = &check.Services{Access: check.Access{
		HTTP:       stub.Client(),
		Register:   server.Settings.TargetRepository(),
		RawContent: stub.URL + "/raw",
		GitHub:     stub.URL + "/github",
	}}
	return stub
}

// archiveAsked puts an archive record on the issue, raised however long ago,
// after however many reminders.
func archiveAsked(t *testing.T, server *Server, replies *organisation, reminders int, ago time.Duration) {
	t.Helper()
	block, err := followup.Render(followup.Record{
		Kind: followup.KindArchive, Check: archiveCheck,
		Subject: "github::codecheckers/demo", By: "nuest", Reminders: reminders,
		Asked: time.Now().UTC().Add(-ago).Format(time.RFC3339),
	}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(block + "archive it, please")
}

// archiving is a sweep's server, with the copy and the register as given.
func archiving(t *testing.T, archived, row bool) (*Server, *organisation, *testserver.Server) {
	t.Helper()
	server, replies := organised(t)
	stub := withServices(t, server)
	copyArchived(stub, archived)
	inTheRegister(stub, server, row)
	return server, replies, stub
}

// finishing is a check ready for `check finish`, its configuration from the
// fixture named and its copy archived or not.
func finishing(t *testing.T, fixture string, archived bool) *Server {
	t.Helper()
	server, path := checking(t, fixture, "work in progress")
	copyArchived(path.stub, archived)
	inTheRegister(path.stub, server, false)
	return server
}

func TestCheckFinishWarnsAboutACopyNotArchived(t *testing.T) {
	server := finishing(t, "valid-2.0", false)

	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"},
		command.Command{Name: command.CheckFinish, Args: []string{"github::codecheckers/demo"}})

	for _, want := range []string{"`github::codecheckers/demo` is not archived",
		"archiving-repositories", "I will remind `@nuest`",
		fmt.Sprintf("%d times at most", mostArchiveReminders)} {
		if !strings.Contains(reply.body, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply.body)
		}
	}
	if reply.follow == nil || reply.follow.Kind != followup.KindArchive ||
		reply.follow.Subject != "github::codecheckers/demo" || reply.follow.By != "nuest" {
		t.Errorf("the reply leaves %+v, want an archive record for the copy", reply.follow)
	}
}

func TestCheckFinishSaysNothingAboutAnArchivedCopy(t *testing.T) {
	server := finishing(t, "valid-2.0", true)

	reply := answer(t, server, command.CheckFinish, []string{"github::codecheckers/demo"}, "")
	if strings.Contains(reply, "archive") {
		t.Errorf("an archived copy was mentioned:\n%s", reply)
	}
}

// A check waiting on metadata is not finished, and is asked about the archive
// when it is.
func TestCheckFinishLeavesTheArchiveOfAPendingCheck(t *testing.T) {
	server := finishing(t, "placeholders", false)

	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"},
		command.Command{Name: command.CheckFinish, Args: []string{"github::codecheckers/demo"}})
	if strings.Contains(reply.body, "archive") || reply.follow != nil {
		t.Errorf("a pending check was told about the archive (%+v):\n%s", reply.follow, reply.body)
	}
}

// A repository outside the organisation is the author's, and not the
// register's to archive: nothing is said, and nothing is asked of GitHub.
func TestCheckFinishLeavesARepositoryOutsideTheOrganisation(t *testing.T) {
	server, _ := checking(t, "valid-2.0", "work in progress")
	body, record := server.archiveFollowUp(mention{Issue: 1, Author: "nuest"},
		"github::a-made-up-author/paper", people.Reading{}, server.Services)
	if body != "" || record != nil {
		t.Errorf("an author's repository was reported: %q, %+v", body, record)
	}
}

// GitLab does not say without a token, so the copy is named as not checked and
// nothing is left to chase (#65).
func TestCheckFinishSaysAGitLabCopyCouldNotBeChecked(t *testing.T) {
	server, _ := checking(t, "valid-2.0", "work in progress")
	body, record := server.archiveFollowUp(mention{Issue: 1, Author: "nuest"},
		"gitlab::cdchck/community-codechecks/demo", people.Reading{}, server.Services)
	if !strings.Contains(body, "cannot see whether") || !strings.Contains(body, "#archive-a-project") {
		t.Errorf("the GitLab copy is not named as unchecked:\n%s", body)
	}
	if record != nil {
		t.Errorf("an unreadable copy left a record to chase: %+v", record)
	}
}

// A status nobody could read is a warning about nothing.
func TestCheckFinishSaysNothingWhenGitHubCannotSay(t *testing.T) {
	server, _ := checking(t, "valid-2.0", "work in progress")
	stub := withServices(t, server)
	stub.Status("/github/repos/codecheckers/demo", 502)
	body, record := server.archiveFollowUp(mention{Issue: 1, Author: "nuest"},
		"github::codecheckers/demo", people.Reading{}, server.Services)
	if body != "" || record != nil {
		t.Errorf("an unread status was reported: %q, %+v", body, record)
	}
}

// Before the register pull request is merged, the editor has been told, and
// the sweep says nothing.
func TestTheArchiveReminderWaitsForTheRegister(t *testing.T) {
	server, replies, stub := archiving(t, false, false)
	// Nor is GitHub asked until then: a copy that could not be read would
	// otherwise be a problem every night the pull request stays open.
	stub.Status("/github/repos/codecheckers/demo", 502)
	archiveAsked(t, server, replies, 0, 30*24*time.Hour)

	swept := server.nudge(context.Background())
	if swept.Acted != 0 || swept.Outstanding != 1 || len(swept.Problems) != 0 {
		t.Errorf("swept %+v, want one left waiting and nothing said", swept)
	}
}

// Once the row is there, the editor is reminded at once - mentioned, so that
// they are notified - and the reminder carries a fresh record.
func TestTheArchiveReminderComesOnceRegistered(t *testing.T) {
	server, replies, _ := archiving(t, false, true)
	archiveAsked(t, server, replies, 0, time.Hour)

	swept := server.nudge(context.Background())
	if swept.Acted != 1 {
		t.Fatalf("swept %+v, want a reminder", swept)
	}
	posted := replies.last()
	for _, want := range []string{"@nuest, certificate `1970-021`", "`github::codecheckers/demo` is still not archived"} {
		if !strings.Contains(posted, want) {
			t.Errorf("the reminder does not say %q:\n%s", want, posted)
		}
	}
	record, err := followup.Parse(posted, archiveCheck, server.signer())
	if err != nil || record.Reminders != 1 {
		t.Fatalf("the reminder carries %+v (%v), want a record of one reminder", record, err)
	}

	// And not again the same night.
	if swept := server.nudge(context.Background()); swept.Acted != 0 {
		t.Errorf("reminded twice in a night: %s", replies.last())
	}
}

func TestTheArchiveReminderComesEveryTwoWeeks(t *testing.T) {
	for _, testCase := range []struct {
		ago    time.Duration
		acting int
	}{{13 * 24 * time.Hour, 0}, {15 * 24 * time.Hour, 1}} {
		server, replies, _ := archiving(t, false, true)
		archiveAsked(t, server, replies, 2, testCase.ago)
		if swept := server.nudge(context.Background()); swept.Acted != testCase.acting {
			t.Errorf("%v after the last reminder: acted %d, want %d", testCase.ago, swept.Acted, testCase.acting)
		}
	}
}

// The sixth reminder says it is the last, and after it the bot is quiet.
func TestTheArchiveReminderIsNotForEver(t *testing.T) {
	server, replies, _ := archiving(t, false, true)
	archiveAsked(t, server, replies, mostArchiveReminders-1, 15*24*time.Hour)

	server.nudge(context.Background())
	posted := replies.last()
	if !strings.Contains(posted, "last time I will ask") {
		t.Errorf("the last reminder does not say so:\n%s", posted)
	}
	// Settled, so that nothing waits on a reminder that will not come.
	if record, err := followup.Parse(posted, archiveCheck, server.signer()); err != nil || !record.Settled {
		t.Errorf("the last reminder leaves %+v (%v), want a settled record", record, err)
	}
	before := replies.count()
	if swept := server.nudge(context.Background()); swept.Acted != 0 || swept.Outstanding != 0 ||
		replies.count() != before {
		t.Errorf("swept %+v after the last reminder, want silence and nothing waiting", swept)
	}
}

// A copy that has gone - deleted, or made private - is said once, and not
// reported as a problem every night after.
func TestAGoneCopyIsSaidOnce(t *testing.T) {
	server, replies, stub := archiving(t, false, true)
	stub.Status("/github/repos/codecheckers/demo", 404)
	archiveAsked(t, server, replies, 2, 15*24*time.Hour)

	swept := server.nudge(context.Background())
	if swept.Acted != 1 || len(swept.Problems) != 0 || !strings.Contains(replies.last(), "can no longer read") {
		t.Fatalf("swept %+v, want one word about the copy and no problem:\n%s", swept, replies.last())
	}
	if swept := server.nudge(context.Background()); swept.Acted != 0 || len(swept.Problems) != 0 {
		t.Errorf("swept %+v the second night, want silence", swept)
	}
}

// Archived: one short confirmation, and then nothing, ever - the confirmation
// settles the record.
func TestAnArchivedCopyIsConfirmedOnce(t *testing.T) {
	server, replies, _ := archiving(t, true, true)
	archiveAsked(t, server, replies, 3, time.Hour)

	swept := server.nudge(context.Background())
	if swept.Acted != 1 || swept.Outstanding != 0 {
		t.Fatalf("swept %+v, want one confirmation and nothing waiting", swept)
	}
	posted := replies.last()
	if !strings.Contains(posted, "is archived now") || strings.Contains(posted, "@nuest") {
		t.Errorf("the confirmation is not short, or notifies somebody:\n%s", posted)
	}

	before := replies.count()
	if swept := server.nudge(context.Background()); swept.Acted != 0 || replies.count() != before {
		t.Errorf("confirmed twice: %s", replies.last())
	}
}

// The reminder goes to the handling editor as the record of the check says now,
// not to whoever finished the check.
func TestTheArchiveReminderGoesToTheHandlingEditor(t *testing.T) {
	server, replies, _ := archiving(t, false, true)
	replies.members["a-made-up-editor"] = true
	server.Teams.Reader = teamList{
		server.Settings.EditorsTeam():      {"nuest", "a-made-up-editor"},
		server.Settings.CodecheckersTeam(): {"nuest", "a-codechecker"},
	}
	server.Teams.Refresh(context.Background())
	answer(t, server, command.Assign, []string{"@a-made-up-editor", "as", "handling", "editor"}, "")
	archiveAsked(t, server, replies, 0, time.Hour)

	server.nudge(context.Background())
	if posted := replies.last(); !strings.Contains(posted, "@a-made-up-editor, certificate") {
		t.Errorf("the reminder did not go to the handling editor:\n%s", posted)
	}
}

// A closed check is read for its archive reminder, and for nothing else: the
// register pull request closes the issue, and only then is the reminder due.
func TestAClosedCheckIsSweptForItsArchiveAlone(t *testing.T) {
	server, replies, _ := archiving(t, false, true)
	replies.issues = nil
	replies.closed = []github.Issue{{Number: 1, Closed: true}}
	archiveAsked(t, server, replies, 0, time.Hour)
	asked(t, server, replies, "a-made-up-newcomer", 4*24*time.Hour)

	swept := server.nudge(context.Background())
	if swept.Issues != 0 || swept.Closed != 1 {
		t.Errorf("swept %+v, want the closed check counted apart from the open ones", swept)
	}
	if reply := command.SweptReply(swept.Swept); !strings.Contains(reply, "0 open issues and 1 closed") {
		t.Errorf("the reply does not count the closed check apart:\n%s", reply)
	}
	if swept.Acted != 1 || !strings.Contains(replies.last(), "still not archived") {
		t.Errorf("swept %+v, want the archive reminder alone:\n%s", swept, replies.last())
	}
	if strings.Contains(replies.last(), "@an-owner") {
		t.Error("the owners were asked about a check that is over")
	}
}

// `status` lists an archive reminder as a repository, not as a handle.
func TestStatusNamesTheCopyAsARepository(t *testing.T) {
	line := command.Outstanding{Kind: followup.KindArchive, Subject: "github::codecheckers/demo",
		Thing: true}.Line()
	if !strings.Contains(line, "`github::codecheckers/demo`") || strings.Contains(line, "@") {
		t.Errorf("status line %q", line)
	}
}

// ClosedIssuesSince makes the double a RecentlyClosed reply path, as the
// deployment's is.
func (o *organisation) ClosedIssuesSince(context.Context, string, time.Time) ([]github.Issue, error) {
	return o.closed, nil
}
