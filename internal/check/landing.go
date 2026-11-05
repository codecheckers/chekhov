package check

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// A Published certificate is what the register's landing page for it says:
// index.json, next to the page, which the register renders from the
// codecheck.yml it was given. `announce` composes a toot from it, and
// `check finish` holds it against the configuration in hand.
type Published struct {
	ID             string
	PaperTitle     string
	PaperReference string
	Report         string
	Venue          string
	Authors        []Person
	Codecheckers   []Person
}

// PublishedAt reads the certificate published at a landing page, a URL ending
// in a slash. A page that is not there answers IsNotFound.
func (s *Services) PublishedAt(page string) (Published, error) {
	raw, err := s.FetchFile(page + "index.json")
	if err != nil {
		return Published{}, err
	}
	var published struct {
		Certificate struct {
			ID string `json:"id"`
		} `json:"certificate"`
		Paper struct {
			Title     string   `json:"title"`
			Authors   []Person `json:"authors"`
			Reference string   `json:"reference"`
		} `json:"paper"`
		Codecheck struct {
			Codecheckers []Person `json:"codecheckers"`
			Report       string   `json:"report"`
			Venue        string   `json:"venue"`
		} `json:"codecheck"`
	}
	if err := json.Unmarshal(raw, &published); err != nil {
		return Published{}, fmt.Errorf("%sindex.json could not be read: %w", page, err)
	}
	return Published{
		ID:             strings.TrimSpace(published.Certificate.ID),
		PaperTitle:     strings.Join(strings.Fields(published.Paper.Title), " "),
		PaperReference: strings.TrimSpace(published.Paper.Reference),
		Report:         strings.TrimSpace(published.Codecheck.Report),
		Venue:          strings.TrimSpace(published.Codecheck.Venue),
		Authors:        published.Paper.Authors,
		Codecheckers:   published.Codecheck.Codecheckers,
	}, nil
}

// A Difference is one thing the landing page says otherwise than the
// configuration: what it is about, and what each of them says.
type Difference struct {
	What, Page, File string
}

// Differences are what the landing page says otherwise than the configuration
// does, about the few things a reader of the register looks for first: which
// certificate, which paper, which report, who checked it. Empty when they
// agree.
//
// Compared as a reader would: a title by its words, a reference and a report
// by the DOI in them when there is one, a codechecker by name and, where both
// give one, by ORCID. The page is rendered from a codecheck.yml, so anything
// else means it was rendered from an earlier one - or for another check.
func (p Published) Differences(c Config) []Difference {
	var differ []Difference
	note := func(what, page, file string) {
		differ = append(differ, Difference{What: what, Page: page, File: file})
	}
	if p.ID != strings.TrimSpace(c.Certificate) {
		note("the certificate", p.ID, c.Certificate)
	}
	if Normalise(p.PaperTitle) != Normalise(c.Paper.Title) {
		note("the paper's title", p.PaperTitle, c.Paper.Title)
	}
	if !sameReference(p.PaperReference, c.Paper.Reference) {
		note("the paper's reference", p.PaperReference, c.Paper.Reference)
	}
	if !sameReference(p.Report, c.Report) {
		note("the report", p.Report, c.Report)
	}
	if !samePeople(p.Codecheckers, c.Codechecker) {
		note("the codecheckers", names(p.Codecheckers), names(c.Codechecker))
	}
	return differ
}

// sameReference compares two references by their DOI when both have one, and
// as written otherwise, give or take a trailing slash and the case of the
// scheme and host.
func sameReference(a, b string) bool {
	if DOIIn(a) != "" || DOIIn(b) != "" {
		return sameDOI(a, b)
	}
	trim := func(s string) string { return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "/")) }
	return trim(a) == trim(b)
}

// samePeople is the same codecheckers in the same order: the certificate
// lists them as the file does.
func samePeople(a, b []Person) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if Normalise(a[i].Name) != Normalise(b[i].Name) {
			return false
		}
		orcidA, orcidB := ORCIDDigits(a[i].ORCID), ORCIDDigits(b[i].ORCID)
		if orcidA != "" && orcidB != "" && orcidA != orcidB {
			return false
		}
	}
	return true
}

func names(people []Person) string {
	written := make([]string, 0, len(people))
	for _, person := range people {
		written = append(written, strings.TrimSpace(person.Name))
	}
	return strings.Join(written, ", ")
}

// PageExists is whether a page answers at all, for a page with no index.json:
// a certificate rendered before the register wrote one is still published.
func (s *Services) PageExists(page string) (bool, error) {
	status, err := s.exists(page)
	if err != nil {
		return false, err
	}
	return status == http.StatusOK, nil
}

// RowOfCertificate is the register's row for a certificate, and whether there
// is one: whether it has been registered. For `check finish` and for a target
// written as an identifier; the register rules look rows up themselves,
// because they also judge a row that is not in the file yet.
func (s *Services) RowOfCertificate(certificate string) (RegisterRow, bool, error) {
	if !s.Enabled() {
		return RegisterRow{}, false, fmt.Errorf("looking %s up needs the register", certificate)
	}
	register, err := s.registerCSV()
	if err != nil {
		return RegisterRow{}, false, fmt.Errorf("could not read the register: %w", err)
	}
	row, found := register.entryFor(strings.TrimSpace(certificate))
	return row, found, nil
}
