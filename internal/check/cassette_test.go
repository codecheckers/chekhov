package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// The external checks, run offline against what the real services actually
// answered.
//
// The stub tests decide what a response means; these prove we read the real
// thing correctly. A cassette holds Zenodo's and Crossref's own JSON, recorded
// once, so a field they rename shows up here rather than in production.
//
// Re-record after changing a fixture, or when the weekly drift check complains:
//
//	CHEKHOV_RECORD=1 go test -run Cassette ./internal/check/
//
// Recording talks to the real services and overwrites testdata/cassettes/.

// cassetteRules says which rules each cassette is expected to exercise. It is
// also what TestOfflineCoverageOfServiceRules reads, so a rule covered here
// needs no stub case of its own.
var cassetteRules = map[string][]string{
	"register-entry": {
		"CC-CFG-012", "CC-MET-004", "CC-BUN-004", "CC-MET-002", "CC-MET-003",
		"CC-BUN-001",
		"CC-REP-001", "CC-REP-002", "CC-REP-003", "CC-REP-004", "CC-REP-005", "CC-REP-006",
		"CC-REG-001", "CC-REG-002", "CC-REG-003", "CC-REG-004", "CC-REG-005",
		"CC-REG-006", "CC-REG-007",
	},
	"paper-metadata": {
		"CC-MET-005", "CC-MET-006", "CC-MET-007", "CC-MET-008",
	},
	"reference-other": {
		"CC-MET-009",
	},
}

// recording reports whether this run talks to the real services and writes the
// cassettes, rather than replaying them.
func recording() bool { return os.Getenv("CHEKHOV_RECORD") != "" }

// One recorder per cassette for the whole package run. Several tests use the
// same cassette, and a second recorder on the same file would truncate what the
// first one wrote.
var (
	recordersMu sync.Mutex
	recorders   = map[string]*recorder.Recorder{}
)

// TestMain stops the recorders, which is when a cassette is written to disk.
func TestMain(m *testing.M) {
	code := m.Run()
	for name, rec := range recorders {
		if err := rec.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "cassette %s: %v\n", name, err)
			code = 1
		}
	}
	os.Exit(code)
}

func cassetteRecorder(t *testing.T, name string) *recorder.Recorder {
	t.Helper()
	recordersMu.Lock()
	defer recordersMu.Unlock()

	if rec, ok := recorders[name]; ok {
		return rec
	}

	path := filepath.Join("..", "..", "testdata", "cassettes", name)
	mode := recorder.ModeReplayOnly
	if recording() {
		mode = recorder.ModeRecordOnly
	} else if _, err := os.Stat(path + ".yaml"); err != nil {
		t.Skipf("no cassette %s.yaml; record one with CHEKHOV_RECORD=1", name)
	}

	rec, err := recorder.New(path,
		recorder.WithMode(mode),
		recorder.WithHook(scrubAndShrink, recorder.BeforeSaveHook),
		// Replay at once. go-vcr otherwise reproduces how long each request
		// took when it was recorded, which would put the recorded minutes back
		// into a suite that is meant to be fast.
		recorder.WithSkipRequestLatency(true),
	)
	if err != nil {
		t.Fatalf("cassette %s: %v", name, err)
	}
	recorders[name] = rec
	return rec
}

// scrubAndShrink keeps a cassette safe to commit, small enough to read, and
// free of a bad afternoon at one of the services.
//
// A token must never reach a committed file. A 5xx is never recorded: a
// transient outage would otherwise be pinned in the cassette and replayed as
// though it were how the service answers. And several checks only look at the
// status code of a page - a publisher's landing page, an arXiv abstract - so
// keeping hundreds of kilobytes of HTML would be noise; the JSON and CSV the
// checks actually parse is kept in full.
func scrubAndShrink(i *cassette.Interaction) error {
	delete(i.Request.Headers, "Authorization")

	if i.Response.Code >= 500 {
		fmt.Fprintf(os.Stderr, "not recording %s %s: the service answered %d\n",
			i.Request.Method, i.Request.URL, i.Response.Code)
		i.DiscardOnSave = true
		return nil
	}

	contentType := strings.ToLower(strings.Join(i.Response.Headers["Content-Type"], " "))
	parsed := strings.Contains(contentType, "json") || strings.Contains(contentType, "csv") ||
		strings.Contains(contentType, "yaml") || strings.Contains(contentType, "plain")
	if !parsed && len(i.Response.Body) > 2048 {
		i.Response.Body = "(body dropped: this check reads only the status code)"
		i.Response.ContentLength = int64(len(i.Response.Body))
		delete(i.Response.Headers, "Content-Length")
	}
	return nil
}

// replayServices returns services whose HTTP client answers from a cassette,
// or records one when CHEKHOV_RECORD=1.
func replayServices(t *testing.T, name string) *Services {
	t.Helper()
	rec := cassetteRecorder(t, name)

	services := &Services{Access: Access{
		HTTP:        rec.GetDefaultClient(),
		GitHubToken: os.Getenv("CHEKHOV_GH_ACCESS_TOKEN"),
		// The register-wide rules need the register the fixture's certificate
		// is actually in. Reads only; nothing here writes.
		Register: "codecheckers/register",
		// Named here rather than taken from the settings, so that a recorded
		// URL cannot depend on the environment the recording ran in: a
		// different mailto would be a different request and would not replay.
		Mailto: "chekhov@cdchck.science",
	}}
	services.defaults()
	return services
}

