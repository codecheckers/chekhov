package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A client For the lists comments there and nowhere else, carries none of the
// register client's allowances, and waits in the register client's queue
// (codecheckers/chekhov#49).
func TestAClientForTheListsWritesThereOnly(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	register := stub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 7}`))
	})
	register.Organisation, register.ManagedTeams = "codecheckers", []string{"codecheckers"}
	register.AddableLabels, register.RemovableLabels = []string{"id assigned"}, []string{"id assigned"}
	register.Rows = RegisterRows("register.csv")

	register.Signature = "working on `codecheckers/testing-dev-register`"
	lists := register.For("codecheckers/testing-dev-codecheckers", "working on `codecheckers/testing-dev-codecheckers`")
	ctx := context.Background()

	if _, err := lists.Comment(ctx, "codecheckers/testing-dev-codecheckers", 3, "hello"); err != nil {
		t.Fatalf("a comment on the lists was refused: %v", err)
	}
	if _, err := lists.Comment(ctx, register.Repository, 3, "hello"); err == nil {
		t.Error("the lists client commented on the register")
	}
	if _, err := register.Comment(ctx, lists.Repository, 3, "hello"); err == nil {
		t.Error("the register client commented on the lists")
	}
	if err := lists.AddLabel(ctx, lists.Repository, 3, "id assigned"); err == nil {
		t.Error("the lists client may add a label it was never given")
	}
	if _, err := lists.AddToTeam(ctx, "codecheckers", "codecheckers", "a-made-up-registrant"); err == nil {
		t.Error("the lists client may change a team it was never given")
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"POST /repos/codecheckers/testing-dev-codecheckers/issues/3/comments"}; strings.Join(paths, "; ") != want[0] {
		t.Errorf("requests = %q, want only %q", paths, want)
	}
	if lists.queue() != register.queue() {
		t.Error("the lists client has a write queue of its own, so two repositories make two bursts")
	}
	if lists.Signature == register.Signature {
		t.Error("the lists client signs its comments as working on the register")
	}
	if lists.Rows.writes() || len(lists.AddableLabels) != 0 || lists.Organisation != "" {
		t.Errorf("the lists client carries the register's allowances: %+v", lists)
	}
}

// Closing is a PATCH of the state on an issue of the client's own repository,
// and nothing else (#49).
func TestCloseIssueClosesOnlyItsOwnRepositorysIssue(t *testing.T) {
	var sent []string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = append(sent, r.Method+" "+r.URL.Path+" "+string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()
	if err := client.CloseIssue(ctx, client.Repository, 7); err == nil {
		t.Error("a client not set to close closed an issue")
	}
	client.Closes = true
	if err := client.CloseIssue(ctx, client.Repository, 7); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := client.CloseIssue(ctx, "codecheckers/register", 7); err == nil {
		t.Error("closed an issue of another repository")
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "PATCH /repos/"+client.Repository+"/issues/7 ") ||
		!strings.Contains(sent[0], `"state":"closed"`) {
		t.Errorf("sent %q", sent)
	}
}

// Opening is a POST of an issue in the client's own repository, with a label
// it may add, from a client set to open - nothing else (#49).
func TestCreateIssueOpensOnlyWhereAllowed(t *testing.T) {
	var sent []string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = append(sent, r.Method+" "+r.URL.Path+" "+string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number": 9}`))
	})
	ctx := context.Background()
	if _, err := client.CreateIssue(ctx, client.Repository, "t", "b", nil); err == nil {
		t.Error("a client not set to open opened an issue")
	}
	client.Opens, client.AddableLabels = true, []string{"registration"}
	if _, err := client.CreateIssue(ctx, client.Repository, "t", "b", []string{"bug"}); err == nil {
		t.Error("opened an issue with a label it may not add")
	}
	if _, err := client.CreateIssue(ctx, "codecheckers/register", "t", "b", nil); err == nil {
		t.Error("opened an issue in another repository")
	}
	number, err := client.CreateIssue(ctx, client.Repository, "t", "b", []string{"registration"})
	if err != nil || number != 9 {
		t.Fatalf("open: %d, %v", number, err)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "POST /repos/"+client.Repository+"/issues ") ||
		!strings.Contains(sent[0], `"labels":["registration"]`) {
		t.Errorf("sent %q", sent)
	}
}
