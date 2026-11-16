package check

import (
	"context"
	"net/http"
	"testing"
)

// An issue's first post and comments, read for `chekhov include check` with
// an issue URL (codecheckers/chekhov#49). The handles were checked to belong
// to nobody.
func TestIssueReadsTheFirstPostAndTheComments(t *testing.T) {
	s := newStub(t)
	s.JSON("/github/repos/codecheckers/testing-lists/issues/7", `{"user": {"login": "a-made-up-registrant"}, "body": "Your data:"}`)
	s.JSON("/github/repos/codecheckers/testing-lists/issues/7/comments",
		`[{"user": {"login": "chekhovbot"}, "body": "Thanks"}, {"user": {"login": "a-made-up-registrant"}, "body": "again"}]`)
	s.Status("/github/repos/codecheckers/testing-lists/issues/8", http.StatusNotFound)
	ctx := context.Background()

	issue, comments, err := s.services.Issue(ctx, "codecheckers/testing-lists", 7)
	if err != nil {
		t.Fatal(err)
	}
	if issue != (IssueText{Author: "a-made-up-registrant", Body: "Your data:"}) || len(comments) != 2 ||
		comments[1] != (IssueText{Author: "a-made-up-registrant", Body: "again"}) {
		t.Errorf("issue = %+v, comments = %+v", issue, comments)
	}
	if _, _, err := s.services.Issue(ctx, "codecheckers/testing-lists", 8); !IsNotFound(err) {
		t.Errorf("an issue GitHub does not have: %v, want not found", err)
	}
	if _, _, err := (*Services)(nil).Issue(ctx, "codecheckers/testing-lists", 7); err == nil {
		t.Error("an offline run read an issue")
	}
}

func TestIssueURL(t *testing.T) {
	for text, want := range map[string]struct {
		repository string
		number     int
		ok         bool
	}{
		"https://github.com/codecheckers/codecheckers/issues/98":                      {"codecheckers/codecheckers", 98, true},
		"github.com/codecheckers/codecheckers/issues/98":                              {"codecheckers/codecheckers", 98, true},
		"https://github.com/codecheckers/codecheckers/issues/98#issue-6315570486":     {"codecheckers/codecheckers", 98, true},
		"https://github.com/codecheckers/codecheckers/issues/98#issuecomment-1234567": {"codecheckers/codecheckers", 98, true},
		"https://github.com/codecheckers/codecheckers/pull/101":                       {},
		"https://example.invalid/codecheckers/codecheckers/issues/98":                 {},
		"codecheckers.csv": {},
	} {
		repository, number, ok := IssueURL(text)
		if repository != want.repository || number != want.number || ok != want.ok {
			t.Errorf("IssueURL(%q) = %q, %d, %v, want %+v", text, repository, number, ok, want)
		}
	}
}
