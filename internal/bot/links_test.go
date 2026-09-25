package bot

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// editingRecorder is a reply path that can also replace a reply it posted.
type editingRecorder struct {
	*recorder
	mu    sync.Mutex
	edits []struct {
		Comment int64
		Body    string
	}
}

func (r *editingRecorder) EditReply(_ context.Context, _ string, comment int64, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.edits = append(r.edits, struct {
		Comment int64
		Body    string
	}{comment, body})
	return nil
}

// linksServer is a bot whose check can read a configuration on disk naming
// links, each answered by the stub: one gone, the rest there.
func linksServer(t *testing.T, count int) (*Server, string) {
	t.Helper()
	server, _ := testServer(t)
	stub := testserver.New(t)
	server.Services = &check.Services{Access: check.Access{
		HTTP: stub.Client(), Register: server.Settings.TargetRepository(),
	}}
	server.LocalPaths = true

	var configuration strings.Builder
	configuration.WriteString("repository:\n")
	for i := range count {
		path := fmt.Sprintf("/page-%d", i)
		stub.Status(path, http.StatusOK)
		fmt.Fprintf(&configuration, "  - %s\n", stub.At(path))
	}
	stub.Remove("/page-0")

	dir := t.TempDir()
	name := filepath.Join(dir, "codecheck.yml")
	if err := os.WriteFile(name, []byte(configuration.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return server, name
}

// Enough links to take a while: the bot says it has started, and replaces
// that comment with the answer, so one command leaves one comment.
func TestCheckLinksReplacesItsNoticeWithTheAnswer(t *testing.T) {
	server, target := linksServer(t, 6)
	replies := &editingRecorder{recorder: &recorder{}}
	server.Replies = replies

	event := mention{Repository: server.Settings.TargetRepository(), Issue: 3}
	answer := server.answer(context.Background(), event,
		command.Command{Name: command.Check, Args: []string{"links", target}})
	// As act does: post the answer, which knows what it replaces.
	id, err := server.post(context.Background(), event, answer)
	if err != nil || id != 1 {
		t.Fatalf("the answer was posted as comment %d, %v; want it in place of the notice", id, err)
	}

	if replies.count() != 1 || !strings.Contains(replies.last(), "Checking 6 links") {
		t.Fatalf("no notice was posted first: %d comments, last %q", replies.count(), replies.last())
	}
	if len(replies.edits) != 1 || replies.edits[0].Comment != 1 {
		t.Fatalf("the notice was not replaced: %+v", replies.edits)
	}
	for _, want := range []string{"6 found, 6 asked", "✖ 1 gone", "/page-0", "answers 404"} {
		if !strings.Contains(replies.edits[0].Body, want) {
			t.Errorf("the answer does not say %q:\n%s", want, replies.edits[0].Body)
		}
	}
}

// A reply path that cannot edit - the test recorder, the preview - gets the
// answer and nothing before it; so does a run with only a few links.
func TestCheckLinksAnswersInOneCommentWhenItCannotEdit(t *testing.T) {
	for name, setup := range map[string]struct {
		links int
		edits bool
	}{
		"cannot edit":   {6, false},
		"a few links":   {2, true},
		"no issue here": {6, true},
	} {
		t.Run(name, func(t *testing.T) {
			server, target := linksServer(t, setup.links)
			plain := &recorder{}
			server.Replies = plain
			if setup.edits {
				server.Replies = &editingRecorder{recorder: plain}
			}
			issue := 3
			if name == "no issue here" {
				issue = 0
			}

			answer := server.answer(context.Background(),
				mention{Repository: server.Settings.TargetRepository(), Issue: issue},
				command.Command{Name: command.Check, Args: []string{"links", target}})
			if plain.count() != 0 {
				t.Errorf("something was posted before the answer: %q", plain.last())
			}
			if !strings.Contains(answer.body, "✖ 1 gone") {
				t.Errorf("the answer is not the report: %s", answer.body)
			}
		})
	}
}

// Like every other check, a target is required: the issue alone is not
// enough to know which configuration and certificate it is about.
func TestCheckLinksNeedsATarget(t *testing.T) {
	server, _ := testServer(t)
	reply := server.answer(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1},
		command.Command{Name: command.Check, Args: []string{"links"}}).body
	if !strings.Contains(reply, "I need to be told what to check") {
		t.Errorf("no target was accepted: %s", reply)
	}
}

// `check links` may take longer than any other command, and only it.
func TestOnlyCheckLinksTakesLonger(t *testing.T) {
	if got := timeoutFor(command.Command{Name: command.Check, Args: []string{"links", "x/y"}}); got != linksTimeout {
		t.Errorf("check links may take %s, want %s", got, linksTimeout)
	}
	for _, other := range []command.Command{
		{Name: command.Check, Args: []string{"metadata", "x/y"}},
		{Name: command.Announce, Args: []string{"links"}},
	} {
		if got := timeoutFor(other); got != commandTimeout {
			t.Errorf("%s %q may take %s, want %s", other.Name, other.Args, got, commandTimeout)
		}
	}
}
