package check

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/rules"
)

// The integration suite. It talks to Crossref, ORCID, Zenodo, GitHub and the
// register, so it runs only when that is asked for:
//
//	CHEKHOV_INTEGRATION=1 go test ./internal/check/ -run Integration
//
// It is the counterpart of the offline exhaustive fixture: there, every check
// that needs nothing external reaches a verdict; here, the 24 that do.
//
// No single file can exercise all of them - a file with a Crossref-registered
// paper DOI is not the same file as one whose certificate is already in the
// register - so the fixtures are split by what they need, and the last test
// asserts that together they leave nothing out.
//
// The fixtures are real published CODECHECKs, copied from their repositories.
// Nothing here writes anything anywhere: every request is a read.

func integrationServices(t *testing.T) *Services {
	t.Helper()
	services := ServicesFromEnv()
	if !services.Enabled() {
		t.Skip("set CHEKHOV_INTEGRATION=1 to run the checks that need external services")
	}
	// The register-wide rules read register.csv and venues.csv, so they need a
	// register that actually contains the fixture's certificate. Reads of the
	// production register are safe; nothing in this suite writes.
	if os.Getenv("CHEKHOV_TARGET_REPO") == "" {
		services.Register = "codecheckers/register"
	}
	return services
}

func runIntegrationFixture(t *testing.T, fixture string, services *Services) Report {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "integration", fixture, "codecheck.yml")
	context, err := FromFile(path)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	// Every fixture is validated against 2.0, whatever it declares: the point
	// is to run every rule, and 2.0 has the most of them.
	report, err := Run(context.WithServices(services), "2.0", false)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	return report
}

// reached reports the rules that got a verdict rather than a skip.
func reached(report Report) map[string]bool {
	verdicts := map[string]bool{}
	for _, result := range report.Results {
		if result.Outcome != OutcomeSkipped && result.Outcome != OutcomeUnchecked {
			verdicts[result.Rule.ID] = true
		}
	}
	return verdicts
}

// requireVerdicts fails when a rule the fixture is meant to exercise skipped,
// and says why it skipped, which is the thing worth reading in a failure.
func requireVerdicts(t *testing.T, report Report, ids ...string) {
	t.Helper()
	for _, id := range ids {
		result := resultFor(t, report, id)
		if result.Outcome == OutcomeSkipped || result.Outcome == OutcomeUnchecked {
			t.Errorf("%s did not reach a verdict: %s", id, result.Detail)
			continue
		}
		t.Logf("%s %s: %s (%s)", id, result.Outcome, result.Detail, result.Rule.Name)
	}
}

// A published certificate that is in the register: the Zenodo record, the
// repository under check, the register row and the checks issue.
func TestIntegrationRegisterEntry(t *testing.T) {
	services := integrationServices(t)
	report := runIntegrationFixture(t, "register-entry", services)

	requireVerdicts(t, report,
		// the report, the reference and the repository answer
		"CC-CFG-012", "CC-MET-004", "CC-BUN-004",
		// the ORCIDs resolve and name the same people
		"CC-MET-002", "CC-MET-003",
		// the manifest files are in the repository
		"CC-BUN-001",
		// the archive record
		"CC-REP-001", "CC-REP-002", "CC-REP-003", "CC-REP-004", "CC-REP-005", "CC-REP-006",
		// the register row and the issue it names
		"CC-REG-001", "CC-REG-002", "CC-REG-003", "CC-REG-004", "CC-REG-005",
		"CC-REG-006", "CC-REG-007",
	)

	// A published certificate should be in order. A failure here is a finding
	// about the certificate, not necessarily about this bot, so it is reported
	// rather than asserted away.
	if !report.OK() {
		t.Logf("the published certificate does not satisfy every rule:\n%s",
			report.FailureMessage())
	}
}

// A paper whose DOI Crossref knows, so the four Crossref rules have something
// to compare the file against.
func TestIntegrationCrossref(t *testing.T) {
	services := integrationServices(t)
	report := runIntegrationFixture(t, "paper-metadata", services)

	requireVerdicts(t, report,
		"CC-MET-005", "CC-MET-006", "CC-MET-007", "CC-MET-008",
		"CC-MET-002", "CC-MET-003",
	)
	// Not CC-MET-004: this publisher answers a DOI request from a robot with
	// 403, which the check reports as blocked rather than broken. The
	// register-entry fixture is where that rule reaches a verdict.
}

// The reference-other list is new in 2.0, and no published certificate carries
// one yet, so it has a fixture of its own.
func TestIntegrationReferenceOther(t *testing.T) {
	services := integrationServices(t)
	report := runIntegrationFixture(t, "reference-other", services)

	requireVerdicts(t, report, "CC-MET-009")
	if got := resultFor(t, report, "CC-MET-009").Outcome; got != OutcomeOK {
		t.Errorf("CC-MET-009: outcome %q, want ok for two resolvable entries", got)
	}
}

// Together, the fixtures have to cover every rule that needs a service. This
// is the test that fails when a new rule arrives with an API behind it and no
// fixture exercises it.
func TestIntegrationFixturesCoverEveryServiceRule(t *testing.T) {
	services := integrationServices(t)

	covered := map[string]bool{}
	for _, fixture := range []string{"register-entry", "paper-metadata", "reference-other"} {
		for id := range reached(runIntegrationFixture(t, fixture, services)) {
			covered[id] = true
		}
	}

	var uncovered []string
	for id := range RequiresService {
		if !covered[id] {
			uncovered = append(uncovered, id)
		}
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Errorf("no fixture makes these rules reach a verdict: %s",
			strings.Join(uncovered, ", "))
	}

	// And with the services on, nothing is left unchecked at all: the offline
	// suite covers the rest.
	catalogue, err := rules.For("2.0")
	if err != nil {
		t.Fatalf("rules for 2.0: %v", err)
	}
	for _, rule := range catalogue {
		if _, needsService := RequiresService[rule.ID]; !needsService {
			continue
		}
		if !covered[rule.ID] {
			t.Errorf("%s (%s) never ran", rule.ID, rule.Name)
		}
	}
	t.Logf("%d of %d rules that need a service reached a verdict",
		len(RequiresService)-len(uncovered), len(RequiresService))
}
