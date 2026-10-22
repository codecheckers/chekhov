package command

import (
	"fmt"
	"strings"
)

// authorGuide is where what an author provides is written down.
const authorGuide = "https://codecheck.org.uk/guide/community-workflow-author#2-requirements"

// A Requirement is one thing `check ready` looked for, as its reply shows it.
type Requirement struct {
	What string
	// Passed says it is there. A requirement that did not pass either has a
	// Fix, because it is missing, or could not be looked at.
	Passed bool
	// Detail is what was found, or why it could not be looked at.
	Detail string
	// Fix is what to do about a requirement that is missing.
	Fix string
}

// A Readiness is `check ready`'s answer, as the reply shows it.
type Readiness struct {
	// Target is where the files were looked for.
	Target string
	// From says where the target came from: the command, the record, the
	// issue's Repository line.
	From string
	// Problem is why nothing could be looked at.
	Problem      string
	Requirements []Requirement
}

// ReadyReply is `check ready`'s answer: one line per requirement, with what to
// do where one is missing, and the guide that asks for them. A requirement
// that could not be looked at is never readiness, and never a missing one.
func ReadyReply(r Readiness) string {
	passed, missing := 0, 0
	var lines strings.Builder
	for _, requirement := range r.Requirements {
		symbol := "…"
		switch {
		case requirement.Passed:
			symbol = "✔"
			passed++
		case requirement.Fix != "":
			symbol = "✖"
			missing++
		}
		line := fmt.Sprintf("- %s %s", symbol, requirement.What)
		if requirement.Detail != "" {
			line += ": " + requirement.Detail
		}
		if requirement.Fix != "" {
			line += ". **To do**: " + requirement.Fix
		}
		lines.WriteString(line + "\n")
	}

	var out strings.Builder
	switch {
	case r.Problem != "":
		fmt.Fprintf(&out, "… **I could not look at %s**: %s\n", Code(r.Target), r.Problem)
	case missing > 0:
		fmt.Fprintf(&out, "✖ **Not ready yet**: %s is missing what an author provides.\n", Code(r.Target))
	case passed == len(r.Requirements):
		fmt.Fprintf(&out, "✔ **Ready for a codechecker**: %s has what an author provides.\n", Code(r.Target))
	default:
		fmt.Fprintf(&out, "… **I could not tell everything** about %s.\n", Code(r.Target))
	}
	if lines.Len() > 0 {
		out.WriteString("\n" + lines.String())
	}
	out.WriteString("\n")
	switch {
	case r.From != "" && r.Problem != "":
		fmt.Fprintf(&out, "%s is named by %s. ", Code(r.Target), r.From)
	case r.From != "":
		fmt.Fprintf(&out, "Read from %s, named by %s. ", Code(r.Target), r.From)
	}
	fmt.Fprintf(&out, "What an author provides before a check starts is in the [author guide](%s).\n", authorGuide)
	return out.String()
}

// NoReadyTarget is `check ready` with nothing to look at: no repository named,
// recorded, or on the issue's Repository line - or an issue that could not be
// read, which is not the same as one without the line.
func NoReadyTarget(issueRead bool) string {
	issue := "the issue has no **Repository** line I can read. Add one to the issue, or name it"
	if !issueRead {
		issue = "I could not read the issue for its **Repository** line. Name the repository"
	}
	return fmt.Sprintf("… I do not know which repository to look at: the command names none, "+
		"the record of this check holds none, and %s:\n\n```\n%s %s github::owner/repo\n```\n",
		issue, Bot, CheckReady)
}

// UnreadableTarget is `check ready` naming something that is not a
// repository it can read.
func UnreadableTarget(word string) string {
	return fmt.Sprintf("… I cannot read %s as a repository. Name it the way `register.csv` does, "+
		"or as a link:\n\n```\n%s %s github::owner/repo\n```\n", Code(word), Bot, CheckReady)
}
