package check

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/rules"
)

// Every rule in the register is either checked here or recorded as not
// checked, with a reason. A rule added to the register fails this test rather
// than being quietly ignored, which is the whole point of sharing the
// identifiers with the codecheck R package.
func TestEveryRuleAccountedFor(t *testing.T) {
	ids, err := rules.IDs()
	if err != nil {
		t.Fatalf("rule identifiers: %v", err)
	}

	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
		_, checked := Checks[id]
		_, excused := NotImplemented[id]
		switch {
		case checked && excused:
			t.Errorf("%s: both checked and recorded as not checked", id)
		case !checked && !excused:
			t.Errorf("%s: neither checked nor recorded in NotImplemented", id)
		}
	}

	for id := range Checks {
		if !known[id] {
			t.Errorf("%s: checked here but not a rule in the register", id)
		}
	}
	for id := range NotImplemented {
		if !known[id] {
			t.Errorf("%s: recorded as not checked but not a rule in the register", id)
		}
	}
}

func TestValidConfigurations(t *testing.T) {
	for _, fixture := range []string{"valid-2.0", "valid-1.0"} {
		t.Run(fixture, func(t *testing.T) {
			report := runFixture(t, fixture, "", false)
			if !report.OK() {
				t.Errorf("%s should be valid, but: %s", fixture, report.FailureMessage())
			}
		})
	}
}

func TestFailingConfigurations(t *testing.T) {
	cases := []struct {
		fixture string
		rule    string
		outcome Outcome
	}{
		{"no-certificate", "CC-CFG-025", OutcomeError},
		{"placeholders", "CC-CFG-023", OutcomeError},
		{"placeholders", "CC-CFG-013", OutcomeError},
		{"placeholders", "CC-CFG-026", OutcomeError},
		{"not-yaml", "CC-CFG-001", OutcomeError},
		{"no-document-marker", "CC-CFG-002", OutcomeError},
		{"bad-orcid", "CC-MET-001", OutcomeError},
	}

	for _, testCase := range cases {
		t.Run(testCase.fixture+"/"+testCase.rule, func(t *testing.T) {
			report := runFixture(t, testCase.fixture, "2.0", false)
			result := resultFor(t, report, testCase.rule)
			if result.Outcome != testCase.outcome {
				t.Errorf("%s: outcome %q, want %q (%s)",
					testCase.rule, result.Outcome, testCase.outcome, result.Detail)
			}
			if report.OK() {
				t.Errorf("%s should not be valid", testCase.fixture)
			}
		})
	}
}

// A file that does not parse is a failed rule, not a crash, and the checks
// that need the parsed content say they could not judge.
func TestUnparseableFileStillReports(t *testing.T) {
	report := runFixture(t, "not-yaml", "2.0", false)
	if got := resultFor(t, report, "CC-CFG-001").Outcome; got != OutcomeError {
		t.Errorf("CC-CFG-001: outcome %q, want error", got)
	}
	if got := resultFor(t, report, "CC-CFG-005").Outcome; got != OutcomeSkipped {
		t.Errorf("CC-CFG-005: outcome %q, want skipped", got)
	}
}

// The same file, two specification versions: one check function, two
// severities. This is the property the whole design exists for.
func TestSeverityFollowsTheSpecificationVersion(t *testing.T) {
	as10 := runFixture(t, "no-certificate", "1.0", false)
	as20 := runFixture(t, "no-certificate", "2.0", false)

	if got := resultFor(t, as10, "CC-CFG-025").Outcome; got != OutcomeInfo {
		t.Errorf("CC-CFG-025 under 1.0: outcome %q, want info", got)
	}
	if got := resultFor(t, as20, "CC-CFG-025").Outcome; got != OutcomeError {
		t.Errorf("CC-CFG-025 under 2.0: outcome %q, want error", got)
	}
}

