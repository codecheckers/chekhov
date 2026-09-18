package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A team larger than one page is read whole. A membership read as far as the
// first hundred would quietly demote every editor after it.
func TestATeamIsReadPageByPage(t *testing.T) {
	var asked []string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			members := make([]string, 0, membersPerPage)
			for i := range membersPerPage {
				members = append(members, fmt.Sprintf(`{"login": "member-%d"}`, i))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(members, ","))
		default:
			_, _ = w.Write([]byte(`[{"login": "NuEsT"}]`))
		}
	})

	handles, err := client.TeamMembers(context.Background(), "codecheckers", "editors")
	if err != nil {
		t.Fatalf("read the team: %v", err)
	}
	if len(handles) != membersPerPage+1 {
		t.Errorf("read %d members, want %d", len(handles), membersPerPage+1)
	}
	if handles[len(handles)-1] != "NuEsT" {
		t.Errorf("the last member is %q; handles are passed on as GitHub writes them", handles[len(handles)-1])
	}
	if len(asked) != 2 {
		t.Errorf("asked for pages %v, want two", asked)
	}
}

// A token without Members: read is an error, never an empty team - and it is
// not retried, because it will not change.
func TestATeamTheTokenMayNotReadIsAnError(t *testing.T) {
	requests := 0
	client := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by personal access token"}`))
	})

	handles, err := client.TeamMembers(context.Background(), "codecheckers", "editors")
	if err == nil {
		t.Fatalf("a refusal was read as a team of %d", len(handles))
	}
	if !strings.Contains(err.Error(), "codecheckers/editors") {
		t.Errorf("the error does not say which team: %v", err)
	}
	if requests != 1 {
		t.Errorf("the refusal was asked %d times; it will not change", requests)
	}
}

// A hiccup is retried, so that one bad response at startup does not leave the
// bot with no editors for the day.
func TestAFailedTeamReadIsRetriedOnce(t *testing.T) {
	requests := 0
	client := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"login": "nuest"}]`))
	})

	handles, err := client.TeamMembers(context.Background(), "codecheckers", "editors")
	if err != nil {
		t.Fatalf("read the team: %v", err)
	}
	if len(handles) != 1 || requests != 2 {
		t.Errorf("read %v after %d requests", handles, requests)
	}
}
