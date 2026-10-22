package check

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readyBundle writes a bundle to disk, {{server}} in a file standing for the
// stub's URL.
func readyBundle(t *testing.T, s *stub, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		content = strings.ReplaceAll(content, "{{server}}", s.URL)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const readyConfig = "---\nmanifest:\n  - file: figure1.png\n    comment: Figure 1\npaper:\n  reference: %s\n"

func withReference(reference string) string {
	return strings.Replace(readyConfig, "%s", reference, 1)
}

// What `check ready` asks of a bundle, item by item: the README, which no
// rule asks for, and the rules about the file, the licence and the paper.
func TestCheckReady(t *testing.T) {
	complete := map[string]string{
		"README.md":     "Run make.",
		"LICENSE":       "MIT",
		"codecheck.yml": withReference("{{server}}/paper"),
	}
	without := func(name string, replaced ...string) map[string]string {
		files := map[string]string{}
		for key, value := range complete {
			if key != name {
				files[key] = value
			}
		}
		if len(replaced) == 1 {
			files[name] = replaced[0]
		}
		return files
	}
	cases := []struct {
		name       string
		files      map[string]string
		issuePaper string
		offline    bool
		// want is each item's status, in the reply's order: README,
		// codecheck.yml, licence, paper.
		want   [4]Status
		detail string // a fragment some item's detail has to contain
	}{
		{"everything is there", complete, "", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusPass}, "from `paper.reference`"},
		{"a README in any case and with any extension", func() map[string]string {
			files := without("README.md")
			files["readme.TXT"] = "Run make."
			return files
		}(), "", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusPass}, ""},
		{"no README", without("README.md"), "", false,
			[4]Status{StatusFail, StatusPass, StatusPass, StatusPass}, "no README"},
		{"no licence", without("LICENSE"), "", false,
			[4]Status{StatusPass, StatusPass, StatusFail, StatusPass}, "no LICENSE"},
		{"no manifest", without("codecheck.yml", "---\npaper:\n  reference: {{server}}/paper\n"), "", false,
			[4]Status{StatusPass, StatusFail, StatusPass, StatusPass}, "no root-level manifest"},
		{"a file that is not YAML", without("codecheck.yml", "---\nmanifest: [\n"), "{{server}}/paper", false,
			[4]Status{StatusPass, StatusFail, StatusPass, StatusPass}, "not valid YAML"},
		// No file still has a README and a licence to look for, and the
		// issue's paper.
		{"no codecheck.yml", without("codecheck.yml"), "{{server}}/paper", false,
			[4]Status{StatusPass, StatusFail, StatusPass, StatusPass}, "from the issue"},
		// The template's placeholder is no reference: the issue's link is
		// asked instead, and without one the item says which.
		{"a placeholder reference and a link in the issue",
			without("codecheck.yml", withReference("https://doi.org/FIXME")), "{{server}}/paper", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusPass}, "from the issue"},
		{"a placeholder reference and none in the issue",
			without("codecheck.yml", withReference("https://doi.org/FIXME")), "", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusFail}, "still a placeholder"},
		// A bare DOI is a link all the same, and a reference that is not a
		// link gives way to the issue's.
		{"a bare DOI", without("codecheck.yml", withReference("10.1234/rain")), "", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusSkip}, "https://doi.org/10.1234/rain"},
		{"a reference that is not a link, and one in the issue",
			without("codecheck.yml", withReference("Smith et al., Rain, 2020")), "{{server}}/paper", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusPass}, "from the issue"},
		{"a paper that is not there", without("codecheck.yml", withReference("{{server}}/gone")), "", false,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusFail}, "answers 404"},
		// Could not check is never ready, and never a failure either.
		{"offline", complete, "", true,
			[4]Status{StatusPass, StatusPass, StatusPass, StatusSkip}, "external services not enabled"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			s := newStub(t)
			s.Text("/paper", "a paper")
			s.Status("/gone", http.StatusNotFound)
			services := s.services
			if testCase.offline {
				services = nil
			}
			dir := readyBundle(t, s, testCase.files)
			ready := Ready(dir, true, services, strings.ReplaceAll(testCase.issuePaper, "{{server}}", s.URL))
			if ready.Problem != "" {
				t.Fatalf("could not look: %s", ready.Problem)
			}
			var got [4]Status
			details := ""
			for i, item := range ready.Items {
				got[i] = item.Status
				details += item.Detail + "\n"
				if (item.Status == StatusFail) != (item.Fix != "") {
					t.Errorf("%s: %s with the fix %q", item.What, item.Status, item.Fix)
				}
			}
			if got != testCase.want {
				t.Errorf("got %v, want %v:\n%s", got, testCase.want, details)
			}
			if !strings.Contains(details, testCase.detail) {
				t.Errorf("no item says %q:\n%s", testCase.detail, details)
			}
		})
	}
}

// A bundle that cannot be read at all is said so, rather than reported as a
// bundle without a codecheck.yml.
func TestCheckReadyOnARepositoryThatIsNotThere(t *testing.T) {
	s := newStub(t)
	s.Status("/github/repos/codecheckers/gone/contents/", http.StatusNotFound)
	s.Status("/raw/codecheckers/gone/HEAD/codecheck.yml", http.StatusNotFound)
	ready := Ready("github::codecheckers/gone", false, s.services, "")
	if !strings.Contains(ready.Problem, "404") || len(ready.Items) != 0 {
		t.Errorf("got %+v, want a problem and no items", ready)
	}
}
