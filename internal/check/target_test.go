package check

import (
	"strings"
	"testing"
)

// The shortcuts, one case per rule, plus what must stay ambiguous.
func TestResolveTargetReadsTheShortcuts(t *testing.T) {
	cases := []struct {
		target   string
		resolved string
		refused  string // a fragment of the error, when the target is refused
	}{
		{target: "github::codecheckers/Piccolo-2020", resolved: "github::codecheckers/Piccolo-2020"},
		{target: "github::reproducible-agile/reviews-2025|reports/08",
			resolved: "github::reproducible-agile/reviews-2025|reports/08"},
		{target: "codecheckers/Piccolo-2020", resolved: "github::codecheckers/Piccolo-2020"},
		{target: "reproducible-agile/reviews-2025|reports/08",
			resolved: "github::reproducible-agile/reviews-2025|reports/08"},
		{target: "cdchck/some-project", resolved: "gitlab::cdchck/some-project"},
		// The group is a name, and names are not case-sensitive here - but
		// GitLab serves it lowercase, so that is what it resolves to.
		{target: "CdChck/some-project", resolved: "gitlab::cdchck/some-project"},
		{target: "ab12c", resolved: "osf::ab12c"},
		{target: "3674056", resolved: "zenodo::3674056"},
		// A certificate identifier is neither an OSF node nor a Zenodo record,
		// and only the register can say what it is - which needs services.
		{target: "2020-001", refused: "needs the register"},
		{target: "", refused: "no target"},
		{target: "codecheck.yml", refused: "I cannot tell"},
		{target: "/etc/passwd", refused: "I cannot tell"},
		{target: "ab12", refused: "I cannot tell"},
		{target: "svn::codecheckers/demo", refused: "unsupported repository type"},
	}

	for _, test := range cases {
		spec, err := ResolveTarget(test.target, nil)
		switch {
		case test.refused != "":
			if err == nil {
				t.Errorf("%q was read as %q, expected a refusal", test.target, spec)
			} else if !strings.Contains(err.Error(), test.refused) {
				t.Errorf("%q: %v, expected it to mention %q", test.target, err, test.refused)
			}
		case err != nil:
			t.Errorf("%q: %v", test.target, err)
		case spec.String() != test.resolved:
			t.Errorf("%q was read as %q, expected %q", test.target, spec, test.resolved)
		}
	}
}

// The forms a target may take are listed from the one list of them, so a
// platform added to the register cannot go missing from the refusal.
func TestARefusedTargetNamesEveryForm(t *testing.T) {
	_, err := ResolveTarget("nonsense-target", nil)
	if err == nil {
		t.Fatal("nonsense-target was accepted")
	}
	for _, known := range knownRepositoryTypes {
		if !strings.Contains(err.Error(), known+"::") {
			t.Errorf("%q is not offered: %v", known, err)
		}
	}
}

