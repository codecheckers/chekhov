package mastodon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const token = "a-secret-mastodon-token"

// instance is a stubbed Mastodon that records what it was asked.
type instance struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	routes   map[string]http.HandlerFunc
}

func newInstance(t *testing.T) (*instance, *Client) {
	t.Helper()
	stub := &instance{routes: map[string]http.HandlerFunc{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.requests = append(stub.requests, r)
		stub.bodies = append(stub.bodies, string(body))
		handler, ok := stub.routes[r.Method+" "+r.URL.Path]
		stub.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, "codecheck", token, "direct")
	client.HTTP = server.Client()
	client.RetryWait = time.Millisecond
	client.MediaWait = time.Second
	client.Logger = slog.New(slog.DiscardHandler)
	return stub, client
}

func (s *instance) handle(route string, handler http.HandlerFunc) { s.routes[route] = handler }

func (s *instance) count(route string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, request := range s.requests {
		if request.Method+" "+request.URL.Path == route {
			n++
		}
	}
	return n
}

func answer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func TestPostSendsTheStatusOnceWithItsKey(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("POST /api/v1/statuses", answer(200, `{"id":"1","url":"https://example.social/@codecheck/1"}`))

	posted, err := client.Post(context.Background(), Status{
		Text: "hello", MediaIDs: []string{"m1"}, Visibility: "direct", IdempotencyKey: "codecheck-1970-001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if posted.URL != "https://example.social/@codecheck/1" {
		t.Errorf("url = %q", posted.URL)
	}
	if n := stub.count("POST /api/v1/statuses"); n != 1 {
		t.Fatalf("%d posts, want 1", n)
	}
	request := stub.requests[0]
	if got := request.Header.Get("Idempotency-Key"); got != "codecheck-1970-001" {
		t.Errorf("Idempotency-Key = %q", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization = %q", got)
	}
	for _, want := range []string{"status=hello", "visibility=direct", "media_ids%5B%5D=m1"} {
		if !strings.Contains(stub.bodies[0], want) {
			t.Errorf("the form lacks %s: %s", want, stub.bodies[0])
		}
	}
}

// A development deployment must never post in public, whatever a caller asks.
func TestAnotherVisibilityIsRefusedBeforeAnyRequest(t *testing.T) {
	stub, client := newInstance(t)
	if _, err := client.Post(context.Background(), Status{Text: "hello", Visibility: "public"}); err == nil {
		t.Fatal("a public status was not refused")
	}
	if len(stub.requests) != 0 {
		t.Errorf("%d requests were made", len(stub.requests))
	}
}

func TestARefusalIsNotRetried(t *testing.T) {
	for _, code := range []int{401, 403, 422} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			stub, client := newInstance(t)
			stub.handle("POST /api/v1/statuses", answer(code, `{"error":"no"}`))
			_, err := client.Post(context.Background(), Status{Text: "hello", Visibility: "direct"})
			if err == nil {
				t.Fatal("no error")
			}
			if n := stub.count("POST /api/v1/statuses"); n != 1 {
				t.Errorf("%d attempts, want 1", n)
			}
			if strings.Contains(err.Error(), token) {
				t.Error("the error carries the token")
			}
		})
	}
}

func TestATransientFailureIsRetriedOnce(t *testing.T) {
	for _, code := range []int{429, 502} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			stub, client := newInstance(t)
			attempts := 0
			stub.handle("POST /api/v1/statuses", func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if attempts == 1 {
					// The rate limit refills in a moment, and the retry waits for it.
					w.Header().Set("X-RateLimit-Reset", time.Now().Add(50*time.Millisecond).UTC().Format(time.RFC3339Nano))
					w.WriteHeader(code)
					return
				}
				answer(200, `{"id":"2","url":"https://example.social/@codecheck/2"}`)(w, r)
			})
			started := time.Now()
			if _, err := client.Post(context.Background(), Status{Text: "hello", Visibility: "direct"}); err != nil {
				t.Fatal(err)
			}
			if attempts != 2 {
				t.Errorf("%d attempts, want 2", attempts)
			}
			if code == 429 && time.Since(started) < 40*time.Millisecond {
				t.Error("the retry did not wait for the rate limit to refill")
			}
		})
	}
}

func TestUploadWaitsForProcessing(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("POST /api/v2/media", answer(202, `{"id":"m1","url":null}`))
	polls := 0
	stub.handle("GET /api/v1/media/m1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls < 3 {
			answer(206, `{"id":"m1","url":null}`)(w, r)
			return
		}
		answer(200, `{"id":"m1","url":"https://example.social/m1.gif"}`)(w, r)
	})

	id, err := client.UploadMedia(context.Background(), "certificate.gif", "image/gif", []byte("GIF89a"), "the certificate")
	if err != nil {
		t.Fatal(err)
	}
	if id != "m1" || polls != 3 {
		t.Errorf("id = %q after %d polls, want m1 after 3", id, polls)
	}
	if !strings.Contains(stub.bodies[0], `filename="certificate.gif"`) || !strings.Contains(stub.bodies[0], "the certificate") {
		t.Errorf("the upload lacks the file or its description: %.200s", stub.bodies[0])
	}
}

