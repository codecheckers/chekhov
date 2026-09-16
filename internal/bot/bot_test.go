package bot

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/command"
)

const secret = "a-test-webhook-secret"

// recorder stands in for the reply path, so a test can read what the bot would
// have posted without a GitHub.
type recorder struct {
	mu       sync.Mutex
	comments []struct {
		Repository string
		Issue      int
		Body       string
	}
	err error
}

func (r *recorder) Comment(_ context.Context, repository string, issue int, body string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comments = append(r.comments, struct {
		Repository string
		Issue      int
		Body       string
	}{repository, issue, body})
	return int64(len(r.comments)), r.err
}

func (r *recorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.comments) == 0 {
		return ""
	}
	return r.comments[len(r.comments)-1].Body
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.comments)
}

func testServer(t *testing.T) (*Server, *recorder) {
	t.Helper()
	settings, err := config.Load("development")
	if err != nil {
		t.Fatal(err)
	}
	replies := &recorder{}
	server := New(settings, secret, replies, command.Deployment{
		Version: "0.1.0-test", Commit: "0123456789abcdef",
		Register: settings.TargetRepository(), Bot: settings.BotUser(),
		Environment: "development",
	})
	server.Logger = slog.New(slog.DiscardHandler)
	server.done = make(chan struct{}, 1)
	return server, replies
}

// deliver posts a recorded payload the way GitHub would, signature and all.
func deliver(t *testing.T, server *Server, eventType, fixture string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "webhook", fixture))
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/dispatch", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", eventType)
	request.Header.Set("X-GitHub-Delivery", "test-delivery")
	request.Header.Set("X-Hub-Signature-256", Sign(secret, body))

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code == http.StatusAccepted {
		// The command runs after the delivery is answered; wait for it rather
		// than sleeping.
		select {
		case <-server.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the command did not finish")
		}
	}
	return response
}

func TestCommandsIsAnswered(t *testing.T) {
	server, replies := testServer(t)

	if code := deliver(t, server, "issue_comment", "issue-comment-commands.json").Code; code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	reply := replies.last()
	if !strings.Contains(reply, "Commands I understand") {
		t.Errorf("the reply is not the listing: %s", reply)
	}
	if !strings.Contains(reply, "check") {
		t.Error("the listing does not mention the check command")
	}
	if strings.Contains(reply, "hello") {
		t.Error("the listing shows a hidden command")
	}
}

func TestHelloNamesTheDeployment(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issue_comment", "issue-comment-hello.json")
	reply := replies.last()
	for _, want := range []string{"0.1.0-test", "codecheckers/testing-dev-register"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q: %s", want, reply)
		}
	}
}

// An opened issue is a comment made of its own body: people write the first
// command into the issue they open.
func TestAnOpenedIssueIsRead(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issues", "issue-opened-hello.json")
	if replies.count() != 1 {
		t.Fatalf("%d replies, want 1", replies.count())
	}
	if comment := replies.comments[0]; comment.Issue != 43 {
		t.Errorf("answered issue %d, want 43", comment.Issue)
	}
}

func TestUnknownCommandIsAnswered(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issue_comment", "issue-comment-unknown.json")
	if !strings.Contains(replies.last(), "I do not know the command") {
		t.Errorf("the reply is not the unknown-command hint: %s", replies.last())
	}
}

// Everything that must not produce a reply, and must not be an error either.
func TestDeliveriesThatAreNotActedOn(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		fixture   string
	}{
		{"a comment that is conversation", "issue_comment", "issue-comment-conversation.json"},
		{"the bot's own comment", "issue_comment", "issue-comment-from-the-bot.json"},
		{"another repository", "issue_comment", "issue-comment-wrong-repository.json"},
		{"an edited comment", "issue_comment", "issue-comment-edited.json"},
		{"an event the bot does not handle", "pull_request", "pull-request-opened.json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, replies := testServer(t)

			response := deliver(t, server, testCase.eventType, testCase.fixture)
			if response.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", response.Code)
			}
			if replies.count() != 0 {
				t.Errorf("the bot answered: %s", replies.last())
			}
		})
	}
}

// The signature is checked before the body is read, so an unsigned delivery is
// never data the bot acts on.
func TestABadSignatureIsRefused(t *testing.T) {
	server, replies := testServer(t)
	body, err := os.ReadFile(filepath.Join("testdata", "webhook", "issue-comment-commands.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, signature := range []string{"", "sha256=deadbeef", Sign("the wrong secret", body)} {
		request := httptest.NewRequest(http.MethodPost, "/dispatch", strings.NewReader(string(body)))
		request.Header.Set("X-GitHub-Event", "issue_comment")
		request.Header.Set("X-Hub-Signature-256", signature)

		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("signature %q: status = %d, want 401", signature, response.Code)
		}
	}
	if replies.count() != 0 {
		t.Error("an unsigned delivery was acted on")
	}
}

func TestHealthSaysWhichBotThisIs(t *testing.T) {
	server, _ := testServer(t)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{"codecheckers/testing-dev-register", "0.1.0-test", "development", "rules_commit"} {
		if !strings.Contains(body, want) {
			t.Errorf("healthz does not report %q: %s", want, body)
		}
	}
}

// The check command needs to be told what to check until chekhov#13 lands, and
// says so rather than checking the wrong thing.
func TestCheckWithoutATargetExplainsItself(t *testing.T) {
	server, _ := testServer(t)

	reply := server.answer(context.Background(), mention{Repository: "codecheckers/testing-dev-register", Issue: 1},
		command.Command{Name: command.Check})
	if !strings.Contains(reply, "github::owner/repo") {
		t.Errorf("the reply does not say how to name a repository: %s", reply)
	}
}

// A path in a comment is written by anyone on the internet. A deployment does
// not read its own disk on their say-so.
func TestADeploymentRefusesALocalPath(t *testing.T) {
	server, _ := testServer(t)

	reply := server.answer(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1},
		command.Command{Name: command.Check, Args: []string{"/etc/passwd"}})
	if !strings.Contains(reply, "github::owner/repo") {
		t.Errorf("a path was taken as a target: %s", reply)
	}
}

// The health endpoint is unauthenticated. In production it says only what a
// stranger may know.
func TestHealthSaysLessInProduction(t *testing.T) {
	server, _ := testServer(t)
	server.Deployment.Environment = "production"

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	body := response.Body.String()
	for _, secret := range []string{"commit", "rules_commit", "token_expires", "online"} {
		if strings.Contains(body, secret) {
			t.Errorf("production health reports %q: %s", secret, body)
		}
	}
	for _, want := range []string{"status", "version", "register"} {
		if !strings.Contains(body, want) {
			t.Errorf("production health does not report %q: %s", want, body)
		}
	}
}

// version is answered from a comment, and names the register it works on.
func TestVersionIsAnswered(t *testing.T) {
	server, replies := testServer(t)

	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "acodechecker"},
		command.Command{Name: command.Version})
	if !strings.Contains(reply, "codecheckers/testing-dev-register") {
		t.Errorf("the version reply does not name the register:\n%s", reply)
	}
	if !strings.Contains(reply, "validation rules") {
		t.Errorf("the version reply does not name the rules catalogue:\n%s", reply)
	}
	_ = replies
}
