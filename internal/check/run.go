package check

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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
	// Lines are where in the configuration the finding is, see Result.
	Lines []int
}

// Where says which lines of the configuration a finding is about, " at line
// 26" to follow an identifier, or empty when it is about none.
func (r RuleResult) Where() string {
	switch len(r.Lines) {
	case 0:
		return ""
	case 1:
		return " at line " + lineNumbers(r.Lines, "")
	default:
		return " at lines " + lineNumbers(r.Lines, "")
	}
}

// lineNumbers lists line numbers, each a link to that line of the file when
// there is a page for it.
func lineNumbers(lines []int, file string) string {
	numbers := make([]string, len(lines))
	for i, line := range lines {
		numbers[i] = strconv.Itoa(line)
		if file != "" {
			numbers[i] = fmt.Sprintf("[%d](%s#L%d)", line, file, line)
		}
	}
	return strings.Join(numbers, ", ")
}

// Text describes one rule in words.
//
// A rule reported on its own always carries what the rule says, because an
// identifier alone tells a reader nothing. Where several identifiers are
// listed or counted, Line is used instead: the descriptions would bury the
// list.
func (r RuleResult) Text() string {
	text := r.Rule.ID + " " + r.Rule.Name + r.Where()
	if r.Detail != "" {
		text += ": " + r.Detail
	}
	return text + " (" + r.Rule.Description + ")"
}

// Line describes one rule by identifier and finding only, for lists.
func (r RuleResult) Line() string {
	if r.Detail == "" {
		return r.Rule.ID + r.Where()
	}
	return r.Rule.ID + r.Where() + ": " + r.Detail
}

// Report is the outcome of validating one codecheck.yml.
type Report struct {
	Label       string
	SpecVersion string
	// SpecVersionReason says how that version was chosen: declared by the
	// file, dated, or assumed. A codechecker reading "checked against 1.0"
	// should not have to guess which.
	SpecVersionReason string
	// Source is where the configuration was read from, as a link when there is
	// one.
	Source string
	// File is the configuration itself as a link, which a line number can be
	// added to; empty where the source has no such page.
	File   string
	Strict bool
	// Part is the piece of the catalogue this report covers, empty for all of
	// it, see Report.Subset.
	Part    string
	Results []RuleResult
	// Refused is why no rule was run at all, nil when they were. A refused
	// report is not OK: nothing about the file was established.
	Refused *Refusal
}

// A Refusal is a configuration the rules cannot be applied to, and what to do
// about it.
//
// It is not a rule finding - no rule in the catalogue is about it - but "could
// not check" for the file as a whole, so it stays out of the counts at the
// foot of a report, and a report says it instead of the results rather than
// among them.
type Refusal struct {
	Why    string
	Remedy string
}

// SpecVersion returns the specification version to check a configuration
// against and why, or why there is none to check it against.
//
// The specification asks tools to assume the newest version when a file names
// none. Taken literally that judges a configuration written in 2020 against
// requirements published in 2026, and reports failures its author could not
// have known about - so a file that names no version is dated instead, and
// only an undatable one falls back to the newest. See "Choosing the
// specification version" in the register's RULES.md.
//
// A file that names a version this build does not know is none of those: it
// is refused rather than checked, because every finding would be judged
// against requirements the file does not claim to meet.
func SpecVersion(context Context) (version, why string, refused *Refusal) {
	if context.Config.HasVersion {
		declared := SpecVersionNamed(context.Config.Version)
		if declared == "" {
			return "", "", unknownVersion(context.Config.Version)
		}
		return declared, "declared by the file", nil
	}

	if when, source := configurationDate(context); !when.IsZero() {
		if dated := rules.AsOf(when); dated != "" {
			return dated, fmt.Sprintf("current when the file was %s (%s)",
				source, when.Format(time.DateOnly)), nil
		}
	}
	return rules.Newest(), "assumed: the file names no version and could not be dated", nil
}

