package github

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// A record lives at the end of what it is written into, so an edit that would
// be cut has to be refused rather than silently truncated - the reply path
// truncates, this one must not.
func TestEditingRefusesABodyThatWouldBeCut(t *testing.T) {
	asked := false
	client := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		asked = true
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	err := client.Edit(context.Background(), "codecheckers/testing-dev-register", 1,
		strings.Repeat("x", MaxCommentLength+1))
	if err == nil {
		t.Fatal("an over-long edit was sent")
	}
	if !strings.Contains(err.Error(), "over GitHub's limit") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if asked {
		t.Error("the request was made anyway")
	}
}

// What Edit writes is what is read back: no signature, no truncation, nothing
// appended after the body the caller composed.
func TestEditingWritesTheBodyExactly(t *testing.T) {
	var sent string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		sent = string(body)
		_, _ = w.Write([]byte(`{"id": 1}`))
	})

	if err := client.Edit(context.Background(), "codecheckers/testing-dev-register", 1,
		"<!-- chekhov:roles {} -->\n"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(sent, `chekhov:roles`) || strings.Contains(sent, "chekhov test") {
		t.Errorf("the body was not sent as written: %s", sent)
	}
}

// The bot writes to one repository, whatever it is asked.
func TestTheCommentMethodsRefuseAnotherRepository(t *testing.T) {
	client := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a request was made for another repository")
		_, _ = w.Write([]byte(`[]`))
	})
	ctx := context.Background()

	if _, err := client.Comments(ctx, "codecheckers/register", 1); err == nil {
		t.Error("the comments of another repository were read")
	}
	if err := client.Edit(ctx, "codecheckers/register", 1, "hello"); err == nil {
		t.Error("a comment on another repository was edited")
	}
	if _, err := client.Assign(ctx, "codecheckers/register", 1, "nuest"); err == nil {
		t.Error("somebody was assigned on another repository")
	}
	if err := client.Unassign(ctx, "codecheckers/register", 1, "nuest"); err == nil {
		t.Error("somebody was unassigned on another repository")
	}
	if err := client.AddLabel(ctx, "codecheckers/register", 1, "id assigned"); err == nil {
		t.Error("an issue of another repository was labelled")
	}
	if err := client.SetTitle(ctx, "codecheckers/register", 1, "x | 2026-001"); err == nil {
		t.Error("an issue of another repository was retitled")
	}
}

// A label is added to those the issue has, and a title changes nothing but
// the title: each is one request, to the endpoint that does only that.
func TestLabellingAndRetitlingSendOneNarrowRequest(t *testing.T) {
	var requests []string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		_, _ = w.Write([]byte(`{}`))
	})
	client.AddableLabels = []string{"id assigned"}
	ctx := context.Background()

	if err := client.AddLabel(ctx, "codecheckers/testing-dev-register", 7, "id assigned"); err != nil {
		t.Fatalf("label: %v", err)
	}
	if err := client.SetTitle(ctx, "codecheckers/testing-dev-register", 7, "Baetzel | 2026-020"); err != nil {
		t.Fatalf("title: %v", err)
	}

	want := []string{
		`POST /repos/codecheckers/testing-dev-register/issues/7/labels {"labels":["id assigned"]}`,
		`PATCH /repos/codecheckers/testing-dev-register/issues/7 {"title":"Baetzel | 2026-020"}`,
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(requests, "\n"), strings.Join(want, "\n"))
	}
}

// The labels are the register's: the bot adds or removes only those the
// settings list, and a refused change never reaches GitHub - which would
// otherwise create a label that does not exist yet.
func TestOnlyTheManagedLabelsChange(t *testing.T) {
	var requests []string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/absent") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "Label does not exist"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	client.AddableLabels = []string{"id assigned"}
	client.RemovableLabels = []string{"needs codechecker", "absent"}
	ctx, repo := context.Background(), "codecheckers/testing-dev-register"

	for _, refused := range []error{
		client.AddLabel(ctx, repo, 7, "institution"),
		client.AddLabel(ctx, repo, 7, "id asigned"),
		client.RemoveLabel(ctx, repo, 7, "id assigned"),
	} {
		if refused == nil || !strings.Contains(refused.Error(), "labels.managed") {
			t.Errorf("a label outside the list was changed: %v", refused)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("a refused change was sent: %v", requests)
	}

	if err := client.AddLabel(ctx, repo, 7, "ID Assigned"); err != nil {
		t.Errorf("a listed label, written differently, was refused: %v", err)
	}
	if err := client.RemoveLabel(ctx, repo, 7, "needs codechecker"); err != nil {
		t.Errorf("a listed label was not removed: %v", err)
	}
	// Removing a label the issue does not carry leaves it as asked.
	if err := client.RemoveLabel(ctx, repo, 7, "absent"); err != nil {
		t.Errorf("removing a label that is not there failed: %v", err)
	}
	want := "POST /repos/" + repo + "/issues/7/labels\n" +
		"DELETE /repos/" + repo + "/issues/7/labels/needs codechecker\n" +
		"DELETE /repos/" + repo + "/issues/7/labels/absent"
	if got := strings.Join(requests, "\n"); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
