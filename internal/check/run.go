package check

import (
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/rules"
)

func init() {
	// The known specification versions live in the rules package; the checks
	// read them through here so there is one list.
	specVersions = rules.SpecVersions()
}

// Outcome of a rule once its severity has been applied.
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeError     Outcome = "error"
	OutcomeWarning   Outcome = "warning"
	OutcomeInfo      Outcome = "info"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeUnchecked Outcome = "unchecked"
)

// A RuleResult is one rule and what came of it.
type RuleResult struct {
	Rule    rules.Rule
	Outcome Outcome
	Detail  string
}

// Text describes one rule in words.
//
// A rule reported on its own always carries what the rule says, because an
// identifier alone tells a reader nothing. Where several identifiers are
// listed or counted, Line is used instead: the descriptions would bury the
// list.
func (r RuleResult) Text() string {
	text := r.Rule.ID + " " + r.Rule.Name
	if r.Detail != "" {
		text += ": " + r.Detail
	}
	return text + " (" + r.Rule.Description + ")"
}

// Line describes one rule by identifier and finding only, for lists.
func (r RuleResult) Line() string {
	if r.Detail == "" {
		return r.Rule.ID
	}
	return r.Rule.ID + ": " + r.Detail
}

// Report is the outcome of validating one codecheck.yml.
type Report struct {
	Label       string
	SpecVersion string
	Strict      bool
	Results     []RuleResult
}

// SpecVersion returns the specification version a configuration declares, or
// the newest one when it declares none, which is what the specification asks
// tools to assume.
func SpecVersion(config Config) string {
	if version := SpecVersionFromURL(config.Version); version != "" {
		return version
	}
	return rules.Newest()
}

// Run applies the rules of one specification version to a codecheck.yml.
//
// Which rules run comes from that version's rule file, so a rule added to a
// newer specification is not applied to a file that declares an older one.
// strict escalates warnings to errors, and never the other way round.
func Run(context Context, specVersion string, strict bool) (Report, error) {
	if specVersion == "" {
		specVersion = SpecVersion(context.Config)
	}
	catalogue, err := rules.For(specVersion)
	if err != nil {
		return Report{}, err
	}

	report := Report{Label: context.Label, SpecVersion: specVersion, Strict: strict}
	for _, rule := range catalogue {
		if !rule.Active() {
			continue
		}
		report.Results = append(report.Results, runRule(rule, context, strict))
	}
	return report, nil
}

func runRule(rule rules.Rule, context Context, strict bool) RuleResult {
	checkFunc, ok := Checks[rule.ID]
	if !ok {
		reason := NotImplemented[rule.ID]
		if reason == "" {
			reason = "no check function for this rule yet"
		}
		return RuleResult{Rule: rule, Outcome: OutcomeUnchecked, Detail: reason}
	}

	result := checkFunc(context)
	switch result.Status {
	case StatusPass:
		return RuleResult{Rule: rule, Outcome: OutcomeOK, Detail: result.Detail}
	case StatusSkip:
		return RuleResult{Rule: rule, Outcome: OutcomeSkipped, Detail: result.Detail}
	default:
		// A failed check is reported at the rule's own severity, which strict
		// only ever raises.
		outcome := Outcome(rule.Severity)
		if strict && rule.Severity == rules.SeverityWarning {
			outcome = OutcomeError
		}
		return RuleResult{Rule: rule, Outcome: outcome, Detail: result.Detail}
	}
}

// Count returns how many results had one outcome.
func (r Report) Count(outcome Outcome) int {
	n := 0
	for _, result := range r.Results {
		if result.Outcome == outcome {
			n++
		}
	}
	return n
}

// Failures returns the results that failed at severity error.
func (r Report) Failures() []RuleResult {
	var failures []RuleResult
	for _, result := range r.Results {
		if result.Outcome == OutcomeError {
			failures = append(failures, result)
		}
	}
	return failures
}

// OK reports whether the configuration is valid: no rule failed as an error.
func (r Report) OK() bool { return len(r.Failures()) == 0 }

// FailureMessage says what failed. One rule is reported in full, with the
// rule's own words; several are listed by identifier and finding, so the list
// stays readable.
func (r Report) FailureMessage() string {
	failures := r.Failures()
	switch len(failures) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("rule failed for %s: %s", r.Label, failures[0].Text())
	default:
		lines := make([]string, 0, len(failures))
		for _, failure := range failures {
			lines = append(lines, "  "+failure.Line())
		}
		return fmt.Sprintf("%d rules failed for %s:\n%s",
			len(failures), r.Label, strings.Join(lines, "\n"))
	}
}
