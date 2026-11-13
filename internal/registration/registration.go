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
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/codecheckers/chekhov/internal/check"
)

// columns are the columns of codecheckers.csv, in its order.
var columns = []string{"name", "handle", "ORCID", "contact", "fields", "languages",
	"ecr_until", "ecr_checked", "fediverse"}

// SeeORCID is what `contact` says when the address is on the ORCID record.
const SeeORCID = "see ORCID page"

// pointsToORCID reports whether a contact sends the reader to the ORCID
// record and nowhere else, however it is put: `see ORCID page`, `see ORCID`,
// `ORCID profile`, a link to orcid.org. Such a contact reaches somebody only
// when the record shows an address, so it is checked as `see ORCID page` is
// (codecheckers/codecheckers#98 wrote `see ORCID`, and was let through).
// linked are the iDs it names, in a link or bare, each once.
//
// It is decided word by word, and a single word that is neither ORCID, an
// iD, a link to orcid.org, nor one of the words people point with makes the
// contact the registrant's own, taken as written: an address, spelt out or
// not, or a page elsewhere, even when ORCID is named beside it.
func pointsToORCID(contact string) (points bool, linked []string) {
	for _, word := range contactWords(contact) {
		word = strings.ToLower(bare(word))
		switch {
		case word == "orcid" || word == "orcid's":
			points = true
		case pointingWords[word]:
		case word != "" && strings.EqualFold(check.ORCIDDigits(word), word):
			points, linked = true, appendNew(linked, strings.ToUpper(word))
		case slices.Contains([]string{"orcid.org", "www.orcid.org"}, hostOf(word)):
			points = true
			if id := check.ORCIDDigits(word); id != "" {
				linked = appendNew(linked, id)
			}
		default:
			return false, nil
		}
	}
	return points, linked
}

func appendNew(list []string, item string) []string {
	if slices.Contains(list, item) {
		return list
	}
	return append(list, item)
}

// contactWords are the words of a contact, split where a sentence or a list
// would split them - and around the angle brackets of `Name <address>` - with
// whatever punctuation ends them still on: what a word is, is decided as
// written, and bare takes the punctuation off where that matters.
func contactWords(contact string) []string {
	return strings.FieldsFunc(contact, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",;<>()[]\"'", r)
	})
}

// bare is a word without the punctuation a sentence ends it with.
func bare(word string) string { return strings.TrimRight(word, ".:!?") }

// hostOf is the host a word names as a link, with or without its scheme, in
// lower case; empty for a word that is not one.
func hostOf(word string) string {
	if !strings.Contains(word, "://") {
		word = "https://" + word
	}
	link, err := url.Parse(word)
	if err != nil {
		return ""
	}
	return strings.ToLower(link.Hostname())
}

// dotted reports whether a domain or a host is one on the internet: a dot in
// it, and none at either end or doubled.
func dotted(domain string) bool {
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".") &&
		!strings.Contains(domain, "..")
}

// pointingWords are the words a contact points to ORCID with, around the
// word ORCID itself.
var pointingWords = map[string]bool{
	"see": true, "my": true, "the": true, "on": true, "at": true, "in": true, "via": true, "please": true,
	"page": true, "profile": true, "record": true, "id": true, "email": true, "is": true, "listed": true,
}

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

// Cells is the row by the list's column names, which is how the pull request
// adding it writes it: in the order of the file's own header, whatever that is.
func (r Row) Cells() map[string]string {
	values := []string{r.Name, r.Handle, r.ORCID, r.Contact, r.Fields, r.Languages,
		r.ECRUntil, r.ECRChecked, r.Fediverse}
	cells := make(map[string]string, len(columns))
	for i, column := range columns {
		cells[column] = values[i]
	}
	return cells
}

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
	// listed marks the one failure that is not the registration's to fix:
	// the person is on the list already - which, after `include`, is what a
	// merged pull request looks like.
	listed bool
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

// A SurveyQuestion is one of the registration template's optional questions,
// each written as normalise writes it.
type SurveyQuestion struct {
	// Topic is what the question asks about, as the reminder names it.
	Topic       string
	question    string // how the question line starts
	placeholder string // the answer line the template ships with
}

// surveyQuestions are the template's three, in its order.
var surveyQuestions = []SurveyQuestion{
	{"how you heard about CODECHECK", "howdidyouhearaboutcodecheck", "i..."},
	{"what motivates you", "whatisyourmotivation", "mymotivationis..."},
	{"how much time you could spend", "howmuchtimewouldyou", "about/atleast/notmorethan..."},
}