// unknownVersion refuses a configuration that declares a specification
// version this build does not know, or declares one in a form nobody could.
func unknownVersion(declared string) *Refusal {
	known := rules.SpecVersions()
	knownList := known[len(known)-1]
	if len(known) > 1 {
		knownList = strings.Join(known[:len(known)-1], ", ") + " and " + knownList
	}
	why := "the version node is empty, which names no specification"
	if declared = strings.TrimSpace(declared); declared != "" {
		why = fmt.Sprintf("the file declares specification version '%s', "+
			"which is not one this build knows", declared)
	}
	return &Refusal{
		Why: fmt.Sprintf("%s. It knows %s, and a report against a specification "+
			"the file does not claim to meet would be wrong in every line", why, knownList),
		Remedy: fmt.Sprintf("Name a published version, such as "+
			"https://codecheck.org.uk/spec/config/%s/. If the file means a "+
			"specification newer than %s, this build is behind the register "+
			"and its rules need refreshing.", rules.Newest(), rules.Newest()),
	}
}

// configurationDate is the best evidence of when a configuration was written:
// what the file itself says, then what its source says.
func configurationDate(context Context) (time.Time, string) {
	if when, ok := parseCheckTime(context.Config.CheckTime); ok {
		return when, "checked"
	}
	if !context.Modified.IsZero() {
		return context.Modified, "last changed"
	}
	return time.Time{}, ""
}

// parseCheckTime reads the check_time node, which the specification writes as
// "2019-02-14 10:00:00" but which files in the wild also carry as a date or an
// RFC 3339 timestamp.
func parseCheckTime(value string) (time.Time, bool) { return parseTimestamp(value) }

// timestampLayouts are the ways a date reaches this bot as text: the
// specification's own form, a bare date, the RFC 3339 an API answers with,
// and the +0000 variant of it that provenance.json is written in. One table,
// because a date that one reader understands and another does not is the
// worst of both.
var timestampLayouts = []string{
	"2006-01-02 15:04:05",
	time.DateOnly,
	time.RFC3339,
	"2006-01-02 15:04",
	"2006-01-02T15:04:05-0700",
}

func parseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range timestampLayouts {
		if when, err := time.Parse(layout, value); err == nil {
			return when, true
		}
	}
	return time.Time{}, false
}

// Run applies the rules of one specification version to a codecheck.yml.
//
// Which rules run comes from that version's rule file, so a rule added to a
// newer specification is not applied to a file that declares an older one.
// strict escalates warnings to errors, and never the other way round.
func Run(context Context, specVersion string, strict bool) (Report, error) {
	return RunPart(context, specVersion, strict, "")
}

