package github

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// guarded is a client as a deployment configures it: one repository, one
// organisation, the teams it may add people to.
func guarded() *Client {
	client := New("a-token", "codecheckers/testing-dev-register", "")
	client.Organisation, client.ManagedTeams = "codecheckers", []string{"codecheckers", "institutional-codecheckers"}
	return client
}

// The writes the bot makes today all pass, and every other write is refused
// in do, whichever method built it.
func TestTheGuardLetsThroughOnlyTheBotsOwnWrites(t *testing.T) {
	const repo = "https://api.github.com/repos/codecheckers/testing-dev-register"
	cases := []struct {
		name, method, url string
		allowed           bool
	}{
		{"a comment", http.MethodPost, repo + "/issues/7/comments", true},
		{"editing a comment", http.MethodPatch, repo + "/issues/comments/99", true},
		{"an assignee", http.MethodPost, repo + "/issues/7/assignees", true},
		{"an assignee removed", http.MethodDelete, repo + "/issues/7/assignees", true},
		{"a label", http.MethodPost, repo + "/issues/7/labels", true},
		{"a label removed", http.MethodDelete, repo + "/issues/7/labels/needs%20codechecker", true},
		{"a title", http.MethodPatch, repo + "/issues/7", true},
		// No method builds it, so the last fence is no looser than mine.
		{"the repository's name written differently", http.MethodPost,
			"https://api.github.com/repos/Codecheckers/Testing-Dev-Register/issues/7/comments", false},
		{"a managed team", http.MethodPut,
			"https://api.github.com/orgs/codecheckers/teams/codecheckers/memberships/a-codechecker", true},
		{"reading anywhere", http.MethodGet, "https://api.github.com/repos/codecheckers/register/issues", true},
		{"reading the organisation", http.MethodGet, "https://api.github.com/orgs/codecheckers/members", true},

		{"the production register", http.MethodPost,
			"https://api.github.com/repos/codecheckers/register/issues/7/comments", false},
		{"a repository's own settings", http.MethodPatch, repo, false},
		{"a file in the repository", http.MethodPut, repo + "/contents/register.csv", false},
		{"a repository's labels themselves", http.MethodPost, repo + "/labels", false},
		{"a path that climbs out", http.MethodPost,
			repo + "/issues/../../register/issues/7/comments", false},
		{"the organisation role", http.MethodPut,
			"https://api.github.com/orgs/codecheckers/memberships/a-codechecker", false},
		{"the editors team", http.MethodPut,
			"https://api.github.com/orgs/codecheckers/teams/editors/memberships/a-codechecker", false},
		{"another organisation", http.MethodPut,
			"https://api.github.com/orgs/elsewhere/teams/codecheckers/memberships/a-codechecker", false},
		{"removing from a team", http.MethodDelete,
			"https://api.github.com/orgs/codecheckers/teams/codecheckers/memberships/a-codechecker", false},
		{"another host, even to read", http.MethodGet,
			"https://api.example.invalid/repos/codecheckers/testing-dev-register/issues", false},
		{"the API without TLS", http.MethodPost,
			"http://api.github.com/repos/codecheckers/testing-dev-register/issues/7/comments", false},
	}
	client := guarded()
	for _, testCase := range cases {
		target, err := url.Parse(testCase.url)
		if err != nil {
			t.Fatal(err)
		}
		err = client.guard(testCase.method, target)
		if (err == nil) != testCase.allowed {
			t.Errorf("%s: %s %s: allowed %v, got %v", testCase.name, testCase.method, testCase.url,
				testCase.allowed, err)
		}
	}
}

