package check

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/rules"
)

// ruleFiles are the files the bundle records, and the stub routes they are
// served from.
var ruleFiles = []string{"rules-1.0.yml", "rules-2.0.yml"}

func rawRule(name string) string { return "/raw/codecheckers/register/HEAD/" + name }

// servesRules makes the stub answer as the register does: the rule files
// themselves, which decide the verdict, and the commit that last touched
// them, which the report shows. change may rewrite a file, which is a
// register that has moved on.
func (s *stub) servesRules(t *testing.T, sha, date string, change func(name string, raw []byte) []byte) {
	t.Helper()
	for _, name := range ruleFiles {
		raw, err := os.ReadFile(filepath.Join("..", "rules", "data", name))
		if err != nil {
			t.Fatalf("read the bundled %s: %v", name, err)
		}
		if change != nil {
			raw = change(name, raw)
		}
		s.Bytes(rawRule(name), raw)
	}
	s.JSON("/github/repos/codecheckers/register/commits",
		fmt.Sprintf(`[{"sha": %q, "commit": {"committer": {"date": %q}}}]`, sha, date))
}

// bundledCommit is what the build actually carries, so the tests compare
// against the bundle rather than against a number written down twice.
func bundledCommit(t *testing.T) string {
	t.Helper()
	provenance, err := rules.Provenance()
	if err != nil {
		t.Fatal(err)
	}
	if commit := provenance.Commit(); commit != "" {
		return commit
	}
	t.Fatal("the bundled rule files disagree about which commit they came from")
	return ""
}

// A register that has not moved since the bundle was taken.
func TestRulesThatAreTheRegistersCurrentOnes(t *testing.T) {
	stub := newStub(t)
	stub.servesRules(t, bundledCommit(t), "2026-09-18T20:36:01Z", nil)

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !drift.Known() || drift.Behind() {
		t.Errorf("an unmoved register should agree: %+v", drift)
	}
	if drift.Register != "codecheckers/register" {
		t.Errorf("the rules come from %q, whichever register the bot works on", drift.Register)
	}
	if !strings.Contains(drift.Markdown(), Symbol(OutcomeOK)) {
		t.Errorf("agreement should read as a pass:\n%s", drift.Markdown())
	}
}