// RunPart applies one part of the catalogue, see Parts.
//
// The rules outside the part are not run at all: asking about the bundle must
// not cost a Crossref lookup and a Zenodo record, and on a bad day must not
// fail because one of them is down.
func RunPart(context Context, specVersion string, strict bool, part string) (Report, error) {
	// What was asked comes before what the file says: a mistyped part is the
	// caller's to fix, whatever version the file declares.
	if !IsPart(part) {
		return Report{}, fmt.Errorf("no such check %q; try one of %s, or leave it off for all of them",
			part, strings.Join(Parts(), ", "))
	}
	report := Report{Label: context.Label, Source: context.SourceURL(),
		File: context.FileURL(), Strict: strict}
	if part != "all" {
		report.Part = part
	}
	// A caller that pinned the version knows why; only a chosen one needs
	// explaining. A file no known version can be chosen for is refused, and
	// the report says so instead of running anything.
	if specVersion == "" {
		specVersion, report.SpecVersionReason, report.Refused = SpecVersion(context)
		if report.Refused != nil {
			return report, nil
		}
	}
	report.SpecVersion = specVersion
	catalogue, err := rules.For(specVersion)
	if err != nil {
		return Report{}, err
	}

	for _, rule := range catalogue {
		if !rule.Active() || !inPart(rule, part) {
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
	reported := RuleResult{Rule: rule, Detail: result.Detail, Lines: result.Lines}
	switch result.Status {
	case StatusPass:
		reported.Outcome = OutcomeOK
	case StatusSkip:
		reported.Outcome = OutcomeSkipped
	default:
		// A failed check is reported at the rule's own severity, which strict
		// only ever raises.
		reported.Outcome = Outcome(rule.Severity)
		if strict && rule.Severity == rules.SeverityWarning {
			reported.Outcome = OutcomeError
		}
	}
	return reported
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
func (r Report) OK() bool { return r.Refused == nil && len(r.Failures()) == 0 }

// FailureMessage says what failed. One rule is reported in full, with the
// rule's own words; several are listed by identifier and finding, so the list
// stays readable.
func (r Report) FailureMessage() string {
	if r.Refused != nil {
		return fmt.Sprintf("%s was not checked: %s", r.Label, r.Refused.Why)
	}
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

// Parts of the rule catalogue, as the bot's check commands name them.
//
// "@chekhovbot check bundle" asks about the bundle, not about everything, and a
// codechecker fixing manifest paths does not want the Crossref comparison
// again - so a part is chosen before the checks run, not filtered out of the
// answer afterwards.
//
// All but one are a rule area, which the catalogue carries. The exception is
// `references`, which crosses two areas because the catalogue separates the
// form of a reference from what resolving it says.
var partNames = []string{"bundle", "config", "metadata", "references", "register", "report"}

// referenceRules are the rules about what the paper is and where it is, which
// is what "check references" means to a codechecker.
//
// Hand-maintained, and the one place in this package where a rule's properties
// are written in code rather than read from the register. TestReferenceRules
// guards it: a rule the register adds with a reference-shaped name has to be
// classified here. A `tags:` field in the rule files would remove the list
// entirely, see the note in CLAUDE.md.
var referenceRules = map[string]bool{
	"CC-CFG-021": true, // paper-reference
	"CC-CFG-022": true, // reference-not-bare-pdf
	"CC-CFG-027": true, // reference-is-url
	"CC-CFG-028": true, // reference-prefers-doi
	"CC-CFG-029": true, // reference-other-is-list
	"CC-CFG-030": true, // reference-other-item-form
	"CC-CFG-031": true, // reference-pdf-is-archived
	"CC-MET-004": true, // paper-reference-resolves
	"CC-MET-005": true, // paper-title-match
	"CC-MET-006": true, // paper-author-count-match
	"CC-MET-007": true, // paper-author-name-match
	"CC-MET-008": true, // paper-author-orcid-match
	"CC-MET-009": true, // reference-other-resolves
}

// Parts are the names a check may be narrowed to.
func Parts() []string { return partNames }

// Words are what `check` takes before its target: the parts of the catalogue,
// and AboutRepository, which is a description rather than a part.
//
// One list, so that a caller parsing a command has no vocabulary of its own to
// keep in step - and so that the usage a reader is shown is the one the parser
// actually uses.
func Words() []string { return append(slices.Clone(partNames), AboutRepository) }

// IsWord reports whether an argument says what to answer rather than what to
// read. The empty string is not one: only IsPart treats it as "all of them",
// and a caller reading a comment word by word must not.
func IsWord(name string) bool {
	return name != "" && (IsPart(name) || name == AboutRepository)
}

// IsPart reports whether a name selects part of the catalogue. "all" and the
// empty string mean the whole of it, so that a caller has no vocabulary of its
// own to keep in step.
func IsPart(name string) bool {
	return name == "" || name == "all" || slices.Contains(partNames, name)
}

// inPart reports whether a rule belongs to the named part.
func inPart(rule rules.Rule, part string) bool {
	switch part {
	case "", "all":
		return true
	case "references":
		return referenceRules[rule.ID]
	default:
		return rule.Area == part
	}
}
