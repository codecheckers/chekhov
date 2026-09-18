package check

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestLanguages(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/github/repos/codecheckers/demo/languages", `{"R": 4000, "Python": 9000, "": 1}`)
	// The test server sees the path decoded, %2F and all.
	stub.JSON("/gitlab/api/v4/projects/cdchck/demo/languages", `{"Julia": 80.0, "Shell": 20.0}`)
	stub.Status("/github/repos/codecheckers/blocked/languages", http.StatusForbidden)
	stub.JSON("/github/repos/codecheckers/empty/languages", `{}`)

	cases := []struct {
		name   string
		spec   string
		want   []string
		errors string
	}{
		{name: "GitHub answers bytes per language, most first",
			spec: "github::codecheckers/demo", want: []string{"Python", "R"}},
		{name: "GitLab answers percentages, and is read the same way",
			spec: "gitlab::cdchck/demo", want: []string{"Julia", "Shell"}},
		{name: "a repository with no code has no languages, which is not an error",
			spec: "github::codecheckers/empty"},
		{name: "a repository that blocks us could not be read, not read as empty",
			spec: "github::codecheckers/blocked", errors: "could not read"},
		{name: "a Zenodo deposit cannot answer the question at all",
			spec: "zenodo::3674056", errors: "does not report languages"},
		{name: "nor can an OSF project", spec: "osf::abcde", errors: "does not report languages"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			spec, err := ParseRepositorySpec(testCase.spec)
			if err != nil {
				t.Fatalf("spec: %v", err)
			}
			languages, err := stub.services.Languages(spec)
			switch {
			case testCase.errors != "" && err == nil:
				t.Fatalf("read %v, want an error about %q", languages, testCase.errors)
			case testCase.errors != "":
				if !strings.Contains(err.Error(), testCase.errors) {
					t.Fatalf("error %q, want it to say %q", err, testCase.errors)
				}
				return
			case err != nil:
				t.Fatalf("languages: %v", err)
			}
			if strings.Join(languages, ",") != strings.Join(testCase.want, ",") {
				t.Errorf("read %v, want %v", languages, testCase.want)
			}
		})
	}
}

func TestLanguagesOfAnArchiveSaysSoOnce(t *testing.T) {
	stub := newStub(t)
	_, err := stub.services.Languages(RepositorySpec{Type: "zenodo", Path: "1"})
	if !errors.Is(err, ErrNoLanguages) {
		t.Errorf("error %v, want it to wrap ErrNoLanguages so a caller can tell it apart", err)
	}
}

func TestLanguagesNeedsTheServices(t *testing.T) {
	var offline *Services
	if _, err := offline.Languages(RepositorySpec{Type: "github", Path: "a/b"}); err == nil {
		t.Error("an offline bot should say it cannot look, not answer that there are no languages")
	}
}

func TestCrossrefWork(t *testing.T) {
	stub := newStub(t)
	stub.services.Metadata = "crossref"
	stub.JSON("/crossref/works/10.1000/fake", `{"message": {
		"title": ["A paper"], "container-title": ["A journal"],
		"subject": ["Neuroscience"], "abstract": "About R.",
		"author": [{"given": "A.", "family": "Da", "ORCID": "https://orcid.org/0000-0000-0000-0001"}]}}`)

	work, err := stub.services.Work("https://doi.org/10.1000/fake")
	if err != nil {
		t.Fatalf("work: %v", err)
	}
	if work.Title != "A paper" || work.Journal != "A journal" || work.Abstract != "About R." {
		t.Errorf("read %+v", work)
	}
	if len(work.Subjects) != 1 || work.Subjects[0] != "Neuroscience" {
		t.Errorf("subjects %v", work.Subjects)
	}
	if len(work.Authors) != 1 || work.Authors[0].ORCID != "0000-0000-0000-0001" ||
		work.Authors[0].Name != "A. Da" {
		t.Errorf("authors %+v", work.Authors)
	}
	if _, err := stub.services.Work("not a doi"); err == nil {
		t.Error("something that is not a DOI should be refused before Crossref is asked")
	}
}
