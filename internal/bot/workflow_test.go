package bot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// `check start`, `status` and `check finish` (codecheckers/chekhov#22).
//
// The tests that matter here are about the writes that leave: which labels are
// changed, in which order, and which are refused before any request. A reply
// that reads well is worth checking too, but a label moved on a check nobody
// meant to move is the mistake that reaches the register.

// workflow is the reply path these commands need: comments that can be edited,
// the register's issues, and the labels.
type workflow struct {
	*issueReader
	// moved is every label write that left, in order, as "+label" or "-label".
	moved []string
	// refuse is an error every label write answers with, for the test that a
	// failure is reported rather than undone.
	refuse error
}

func (w *workflow) Comments(context.Context, string, int) ([]github.Comment, error) {
	return nil, nil
}

func (w *workflow) Edit(_ context.Context, _ string, comment int64, body string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int(comment) < 1 || int(comment) > len(w.comments) {
		return fmt.Errorf("no such comment %d", comment)
	}
	w.comments[comment-1].Body = body
	return nil
}

// EditReply is how a reply is rewritten; the double keeps no footer to add.
func (w *workflow) EditReply(ctx context.Context, repository string, comment int64, body string) error {
	return w.Edit(ctx, repository, comment, body)
}

func (w *workflow) AddLabel(_ context.Context, _ string, number int, label string) error {
	if w.refuse != nil {
		return w.refuse
	}
	w.moved = append(w.moved, "+"+label)
	for i := range w.issues {
		if w.issues[i].Number == number {
			w.issues[i].Labels = append(w.issues[i].Labels, label)
		}
	}
	return nil
}

func (w *workflow) RemoveLabel(_ context.Context, _ string, number int, label string) error {
	if w.refuse != nil {
		return w.refuse
	}
	w.moved = append(w.moved, "-"+label)
	for i := range w.issues {
		if w.issues[i].Number != number {
			continue
		}
		var kept []string
		for _, carried := range w.issues[i].Labels {
			if !strings.EqualFold(carried, label) {
				kept = append(kept, carried)
			}
		}
		w.issues[i].Labels = kept
	}
	return nil
}

// checking wires a server to one check, #1, carrying the labels given, and to
// a stub serving a codecheck.yml from the fixture directory named.
func checking(t *testing.T, fixture string, labels ...string) (*Server, *workflow) {
	t.Helper()
	server, replies := testServer(t)
	path := &workflow{issueReader: &issueReader{recorder: replies, issues: []github.Issue{
		{Number: 1, Title: "Baetzel et al | 2026-001", Labels: labels, Assignees: []string{"a-codechecker"}},
	}}}
	server.Replies = path

	stub := testserver.New(t)
	if fixture != "" {
		configuration, err := os.ReadFile("../../testdata/" + fixture + "/codecheck.yml")
		if err != nil {
			t.Fatal(err)
		}
		stub.Bytes("/raw/codecheckers/demo/HEAD/codecheck.yml", configuration)
	}
	server.Services = &check.Services{Access: check.Access{
		HTTP:       stub.Client(),
		Register:   server.Settings.TargetRepository(),
		RawContent: stub.URL + "/raw",
	}}
	return server, path
}

// goodbye is manners, and only that: on a check in progress it answers and
// moves no label, which `check finish` is for (codecheckers/chekhov#6).
func TestGoodbyeChangesNothing(t *testing.T) {
	server, path := checking(t, "", "work in progress")

	reply := answer(t, server, command.Goodbye, nil, "")
	if !strings.Contains(reply, "Goodbye") {
		t.Errorf("the reply does not sign off:\n%s", reply)
	}
	if len(path.moved) != 0 {
		t.Errorf("goodbye moved labels: %v", path.moved)
	}
}

