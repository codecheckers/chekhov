// Package suggest finds the codecheckers who could check a paper.
//
// After the first checks an editor has to find somebody: read down the
// codechecker lists, compare what people declare they work with against what
// the paper and its repository are made of, and rule out whoever cannot take
// it. The lists are maintained by the codecheckers themselves, in the register
// the bot works on; this package reads them, works out what the check is
// about, and ranks the candidates. Posting the answer is internal/bot's
// business, and mentioning anybody is the editor's: a suggestion names a
// handle, it never notifies its owner.
//
// Shaped after internal/announce: it gathers and it composes, it does not
// write anywhere.
package suggest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
)

// A Codechecker is one row of a codechecker list.
type Codechecker struct {
	Name   string
	Handle string
	ORCID  string
	// Fields and Languages are what they declared they work with, lowercased
	// and split on the comma the lists use.
	Fields    []string
	Languages []string
	// List is the URL the row came from, so a reply can say which list.
	List string
}

// Load reads every configured codechecker list.
//
// A list that cannot be read is named rather than fatal: the lists are
// separate files, and an answer from two of the three is better than no
// answer. The institutional list has no fields or languages columns, and its
// people are still candidates - they simply match on nothing, which is the
// truth about what they declared.
func Load(services *check.Services, settings *config.Settings) ([]Codechecker, []string) {
	var codecheckers []Codechecker
	var unread []string

	seen := map[string]bool{}
	lists := settings.CodecheckerLists()
	for i, table := range services.CSVs(lists...) {
		if table.Err != nil {
			unread = append(unread, lists[i])
			continue
		}
		for _, row := range table.Rows {
			handle := command.Handle(row["handle"])
			if handle == "" || seen[handle] {
				// A person on two lists is one person, and the first list
				// wins: CSVs keeps the order the settings name them in.
				continue
			}
			seen[handle] = true
			codecheckers = append(codecheckers, Codechecker{
				Name:      strings.TrimSpace(row["name"]),
				Handle:    handle,
				ORCID:     check.ORCIDDigits(firstOf(row, "ORCID", "orcid")),
				Fields:    terms(row["fields"]),
				Languages: terms(row["languages"]),
				List:      lists[i],
			})
		}
	}
	return codecheckers, unread
}

// terms splits a declared list of fields or languages. The lists write them
// comma-separated, most proficient first, and some rows carry a note in
// brackets - "R(expert)" is R.
//
// The spelling is kept as the person wrote it, because a reply shows it back:
// "R" and "MATLAB" are how they are written, and neither survives a
// lowercasing. Everything that compares terms does so case-insensitively.
func terms(written string) []string {
	var split []string
	for _, term := range strings.Split(written, ",") {
		// The brackets are the person's own prose around the term: one row
		// reads "utilitarian languages (Python, Go), systems programming (C)",
		// which the comma splits into fragments carrying half a bracket each.
		term, _, _ = strings.Cut(term, "(")
		if term = strings.Trim(strings.TrimSpace(term), "()[].;"); term != "" {
			split = append(split, strings.TrimSpace(term))
		}
	}
	return split
}

func firstOf(row map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(row[key]); value != "" {
			return value
		}
	}
	return ""
}

// Vocabulary is every term the lists use, so that a word found in an abstract
// can be matched against what people actually declare.
//
// Built from the lists rather than written down here: a language somebody
// declares is a language worth finding in a paper, and a hand-kept list of
// "known languages" would go stale the first time somebody registered with
// Julia.
type Vocabulary struct {
	Languages []string
	Fields    []string
}

// VocabularyOf collects the distinct terms of a set of candidates.
func VocabularyOf(codecheckers []Codechecker) Vocabulary {
	return Vocabulary{
		Languages: distinct(codecheckers, func(c Codechecker) []string { return c.Languages }),
		Fields:    distinct(codecheckers, func(c Codechecker) []string { return c.Fields }),
	}
}

func distinct(codecheckers []Codechecker, of func(Codechecker) []string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, codechecker := range codecheckers {
		for _, term := range of(codechecker) {
			if key := strings.ToLower(term); !seen[key] {
				seen[key] = true
				terms = append(terms, term)
			}
		}
	}
	sort.Slice(terms, func(i, j int) bool {
		return strings.ToLower(terms[i]) < strings.ToLower(terms[j])
	})
	return terms
}