// A rule that only exists in 2.0 is never applied to a file that declares 1.0.
func TestOlderVersionRunsFewerRules(t *testing.T) {
	as10 := runFixture(t, "valid-2.0", "1.0", false)
	as20 := runFixture(t, "valid-2.0", "2.0", false)

	in10 := map[string]bool{}
	for _, result := range as10.Results {
		in10[result.Rule.ID] = true
	}
	for _, id := range []string{"CC-CFG-029", "CC-CFG-030", "CC-MET-009"} {
		if in10[id] {
			t.Errorf("%s is a 2.0 rule but ran against 1.0", id)
		}
	}
	if len(as20.Results) <= len(as10.Results) {
		t.Error("2.0 should run more rules than 1.0")
	}
}

func TestStrictEscalatesWarningsOnly(t *testing.T) {
	relaxed := runFixture(t, "valid-1.0", "1.0", false)
	strict := runFixture(t, "valid-1.0", "1.0", true)

	for i, result := range strict.Results {
		before := relaxed.Results[i]
		switch before.Outcome {
		case OutcomeWarning:
			if result.Outcome != OutcomeError {
				t.Errorf("%s: warning should be an error under strict", result.Rule.ID)
			}
		case OutcomeInfo, OutcomeOK, OutcomeSkipped:
			if result.Outcome != before.Outcome {
				t.Errorf("%s: %q became %q under strict, which only raises warnings",
					result.Rule.ID, before.Outcome, result.Outcome)
			}
		}
	}
}

func TestSpecVersionFromTheFile(t *testing.T) {
	cases := map[string]string{
		"https://codecheck.org.uk/spec/config/1.0/": "1.0",
		"https://codecheck.org.uk/spec/config/2.0":  "2.0",
		"http://codecheck.org.uk/spec/config/1.0":   "1.0",
		"https://example.com/spec":                  "",
		"":                                          "",
	}
	for version, want := range cases {
		if got := SpecVersionFromURL(version); got != want {
			t.Errorf("SpecVersionFromURL(%q) = %q, want %q", version, got, want)
		}
	}

	// A file that names no version and cannot be dated is validated against
	// the newest, which is what the specification asks tools to assume.
	if got, why, _ := SpecVersion(Context{}); got != rules.Newest() {
		t.Errorf("a file without a version node: %q (%s), want %q", got, why, rules.Newest())
	}
}

// The historical version URL names a version even though the page behind it
// was never published: certificates from 2020 carry it, and checking them
// against the 2026 requirements reports failures their authors could not have
// known about.
func TestTheHistoricalVersionURLIsUnderstood(t *testing.T) {
	context := FromBytes([]byte("---\nversion: https://codecheck.org.uk/spec/1.0\n"))

	version, why, _ := SpecVersion(context)
	if version != "1.0" {
		t.Errorf("version = %q (%s), want 1.0", version, why)
	}
	// It is still not a published URL, and CC-CFG-015 still says so.
	if SpecVersionFromURL("https://codecheck.org.uk/spec/1.0") != "" {
		t.Error("the historical URL must not count as published")
	}
}

