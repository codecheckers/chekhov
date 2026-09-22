package check

import (
	"slices"
	"strings"
	"testing"
)

// linesFixture breaks the rules node by node, so that each finding has one
// place in the file it can point at. The line numbers in the test are the ones below.
const linesFixture = `---
version: https://codecheck.org.uk/spec/config/2.0/
manifest:
  - file: figure1.png
  - comment: ""
  - file: ../outside.csv
    comment: leaves the bundle
paper:
  authors:
    - name: Josiah Carberry
      ORCID: https://orcid.org/0000-0002-1825-0097
    - ORCID: 0000-0002-1825-0097
  reference: https://example.org/paper.pdf
  reference-other:
    - https://doi.org/10.5555/fine
    - not a URL
codechecker:
  - name: A. Codechecker
report: https://doi.org/10.5281/zenodo.FIXME
summary: ""
certificate: 2026-1
`

// Each finding says which line of the file it is about, because "manifest
// item 2" is a position to the check and a hunt to the person fixing it.
func TestFindingsSayWhichLine(t *testing.T) {
	report, err := Run(FromBytes([]byte(linesFixture)), "2.0", false)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]int{
		"CC-CFG-005": {5},    // the manifest item without a file
		"CC-CFG-006": {6},    // its file, not the item
		"CC-CFG-007": {4, 5}, // the items without a comment, an empty one too
		"CC-CFG-010": {18},   // the codechecker without an ORCID
		"CC-CFG-013": {19},   // report
		"CC-CFG-017": {8},    // no title: where the paper is
		"CC-CFG-019": {12},   // the author without a name
		"CC-CFG-022": {13},   // reference
		"CC-CFG-023": {19},   // the placeholder
		"CC-CFG-024": {20},   // an empty summary is still on a line
		"CC-CFG-026": {21},   // certificate
		"CC-CFG-030": {16},   // the reference-other entry, not its list
		"CC-MET-001": {11},   // the malformed ORCID, not its person
		"CC-CFG-031": {13},   // the same reference, advisory
		"CC-CFG-028": {13},   // and not a DOI either
	}
	// Every other rule passed, skipped, or is about something other than the
	// file, and has no line to point at.
	for _, result := range report.Results {
		if lines := want[result.Rule.ID]; !slices.Equal(result.Lines, lines) {
			t.Errorf("%s (%s: %s) is about lines %v, want %v",
				result.Rule.ID, result.Outcome, result.Detail, result.Lines, lines)
		}
	}
}

// A node that is not there has no line, and its container's line is the best
// place to add it; a node whose container is missing too has none at all.
func TestLineFallsBackToTheContainer(t *testing.T) {
	config := FromBytes([]byte(linesFixture)).Config
	cases := map[string]int{
		"paper.authors.1.ORCID": 12,
		"paper.authors.1.name":  12,
		"paper.title":           8,
		"report":                19,
		"check_time":            0,
		"repository.0":          0,
		"":                      0,
	}
	for path, want := range cases {
		if got := config.Line(path); got != want {
			t.Errorf("Line(%q) = %d, want %d", path, got, want)
		}
	}
}

// The file's own form: where the bytes stop being UTF-8, and where it fails to
// start with a document marker, are counted here. Where it stops being YAML is
// the parser's to say, and yaml.v3 says it wrongly - the bracket opens on line
// 3 and the parser says "line 2" - so the finding carries the parser's words
// and no line of its own.
func TestTheFileItselfSaysWhichLine(t *testing.T) {
	context := FromBytes([]byte("---\nversion: fine\nthis: [is not\n  valid: yaml\n"))
	result := yamlParses(context)
	if result.Status != StatusFail || result.Lines != nil {
		t.Errorf("parse error: %s %v (%s), want a failure with no line", result.Status, result.Lines, result.Detail)
	}

	notUTF8 := FromBytes([]byte("---\nversion: fine\ntitle: caf\xe9\n"))
	if result := yamlParses(notUTF8); !slices.Equal(result.Lines, []int{3}) {
		t.Errorf("invalid UTF-8 at line %v, want 3", result.Lines)
	}

	marker := explicitDocument(FromBytes([]byte("\n\nversion: fine\n")))
	if !slices.Equal(marker.Lines, []int{3}) {
		t.Errorf("a missing document marker is reported at %v, want the first line with anything on it, 3", marker.Lines)
	}
}

// Where the line goes in each rendering: after the identifier in the
// terminal, in its own column of the reply, and as a link to that line where
// the source has a page that can show one.
func TestTheLineIsRendered(t *testing.T) {
	report, err := Run(FromBytes([]byte(linesFixture)), "2.0", false)
	if err != nil {
		t.Fatal(err)
	}

	text := report.Text()
	for _, want := range []string{
		"CC-CFG-026 certificate-id-format at line 21: ",
		"CC-CFG-007 manifest-item-comment at lines 4, 5: ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the terminal report lacks %q", want)
		}
	}
	if !strings.Contains(report.FailureMessage(), "CC-CFG-026 at line 21: ") {
		t.Errorf("the failure message lacks the line:\n%s", report.FailureMessage())
	}

	markdown := report.Markdown()
	if !strings.Contains(markdown, "| Rule | Line | Finding |") ||
		!strings.Contains(markdown, "certificate-id-format | 21 |") {
		t.Errorf("the reply has no line column:\n%s", markdown)
	}

	report.File = RepositorySpec{Type: "github", Path: "owner/repo", SubPath: "sub"}.FileURL("codecheck.yml")
	if want := "[21](https://github.com/owner/repo/blob/HEAD/sub/codecheck.yml#L21)"; !strings.Contains(report.Markdown(), want) {
		t.Errorf("the reply does not link the line, want %s", want)
	}

	// A report with nothing to point at has no empty column.
	registerOnly, err := RunPart(FromBytes([]byte(linesFixture)), "2.0", false, "register")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(registerOnly.Markdown(), "| Line |") {
		t.Error("a report without a line to show has a line column")
	}
}

// Only a source with a page for a file can be linked to a line of it.
func TestFileURL(t *testing.T) {
	cases := map[RepositorySpec]string{
		{Type: "github", Path: "o/r"}:                 "https://github.com/o/r/blob/HEAD/codecheck.yml",
		{Type: "gitlab", Path: "g/p", SubPath: "a/b"}: "https://gitlab.com/g/p/-/blob/HEAD/a/b/codecheck.yml",
		{Type: "osf", Path: "abcde"}:                  "",
		{Type: "zenodo", Path: "123"}:                 "",
		{}:                                            "",
	}
	for spec, want := range cases {
		if got := spec.FileURL("codecheck.yml"); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
	}
}
