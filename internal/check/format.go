package check

import (
	"fmt"
	"strings"
)

// Symbols for the outcomes, used in both the terminal report and the comment
// the bot posts. They carry the meaning at a glance; the words carry it for
// anyone whose client does not render them.
var symbol = map[Outcome]string{
	OutcomeOK:        "✔", // heavy check mark
	OutcomeError:     "✖", // heavy multiplication x
	OutcomeWarning:   "⚠", // warning sign
	OutcomeInfo:      "ℹ", // information source
	OutcomeSkipped:   "…", // horizontal ellipsis
	OutcomeUnchecked: "─", // box drawing light horizontal
}

// Symbol returns the mark shown for an outcome.
func Symbol(outcome Outcome) string { return symbol[outcome] }

// Text renders the report for a terminal, one line per rule.
//
// A line that needs attention says what the rule requires; a line that passed,
// skipped or is unchecked stays short, so a clean run does not fill a screen.
func (r Report) Text() string {
	var out strings.Builder
	fmt.Fprintf(&out, "CODECHECK rules %s%s: %s\n\n", r.SpecVersion, r.partSuffix(), r.Label)
	for _, result := range r.Results {
		line := result.Line()
		if reported(result.Outcome) {
			line = result.Text()
		} else if result.Detail != "" {
			line = result.Rule.ID + " " + result.Rule.Name + ": " + result.Detail
		} else {
			line = result.Rule.ID + " " + result.Rule.Name
		}
		fmt.Fprintf(&out, "%s %s\n", Symbol(result.Outcome), line)
	}
	out.WriteString("\n" + r.Summary() + "\n")
	if r.Strict {
		out.WriteString("strict: warnings are reported as errors\n")
	}
	return out.String()
}

// partSuffix names the piece of the catalogue a narrowed report covers, so
// that "0 failed" cannot be mistaken for "nothing is wrong with this file".
func (r Report) partSuffix() string {
	if r.Part == "" {
		return ""
	}
	return " (" + r.Part + " only)"
}

// Summary counts the outcomes in one line.
func (r Report) Summary() string {
	return fmt.Sprintf("%d passed, %d failed, %d warnings, %d notes, %d skipped, %d not checked",
		r.Count(OutcomeOK), r.Count(OutcomeError), r.Count(OutcomeWarning),
		r.Count(OutcomeInfo), r.Count(OutcomeSkipped), r.Count(OutcomeUnchecked))
}

// Markdown renders the report as the comment the bot posts on the checks
// issue. Only the rules with something to say are listed: a table of every
// passing rule would bury the few lines that need action.
func (r Report) Markdown() string {
	var out strings.Builder

	if r.OK() {
		fmt.Fprintf(&out, "%s **`codecheck.yml` is valid** against specification %s%s.\n",
			Symbol(OutcomeOK), r.SpecVersion, r.partSuffix())
	} else {
		fmt.Fprintf(&out, "%s **`codecheck.yml` is not valid yet** against specification %s%s.\n",
			Symbol(OutcomeError), r.SpecVersion, r.partSuffix())
	}
	fmt.Fprintf(&out, "\n%s\n", r.Summary())

	var reportable []RuleResult
	for _, result := range r.Results {
		if reported(result.Outcome) {
			reportable = append(reportable, result)
		}
	}

	if len(reportable) > 0 {
		out.WriteString("\n| | Rule | Finding | What the rule asks |\n")
		out.WriteString("|---|---|---|---|\n")
		for _, result := range reportable {
			fmt.Fprintf(&out, "| %s | `%s` %s | %s | %s |\n",
				Symbol(result.Outcome), result.Rule.ID, result.Rule.Name,
				escapePipes(result.Detail), escapePipes(result.Rule.Description))
		}
	}

	out.WriteString("\nThe rules are the CODECHECK validation rules, kept in the register: " +
		"https://github.com/codecheckers/register/blob/master/RULES.md\n")
	return out.String()
}

// reported says whether an outcome is one the reader has to act on.
func reported(outcome Outcome) bool {
	return outcome == OutcomeError || outcome == OutcomeWarning || outcome == OutcomeInfo
}

// escapePipes keeps a finding from breaking the Markdown table it sits in.
func escapePipes(text string) string { return strings.ReplaceAll(text, "|", "\\|") }
