// Package registration reads a codechecker's registration issue on the lists
// repository and says what is still missing before they can be listed
// (codecheckers/chekhov#49).
//
// It is the bot's half of the registration runbook in codecheckers/codecheckers
// (registration-runbook.md, steps 2 and 3): take the one CSV row out of the
// issue, put it in the list's conventions, and check it. Reading is done here;
// what the reply says is internal/command's, and what the bot then writes is
// the `include` command's.
//
// Two rules from the runbook shape everything below. A contact is never
// quoted: a reply on the issue is public, and an address somebody chose to
// give only as a page or written out in words stays that way. And nobody is
// listed without a way to reach them besides GitHub, so an ORCID that keeps
// its address private, or cannot be asked, holds the registration up rather
// than letting it through.
package registration

import (
	"encoding/csv"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
)

// columns are the columns of codecheckers.csv, in its order.
var columns = []string{"name", "handle", "ORCID", "contact", "fields", "languages",
	"ecr_until", "ecr_checked", "fediverse"}

// SeeORCID is what `contact` says when the address is on the ORCID record.
const SeeORCID = "see ORCID page"

// Row is one registration, one value per column, normalised to the list's
// conventions.
type Row struct {
	Name       string
	Handle     string // with its @
	ORCID      string // bare
	Contact    string
	Fields     string
	Languages  string
	ECRUntil   string
	ECRChecked string
	Fediverse  string // @user@instance, or empty
}

// Login is the handle without its @.
func (r Row) Login() string { return login(r.Handle) }

// Outcome is how a finding is reported, with the symbols the bot uses
// everywhere.
type Outcome string

const (
	Failed     Outcome = "✖"
	Warning    Outcome = "⚠"
	Note       Outcome = "ℹ"
	NotChecked Outcome = "…"
)

// A Finding is one thing a registration needs attention for.
type Finding struct {
	Outcome Outcome
	// Text says what was found, in a sentence addressed to the registrant.
	Text string
}

// Blocks reports whether a finding holds the registration up. A failure
// does, and so does something that could not be checked: a registration is
// complete when it is known to be, not when nothing was found against it.
func (f Finding) Blocks() bool { return f.Outcome == Failed || f.Outcome == NotChecked }

// Parse takes the row out of a registration issue's body and normalises it.
//
// The findings are about the row as written: the column count, the template
// left untouched, a value in the wrong form. A row comes back whenever one
// was found, so the checks that need the world can still run on it, and the
// zero Row when none was.
func Parse(body string) (Row, []Finding) {
	line, problem := rowIn(body)
	if problem != nil {
		return Row{}, []Finding{*problem}
	}

	reader := csv.NewReader(strings.NewReader(line))
	reader.LazyQuotes, reader.TrimLeadingSpace, reader.FieldsPerRecord = true, true, -1
	cells, err := reader.Read()
	if err != nil {
		return Row{}, []Finding{failed("I could not read the row in the `csv` block as CSV "+
			"(%s). Please check the quotes around fields and languages.", err)}
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}

	switch {
	case len(cells) == len(columns)-1:
		// Filed before the fediverse column existed: it is optional, and
		// empty is what the list writes for somebody who gave none.
		cells = append(cells, "")
	case len(cells) != len(columns):
		return Row{}, []Finding{failed("The row has %d fields, and the list has %d: %s. "+
			"Please put each in its place, empty where you have nothing to say "+
			"(and `NA` for the two ECR columns).", len(cells), len(columns), columnList())}
	}

	row := Row{
		Name:       cells[0],
		Handle:     handle(cells[1]),
		ORCID:      check.ORCIDDigits(cells[2]),
		Contact:    cells[3],
		Fields:     list(cells[4]),
		Languages:  list(cells[5]),
		ECRUntil:   cells[6],
		ECRChecked: cells[7],
		Fediverse:  fediverse(cells[8]),
	}
	return row, form(row, cells)
}