// assignCodechecker records one, the way `assign` does.
func assignCodechecker(t *testing.T, server *Server, handle string) {
	t.Helper()
	_, err := server.Checks.Update(context.Background(), server.Settings.TargetRepository(), 1,
		func(record people.Record) (people.Record, error) {
			record.AssignedCodechecker = handle
			return record, nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// A check is started by the person doing it. Starting one with nobody assigned
// would take it out of the queue that exists to find somebody.
func TestCheckStartRefusesWithNobodyAssigned(t *testing.T) {
	server, path := checking(t, "", "needs codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	if !strings.Contains(reply, "assign @user as codechecker") {
		t.Errorf("the refusal does not say how to fix it:\n%s", reply)
	}
	if len(path.moved) != 0 {
		t.Errorf("labels were moved on a check nobody is doing: %q", path.moved)
	}
}

// The labels are what the command is for: out of the queue, into work.
func TestCheckStartMovesTheLabels(t *testing.T) {
	server, path := checking(t, "", "needs codechecker", "journal")
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	want := []string{"-needs codechecker", "+work in progress"}
	if !equal(path.moved, want) {
		t.Errorf("label writes = %q, want %q", path.moved, want)
	}
	for _, say := range []string{"removed `needs codechecker`", "added `work in progress`",
		"Your checklist", "community-workflow-codechecker"} {
		if !strings.Contains(reply, say) {
			t.Errorf("the reply does not say %q:\n%s", say, reply)
		}
	}
	// The venue type label is none of the bot's business.
	if !command.HasLabel(path.issues[0].Labels, "journal") {
		t.Errorf("the venue type label was taken off: %q", path.issues[0].Labels)
	}
}

// A label the check already carries is not a request: the bot makes no request
// GitHub would answer with nothing, and a line about it on every command is
// how the useful lines get skipped.
func TestCheckStartAsksForNothingAlreadyTrue(t *testing.T) {
	server, path := checking(t, "", "work in progress")
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	if len(path.moved) != 0 {
		t.Errorf("a label already right was written anyway: %q", path.moved)
	}
	if strings.Contains(reply, "Labels:") {
		t.Errorf("the reply reports a change that did not happen:\n%s", reply)
	}
}

// Without a configuration to ask CC-CFG-023 of there is no answer, and a label
// moved on a guess is worse than a label not moved.
func TestCheckFinishRefusesWithNothingToRead(t *testing.T) {
	server, path := checking(t, "", "work in progress")

	reply := answer(t, server, command.CheckFinish, nil, "")
	if !strings.Contains(reply, "I have changed nothing") {
		t.Errorf("the refusal does not say nothing changed:\n%s", reply)
	}
	if len(path.moved) != 0 {
		t.Errorf("labels were moved with nothing to check: %q", path.moved)
	}
}

// A configuration whose DOIs are still placeholders is a check waiting on
// metadata, not a finished one.
func TestCheckFinishHoldsAPlaceholderCheckPending(t *testing.T) {
	server, path := checking(t, "placeholders", "work in progress")

	reply := answer(t, server, command.CheckFinish, []string{"github::codecheckers/demo"}, "")
	want := []string{"-work in progress", "+metadata pending"}
	if !equal(path.moved, want) {
		t.Errorf("label writes = %q, want %q", path.moved, want)
	}
	if !strings.Contains(reply, "CC-CFG-023") {
		t.Errorf("the reply does not name the rule that held it back:\n%s", reply)
	}
	if strings.Contains(reply, "CC-REG") || strings.Contains(reply, "ready to be opened") {
		t.Errorf("a check waiting on metadata says anything about the entry being ready:\n%s", reply)
	}
}

// A clean configuration clears the workflow labels and nothing else, and the
// next step is the register entry.
func TestCheckFinishClearsACleanCheck(t *testing.T) {
	server, path := checking(t, "valid-2.0", "work in progress", "metadata pending", "id assigned", "journal")

	reply := answer(t, server, command.CheckFinish, []string{"github::codecheckers/demo"}, "")
	want := []string{"-work in progress", "-metadata pending"}
	if !equal(path.moved, want) {
		t.Errorf("label writes = %q, want %q", path.moved, want)
	}
	for _, kept := range []string{"id assigned", "journal"} {
		if !command.HasLabel(path.issues[0].Labels, kept) {
			t.Errorf("%q was taken off; only the workflow labels are the bot's", kept)
		}
	}
	if !strings.Contains(reply, "register.csv") {
		t.Errorf("the reply does not point at the next step:\n%s", reply)
	}
}

// A label the settings do not let the bot move is refused by internal/github,
// which is the one fence - and the reply says so in its own words rather than
// passing on the client's sentence. The fence itself is tested where it lives,
// in internal/github/comments_test.go.
func TestARefusedLabelIsExplainedNotPassedOn(t *testing.T) {
	server, path := checking(t, "", "needs codechecker")
	path.refuse = fmt.Errorf("refusing to remove the label %q: %w",
		"needs codechecker", github.ErrLabelNotAllowed)
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	if !strings.Contains(reply, "the settings do not list it under `labels.managed`") {
		t.Errorf("the reply does not say why the label stayed:\n%s", reply)
	}
	if strings.Contains(reply, "refusing to remove") {
		t.Errorf("the client's own sentence reached the reply:\n%s", reply)
	}
}

// A reply path that cannot write labels changes nothing and says so, rather
// than reporting a change it did not make.
func TestAPathThatCannotLabelSaysSo(t *testing.T) {
	server, _ := testServer(t)
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	if !strings.Contains(reply, "ask me on the checks issue") {
		t.Errorf("the reply does not say the labels were not changed:\n%s", reply)
	}
}

// A failed write is reported as not done. Undoing the rest over it would be
// worse: a label is one click to put right, and the command's other work is
// not wrong because GitHub had a bad second.
func TestAFailedLabelWriteIsReportedNotUndone(t *testing.T) {
	server, path := checking(t, "", "needs codechecker")
	path.refuse = fmt.Errorf("502 Bad Gateway\n<html>the proxy's own page</html>")
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	if !strings.Contains(reply, "could not remove `needs codechecker`") {
		t.Errorf("the reply does not report the failure:\n%s", reply)
	}
	if !strings.Contains(reply, "Your checklist") {
		t.Errorf("a failed label write stopped the rest of the command:\n%s", reply)
	}
	if strings.Contains(reply, "\n<html>") {
		t.Errorf("the error's newlines reached the reply:\n%s", reply)
	}
}

// `status` answers from what the bot keeps, and changes nothing.
func TestStatusReportsWhatTheBotKeeps(t *testing.T) {
	server, path := checking(t, "", "needs codechecker", "journal")
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.Status, nil, "")
	for _, say := range []string{"`@a-codechecker`", "needs codechecker", "journal"} {
		if !strings.Contains(reply, say) {
			t.Errorf("the reply does not say %q:\n%s", say, reply)
		}
	}
	if len(path.moved) != 0 {
		t.Errorf("status moved a label: %q", path.moved)
	}
	// The queue label with a codechecker assigned is the disagreement the
	// central note exists for, and it is appended once.
	if got := strings.Count(reply, "The labels may not describe this check"); got != 1 {
		t.Errorf("the label note appears %d times:\n%s", got, reply)
	}
}

// Nothing is guessed out of the issue's prose: an identifier register.csv does
// not know names no repository, so there is nothing to check.
func TestStatusSaysWhenThereIsNothingToCheck(t *testing.T) {
	server, _ := checking(t, "")

	reply := answer(t, server, command.Status, nil, "")
	if !strings.Contains(reply, "name the repository") {
		t.Errorf("the reply does not say what it needs:\n%s", reply)
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The reply must not contradict itself. `check start` takes `needs codechecker`
// off, and the label note that the dispatch appends afterwards is compared
// against the issue - so a note built on the labels as they were before the
// write says the check "still carries" a label the same comment has just
// reported removing.
func TestTheLabelNoteSeesTheLabelsTheCommandJustChanged(t *testing.T) {
	server, path := checking(t, "", "needs codechecker")
	assignCodechecker(t, server, "a-codechecker")

	reply := answer(t, server, command.CheckStart, nil, "")
	if !strings.Contains(reply, "removed `needs codechecker`") {
		t.Fatalf("the label was not removed at all:\n%s", reply)
	}
	if strings.Contains(reply, "still carries the `needs codechecker` label") {
		t.Errorf("the reply reports the label as removed and as still there:\n%s", reply)
	}
	if command.HasLabel(path.issues[0].Labels, "needs codechecker") {
		t.Errorf("the label is still on the issue: %q", path.issues[0].Labels)
	}
}

// The assigned codechecker is offered `check start`, which is theirs to run,
// although `check` itself asks nothing about who they are on the check: the
// roles are read for what is offered, not only for what was typed (#56).
func TestTheAssignedCodecheckerIsOfferedTheirSubcommands(t *testing.T) {
	server, _ := checking(t, "", "needs codechecker")
	assignCodechecker(t, server, "a-codechecker")
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-codechecker"}

	for written, want := range map[string]string{
		"check strt": "Did you mean `@chekhovbot check start`?",
		"check":      "`@chekhovbot check start`",
	} {
		parsed, _ := command.Parse("@chekhovbot " + written)
		if reply := server.answer(context.Background(), event, parsed).body; !strings.Contains(reply, want) {
			t.Errorf("%q: the reply does not say %q:\n%s", written, want, reply)
		}
	}
}
