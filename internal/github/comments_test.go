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
}