// A file that declares a version this build does not know is refused, not
// checked against a guess: every finding would answer requirements the author
// did not claim to meet, and the heading used to say the file named no version
// when it plainly named one. A malformed or empty version is the same case.
//
// The refusal is the report's, not an error: both renderings say it, in place
// of results, and neither shows counts that would read as "nothing wrong".
func TestAnUnknownVersionIsRefused(t *testing.T) {
	for _, declared := range []string{
		"https://codecheck.org.uk/spec/config/5.0/",
		"yes",
		"2",
		`""`,
		"",
	} {
		// The file is otherwise dated, so a fallback would have had an answer.
		context := FromBytes([]byte("---\nversion: " + declared + "\ncheck_time: \"2021-03-01\"\n"))

		report, err := RunPart(context, "", false, "")
		if err != nil {
			t.Fatalf("version %q: %v", declared, err)
		}
		if report.Refused == nil || len(report.Results) > 0 || report.OK() {
			t.Errorf("version %q: refused %v, %d result(s), OK %v; want refused, nothing run, not OK",
				declared, report.Refused, len(report.Results), report.OK())
			continue
		}

		for name, rendered := range map[string]string{
			"markdown": report.Markdown(),
			"text":     report.Text(),
			"failure":  report.FailureMessage(),
		} {
			for _, want := range []string{"not checked", "2.0 and 1.0"} {
				if !strings.Contains(rendered, want) {
					t.Errorf("version %q, %s: does not say %q:\n%s", declared, name, want, rendered)
				}
			}
			for _, unwanted := range []string{"names no version", "0 failed"} {
				if strings.Contains(rendered, unwanted) {
					t.Errorf("version %q, %s: says %q:\n%s", declared, name, unwanted, rendered)
				}
			}
		}
		if !strings.Contains(report.Markdown(), "behind the register") {
			t.Errorf("version %q: the reply does not say what a newer version means", declared)
		}

		// Somebody who pins the version has decided which rules apply.
		if pinned, err := RunPart(context, "2.0", false, ""); err != nil || pinned.Refused != nil {
			t.Errorf("version %q pinned to 2.0: %v, refused %v", declared, err, pinned.Refused)
		}
	}

	if refused := unknownVersion("https://codecheck.org.uk/spec/config/5.0/"); !strings.Contains(refused.Why,
		"'https://codecheck.org.uk/spec/config/5.0/'") {
		t.Errorf("the refusal does not repeat the version the file declared: %s", refused.Why)
	}

	// And a file with no version node at all is still dated, not refused.
	if _, why, refused := SpecVersion(FromBytes([]byte("---\ncheck_time: \"2021-03-01\"\n"))); refused != nil ||
		!strings.Contains(why, "checked") {
		t.Errorf("no version node: %q, %v; want it dated", why, refused)
	}
}

// A file with no version node is judged by the requirements that were current
// when it was checked.
func TestAnUndatedVersionIsTakenFromTheCheckTime(t *testing.T) {
	cases := map[string]string{
		"2019-02-14 10:00:00": "1.0",
		"2026-09-30":          "2.0",
	}
	for checkTime, want := range cases {
		context := FromBytes([]byte("---\ncheck_time: \"" + checkTime + "\"\n"))
		if got, why, _ := SpecVersion(context); got != want {
			t.Errorf("check_time %s: version %q (%s), want %q", checkTime, got, why, want)
		}
	}

	// And by when it was last changed, when the file itself says nothing.
	context := FromBytes([]byte("---\ncertificate: 2020-001\n"))
	context.Modified = time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	if got, why, _ := SpecVersion(context); got != "1.0" {
		t.Errorf("version = %q (%s), want 1.0", got, why)
	}
}