func runCassetteFixture(t *testing.T, fixture string) Report {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "integration", fixture, "codecheck.yml")
	context, err := FromFile(path)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	report, err := Run(context.WithServices(replayServices(t, fixture)), "2.0", false)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	return report
}

// Every rule the cassette covers reaches a verdict, offline.
func TestCassetteFixturesReachVerdicts(t *testing.T) {
	for fixture, ids := range cassetteRules {
		t.Run(fixture, func(t *testing.T) {
			report := runCassetteFixture(t, fixture)
			for _, id := range ids {
				result := resultFor(t, report, id)
				if result.Outcome == OutcomeSkipped || result.Outcome == OutcomeUnchecked {
					t.Errorf("%s did not reach a verdict: %s", id, result.Detail)
					continue
				}
				t.Logf("%s %s: %s", id, result.Outcome, result.Detail)
			}
		})
	}
}

// The outcomes a real published CODECHECK produces, pinned. These are what the
// live integration run reported, so a change here means either the service
// changed its answer or a check changed its mind.
func TestCassetteRegisterEntryOutcomes(t *testing.T) {
	report := runCassetteFixture(t, "register-entry")

	expected := map[string]Outcome{
		"CC-CFG-012": OutcomeOK,
		"CC-MET-002": OutcomeOK,
		"CC-MET-003": OutcomeOK,
		"CC-MET-004": OutcomeOK,
		"CC-BUN-004": OutcomeOK,
		"CC-REP-001": OutcomeOK,
		"CC-REP-002": OutcomeOK,
		"CC-REP-003": OutcomeOK,
		"CC-REP-004": OutcomeOK,
		"CC-REP-005": OutcomeOK,
		"CC-REP-006": OutcomeOK,
		"CC-REG-001": OutcomeOK,
		"CC-REG-002": OutcomeOK,
		"CC-REG-003": OutcomeOK,
		"CC-REG-004": OutcomeOK,
		"CC-REG-005": OutcomeOK,
		"CC-REG-006": OutcomeOK,
		"CC-REG-007": OutcomeOK,
		// The manifest names its outputs at the bundle root, but the repository
		// keeps them under codecheck/outputs/. The specification says the paths
		// are relative to the codecheck.yml, so this is a finding about the
		// certificate, and pinning it here keeps the strict reading honest.
		"CC-BUN-001": OutcomeError,
	}
	for id, want := range expected {
		if got := resultFor(t, report, id).Outcome; got != want {
			t.Errorf("%s: outcome %q, want %q (%s)",
				id, got, want, resultFor(t, report, id).Detail)
		}
	}

	// The record is the certificate's own, and the checks read it, rather than
	// passing because nothing was found to disagree with.
	if detail := resultFor(t, report, "CC-REP-004").Detail; !strings.Contains(detail, "2026-001") {
		t.Errorf("CC-REP-004 read the record title as %q", detail)
	}
	if detail := resultFor(t, report, "CC-REG-006").Detail; !strings.Contains(detail, "#187") {
		t.Errorf("CC-REG-006 read the issue as %q", detail)
	}
}

// The metadata source's own record of a published paper, including the
// disagreement it finds with an older certificate.
func TestCassettePaperMetadataOutcomes(t *testing.T) {
	report := runCassetteFixture(t, "paper-metadata")

	for id, want := range map[string]Outcome{
		"CC-MET-005": OutcomeOK,
		"CC-MET-006": OutcomeOK,
		"CC-MET-007": OutcomeOK,
		// The paper's authors have ORCIDs the source knows and this 1.0-era
		// certificate never recorded.
		"CC-MET-008": OutcomeWarning,
	} {
		result := resultFor(t, report, id)
		if result.Outcome != want {
			t.Errorf("%s: outcome %q, want %q (%s)", id, result.Outcome, want, result.Detail)
		}
	}
	if detail := resultFor(t, report, "CC-MET-008").Detail; !strings.Contains(detail, "0000-") {
		t.Errorf("CC-MET-008 should name the ORCIDs the source has: %q", detail)
	}
}

func TestCassetteReferenceOtherResolves(t *testing.T) {
	report := runCassetteFixture(t, "reference-other")
	result := resultFor(t, report, "CC-MET-009")
	if result.Outcome != OutcomeOK {
		t.Errorf("CC-MET-009: outcome %q, want ok (%s)", result.Outcome, result.Detail)
	}
}

// A cassette that does not carry an interaction the check asks for must fail
// loudly rather than quietly reaching the real service.
func TestCassetteReplayIsStrict(t *testing.T) {
	if recording() {
		t.Skip("recording: every request goes to the real service by design")
	}
	services := replayServices(t, "register-entry")
	if status, _, err := services.get("https://example.org/never-recorded", nil); err == nil {
		t.Errorf("a request that is not in the cassette answered %d instead of failing", status)
	}
}