// Evidence is what is known about the check a codechecker is wanted for.
type Evidence struct {
	// Languages the repository under check is written in.
	Languages []string
	// Fields are the subject areas a paper's title, abstract or Crossref
	// subject named that the lists also name - the lists' own column.
	Fields []string
	// Authors are the people who wrote the paper, by ORCID and by name, so
	// that a codechecker who is one of them can be left out.
	Authors []check.WorkAuthor
	// Sources is what was read, in words, for the reply to say where the
	// evidence came from.
	Sources []string
	// Unread is what could not be read, and why.
	Unread []string
}

// Empty reports whether there is nothing to rank on. A ranking of nothing is
// an arbitrary list of people, which is worse than saying so.
func (e Evidence) Empty() bool { return len(e.Languages) == 0 && len(e.Fields) == 0 }

// addLanguages and addTerms record what a source said, keeping the first
// spelling seen: a reply says what it matched on, and "R" is not "r".
func (e *Evidence) addLanguages(found ...string) {
	e.Languages = addTo(e.Languages, found)
}

func (e *Evidence) addFields(found ...string) {
	e.Fields = addTo(e.Fields, found)
}

func addTo(known []string, found []string) []string {
	for _, term := range found {
		if term = strings.TrimSpace(term); term != "" && !contains(known, term) {
			known = append(known, term)
		}
	}
	return known
}

func (e *Evidence) note(source string) {
	if !contains(e.Sources, source) {
		e.Sources = append(e.Sources, source)
	}
}

func (e *Evidence) couldNotRead(what string, err error) {
	e.Unread = append(e.Unread, fmt.Sprintf("%s: %s", what, err))
}

// contains compares terms the way the lists mean them, which is check's
// definition of the same word written twice.
func contains(list []string, want string) bool { return check.ContainsFold(list, want) }

// Distinct is a list of terms with the repeats taken out, keeping the first
// spelling of each and the order they arrived in.
func Distinct(terms []string) []string {
	var kept []string
	for _, term := range terms {
		if term = strings.TrimSpace(term); term != "" && !contains(kept, term) {
			kept = append(kept, term)
		}
	}
	return kept
}

// PageOf is where a person should be sent to read a list.
//
// The lists are configured as raw.githubusercontent.com URLs, because that is
// what a program reads; a reply is read by a person, who wants the file on
// GitHub, with its history and its registration instructions next to it.
func PageOf(url string) string {
	const raw = "https://raw.githubusercontent.com/"
	if !strings.HasPrefix(url, raw) {
		return url
	}
	pieces := strings.SplitN(strings.TrimPrefix(url, raw), "/", 4)
	if len(pieces) < 4 {
		return url
	}
	owner, repository, ref, path := pieces[0], pieces[1], pieces[2], pieces[3]
	return fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", owner, repository, ref, path)
}

// InstitutionOf is what the codechecker lists say one person checks for: the
// institution a row of theirs names, and whether another row of theirs names
// none.
//
// Which list is the institutional one is read from the rows, not from the
// file's name: the institutional list is the one whose rows carry an
// `institution` column, and a list picked out by a substring of its URL would
// be the wrong list the day somebody renames the file. The ordinary list has
// no such column, so a volunteer is a row that names no institution.
//
// A list that could not be read says nothing about anybody, and so does a
// handle nothing names: this answers what was found and never what was
// missing, because "could not check" must not read as "this is wrong".
//
// A second walk of the same files as Load, deliberately: Load keeps one row
// per handle and lets the first list win, which drops the very row this exists
// to find - somebody on both lists. Merging the two would mean changing what
// Load hands the ranking, which is a question for codecheckers/chekhov#24.
// The bodies are cached by Services for the life of one command, so the
// second walk is a second parse and not a second request.
func InstitutionOf(services *check.Services, settings *config.Settings,
	handle string) (institution string, alsoVolunteers bool) {
	handle = command.Handle(handle)
	if handle == "" {
		return "", false
	}
	lists := settings.CodecheckerLists()
	for _, table := range services.CSVs(lists...) {
		if table.Err != nil {
			continue
		}
		for _, row := range table.Rows {
			if command.Handle(row["handle"]) != handle {
				continue
			}
			named := firstOf(row, "institution", "Institution")
			if strings.EqualFold(named, "NA") {
				// The register writes NA for a column it has no value for,
				// and "checks for NA" is not a sentence to warn anybody with.
				named = ""
			}
			switch {
			case named == "":
				alsoVolunteers = true
			case institution == "":
				// The first list the settings name wins, as it does in Load.
				institution = named
			}
		}
	}
	return institution, alsoVolunteers
}
