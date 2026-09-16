package github

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stub is a GitHub that answers what the test tells it to, in the style of
// internal/check/stub_test.go.
func stub(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := New("test-token", "codecheckers/testing-dev-register", "chekhov test")
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	client.Logger = slog.New(slog.DiscardHandler)
	client.RetryWait = 0
	return client
}

func TestCommentPosts(t *testing.T) {
	var got struct {
		path string
		body string
		auth string
	}
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(raw, &payload)
		got.path, got.body, got.auth = r.URL.Path, payload["body"], r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 42}`))
	})

	id, err := client.Comment(context.Background(), client.Repository, 7, "the answer")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Errorf("comment id = %d, want 42", id)
	}
	if want := "/repos/codecheckers/testing-dev-register/issues/7/comments"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if !strings.HasPrefix(got.body, "the answer") {
		t.Errorf("body = %q, want the reply first", got.body)
	}
	if !strings.Contains(got.body, "chekhov test") {
		t.Error("the comment carries no signature, so a reader cannot tell which bot answered")
	}
	if got.auth != "Bearer test-token" {
		t.Errorf("authorization = %q", got.auth)
	}
}

// The bot writes to one repository. A command that arrived from anywhere else
// is refused before the request, not after.
func TestCommentRefusesAnotherRepository(t *testing.T) {
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was made for a repository that is not the target")
	})

	_, err := client.Comment(context.Background(), "codecheckers/register", 1, "hello")
	if err == nil {
		t.Fatal("commenting on another repository must be refused")
	}
	if !strings.Contains(err.Error(), "codecheckers/register") {
		t.Errorf("the refusal does not name the repository: %v", err)
	}
}

func TestLongRepliesAreTruncatedAtALineBoundary(t *testing.T) {
	var posted string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		posted = payload["body"]
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	long := strings.Repeat("a line of a very long report\n", 4000) // well past the limit
	if _, err := client.Comment(context.Background(), client.Repository, 1, long); err != nil {
		t.Fatal(err)
	}
	if len(posted) > MaxCommentLength {
		t.Errorf("posted %d characters, GitHub accepts %d", len(posted), MaxCommentLength)
	}
	if !strings.Contains(posted, "was cut here") {
		t.Error("the truncated comment does not say that something was dropped")
	}
	if !strings.Contains(posted, "chekhov test") {
		t.Error("truncation dropped the signature")
	}
	if strings.Contains(posted, "a line of a very long repor\n") {
		t.Error("the cut landed in the middle of a line")
	}
}

func TestATransientFailureIsRetriedOnce(t *testing.T) {
	attempts := 0
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id": 5}`))
	})

	id, err := client.Comment(context.Background(), client.Repository, 3, "again")
	if err != nil {
		t.Fatal(err)
	}
	if id != 5 || attempts != 2 {
		t.Errorf("id = %d after %d attempts, want 5 after 2", id, attempts)
	}
}

// A refusal will be refused again: retrying it only delays the log line.
func TestARefusalIsNotRetried(t *testing.T) {
	attempts := 0
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
	})

	if _, err := client.Comment(context.Background(), client.Repository, 3, "nobody home"); err == nil {
		t.Fatal("a 404 must be reported, not swallowed")
	}
	if attempts != 1 {
		t.Errorf("%d attempts, want 1", attempts)
	}
}

func TestTokenExpiryIsRemembered(t *testing.T) {
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("github-authentication-token-expiration", "2099-12-24 15:04:05 UTC")
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	if _, err := client.Comment(context.Background(), client.Repository, 1, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := client.TokenExpiry(); got != "2099-12-24 15:04:05 UTC" {
		t.Errorf("token expiry = %q", got)
	}
}