// A configuration held in memory has no file and no bundle, so the rules about
// those skip rather than fail.
func TestInMemoryConfigurationSkipsFileRules(t *testing.T) {
	context := FromBytes([]byte("---\ncertificate: 1970-001\n"))
	report, err := Run(context, "2.0", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, id := range []string{"CC-CFG-003", "CC-BUN-002", "CC-BUN-005"} {
		if got := resultFor(t, report, id).Outcome; got != OutcomeSkipped {
			t.Errorf("%s: outcome %q, want skipped", id, got)
		}
	}
}

// The bundle rules read the directory the configuration sits in.
func TestBundleRules(t *testing.T) {
	complete := runFixture(t, "valid-2.0", "2.0", false)
	for _, id := range []string{"CC-BUN-002", "CC-BUN-003", "CC-BUN-005"} {
		if got := resultFor(t, complete, id).Outcome; got != OutcomeOK {
			t.Errorf("%s in a complete bundle: outcome %q, want ok", id, got)
		}
	}

	bare := runFixture(t, "no-certificate", "2.0", false)
	if got := resultFor(t, bare, "CC-BUN-002").Outcome; got != OutcomeWarning {
		t.Errorf("CC-BUN-002 in a bare directory: outcome %q, want warning", got)
	}
	if got := resultFor(t, bare, "CC-BUN-003").Outcome; got != OutcomeSkipped {
		t.Errorf("CC-BUN-003 without a codecheck directory: outcome %q, want skipped", got)
	}
}

// A single rule is reported with what the rule says; a list of several carries
// identifiers and findings only.
func TestReportedRulesCarryTheirDescription(t *testing.T) {
	single := runFixture(t, "no-certificate", "2.0", false)
	message := single.FailureMessage()
	rule, err := rules.Get("CC-CFG-025", "2.0")
	if err != nil {
		t.Fatalf("CC-CFG-025: %v", err)
	}
	if !strings.Contains(message, rule.Description) {
		t.Errorf("a single failure should quote the rule: %s", message)
	}

	many := runFixture(t, "placeholders", "2.0", false)
	manyMessage := many.FailureMessage()
	if len(many.Failures()) < 2 {
		t.Fatalf("the placeholders fixture should fail several rules")
	}
	placeholderRule, err := rules.Get("CC-CFG-013", "2.0")
	if err != nil {
		t.Fatalf("CC-CFG-013: %v", err)
	}
	if strings.Contains(manyMessage, placeholderRule.Description) {
		t.Errorf("a list of failures should not carry descriptions: %s", manyMessage)
	}
	if !strings.Contains(manyMessage, "CC-CFG-013") {
		t.Errorf("a list of failures should name the identifiers: %s", manyMessage)
	}
}

func TestMarkdownReplyListsOnlyWhatNeedsAction(t *testing.T) {
	report := runFixture(t, "placeholders", "2.0", false)
	markdown := report.Markdown()

	if !strings.Contains(markdown, "not valid yet") {
		t.Error("a failing reply should say the file is not valid yet")
	}
	if !strings.Contains(markdown, "CC-CFG-023") {
		t.Error("a failing reply should name the rule that failed")
	}
	// A rule that passed has nothing to say, and a table of all of them would
	// bury the lines that need action.
	if strings.Contains(markdown, "CC-CFG-004") {
		t.Error("a passing rule should not appear in the reply")
	}

	valid := runFixture(t, "valid-2.0", "2.0", false).Markdown()
	if !strings.Contains(valid, "is valid") {
		t.Error("a passing reply should say the file is valid")
	}
}

func runFixture(t *testing.T, fixture, specVersion string, strict bool) Report {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", fixture, "codecheck.yml")
	context, err := FromFile(path)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	report, err := Run(context, specVersion, strict)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	return report
}

func resultFor(t *testing.T, report Report, id string) RuleResult {
	t.Helper()
	for _, result := range report.Results {
		if result.Rule.ID == id {
			return result
		}
	}
	t.Fatalf("%s did not run against %s", id, report.Label)
	return RuleResult{}
}

// One file that makes every check reach a verdict, except the ones that need
// the outside world.
//
// A check that skips has told us nothing, and a fixture that skips half the
// rules hides a check that would panic or misread the file it never saw. The
// exhaustive fixture fills in every node any 2.0 rule looks at, so the only
// skips left are the checks that would have to call an API. Those are covered
// by the integration suite, see integration_test.go.
func TestExhaustiveFixtureSkipsOnlyServiceRules(t *testing.T) {
	report := runFixture(t, "exhaustive-2.0", "2.0", false)

	ran := map[string]bool{}
	for _, result := range report.Results {
		needs, needsService := RequiresService[result.Rule.ID]
		switch {
		case needsService:
			if result.Outcome != OutcomeSkipped {
				t.Errorf("%s: outcome %q, want skipped without %s",
					result.Rule.ID, result.Outcome, needs)
			}
			if !strings.Contains(result.Detail, needs) {
				t.Errorf("%s: skipped with %q, which does not say it needs %s",
					result.Rule.ID, result.Detail, needs)
			}
		default:
			ran[result.Rule.ID] = true
			if result.Outcome == OutcomeSkipped {
				t.Errorf("%s skipped on the exhaustive fixture: %s",
					result.Rule.ID, result.Detail)
			}
			if result.Outcome == OutcomeUnchecked {
				t.Errorf("%s has no check function", result.Rule.ID)
			}
		}
	}

	// Every rule that specification 2.0 has and that needs nothing external
	// must have run.
	catalogue, err := rules.For("2.0")
	if err != nil {
		t.Fatalf("rules for 2.0: %v", err)
	}
	for _, rule := range catalogue {
		if _, needsService := RequiresService[rule.ID]; needsService || !rule.Active() {
			continue
		}
		if !ran[rule.ID] {
			t.Errorf("%s did not run against the exhaustive fixture", rule.ID)
		}
	}

	if got, want := report.Count(OutcomeSkipped), len(RequiresService); got != want {
		t.Errorf("%d rule(s) skipped, want %d, the rules that need a service", got, want)
	}
	if got := report.Count(OutcomeUnchecked); got != 0 {
		t.Errorf("%d rule(s) have no check function, want none", got)
	}
}

// The exhaustive fixture is clean: every check that can run without a service
// runs, and every one of them passes.
//
// The reference is what makes that possible. It is a bare URL that carries a
// DOI and is an archived snapshot of a PDF, so the four rules about the form
// of a reference all reach a verdict on one value. A real certificate would
// simply name the DOI; see TestArchivedPDFReference for what each of those
// rules says on its own.
func TestExhaustiveFixtureIsClean(t *testing.T) {
	report := runFixture(t, "exhaustive-2.0", "2.0", false)

	if !report.OK() {
		t.Errorf("the exhaustive fixture should be valid: %s", report.FailureMessage())
	}
	for _, outcome := range []Outcome{OutcomeError, OutcomeWarning, OutcomeInfo} {
		if got := report.Count(outcome); got != 0 {
			t.Errorf("%d rule(s) reported %q, want none", got, outcome)
		}
	}
	if got, want := report.Count(OutcomeOK), len(Checks)-len(RequiresService); got != want {
		t.Errorf("%d rule(s) passed, want %d", got, want)
	}
}

// The reference is an archived snapshot of a PDF, which is the one form that
// satisfies both rules about a PDF reference: the specification asks people to
// avoid a direct PDF link, and to archive it where nothing better exists.
func TestArchivedPDFReference(t *testing.T) {
	cases := []struct {
		name          string
		reference     string
		notBarePDF    Outcome // CC-CFG-022, a warning when it fails
		pdfIsArchived Outcome // CC-CFG-031, advice when it fails
	}{
		{
			name:          "an archived snapshot of a PDF",
			reference:     "https://web.archive.org/web/20200401120000/https://example.org/preprints/lake-cycle.pdf",
			notBarePDF:    OutcomeOK,
			pdfIsArchived: OutcomeOK,
		},
		{
			name:          "a direct PDF link",
			reference:     "https://example.org/preprints/lake-cycle.pdf",
			notBarePDF:    OutcomeWarning,
			pdfIsArchived: OutcomeInfo,
		},
		{
			name:          "a PDF link with a query string is still a PDF link",
			reference:     "https://example.org/download?file=lake-cycle.pdf#page=2",
			notBarePDF:    OutcomeWarning,
			pdfIsArchived: OutcomeInfo,
		},
		{
			// Neither rule has anything to say about a DOI, so the archived
			// rule skips rather than passing: it never looked at a PDF.
			name:          "a DOI is not a PDF link at all",
			reference:     "https://doi.org/10.5555/preprint.1",
			notBarePDF:    OutcomeOK,
			pdfIsArchived: OutcomeSkipped,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			context := FromBytes([]byte("---\npaper:\n  reference: " + testCase.reference + "\n"))
			report, err := Run(context, "2.0", false)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := resultFor(t, report, "CC-CFG-022").Outcome; got != testCase.notBarePDF {
				t.Errorf("CC-CFG-022: outcome %q, want %q", got, testCase.notBarePDF)
			}
			if got := resultFor(t, report, "CC-CFG-031").Outcome; got != testCase.pdfIsArchived {
				t.Errorf("CC-CFG-031: outcome %q, want %q", got, testCase.pdfIsArchived)
			}
		})
	}
}