// rowIn finds the one line of data in the issue: the first non-empty line of
// a fenced block, once the template's own comments are gone - its example row
// lives in one, and would otherwise be read as the registrant's.
func rowIn(body string) (string, *Finding) {
	body = comments.ReplaceAllString(body, "")
	var lines []string
	for _, block := range fences.FindAllStringSubmatch(body, -1) {
		for _, line := range strings.Split(block[1], "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	// A header pasted above the row - the list's own, or the template's line
	// left in place with the row added under it - is not a second row.
	if len(lines) > 1 && (strings.HasPrefix(strings.ToLower(lines[0]), "name,handle,") || template.MatchString(lines[0])) {
		lines = lines[1:]
	}

	var problem Finding
	switch {
	case len(lines) == 0:
		problem = failed("I found no row of data in this issue: the template asks " +
			"for it in the `csv` block under \"Your data\".")
	case template.MatchString(lines[0]):
		problem = failed("The `csv` block still holds the template's line rather than " +
			"your details. Please replace it with your own row.")
	case len(lines) > 1:
		problem = failed("I found %d rows of data, and a registration is one row. "+
			"Please keep only yours.", len(lines))
	default:
		return lines[0], nil
	}
	return "", &problem
}

var (
	comments = regexp.MustCompile(`(?s)<!--.*?-->`)
	fences   = regexp.MustCompile("(?s)```[a-zA-Z]*[ \t]*\r?\n(.*?)```")
	// template matches the line the registration template ships with.
	template = regexp.MustCompile(`@GitHub-handle|ORCID ID \(not url\)`)
)

// form checks the values that can be judged without asking anybody.
func form(row Row, cells []string) []Finding {
	var findings []Finding
	if row.Name == "" {
		findings = append(findings, failed("The `name` column is empty."))
	}
	if row.Handle == "@" || strings.ContainsAny(row.Login(), " /") {
		findings = append(findings, failed("The `handle` column, %s, is not a GitHub handle.", code(cells[1])))
	}
	if !check.ORCIDWellFormed(row.ORCID) {
		findings = append(findings, failed("The `ORCID` column, %s, is not an ORCID iD: four groups of "+
			"four digits, the last of which may be an X, with a check digit that holds.", code(cells[2])))
	}
	if row.Fields == "" {
		findings = append(findings, warning("The `fields` column is empty, so nobody looking for a "+
			"codechecker in your field will find you."))
	}
	if row.Languages == "" {
		findings = append(findings, warning("The `languages` column is empty, so nobody looking for a "+
			"codechecker for a workflow in your languages will find you."))
	}
	if !ecrUntil.MatchString(row.ECRUntil) {
		findings = append(findings, failed("The `ecr_until` column, %s, is none of a month as "+
			"`YYYY-MM`, `open`, `expired` or `NA`.", code(row.ECRUntil)))
	}
	if !ecrChecked.MatchString(row.ECRChecked) {
		findings = append(findings, failed("The `ecr_checked` column, %s, is neither "+
			"`<YYYY-MM>;<link>` nor `NA`.", code(row.ECRChecked)))
	}
	if cells[8] != "" && row.Fediverse == "" {
		findings = append(findings, failed("The `fediverse` column, %s, is not an account as "+
			"`@user@instance`. Please write it that way, or leave it empty.", code(cells[8])))
	}
	return findings
}

var (
	ecrUntil   = regexp.MustCompile(`^(?:[0-9]{4}-(?:0[1-9]|1[0-2])|open|expired|NA)$`)
	ecrChecked = regexp.MustCompile(`^(?:NA|[0-9]{4}-(?:0[1-9]|1[0-2]);https?://\S+)$`)
	account    = regexp.MustCompile(`^@[^@\s/]+@[^@\s/]+\.[^@\s/]+$`)
)

// handle is a GitHub handle as the list writes it: one @ in front, whether
// the registrant wrote none, two, or the profile's address.
func handle(written string) string {
	written = strings.TrimPrefix(strings.TrimPrefix(written, "https://github.com/"), "http://github.com/")
	return "@" + strings.TrimLeft(strings.TrimRight(written, "/"), "@")
}

// list trims every item of a comma-separated list and drops the empty ones,
// joining them the way the registrant did - with or without a space - since
// the list has both and the spacing is theirs.
func list(written string) string {
	separator := ","
	if strings.Contains(written, ", ") {
		separator = ", "
	}
	var items []string
	for _, item := range strings.Split(written, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return strings.Join(items, separator)
}

// fediverse is an account as @user@instance, from that or from the profile's
// address; empty when it is neither, which form reports.
func fediverse(written string) string {
	if account.MatchString(written) {
		return written
	}
	if page, err := url.Parse(written); err == nil && page.Host != "" {
		if user := strings.Trim(page.Path, "/"); strings.HasPrefix(user, "@") && !strings.Contains(user, "/") {
			if candidate := user + "@" + page.Host; account.MatchString(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func columnList() string { return "`" + strings.Join(columns, ",") + "`" }

// code is command.Code, which this package cannot import: command imports it,
// for the reply. login is command.Handle without the lowercasing, because the
// row keeps the handle as GitHub spells it; every comparison here ignores case.
func code(text string) string { return "`" + strings.ReplaceAll(text, "`", "'") + "`" }

func login(handle string) string { return strings.TrimPrefix(strings.TrimSpace(handle), "@") }

func finding(outcome Outcome, format string, args ...any) Finding {
	return Finding{Outcome: outcome, Text: fmt.Sprintf(format, args...)}
}

func failed(format string, args ...any) Finding { return finding(Failed, format, args...) }

func warning(format string, args ...any) Finding { return finding(Warning, format, args...) }