// What happened on 2026-09-18: the register renamed four rules and the
// deployment went on reporting the old names until it was rebuilt.
func TestRulesThatTheRegisterHasMovedPast(t *testing.T) {
	stub := newStub(t)
	stub.servesRules(t, "1f6a3c9e0000000000000000000000000000abcd", "2026-09-19T09:00:00Z",
		func(_ string, raw []byte) []byte { return append(raw, "\n# one more rule\n"...) })

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !drift.Known() {
		t.Fatalf("the register answered, so the comparison was made: %+v", drift)
	}
	if !drift.Behind() {
		t.Error("a register that has moved leaves the bundle behind")
	}
	reply := drift.Markdown()
	for _, want := range []string{"**not** the register's current rules", "update-rules.sh"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

// The rule every check follows: could not look is never fine. A register
// that will not answer must not read as agreement.
func TestARegisterThatCannotBeAskedIsNotAgreement(t *testing.T) {
	stub := newStub(t)
	for _, name := range ruleFiles {
		stub.Status(rawRule(name), http.StatusForbidden)
	}

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("an unreachable register is an answer, not an error: %v", err)
	}
	if drift.Known() {
		t.Errorf("a 403 should leave the comparison unmade: %+v", drift)
	}
	if drift.Behind() {
		t.Error("unknown is not behind; the two must be told apart")
	}
	if drift.Mark() != Symbol(OutcomeSkipped) {
		t.Errorf("mark %q, want the skip symbol", drift.Mark())
	}
	if !strings.Contains(drift.Summary(), "could not check") {
		t.Errorf("summary %q", drift.Summary())
	}
}

// Half a comparison is worse than none: the files that were reached would
// read as current while the one that was not is the one that moved.
func TestOneFileThatCannotBeAskedLeavesTheWholeComparisonUnmade(t *testing.T) {
	stub := newStub(t)
	stub.servesRules(t, bundledCommit(t), "2026-09-18T20:36:01Z", nil)
	stub.Status(rawRule("rules-2.0.yml"), http.StatusInternalServerError)

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift.Known() {
		t.Errorf("one file unanswered leaves the comparison unmade: %+v", drift)
	}
	if drift.Behind() {
		t.Error("unknown is not behind")
	}
	// The file that could be read still says what it found, and the one that
	// could not says why: which of the two failed is what a reader wants.
	if !drift.Files[0].Known() || drift.Files[1].Known() {
		t.Errorf("the comparison should be per file: %+v", drift.Files)
	}
	if !strings.Contains(drift.Markdown(), "rules-2.0.yml") {
		t.Errorf("the report should name the file it could not ask about:\n%s", drift.Markdown())
	}
}

// A commit that did not change what the file says is not a bundle that has
// fallen behind: a whitespace fix, or a commit and its revert, would
// otherwise fail the weekly run for nothing.
func TestACommitThatChangedNothingIsNotDrift(t *testing.T) {
	stub := newStub(t)
	stub.servesRules(t, "1f6a3c9e0000000000000000000000000000abcd", "2026-09-19T09:00:00Z", nil)

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift.Behind() {
		t.Error("the register's copy says the same thing, so nothing is behind")
	}
	if !drift.Known() {
		t.Errorf("the comparison was made: %+v", drift)
	}
}

// An offline bot says it could not ask, rather than answering from the
// bundle alone.
func TestDriftWithoutTheServicesSaysSo(t *testing.T) {
	drift, err := RulesDrift(nil)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift.Known() || !strings.Contains(drift.Unreadable, "not online") {
		t.Errorf("an offline bot should say so: %+v", drift)
	}
	// The bundle is still reported: which rules are being applied is knowable
	// without the register, and is half of what was asked.
	if len(drift.Files) == 0 || drift.Files[0].Commit == "" {
		t.Errorf("the bundled rules should be reported anyway: %+v", drift.Files)
	}
}

// The register the rules come from is not the register the bot works on: a
// deployment against the testing register still judges by the real one's
// rules.
func TestTheRulesComeFromTheRegisterTheyWereTakenFrom(t *testing.T) {
	provenance, err := rules.Provenance()
	if err != nil {
		t.Fatal(err)
	}
	if got := provenance.SourceRepository(); got != "codecheckers/register" {
		t.Errorf("the bundle came from %q", got)
	}
}

// One file unreadable and another provably changed: the bundle is behind,
// and saying "I could not tell" while holding the proof would be perverse.
// The unreadable file is still reported, because there may be more to it.
func TestProofOutranksAnUnmadeComparison(t *testing.T) {
	stub := newStub(t)
	stub.servesRules(t, "1f6a3c9e0000000000000000000000000000abcd", "2026-09-19T09:00:00Z",
		func(_ string, raw []byte) []byte { return append(raw, "\n# one more rule\n"...) })
	stub.Status(rawRule("rules-2.0.yml"), http.StatusInternalServerError)

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !drift.Behind() || drift.Known() {
		t.Fatalf("one proven, one unread: %+v", drift)
	}
	if drift.Mark() != Symbol(OutcomeWarning) {
		t.Errorf("mark %q, want the warning: the drift is proven", drift.Mark())
	}
	if drift.Files[0].Mark() != Symbol(OutcomeWarning) {
		t.Errorf("the file that drifted should say so, whatever happened to the other one")
	}
	if drift.Files[1].Mark() != Symbol(OutcomeSkipped) {
		t.Errorf("the file that could not be read is a skip")
	}

	summary := drift.Summary()
	if !strings.Contains(summary, "not the register's current rules") {
		t.Errorf("summary %q, want the verdict that can be acted on", summary)
	}
	if !strings.Contains(summary, "there may be more") || !strings.Contains(summary, "rules-2.0.yml") {
		t.Errorf("summary %q, want it to name what it could not read", summary)
	}
	if strings.Contains(drift.Text(), "could not check whether") {
		t.Errorf("the report must not contradict itself:\n%s", drift.Text())
	}
}

// The distinction the command exists for: a new commit on a file that says
// the same thing is not drift, and the report says which it is.
func TestTheReportSaysWhatTheRegistersCopyActuallySays(t *testing.T) {
	stub := newStub(t)
	stub.servesRules(t, "1f6a3c9e0000000000000000000000000000abcd", "2026-09-19T09:00:00Z", nil)

	drift, err := RulesDrift(stub.services)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	for _, rendered := range []string{drift.Text(), drift.Markdown()} {
		if !strings.Contains(rendered, "the same file") {
			t.Errorf("a different commit on an unchanged file should say so:\n%s", rendered)
		}
	}
}
