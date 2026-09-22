package check

import (
	"fmt"
	"slices"
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
	// A refused report has no version to name, so the heading leaves it out.
	version := ""
	if r.SpecVersion != "" {
		version = " " + r.SpecVersion
	}
	fmt.Fprintf(&out, "CODECHECK rules%s%s: %s\n", version, r.partSuffix(), r.Label)
	if r.SpecVersionReason != "" {
		fmt.Fprintf(&out, "specification %s %s\n", r.SpecVersion, r.SpecVersionReason)
	}
	if r.Source != "" {
		fmt.Fprintf(&out, "read from %s\n", r.Source)
	}
	out.WriteString("\n")
	if r.Refused != nil {
		fmt.Fprintf(&out, "%s not checked: %s.\n%s\n",
			Symbol(OutcomeUnchecked), r.Refused.Why, r.Refused.Remedy)
		return out.String()
	}
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

	if r.Refused != nil {
		// Said instead of the results, not among them: there are none, and a
		// table of zero counts would read as a file with nothing wrong.
		fmt.Fprintf(&out, "%s **`codecheck.yml` was not checked**: %s.\n\n%s\n",
			Symbol(OutcomeUnchecked), r.Refused.Why, r.Refused.Remedy)
		out.WriteString(r.provenance())
		return out.String()
	}

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

	// The line column is there when a finding has a line to show, so that a
	// report about the register or the bundle does not carry an empty one.
	withLines := slices.ContainsFunc(reportable, func(result RuleResult) bool {
		return len(result.Lines) > 0
	})

	if len(reportable) > 0 {
		columns := []string{"", "Rule", "Finding", "What the rule asks"}
		if withLines {
			columns = slices.Insert(columns, 2, "Line")
		}
		fmt.Fprintf(&out, "\n| %s |\n|%s\n", strings.Join(columns, " | "),
			strings.Repeat("---|", len(columns)))
		for _, result := range reportable {
			fmt.Fprintf(&out, "| %s | `%s` %s |", Symbol(result.Outcome), result.Rule.ID, result.Rule.Name)
			if withLines {
				fmt.Fprintf(&out, " %s |", lineNumbers(result.Lines, r.File))
			}
			fmt.Fprintf(&out, " %s | %s |\n",
				escapePipes(result.Detail), escapePipes(result.Rule.Description))
		}
	}

	out.WriteString(r.provenance())
	out.WriteString("\nThe rules are the CODECHECK validation rules, kept in the register: " +
		"https://github.com/codecheckers/register/blob/master/RULES.md\n")
	return out.String()
}

// provenance says what was checked and against which requirements: the
// repository the file came from, as a link the reader can follow, and how the
// specification version was chosen. "Checked against 1.0" should not leave a
// codechecker guessing whether that was declared or assumed.
func (r Report) provenance() string {
	var out strings.Builder
	if r.Source != "" {
		fmt.Fprintf(&out, "\nRead from [`%s`](%s).", r.Label, r.Source)
	}
	if r.SpecVersionReason != "" {
		if out.Len() == 0 {
			out.WriteString("\n")
		} else {
			out.WriteString(" ")
		}
		fmt.Fprintf(&out, "Checked against specification %s, %s.", r.SpecVersion, r.SpecVersionReason)
	}
	if out.Len() > 0 {
		out.WriteString("\n")
	}
	return out.String()
}

// reported says whether an outcome is one the reader has to act on.
func reported(outcome Outcome) bool {
	return outcome == OutcomeError || outcome == OutcomeWarning || outcome == OutcomeInfo
}

// escapePipes keeps a finding from breaking the Markdown table it sits in.
func escapePipes(text string) string { return strings.ReplaceAll(text, "|", "\\|") }
