package check

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestArchived(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/github/repos/codecheckers/done", `{"full_name": "codecheckers/done", "archived": true}`)
	stub.JSON("/github/repos/codecheckers/open", `{"full_name": "codecheckers/open", "archived": false}`)
	stub.JSON("/github/repos/codecheckers/silent", `{"full_name": "codecheckers/silent"}`)
	stub.Status("/github/repos/codecheckers/blocked", http.StatusForbidden)
	stub.Status("/github/repos/codecheckers/down", http.StatusBadGateway)

	cases := []struct {
		name   string
		spec   string
		want   bool
		errors string
	}{
		{name: "an archived copy", spec: "github::codecheckers/done", want: true},
		{name: "a copy still open", spec: "github::codecheckers/open"},
		{name: "a copy in a directory is the repository's answer",
			spec: "github::codecheckers/done|2021-012", want: true},
		{name: "an answer without the field is not a no",
			spec: "github::codecheckers/silent", errors: "did not say"},
		{name: "a 403 could not tell", spec: "github::codecheckers/blocked", errors: "could not read"},
		{name: "a 5xx could not tell", spec: "github::codecheckers/down", errors: "could not read"},
		{name: "a repository outside the organisation is not asked",
			spec: "github::an-author/paper", errors: "not a copy"},
		{name: "a GitLab copy cannot be read without a token",
			spec: "gitlab::cdchck/community-codechecks/a-check", errors: "without a token"},
		{name: "a Zenodo record is not a copy", spec: "zenodo::3674056", errors: "not a copy"},
		{name: "nor an OSF project", spec: "osf::abcde", errors: "not a copy"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			spec, err := ParseRepositorySpec(testCase.spec)
			if err != nil {
				t.Fatalf("spec: %v", err)
			}
			archived, err := stub.services.Archived(spec)
			switch {
			case testCase.errors != "" && err == nil:
				t.Fatalf("read %v, want an error about %q", archived, testCase.errors)
			case testCase.errors != "":
				if !strings.Contains(err.Error(), testCase.errors) {
					t.Fatalf("error %q, want it to say %q", err, testCase.errors)
				}
				if archived {
					t.Error("an error must not also say archived")
				}
				return
			case err != nil:
				t.Fatalf("archived: %v", err)
			}
			if archived != testCase.want {
				t.Errorf("archived %v, want %v", archived, testCase.want)
			}
		})
	}
}

func TestArchivedOfAGitLabCopySaysWhy(t *testing.T) {
	stub := newStub(t)
	_, err := stub.services.Archived(RepositorySpec{Type: "gitlab", Path: "cdchck/a"})
	if !errors.Is(err, ErrArchiveUnknown) {
		t.Errorf("error %v, want it to wrap ErrArchiveUnknown so a caller can tell it apart", err)
	}
}

func TestArchivedNeedsTheServices(t *testing.T) {
	var offline *Services
	if _, err := offline.Archived(RepositorySpec{Type: "github", Path: "codecheckers/b"}); err == nil {
		t.Error("an offline bot should say it cannot look, not answer that the copy is open")
	}
}

func TestIsCodecheckCopy(t *testing.T) {
	cases := map[string]bool{
		"github::CodeCheckers/a":                true,
		"github::codecheckers/a":                true,
		"github::codecheckers/a|sub":            true,
		"gitlab::cdchck/community-codechecks/a": true,
		"github::codecheckers-fork/a":           false,
		"github::an-author/codecheckers":        false,
		"gitlab::someone/cdchck":                false,
		"zenodo::1234567":                       false,
		"osf::abcde":                            false,
	}
	for written, want := range cases {
		spec, err := ParseRepositorySpec(written)
		if err != nil {
			t.Fatalf("%s: %v", written, err)
		}
		if got := IsCodecheckCopy(spec); got != want {
			t.Errorf("IsCodecheckCopy(%s) = %v, want %v", written, got, want)
		}
	}
}

func TestRowOfIssue(t *testing.T) {
	stub := newStub(t)
	stub.register("Certificate,Repository,Type,Venue,Issue\n"+
		"1970-001,github::codecheckers/one,journal,FAKE,101\n"+
		"1970-002,github::codecheckers/two,journal,FAKE, 102 \n"+
		"1970-003,github::codecheckers/three,journal,FAKE,NA\n", "")

	row, found, err := stub.services.RowOfIssue(102)
	if err != nil || !found || row.Certificate != "1970-002" {
		t.Errorf("RowOfIssue(102) = %+v, %v, %v; want 1970-002, found", row, found, err)
	}
	if _, found, err := stub.services.RowOfIssue(103); err != nil || found {
		t.Errorf("RowOfIssue(103) found %v, error %v; want no row and no error", found, err)
	}
	var offline *Services
	if _, _, err := offline.RowOfIssue(101); err == nil {
		t.Error("an offline bot should say it cannot look, not answer that there is no row")
	}
}