// SurveyUnanswered is the template's optional questions whose answer is
// still the template's placeholder and nothing else: what is under the
// question, up to the next one, ignoring blank lines. One such is enough.
//
// The placeholder is recognised in whatever markup it was left in or given -
// emphasis of either kind or none, a quote, an ellipsis character, other
// spacing or case - and only under its own question, so a line like it
// elsewhere in the post means nothing. A question deleted is the template's
// own way of declining ("PLEASE DELETE IF YOU PREFER NOT TO ANSWER"), and an
// answer written beside a placeholder left in place is an answer.
func SurveyUnanswered(body string) []SurveyQuestion {
	// What GitHub does not show is not asked about: a comment, and everything
	// after one that is never closed.
	body = unclosed.ReplaceAllString(comments.ReplaceAllString(body, ""), "")
	var unanswered []SurveyQuestion
	asked, onlyPlaceholder := -1, false
	settle := func() {
		if asked >= 0 && onlyPlaceholder {
			unanswered = append(unanswered, surveyQuestions[asked])
		}
	}
	for line := range strings.Lines(body) {
		line = normalise(line)
		if next := questionOf(line); next >= 0 {
			settle()
			asked, onlyPlaceholder = next, false
			continue
		}
		switch {
		case asked < 0 || line == "":
		case line == surveyQuestions[asked].placeholder:
			onlyPlaceholder = true
		default:
			// An answer, or the post going on after the survey: either way
			// the question is not left with only its placeholder.
			asked = -1
		}
	}
	settle()
	return unanswered
}

// questionOf is which of the template's questions a normalised line is, -1
// for none.
func questionOf(line string) int {
	return slices.IndexFunc(surveyQuestions, func(q SurveyQuestion) bool { return strings.HasPrefix(line, q.question) })
}

// normalise is a line without its markup, spacing and case, every ellipsis
// written as three dots, to be compared with surveyQuestions.
func normalise(line string) string {
	line = strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune("_*~`>", r), unicode.IsSpace(r):
			return -1
		case r == '…':
			return '.'
		}
		return unicode.ToLower(r)
	}, line)
	return dots.ReplaceAllString(line, "...")
}

var (
	comments = regexp.MustCompile(`(?s)<!--.*?-->`)
	unclosed = regexp.MustCompile(`(?s)<!--.*`)
	dots     = regexp.MustCompile(`\.+`)
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
	return append(findings, contactForm(row.Contact)...)
}

// contactForm checks what in a contact is written as an address or a link,
// and leaves everything else as written: an address spelt out in words, kept
// from the spammers on purpose, has no form to check, and a handle - a word
// that starts with its @ - is somebody's account, not an address.
//
// What is wrong is said without quoting it: a contact is the registrant's to
// publish, and a reply on the issue is public.
func contactForm(contact string) []Finding {
	var badAddress, badLink bool
	for _, word := range contactWords(contact) {
		// Classified as written, read bare: `www.` is a link with nothing
		// after it, not the word www.
		lower, at := strings.ToLower(word), strings.Index(word, "@")
		switch {
		case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
			strings.HasPrefix(lower, "www.") || at > 0 && strings.Contains(word[:at], "/"):
			// A page with an @ in its path - `fediscience.org/@name` - is a
			// link written without its scheme, not an address.
			badLink = badLink || !dotted(hostOf(bare(word)))
		case at == 0:
		case at > 0:
			badAddress = badAddress || !addressReadable(bare(word))
		}
	}
	var findings []Finding
	if badAddress {
		findings = append(findings, failed("An email address in the `contact` column is not written as one "+
			"I can read: a part missing on either side of the @, a second @, or a domain without a dot. "+
			"Please check it."))
	}
	if badLink {
		findings = append(findings, failed("A link in the `contact` column has no web address I can read. "+
			"Please check it."))
	}
	return findings
}

// addressReadable reports whether a word is one email address, with a domain
// on the internet - net/mail alone lets `name@localhost` go - once a label
// stuck to its front (`mailto:`, `email:`) and a query on its end
// (`?subject=…`) are off.
func addressReadable(word string) bool {
	at := strings.Index(word, "@")
	if label := strings.LastIndex(word[:at], ":"); label >= 0 {
		word = word[label+1:]
	}
	word, _, _ = strings.Cut(word, "?")
	address, err := mail.ParseAddress(word)
	return err == nil && address.Address == word && dotted(word[strings.LastIndex(word, "@")+1:])
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
