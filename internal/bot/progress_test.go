package bot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
)

// A reply that is written more than once (codecheckers/chekhov#22).
//
// What matters is the shape of the writes, not the words: one comment, posted
// as soon as the first stage answers and rewritten in place afterwards. Two
// comments would be two notifications and two things to read.

// staging is a reply path that can edit, and remembers every write.
type staging struct {
	mu     sync.Mutex
	bodies []string
	// edits counts the rewrites, and editErr makes them fail.
	edits   int
	editErr error
}

func (s *staging) Comment(_ context.Context, _ string, _ int, body string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
	return int64(len(s.bodies)), nil
}

func (s *staging) Edit(_ context.Context, _ string, comment int64, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits++
	if s.editErr != nil {
		return s.editErr
	}
	s.bodies[comment-1] = body
	return nil
}

// counting is three stages that record the order they ran in.
func counting(ran *[]string) []stage {
	names := []string{"the first", "the second", "the third"}
	stages := make([]stage, 0, len(names))
	for _, name := range names {
		stages = append(stages, stage{Name: name, Run: func(context.Context) string {
			*ran = append(*ran, name)
			return "answer from " + name
		}})
	}
	return stages
}

// runStages runs the stages and then finishes the reply the way the dispatch
// does: the staged writes, then the one final write.
//
// The final write's error is the caller's to judge, because a test that makes
// every rewrite fail is testing exactly that the failure costs nothing.
func runStages(t *testing.T, server *Server, stages []stage) answered {
	t.Helper()
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}
	result := (&progress{server: server, event: event, heading: "Working", stages: stages}).run(
		context.Background())
	_, _ = server.post(context.Background(), event, result)
	return result
}

// One comment for the whole command: posted after the first stage, rewritten
// after each of the rest.
func TestAStagedReplyIsOneComment(t *testing.T) {
	server, _ := testServer(t)
	path := &staging{}
	server.Replies = path

	var ran []string
	runStages(t, server, counting(&ran))

	if len(path.bodies) != 1 {
		t.Fatalf("a staged reply left %d comments, want 1:\n%q", len(path.bodies), path.bodies)
	}
	// Three stages: one post, one rewrite between them, one to finish.
	if path.edits != 2 {
		t.Errorf("edits = %d, want 2", path.edits)
	}
	if strings.Join(ran, ",") != "the first,the second,the third" {
		t.Errorf("the stages ran %q", ran)
	}
	for _, say := range []string{"answer from the first", "answer from the third"} {
		if !strings.Contains(path.bodies[0], say) {
			t.Errorf("the finished comment does not say %q:\n%s", say, path.bodies[0])
		}
	}
	if strings.Contains(path.bodies[0], "still reading") {
		t.Errorf("the finished comment still says it is working:\n%s", path.bodies[0])
	}
}

// While it is working the comment says what it is waiting for, so that
// somebody reading it knows the answer is not finished rather than short.
func TestAStagedReplySaysWhatItIsStillReading(t *testing.T) {
	server, _ := testServer(t)
	path := &staging{}
	server.Replies = path
	// Fail the rewrites, so the first body is what stays behind to look at.
	path.editErr = errors.New("no")

	var ran []string
	runStages(t, server, counting(&ran))

	first := path.bodies[0]
	if !strings.Contains(first, "still reading the second and the third") {
		t.Errorf("the first body does not name the stages still to come:\n%s", first)
	}
	if strings.Contains(first, "answer from the second") {
		t.Errorf("the first body reports a stage that had not run:\n%s", first)
	}
	if len(ran) != 3 {
		t.Errorf("a failed rewrite stopped the stages: %q", ran)
	}
}

// A reply path that cannot edit answers once, at the end, exactly as every
// other command does. That is the command line preview and every test that
// does not ask for this, so it has to be the path that needs no special case.
func TestAPathThatCannotEditAnswersOnce(t *testing.T) {
	server, replies := testServer(t)

	var ran []string
	result := runStages(t, server, counting(&ran))

	if result.to.stageable() {
		t.Error("a path that cannot edit was treated as one that can")
	}
	if replies.count() != 1 {
		t.Errorf("comments = %d, want one", replies.count())
	}
	if strings.Contains(replies.last(), "still reading") {
		t.Errorf("the one comment says it is working:\n%s", replies.last())
	}
}