// The bot's check commands name a part of the catalogue: "check bundle" asks
// about the bundle and nothing else, and the rest is not run at all.
func TestRunPart(t *testing.T) {
	context, err := FromFile(filepath.Join("..", "..", "testdata", "exhaustive-2.0", "codecheck.yml"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	for _, name := range []string{"config", "metadata", "bundle", "report", "register"} {
		report, err := RunPart(context, "2.0", false, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(report.Results) == 0 {
			t.Errorf("%s: no rules", name)
		}
		for _, result := range report.Results {
			if result.Rule.Area != name {
				t.Errorf("%s ran %s, which is a %s rule", name, result.Rule.ID, result.Rule.Area)
			}
		}
	}

	// The references cross two areas, because the catalogue separates the form
	// of a reference from what resolving it says.
	references, err := RunPart(context, "2.0", false, "references")
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	ran := map[string]bool{}
	for _, result := range references.Results {
		ran[result.Rule.ID] = true
	}
	for _, id := range []string{"CC-CFG-021", "CC-CFG-031", "CC-MET-005", "CC-MET-009"} {
		if !ran[id] {
			t.Errorf("the references part should run %s", id)
		}
	}
	if ran["CC-CFG-001"] {
		t.Error("the references part should not run the parsing rule")
	}

	// Nothing is lost: no part and "all" are the whole catalogue.
	all, err := Run(context, "2.0", false)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	everything, err := RunPart(context, "2.0", false, "all")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all.Results) != len(everything.Results) {
		t.Error(`"all" and no part should be the same run`)
	}

	if _, err := RunPart(context, "2.0", false, "whatever"); err == nil {
		t.Error("an unknown part should be an error, not an empty report")
	}
}

// A narrowed report says so, so that "0 failed" cannot be read as "this file is
// fine".
func TestNarrowedReportSaysWhatItCovered(t *testing.T) {
	context, err := FromFile(filepath.Join("..", "..", "testdata", "exhaustive-2.0", "codecheck.yml"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	bundle, err := RunPart(context, "2.0", false, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if !strings.Contains(bundle.Text(), "bundle only") {
		t.Error("the report should say which part it covers")
	}
	if !strings.Contains(bundle.Markdown(), "bundle only") {
		t.Error("the reply should say which part it covers")
	}
}

// The one list of rule properties this package keeps in code. A rule the
// register adds with a reference-shaped name has to be classified, or
// "check references" would quietly stop covering it.
func TestReferenceRulesCoverTheRegister(t *testing.T) {
	for _, version := range rules.SpecVersions() {
		catalogue, err := rules.For(version)
		if err != nil {
			t.Fatalf("rules for %s: %v", version, err)
		}
		for _, rule := range catalogue {
			// Only the paper's own references: CC-REG-007 is about the checks
			// issue carrying the certificate identifier, which is a different
			// sense of the word.
			aboutThePaper := rule.Area == "config" || rule.Area == "metadata"
			if aboutThePaper && strings.Contains(rule.Name, "reference") && !referenceRules[rule.ID] {
				t.Errorf("%s %s looks like a reference rule but is not in referenceRules",
					rule.ID, rule.Name)
			}
		}
	}
	for id := range referenceRules {
		if _, err := rules.Get(id, "2.0"); err != nil {
			t.Errorf("referenceRules names %s, which is not a rule: %v", id, err)
		}
	}
}

// The posted comment says where the file came from and how the version was
// chosen - once. It was written twice, once after the summary and once in the
// footer, which a reader reads as a bug in the bot.
func TestTheProvenanceIsStatedOnce(t *testing.T) {
	context, err := FromFile(filepath.Join("..", "..", "testdata", "valid-2.0", "codecheck.yml"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(context, "", false)
	if err != nil {
		t.Fatal(err)
	}

	markdown := report.Markdown()
	if got := strings.Count(markdown, "Checked against specification"); got != 1 {
		t.Errorf("the reply explains the version %d times:\n%s", got, markdown)
	}
}

// A version the caller pinned needs no explanation, and "specification 2.0
// asked for" is not a sentence.
func TestAPinnedVersionCarriesNoReason(t *testing.T) {
	context, err := FromFile(filepath.Join("..", "..", "testdata", "valid-2.0", "codecheck.yml"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(context, "1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.SpecVersionReason != "" {
		t.Errorf("reason = %q, want none for a pinned version", report.SpecVersionReason)
	}
}