// GitHub answers a write to a renamed repository with a 307 to
// /repositories/<id>/..., and Go would follow it with the same method, body
// and token. The hop is put to the guard as the first request was.
func TestARedirectedWriteIsGuardedToo(t *testing.T) {
	var paths []string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/repos/codecheckers/testing-dev-register/issues/7/comments" {
			w.Header().Set("Location", "/repositories/123/issues/7/comments")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	// Through attempt, because the refusal comes back wrapped by http.Client
	// and a retry that read it as the network would send the write twice.
	ctx := context.Background()
	err := client.attempt(ctx, "posting through a redirect", func() error {
		_, err := client.do(ctx, http.MethodPost,
			client.BaseURL+"/repos/codecheckers/testing-dev-register/issues/7/comments", []byte(`{"body":"hi"}`))
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("the redirected write was not refused: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("the redirect was followed: %v", paths)
	}
}

// A write that do refuses never reaches the server, carries no token there,
// and is not tried a second time: a refusal will not change a second later.
func TestARefusedWriteNeverLeaves(t *testing.T) {
	requests := 0
	client := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()

	// Built by hand, as a method that forgot mine would build it.
	other := client.BaseURL + "/repos/codecheckers/register/issues/7/comments"
	err := client.attempt(ctx, "posting where it should not", func() error {
		_, err := client.do(ctx, http.MethodPost, other, []byte(`{"body":"hello"}`))
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("the write was not refused as such: %v", err)
	}
	if requests != 0 {
		t.Errorf("%d request(s) reached the server", requests)
	}

	// The same client still writes where it may.
	if _, err := client.do(ctx, http.MethodPost,
		client.BaseURL+"/repos/"+client.Repository+"/issues/7/comments", []byte(`{}`)); err != nil {
		t.Errorf("a write to the configured repository was refused: %v", err)
	}
	if requests != 1 {
		t.Errorf("%d request(s) reached the server, want 1", requests)
	}
}

// Writes queue rather than burst: several sent at once reach GitHub one
// interval apart, every one of them, and a read is not held up by the queue.
func TestWritesArePacedAndQueued(t *testing.T) {
	const interval = 60 * time.Millisecond
	var mu sync.Mutex
	var writes []time.Time
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mu.Lock()
			writes = append(writes, time.Now())
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"id": 1}`))
	})
	client.WriteInterval = interval
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Comment(ctx, client.Repository, 7, "hello"); err != nil {
				t.Errorf("a queued comment failed: %v", err)
			}
		}()
	}
	// A read while the writes queue is answered at once.
	time.Sleep(interval / 4)
	started := time.Now()
	if _, err := client.Issue(ctx, client.Repository, 7); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(started); waited > interval {
		t.Errorf("a read waited %v behind the writes", waited)
	}
	wg.Wait()

	if len(writes) != 4 {
		t.Fatalf("%d writes reached GitHub, want all 4", len(writes))
	}
	for i := 1; i < len(writes); i++ {
		// A little slack for the clock the handler reads on the other side.
		if gap := writes[i].Sub(writes[i-1]); gap < interval-5*time.Millisecond {
			t.Errorf("writes %d and %d were %v apart, want at least %v", i, i+1, gap, interval)
		}
	}
}

// A write whose context ends while it waits for its turn gives up its place:
// it is not sent, and the caller is told it was not made.
func TestAWriteThatStopsWaitingIsNotSent(t *testing.T) {
	requests := 0
	client := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"id": 1}`))
	})
	client.WriteInterval = time.Hour
	if _, err := client.Comment(context.Background(), client.Repository, 7, "first"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Comment(ctx, client.Repository, 7, "second")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the queued write did not say it gave up: %v", err)
	}
	if requests != 1 {
		t.Errorf("%d requests reached GitHub, want only the first", requests)
	}
}

// GitHub's secondary limit is a 403 that says so only in its body, and names
// no time; the queue holds for the minute GitHub asks for, rather than writing
// on into it three seconds apart.
func TestTheSecondaryLimitHoldsTheQueue(t *testing.T) {
	cases := map[string]struct {
		err  error
		want time.Duration
	}{
		"a secondary limit": {&httpretry.StatusError{Status: http.StatusForbidden,
			Body: `{"message":"You have exceeded a secondary rate limit"}`}, secondaryLimitPause},
		"one that names its time": {&httpretry.StatusError{Status: http.StatusForbidden,
			Body: "secondary rate limit", RetryAfter: 30 * time.Second}, 30 * time.Second},
		"a 403 that is a refusal": {&httpretry.StatusError{Status: http.StatusForbidden,
			Body: "Resource not accessible by integration"}, 0},
		"no failure": {nil, 0},
	}
	for name, testCase := range cases {
		if got := hold(testCase.err); got != testCase.want {
			t.Errorf("%s: hold = %v, want %v", name, got, testCase.want)
		}
	}
}