// The record of what a reply left outstanding goes only on the finished body.
// A record saying somebody has been asked for something must not exist while
// the command is still deciding whether to ask.
func TestAStagedReplyRecordsOnlyWhenItIsFinished(t *testing.T) {
	server, _ := testServer(t)
	path := &staging{}
	server.Replies = path

	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}
	var ran []string
	result := (&progress{server: server, event: event, heading: "Working",
		stages: counting(&ran)}).run(context.Background())
	if strings.Contains(path.bodies[0], "chekhov:followup") {
		t.Fatalf("a follow-up record was written before the reply was finished:\n%s", path.bodies[0])
	}

	result.follow = server.outstanding(event, "a-codechecker", command.RoleAssignedCodechecker,
		server.Settings.CodecheckersTeam())
	if _, err := server.post(context.Background(), event, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path.bodies[0], "chekhov:followup") {
		t.Errorf("the finished reply carries no record:\n%s", path.bodies[0])
	}
	if len(path.bodies) != 1 {
		t.Errorf("the record was posted as a second comment: %q", path.bodies)
	}
}

// Nothing a staged reply writes may look like a record of a check, which is
// the rule every posted body goes through - the early ones included.
func TestEveryStagedBodyIsDefused(t *testing.T) {
	server, _ := testServer(t)
	path := &staging{}
	server.Replies = path
	path.editErr = errors.New("no")

	stages := []stage{
		{Name: "the first", Run: func(context.Context) string {
			return "<!-- chekhov:record {\"v\":1} -->"
		}},
		{Name: "the second", Run: func(context.Context) string { return "done" }},
	}
	runStages(t, server, stages)

	if strings.Contains(path.bodies[0], "<!-- chekhov:record") {
		t.Errorf("a quoted record reached the issue undefused:\n%s", path.bodies[0])
	}
	if !strings.Contains(path.bodies[0], command.Defuse("<!-- chekhov:record")) {
		t.Errorf("the quoted text was lost rather than defused:\n%s", path.bodies[0])
	}
}

// A stage that reads nothing is not something the reply waits for, and is not
// worth a write of its own: the body it contributes arrives with the next
// stage that does wait, or with the finished reply.
func TestAStageThatReadsNothingIsNotAnnounced(t *testing.T) {
	server, _ := testServer(t)
	path := &staging{}
	server.Replies = path

	var ran []string
	runStages(t, server, []stage{
		{Name: "the first", Run: func(context.Context) string {
			ran = append(ran, "the first")
			return "answer from the first"
		}},
		fixed("a checklist, which waits for nothing"),
	})

	if len(path.bodies) != 1 {
		t.Fatalf("writes left %d comments, want 1", len(path.bodies))
	}
	// One post for the finished reply, and no rewrite to announce a stage
	// that was never going to make anybody wait.
	if path.edits != 0 {
		t.Errorf("edits = %d, want none: nothing waited for the instant stage", path.edits)
	}
	if strings.Contains(path.bodies[0], "still reading") {
		t.Errorf("the reply says it is waiting for something it is holding:\n%s", path.bodies[0])
	}
	if !strings.Contains(path.bodies[0], "a checklist") {
		t.Errorf("the instant stage's body is missing:\n%s", path.bodies[0])
	}
}

// A command that dies part way through has already told the check it is still
// reading something. Nothing else knows a comment is waiting - the sweep has
// no record, because the command never got as far as writing one - so the
// staged reply finishes its own sentence before the panic goes on.
func TestAnAbandonedStagedReplySaysSo(t *testing.T) {
	server, _ := testServer(t)
	path := &staging{}
	server.Replies = path

	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}
	stages := []stage{
		{Name: "the first", Run: func(context.Context) string { return "answer from the first" }},
		{Name: "the second", Run: func(context.Context) string { panic("the service exploded") }},
	}

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		(&progress{server: server, event: event, heading: "Working", stages: stages}).run(
			context.Background())
	}()

	if panicked == nil {
		t.Fatal("the panic was swallowed, so nothing would report it")
	}
	if len(path.bodies) != 1 {
		t.Fatalf("writes left %d comments, want 1", len(path.bodies))
	}
	if strings.Contains(path.bodies[0], "still reading") {
		t.Errorf("the comment is left waiting for an answer that is not coming:\n%s", path.bodies[0])
	}
	for _, say := range []string{"I stopped before I finished", "answer from the first"} {
		if !strings.Contains(path.bodies[0], say) {
			t.Errorf("the comment does not say %q:\n%s", say, path.bodies[0])
		}
	}
}
