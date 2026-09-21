package check

import (
	"crypto/md5"
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/build"
	"github.com/codecheckers/chekhov/internal/rules"
)

// Whether this build judges by the register's current rules.
//
// The rules are maintained in the register and bundled into the binary, so a
// deployment goes on applying whatever catalogue was bundled when it was last
// built, and nothing says when that has fallen behind. It has happened:
// codecheckers/register#220 renamed four rules and reworded five
// descriptions, and the deployment reported the old names for a day, which
// nobody could see without reading /healthz and the GitHub API side by side.
// See codecheckers/chekhov#43.

// A Drift is what the bundled rules are, and what the register has now.
type Drift struct {
	// Register is where the rules come from, as owner/repo.
	Register string
	// Retrieved is when the bundle was taken, which tells "behind" from "the
	// same day, a different file".
	Retrieved string
	Files     []FileDrift
	// Unreadable is why the register could not be asked, empty when it could.
	// A register that would not answer is never reported as agreement.
	Unreadable string
}

// A FileDrift is one rule file: what is bundled, and what the register has.
//
// The bundled half is the provenance record itself rather than a copy of its
// fields, so that something the register starts recording cannot be silently
// left off here - which is how the OpenAlex base URL was once left out of a
// hand-copied struct.
type FileDrift struct {
	rules.File
	// Current is the register's commit for this file now, and CurrentDate
	// when it was made. Shown so that a reader can see which version the
	// register has; the verdict is Differs, which is about the bytes.
	Current     string
	CurrentDate time.Time
	// Differs is whether the register's copy says something other than the
	// bundled one. The commit alone would report a whitespace fix, or a
	// commit and its revert, as a bundle that has fallen behind.
	Differs bool
	// Unreadable is why this file could not be compared, empty when it
	// could. Per file, because which one could not be asked is the thing a
	// reader of a failed weekly run wants to know.
	Unreadable string
}

// Behind reports whether the register's copy of this file says something the
// bundled one does not. A file that could not be compared is not behind; it
// is unknown, which Unreadable says.
func (f FileDrift) Behind() bool { return f.Unreadable == "" && f.Differs }

// Known reports whether this file could be compared at all.
func (f FileDrift) Known() bool { return f.Unreadable == "" }

// Behind reports whether any bundled rule file is provably out of date.
//
// True is certain: it means a file was read and says something the bundle
// does not. It is independent of Known, and deliberately outranks it - a
// proven drift is worth acting on even when another file could not be read,
// and it would be perverse to answer "I could not tell" while holding proof.
// Unproven is not the same as false, which is what Known is for.
func (d Drift) Behind() bool {
	for _, file := range d.Files {
		if file.Behind() {
			return true
		}
	}
	return false
}

// Known reports whether every file could be compared.
//
// One file that could not be asked about leaves the answer unknown: reporting
// the files that were reached as current, while the one that was not is the
// one that moved, is the reading this command exists to prevent. Read with
// Behind, which is proof rather than the absence of it.
func (d Drift) Known() bool {
	if d.Unreadable != "" {
		return false
	}
	for _, file := range d.Files {
		if !file.Known() {
			return false
		}
	}
	return true
}

// Why is the reason the comparison could not be made, empty when it could.
func (d Drift) Why() string {
	if d.Unreadable != "" {
		return d.Unreadable
	}
	for _, file := range d.Files {
		if !file.Known() {
			return file.Unreadable
		}
	}
	return ""
}

// RulesDrift compares the bundled rules with the register's current ones.
//
// One request per rule file, to the register the bundle came from rather than
// to the register the bot works on: a bot answering about the testing register
// still judges by the rules in codecheckers/register.
func RulesDrift(services *Services) (Drift, error) {
	provenance, err := rules.Provenance()
	if err != nil {
		return Drift{}, err
	}

	drift := Drift{Register: provenance.SourceRepository(), Retrieved: provenance.Retrieved}
	for _, file := range provenance.Files {
		drift.Files = append(drift.Files, FileDrift{File: file})
	}

	if drift.Register == "" {
		drift.Unreadable = "the bundle does not record which register it came from"
		return drift, nil
	}
	if !services.Enabled() {
		drift.Unreadable = "this bot is not online, so the register could not be asked"
		return drift, nil
	}

	for i := range drift.Files {
		drift.Files[i].compare(drift.Register, services)
	}
	return drift, nil
}

// compare asks the register what it has for this file: what it says, which
// decides the verdict, and which commit last touched it, which is shown so a
// reader can see the version.
func (f *FileDrift) compare(register string, services *Services) {
	raw, err := services.FetchFile(services.rawURL(register, "HEAD", f.Name))
	if err != nil {
		f.Unreadable = fmt.Sprintf("could not read %s from %s: %s", f.Name, register, err)
		return
	}
	// Against the md5 the refresh recorded, which a test holds to the bytes
	// that are actually embedded.
	f.Differs = fmt.Sprintf("%x", md5.Sum(raw)) != f.MD5

	// The commit is for the reader, so failing to get it is not failing the
	// comparison: the verdict is already in.
	if commit, when, err := services.lastCommit(register, f.Name); err == nil {
		f.Current, f.CurrentDate = commit, when
	}
}