// A direct status may or may not be in the account's own timeline, so the
// conversations are read as well, without listing a status twice.
func TestRecentStatusesIncludeDirectConversations(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v1/accounts/verify_credentials", answer(200, `{"id":"42","username":"codecheck"}`))
	stub.handle("GET /api/v1/accounts/42/statuses", answer(200, `[{"id":"1","content":"one"}]`))
	stub.handle("GET /api/v1/conversations", answer(200,
		`[{"last_status":{"id":"1","content":"one"}},{"last_status":{"id":"2","content":"two"}},{"last_status":null}]`))

	statuses, err := client.RecentStatuses(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if query := stub.requests[1].URL.RawQuery; !strings.Contains(query, "exclude_reblogs=true") {
		t.Errorf("the statuses were read with %q, which counts boosts", query)
	}
	if len(statuses) != 2 || statuses[1].Content != "two" {
		t.Errorf("statuses = %+v", statuses)
	}

	client.Visibility = "public"
	if _, err := client.RecentStatuses(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if n := stub.count("GET /api/v1/conversations"); n != 1 {
		t.Errorf("conversations read %d times, want only for direct", n)
	}
}

func TestLimitsAreRead(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v2/instance", answer(200, `{"configuration":{
		"statuses":{"max_characters":1500,"max_media_attachments":4,"characters_reserved_per_url":23},
		"media_attachments":{"image_size_limit":16777216,"supported_mime_types":["image/png","image/gif"]}}}`))

	limits, err := client.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxCharacters != 1500 || limits.CharactersPerURL != 23 || limits.ImageSizeLimit != 16777216 {
		t.Errorf("limits = %+v", limits)
	}
}

func TestResolveAccountSearchesWithResolve(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v2/search", answer(200, `{"accounts":[{"id":"9","acct":"alice@example.social"}]}`))

	account, err := client.ResolveAccount(context.Background(), "@alice@example.social")
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != "9" || account.Acct != "alice@example.social" {
		t.Errorf("account = %+v", account)
	}
	query := stub.requests[0].URL.Query()
	if query.Get("q") != "alice@example.social" || query.Get("resolve") != "true" || query.Get("type") != "accounts" {
		t.Errorf("query = %v", query)
	}
}

func TestResolveAccountRefusesWhenNothingIsFound(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v2/search", answer(200, `{"accounts":[]}`))

	if _, err := client.ResolveAccount(context.Background(), "@nobody@example.social"); err == nil {
		t.Fatal("no error for an account nothing resolved to")
	}
}

func TestRelationshipsAsksForEveryID(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v1/accounts/relationships", answer(200, `[{"id":"1","following":true},{"id":"2","following":false}]`))

	relationships, err := client.Relationships(context.Background(), []string{"1", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(relationships) != 2 || !relationships[0].Following || relationships[1].Following {
		t.Errorf("relationships = %+v", relationships)
	}
	query := stub.requests[0].URL.RawQuery
	if !strings.Contains(query, "id%5B%5D=1") || !strings.Contains(query, "id%5B%5D=2") {
		t.Errorf("query = %s", query)
	}
}

func TestRelationshipsOfNoAccountsMakesNoRequest(t *testing.T) {
	stub, client := newInstance(t)
	if _, err := client.Relationships(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.requests) != 0 {
		t.Error("a request was made for no accounts")
	}
}

func TestFollowFollows(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("POST /api/v1/accounts/9/follow", answer(200, `{"id":"9","following":true}`))

	if err := client.Follow(context.Background(), "9"); err != nil {
		t.Fatal(err)
	}
	if n := stub.count("POST /api/v1/accounts/9/follow"); n != 1 {
		t.Errorf("%d follow requests, want 1", n)
	}
}

func TestListsFindsOrCreatesAndAdds(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v1/lists", answer(200, `[{"id":"1","title":"Codecheckers"}]`))
	stub.handle("POST /api/v1/lists", answer(200, `{"id":"2","title":"Authors"}`))
	stub.handle("GET /api/v1/lists/1/accounts", answer(200, `[{"id":"9","acct":"alice@example.social"}]`))
	stub.handle("POST /api/v1/lists/1/accounts", answer(200, `[]`))

	lists, err := client.Lists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 1 || lists[0].Title != "Codecheckers" {
		t.Errorf("lists = %+v", lists)
	}

	created, err := client.CreateList(context.Background(), "Authors")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "2" || created.Title != "Authors" {
		t.Errorf("created = %+v", created)
	}

	already, err := client.ListAccounts(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(already) != 1 || already[0].Acct != "alice@example.social" {
		t.Errorf("already = %+v", already)
	}

	if err := client.AddToList(context.Background(), "1", []string{"10", "11"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stub.bodies[len(stub.bodies)-1], "account_ids%5B%5D=10") {
		t.Errorf("the add request lacks the account ids: %s", stub.bodies[len(stub.bodies)-1])
	}
}

func TestAddToListOfNoAccountsMakesNoRequest(t *testing.T) {
	stub, client := newInstance(t)
	if err := client.AddToList(context.Background(), "1", nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.requests) != 0 {
		t.Error("a request was made for no accounts")
	}
}

// A token of another account must not post in this deployment's name.
func TestVerifyAccountAcceptsTheConfiguredAccount(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v1/accounts/verify_credentials", answer(200, `{"id":"7","username":"codecheck"}`))

	if err := client.VerifyAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAccountRefusesAnotherAccount(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v1/accounts/verify_credentials", answer(200, `{"id":"7","username":"someone"}`))

	err := client.VerifyAccount(context.Background())
	if err == nil || !strings.Contains(err.Error(), "belongs to @someone") {
		t.Fatalf("error = %v, want a refusal naming the account", err)
	}
}

func TestATokenOfAnotherAccountIsRefused(t *testing.T) {
	stub, client := newInstance(t)
	stub.handle("GET /api/v1/accounts/verify_credentials", answer(200, `{"id":"7","username":"someone"}`))

	_, err := client.RecentStatuses(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "belongs to @someone") {
		t.Fatalf("error = %v, want a refusal naming the account", err)
	}
	if n := stub.count("GET /api/v1/accounts/7/statuses"); n != 0 {
		t.Error("the statuses of the wrong account were read")
	}
}
