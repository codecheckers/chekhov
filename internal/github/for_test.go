package github

import (
	"context"
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