// --- saying it -------------------------------------------------------------

// Summary is the verdict in one line, as plain prose. Markdown adds its own
// emphasis rather than having this strip any.
func (d Drift) Summary() string {
	switch {
	case d.Behind():
		return "These are not the register's current rules." + d.caveat()
	case !d.Known():
		return "I could not check whether these are the register's current rules: " + d.Why()
	default:
		return "These are the register's current rules."
	}
}

// caveat is what a proven drift still could not establish: the bundle is
// behind either way, and there may be more to it than the report shows.
func (d Drift) caveat() string {
	if d.Known() {
		return ""
	}
	return " I could not read every file, so there may be more: " + d.Why()
}

// emphasised is the verdict for a comment, where the word that matters is
// worth seeing without reading the sentence.
func (d Drift) emphasised() string {
	if d.Behind() {
		return "These are **not** the register's current rules." + d.caveat()
	}
	return d.Summary()
}

// Mark is the symbol the verdict is shown with. A register that could not be
// asked is a skip, as it is everywhere else: could not look is never fine.
func (d Drift) Mark() string { return driftMark(d.Behind(), d.Known()) }

// Mark is the symbol for one file, from what was established about that file:
// a row for a file that provably drifted says so, whatever happened to the
// others.
func (f FileDrift) Mark() string { return driftMark(f.Behind(), f.Known()) }

// driftMark puts proof first. Behind is established; Known is only the
// absence of a reason to doubt, so it cannot overrule it.
func driftMark(behind, known bool) string {
	switch {
	case behind:
		return Symbol(OutcomeWarning)
	case !known:
		return Symbol(OutcomeSkipped)
	default:
		return Symbol(OutcomeOK)
	}
}

// says is what reading the register's copy established about this file. The
// commit alone cannot say it: a revert leaves a new commit on an unchanged
// file, and an amended commit leaves the same one on a changed file.
func (f FileDrift) says() string {
	switch {
	case !f.Known():
		return "could not ask: " + escapePipes(f.Unreadable)
	case f.Differs:
		return "a different file"
	default:
		return "the same file"
	}
}

// remedy is what fixes a bundle that has fallen behind. Written plainly;
// the comment adds its own backticks.
const remedy = "run scripts/update-rules.sh, then commit the refreshed files and deploy."

// Text renders the comparison for a terminal.
func (d Drift) Text() string {
	var out strings.Builder
	fmt.Fprintf(&out, "rules bundled from %s, taken %s\n\n", d.Register, day(d.Retrieved))
	for _, file := range d.Files {
		fmt.Fprintf(&out, "%s %s (specification %s, %d rules)\n",
			file.Mark(), file.Name, file.SpecVersion, file.Rules)
		fmt.Fprintf(&out, "    bundled %s (%s)\n", build.Shorten(file.Commit), day(file.CommitDate))
		if file.Current != "" {
			fmt.Fprintf(&out, "    register %s (%s)\n",
				build.Shorten(file.Current), onDay(file.CurrentDate))
		}
		fmt.Fprintf(&out, "    %s\n", file.says())
	}
	fmt.Fprintf(&out, "\n%s\n", d.Summary())
	if d.Behind() {
		fmt.Fprintf(&out, "to catch up, %s\n", remedy)
	}
	return out.String()
}

// Markdown renders the comparison as the comment the bot posts.
func (d Drift) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s %s\n\n", d.Mark(), d.emphasised())
	fmt.Fprintf(&out, "I judge every `codecheck.yml` by the rules bundled into this build, "+
		"taken from [`%s`](https://github.com/%s/blob/master/RULES.md) on %s.\n\n",
		d.Register, d.Register, day(d.Retrieved))

	out.WriteString("| | Rules | Bundled | The register now |\n|---|---|---|---|\n")
	for _, file := range d.Files {
		current := file.says()
		if file.Current != "" {
			current = fmt.Sprintf("`%s` (%s), %s",
				build.Shorten(file.Current), onDay(file.CurrentDate), current)
		}
		fmt.Fprintf(&out, "| %s | `%s`, specification %s, %d rules | `%s` (%s) | %s |\n",
			file.Mark(), file.Name, file.SpecVersion, file.Rules,
			build.Shorten(file.Commit), day(file.CommitDate), current)
	}
	if d.Behind() {
		out.WriteString("\nTo catch up: run `scripts/update-rules.sh`, " +
			"then commit the refreshed files and deploy.\n")
	}
	return out.String()
}

// onDay is a time as a reply writes one, to the day.
func onDay(when time.Time) string { return when.UTC().Format(time.DateOnly) }

// day is a timestamp provenance.json wrote, as a reply writes one. To the
// day, because the commit already identifies the version: the date is there
// so that a reader can see how old the bundle is.
func day(timestamp string) string {
	if when, ok := parseTimestamp(timestamp); ok {
		return onDay(when)
	}
	return timestamp
}
