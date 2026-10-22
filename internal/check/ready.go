package check

import (
	"fmt"
	"regexp"
	"strings"
)

// `check ready` (codecheckers/chekhov#28): whether what an author provides
// before a check starts is there, so that a check does not stall after a
// codechecker has been found.
//
// A summary, deliberately. Each item but one is a rule that already exists,
// asked directly: the detailed reports stay with `check config`, `check
// bundle` and `check metadata`, and this must not grow its own copies of
// them. The README is the exception, because no rule asks for one - the
// specification is about the codecheck.yml and its bundle, and the README is
// the author guide's requirement, not the file's.
//
// What counts is what the author provides. The template's placeholders are
// expected at this stage, so CC-CFG-023 is not asked, and a paper.reference
// still written FIXME is the same as none.

// A ReadyItem is one requirement, as `check ready` reports it.
type ReadyItem struct {
	// What is the requirement, as the reply names it.
	What string
	// Status is pass, fail, or skip for "could not check".
	Status Status
	// Detail is what was found, or why it could not be looked at.
	Detail string
	// Fix is what to do about a failure, empty when there is nothing to do.
	Fix string
}

// Readiness is the answer to `check ready`.
type Readiness struct {
	// Target is where the files were looked for, as resolved.
	Target string
	// Problem is why nothing could be looked at; the items are empty then.
	Problem string
	Items   []ReadyItem
}

// Ready looks at a target for what an author provides. issuePaper is a link
// to the paper the issue gives, used when the codecheck.yml names none.
func Ready(target string, localPaths bool, services *Services, issuePaper string) Readiness {
	context, configured, problem := readyContext(target, localPaths, services)
	if problem != "" {
		return Readiness{Target: target, Problem: problem}
	}
	return Readiness{Target: context.Bundle.Describe(), Items: []ReadyItem{
		readme(context),
		configuration(context, configured),
		item("a licence", Checks["CC-BUN-005"](context),
			"add a LICENSE file at the root, naming the licences of the code and the data"),
		paper(context, issuePaper),
	}}
}

// readyContext reads the target's configuration through its bundle, which
// it locates first: a repository without a codecheck.yml still has a README
// and a licence to look for. configured is false when there is no
// codecheck.yml; problem is set when not even the bundle could be read.
//
// Not Load, which would also date a file that names no version - a request
// no item here reads - and whose failure could not say whether the file is
// missing or the repository is.
func readyContext(target string, localPaths bool, services *Services) (Context, bool, string) {
	_, bundle, err := bundleAt(target, localPaths, services)
	if err != nil {
		return Context{}, false, err.Error()
	}
	context := Context{Bundle: bundle, Services: services}
	if _, listed := bundleListing(context, ""); listed.Status != "" {
		return Context{}, false, listed.Detail
	}
	// The listing is in hand, so asking it costs nothing; Bundle.Exists
	// would ask again.
	if there, err := existsInListing(bundle, "codecheck.yml"); err != nil || !there {
		return context, false, ""
	}
	raw, err := bundle.Read("codecheck.yml")
	if err != nil {
		return Context{}, false, err.Error()
	}
	read := FromBytes(raw)
	read.Bundle, read.Services = bundle, services
	return read, true, ""
}

// readmeFile is how an author names the instructions: README, with any
// extension, in any case.
var readmeFile = regexp.MustCompile(`(?i)^README(\..*)?$`)

// readme asks for the instructions at the root of the bundle.
func readme(c Context) ReadyItem {
	const what = "a README"
	entries, result := bundleListing(c, "")
	if result.Status != "" {
		return ReadyItem{What: what, Status: result.Status, Detail: result.Detail}
	}
	if name := fileNamed(entries, readmeFile); name != "" {
		return ReadyItem{What: what, Status: StatusPass, Detail: name}
	}
	return ReadyItem{What: what, Status: StatusFail, Detail: "no README at the root of the bundle",
		Fix: "add a README at the root that says how to run the workflow"}
}

// configuration asks for a codecheck.yml that parses and has a manifest. The
// rest of what the file should say comes with the check; the manifest is what
// the author knows best, because it lists what the workflow writes.
func configuration(c Context, configured bool) ReadyItem {
	const what = "a codecheck.yml with a manifest"
	const fix = "add a codecheck.yml whose manifest lists the files the workflow writes"
	if !configured {
		return ReadyItem{What: what, Status: StatusFail, Detail: "no codecheck.yml at the root of the bundle", Fix: fix}
	}
	if parsed := Checks["CC-CFG-001"](c); parsed.Status != StatusPass {
		return item(what, parsed, "correct the codecheck.yml so that it is valid YAML")
	}
	return item(what, Checks["CC-CFG-004"](c), fix)
}

// paper asks for a paper to check: the codecheck.yml's paper.reference, or
// else the link the issue gives, and whether it resolves.
func paper(c Context, issuePaper string) ReadyItem {
	const what = "a paper to check"
	reference, from := strings.TrimSpace(c.Config.Paper.Reference), "`paper.reference`"
	line := c.Config.Line("paper.reference")
	// A bare DOI is a link to the paper all the same; CC-CFG-027 is what
	// asks for it to be written as one.
	if doi := DOIIn(reference); doi != "" && doi == strings.TrimPrefix(reference, "doi:") {
		reference = "https://doi.org/" + doi
	}
	issuePaper = strings.TrimSpace(issuePaper)
	// A reference that is not a link cannot be asked, and a link the issue
	// gives can: the reference is CC-CFG-027's to report.
	if reference == "" || isPlaceholder(reference) || (!bareURL.MatchString(reference) && issuePaper != "") {
		missing := "`paper.reference` gives none"
		if reference != "" {
			missing = "`paper.reference` is still a placeholder"
		}
		reference, from, line = issuePaper, "the issue", 0
		if reference == "" {
			return ReadyItem{What: what, Status: StatusFail,
				Detail: missing + ", and the issue links no paper",
				Fix: "link the preprint, the published paper or the submission on the issue's " +
					"**Workflow** line, or in `paper.reference`"}
		}
	}
	resolved := paperResolves(c.Services, reference, line)
	if resolved.Detail == "" {
		resolved.Detail = reference
	}
	resolved.Detail = fmt.Sprintf("%s, from %s", resolved.Detail, from)
	return item(what, resolved, "correct the link to the paper, or give one that answers")
}

// item is a rule's result as a requirement, with what to do about a failure.
func item(what string, result Result, fix string) ReadyItem {
	ready := ReadyItem{What: what, Status: result.Status, Detail: result.Detail}
	if result.Status == StatusFail {
		ready.Fix = fix
	}
	return ready
}