// A shortcut resolves to the same repository as the long form, sub-directory
// and all, because there is one parser behind both.
func TestResolveTargetKeepsTheSubDirectory(t *testing.T) {
	spec, err := ResolveTarget("reproducible-agile/reviews-2025|reports/08", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.Type != "github" || spec.Path != "reproducible-agile/reviews-2025" || spec.SubPath != "reports/08" {
		t.Errorf("resolved to %+v", spec)
	}
}

// A certificate identifier is looked up in the register, which is the only
// thing that knows where a finished CODECHECK lives (chekhov#13).
func TestResolveTargetLooksACertificateUp(t *testing.T) {
	stub := newStub(t)
	stub.register("Certificate,Repository,Type,Venue,Issue\n"+
		"2020-001,github::codecheckers/Piccolo-2020,journal,GigaScience,3\n", "")

	spec, err := ResolveTarget("2020-001", stub.services)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.String() != "github::codecheckers/Piccolo-2020" {
		t.Errorf("2020-001 resolved to %q", spec)
	}

	// A row that omits the platform is CC-REG-003's finding to report, not a
	// reason to refuse to check it.
	loose := newStub(t)
	loose.register("Certificate,Repository,Type,Venue,Issue\n"+
		"2020-002,codecheckers/Piccolo-2020,journal,GigaScience,3\n", "")
	if spec, err := ResolveTarget("2020-002", loose.services); err != nil {
		t.Errorf("a register row without its platform: %v", err)
	} else if spec.String() != "github::codecheckers/Piccolo-2020" {
		t.Errorf("2020-002 resolved to %q", spec)
	}

	if _, err := ResolveTarget("2020-002", stub.services); err == nil ||
		!strings.Contains(err.Error(), "not in the register") {
		t.Errorf("a certificate nobody has issued: %v", err)
	}
}

// A path is told from a repository by what it looks like, so that a mistyped
// path is answered as a missing file rather than as an unknown platform.
func TestIsLocalPath(t *testing.T) {
	cases := map[string]bool{
		"testdata/valid-2.0/codecheck.yml": true,
		"codecheck.yml":                    true,
		"codechek.yml":                     true, // not there, but meant as a file
		"./somewhere/config.yaml":          true,
		"/etc/passwd":                      true,
		"codecheckers/Piccolo-2020":        false,
		"github::codecheckers/demo":        false,
		"2020-001":                         false,
		"ab12c":                            false,
		"":                                 false,
		"target.go":                        true, // it is there, so it is the caller's own
		"nothing/here":                     false,
	}
	for target, expected := range cases {
		if IsLocalPath(target) != expected {
			t.Errorf("IsLocalPath(%q) = %v", target, !expected)
		}
	}
}

// A target that had to be resolved says what it came to when it cannot be
// read: "1970-001 answered 404" names nothing a reader can go and look at.
func TestAnUnreadableTargetNamesWhatItResolvedTo(t *testing.T) {
	stub := newStub(t)
	stub.register("Certificate,Repository,Type,Venue,Issue\n"+
		"1970-001,github::testuser/FAKE-demo,journal,FAKE Journal,101\n", "")

	_, err := FromRepository("1970-001", stub.services)
	if err == nil {
		t.Fatal("a repository that is not there was read")
	}
	if !strings.Contains(err.Error(), "github::testuser/FAKE-demo") {
		t.Errorf("the error does not say which repository: %v", err)
	}

	// Nothing to add when the target was already written out in full.
	_, err = FromRepository("github::testuser/FAKE-demo", stub.services)
	if err == nil {
		t.Fatal("a repository that is not there was read")
	}
	if strings.Contains(err.Error(), "github::testuser/FAKE-demo:") {
		t.Errorf("the repository the caller just named is repeated back: %v", err)
	}
}

func TestTargetIn(t *testing.T) {
	cases := []struct{ written, target string }{
		{"github::codecheckers/demo", "github::codecheckers/demo"},
		{"codecheckers/demo", "codecheckers/demo"},
		// A link says which platform it is, and says so in the answer.
		{"https://github.com/codecheckers/demo", "github::codecheckers/demo"},
		{"https://github.com/codecheckers/demo/tree/HEAD/sub", "github::codecheckers/demo"},
		{"https://gitlab.com/someorg/project", "gitlab::someorg/project"},
		{"https://gitlab.com/cdchck/demo.git", "gitlab::cdchck/demo"},
		{"https://zenodo.org/records/3674056", "zenodo::3674056"},
		{"https://zenodo.org/record/3674056/files/data.zip", "zenodo::3674056"},
		{"https://osf.io/abcde/", "osf::abcde"},
		{"https://osf.io/abcde/files/osfstorage", "osf::abcde"},
		{"https://osf.io/dashboard", ""},
		{"https://github.com/codecheckers", ""},
		{"2020-001", "2020-001"},
		// A DOI reads as owner/repo and is not one.
		{"10.5281/zenodo.3674056", ""},
		{"https://doi.org/10.5281/zenodo.3674056", ""},
		// In prose a five-letter word is a word, not an OSF project.
		{"paper", ""},
		{"12345", ""},
		{"", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.written, func(t *testing.T) {
			got := TargetIn(testCase.written)
			if got != testCase.target {
				t.Fatalf("TargetIn(%q) = %q, want %q", testCase.written, got, testCase.target)
			}
			// Whatever it answers has to be something ResolveTarget reads.
			if got != "" && !IsCertificateID(got) {
				if _, err := ResolveTarget(got, nil); err != nil {
					t.Errorf("ResolveTarget(%q): %v", got, err)
				}
			}
		})
	}
}
