package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// How often one command lists the comments of the issue it is on
// (codecheckers/chekhov#54). The record of the check, the label note, the
// note already said and the follow-ups `status` reports all come out of them,
// and on a long checks issue each listing is several requests.

// thread is a deployment's reply path, where the record of the check and the
// replies are one client, counting every listing of the comments that leaves.
type thread struct {
	*workflow
	record   *issueComments
	listings int
}

func (t *thread) Comments(ctx context.Context, repository string, issue int) ([]github.Comment, error) {
	t.listings++
	return t.record.Comments(ctx, repository, issue)
}

func (t *thread) Post(ctx context.Context, repository string, issue int, body string) (int64, error) {
	return t.record.Post(ctx, repository, issue, body)
}

func (t *thread) Edit(ctx context.Context, repository string, comment int64, body string) error {
	return t.record.Edit(ctx, repository, comment, body)
}

// threaded is a check with a codechecker assigned, wired as a deployment is.
func threaded(t *testing.T, labels ...string) (*Server, *thread) {
	t.Helper()
	server, path := checking(t, "valid-2.0", labels...)
	assignCodechecker(t, server, "a-codechecker")
	replies := &thread{workflow: path, record: server.Checks.Comments.(*issueComments)}
	server.Replies, server.Checks.Comments = replies, replies
	return server, replies
}

func TestACommandListsTheCommentsOnce(t *testing.T) {
	for _, run := range []struct {
		name command.Name
		args []string
	}{
		{command.ListRoles, nil},
		{command.Status, nil},
		{command.CheckStart, nil},
		{command.CheckFinish, []string{"github::codecheckers/demo"}},
		{command.SuggestCodecheckers, []string{"R"}},
	} {
		t.Run(string(run.name), func(t *testing.T) {
			// The queue label with a codechecker assigned, so that the label
			// note has something to say and the note already said is looked for.
			server, replies := threaded(t, "needs codechecker")
			listing(t, server)

			answer(t, server, run.name, run.args, "")
			if replies.listings != 1 {
				t.Errorf("`%s` listed the comments %d times, want once", run.name, replies.listings)
			}
		})
	}
}

// A command that writes the record lists under the lock, because a change is
// made from what the issue says now, and the label note after it reads what
// was written from the same listing rather than listing again. `assign` and
// `remove` are an editor's, so nothing reads the record before the write and
// that listing is the only one; a command gated on a per-check role would
// make it two.
func TestACommandThatWritesTheRecordListsOnce(t *testing.T) {
	for _, run := range []struct {
		name command.Name
		args []string
	}{
		{command.Assign, []string{"@a-handling-editor", "as", "author"}},
		{command.Remove, []string{"@a-codechecker", "as", "codechecker"}},
	} {
		t.Run(string(run.name), func(t *testing.T) {
			server, replies := threaded(t)
			before := replies.record.comments[0].Body

			reply := answer(t, server, run.name, run.args, "")
			if replies.record.comments[0].Body == before {
				t.Fatalf("`%s` wrote nothing, so the test proves nothing:\n%s", run.name, reply)
			}
			if replies.listings != 1 {
				t.Errorf("`%s` listed the comments %d times, want once", run.name, replies.listings)
			}
		})
	}
}

// The listing belongs to the command: the next one lists afresh, and sees
// what somebody did to the issue in between.
func TestTheNextCommandListsAfresh(t *testing.T) {
	server, replies := threaded(t)

	answer(t, server, command.ListRoles, nil, "")
	replies.record.comments = nil
	reply := answer(t, server, command.ListRoles, nil, "")
	if replies.listings != 2 {
		t.Errorf("two commands listed the comments %d times, want twice", replies.listings)
	}
	if strings.Contains(reply, "a-codechecker") {
		t.Errorf("the second command answered from the first one's listing:\n%s", reply)
	}
}
