package check

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Checks that need the outside world. Each one skips with what it would have
// needed when the services are not enabled, so a run without network says "not
// checked" rather than "fine".

// --- things that simply have to resolve ------------------------------------

// rule: CC-CFG-012 report-resolvable
func reportResolvable(c Context) Result {
	report := strings.TrimSpace(c.Config.Report)
	if report == "" {
		return skip("no report identifier to resolve")
	}
	if !strings.HasPrefix(report, "http") {
		return fail(fmt.Sprintf("'%s' is not a resolvable URL", report))
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-CFG-012"])
	}
	return c.Services.resolves(report)
}

// rule: CC-MET-004 paper-reference-resolves
func paperReferenceResolves(c Context) Result {
	reference := strings.TrimSpace(c.Config.Paper.Reference)
	if reference == "" {
		return skip("no reference to resolve")
	}
	if !bareURL.MatchString(reference) {
		return skip("the reference is not a URL, so there is nothing to resolve")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-MET-004"])
	}
	return c.Services.resolves(reference)
}

// rule: CC-MET-009 reference-other-resolves
func referenceOtherResolves(c Context) Result {
	if !c.Config.Paper.ReferenceOtherIsList || len(c.Config.Paper.ReferenceOther) == 0 {
		return skip("no reference-other sequence")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-MET-009"])
	}

	var unresolved []string
	reached := 0
	for _, entry := range c.Config.Paper.ReferenceOther {
		entry = strings.TrimSpace(entry)
		if !bareURL.MatchString(entry) {
			continue
		}
		switch result := c.Services.resolves(entry); result.Status {
		case StatusFail:
			unresolved = append(unresolved, entry)
			reached++
		case StatusPass:
			reached++
		}
	}
	if reached == 0 {
		return skip("could not reach any reference-other entry")
	}
	if len(unresolved) > 0 {
		return fail("reference-other entries that do not resolve: " +
			strings.Join(unresolved, ", "))
	}
	return pass(fmt.Sprintf("%d entry(s) resolve", reached))
}

// rule: CC-BUN-004 repository-reachable
func repositoryReachable(c Context) Result {
	if len(c.Config.Repository) == 0 {
		return skip("no repository recorded")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-BUN-004"])
	}

	var unreachable []string
	for _, repository := range c.Config.Repository {
		if result := c.Services.resolves(repository); result.Status == StatusFail {
			unreachable = append(unreachable, repository)
		}
	}
	if len(unreachable) > 0 {
		return fail("repository(s) that do not answer: " + strings.Join(unreachable, ", "))
	}
	return pass(strings.Join(c.Config.Repository, ", "))
}

// rule: CC-BUN-001 manifest-files-exist
//
// The specification is explicit about where to look: "The relative paths MUST
// be relative to the location of the codecheck.yml." A copy of an output under
// codecheck/outputs/ is what the R package archives after a check, not where
// the manifest says the file is, so it does not count here.
func manifestFilesExist(c Context) Result {
	if len(c.Config.Manifest) == 0 {
		return skip("no manifest to inspect")
	}
	if c.Bundle == nil {
		return skip("no bundle to look in")
	}

	var missing []string
	for _, item := range c.Config.Manifest {
		if item.File == "" {
			continue
		}
		found, err := c.Bundle.Exists(item.File)
		if err != nil {
			if errors.Is(err, errNoServices) {
				return needsServices("the bundle under check")
			}
			return skip(fmt.Sprintf("could not look in %s: %s", c.Bundle.Describe(), err))
		}
		if !found {
			missing = append(missing, item.File)
		}
	}
	if len(missing) > 0 {
		return fail("manifest file(s) not where the configuration says: " + strings.Join(missing, ", "))
	}
	return pass(fmt.Sprintf("%d manifest file(s) present", len(c.Config.Manifest)))
}

// --- ORCID -----------------------------------------------------------------

type orcidPerson struct {
	Name struct {
		GivenNames struct {
			Value string `json:"value"`
		} `json:"given-names"`
		FamilyName struct {
			Value string `json:"value"`
		} `json:"family-name"`
	} `json:"name"`
}

// rule: CC-MET-002 orcid-resolves
func orcidResolves(c Context) Result {
	people := everyone(c)
	if len(people) == 0 {
		return skip("no ORCID to resolve")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-MET-002"])
	}

	var unresolved []string
	for _, person := range people {
		if _, err := orcidRecord(c, person.ORCID); err != nil {
			unresolved = append(unresolved, person.ORCID)
		}
	}
	if len(unresolved) > 0 {
		return fail("ORCID(s) that do not resolve: " + strings.Join(unresolved, ", "))
	}
	return pass(fmt.Sprintf("%d ORCID(s) resolve", len(people)))
}

// rule: CC-MET-003 orcid-name-match
func orcidNameMatch(c Context) Result {
	people := everyone(c)
	if len(people) == 0 {
		return skip("no ORCID to compare a name against")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-MET-003"])
	}

	var mismatched []string
	compared := 0
	for _, person := range people {
		record, err := orcidRecord(c, person.ORCID)
		if err != nil {
			continue
		}
		compared++
		family := record.Name.FamilyName.Value
		// The family name is the part that survives initials, transliteration
		// and the order names are written in, so it is what is compared.
		if family != "" && !strings.Contains(Normalise(person.Name), Normalise(family)) {
			mismatched = append(mismatched, fmt.Sprintf("%s is %s %s at ORCID",
				person.Name, record.Name.GivenNames.Value, family))
		}
	}
	if compared == 0 {
		return skip("no ORCID record could be read")
	}
	if len(mismatched) > 0 {
		return fail("name(s) that differ from the ORCID record: " +
			strings.Join(mismatched, "; "))
	}
	return pass(fmt.Sprintf("%d name(s) match", compared))
}

func orcidRecord(c Context, orcid string) (orcidPerson, error) {
	var person orcidPerson
	url := c.Services.ORCID + "/" + orcid + "/person"
	err := c.Services.getJSON(url, nil, &person)
	return person, err
}

// --- Crossref --------------------------------------------------------------

type crossrefWork struct {
	Message struct {
		Title  []string `json:"title"`
		Author []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
			ORCID  string `json:"ORCID"`
		} `json:"author"`
		// Subject, Abstract and ContainerTitle say what the paper is about.
		// No rule reads them - they are what `suggest codecheckers` matches a
		// codechecker's declared fields against, see internal/suggest.
		Subject        []string `json:"subject"`
		Abstract       string   `json:"abstract"`
		ContainerTitle []string `json:"container-title"`
	} `json:"message"`
}

// Work is what Crossref says about a DOI, in the terms a caller outside this
// package speaks: the rules compare the fields of crossrefWork, everybody else
// wants the paper described.
type Work struct {
	Title    string
	Journal  string
	Abstract string
	Subjects []string
	Authors  []Person
}

// CrossrefWork asks Crossref about one DOI.
//
// Exported for the commands that are about the paper rather than about a rule;
// the rules reach the same response through crossrefWorkFor, so Crossref is
// still asked in one place and answered out of one cache.
func (s *Services) CrossrefWork(doi string) (Work, error) {
	doi = DOIIn(doi)
	if doi == "" {
		return Work{}, fmt.Errorf("that is not a DOI")
	}
	if !s.Enabled() {
		return Work{}, fmt.Errorf("asking Crossref needs the external services")
	}

	work, err := s.crossrefWorkOf(doi)
	if err != nil {
		return Work{}, err
	}

	described := Work{
		Abstract: work.Message.Abstract,
		Subjects: work.Message.Subject,
	}
	if len(work.Message.Title) > 0 {
		described.Title = work.Message.Title[0]
	}
	if len(work.Message.ContainerTitle) > 0 {
		described.Journal = work.Message.ContainerTitle[0]
	}
	for _, author := range work.Message.Author {
		described.Authors = append(described.Authors, Person{
			Name:  strings.TrimSpace(author.Given + " " + author.Family),
			ORCID: ORCIDDigits(author.ORCID),
		})
	}
	return described, nil
}

// rule: CC-MET-005 crossref-title-match
func crossrefTitleMatch(c Context) Result {
	work, result := crossrefWorkFor(c, "CC-MET-005")
	if result != nil {
		return *result
	}
	if len(work.Message.Title) == 0 {
		return skip("Crossref records no title for this DOI")
	}
	if Normalise(work.Message.Title[0]) == Normalise(c.Config.Paper.Title) {
		return pass("")
	}
	return fail(fmt.Sprintf("the title is '%s' but Crossref says '%s'",
		c.Config.Paper.Title, work.Message.Title[0]))
}

// rule: CC-MET-006 crossref-author-count-match
func crossrefAuthorCountMatch(c Context) Result {
	work, result := crossrefWorkFor(c, "CC-MET-006")
	if result != nil {
		return *result
	}
	if len(work.Message.Author) == 0 {
		return skip("Crossref records no authors for this DOI")
	}
	if len(work.Message.Author) == len(c.Config.Paper.Authors) {
		return pass(fmt.Sprintf("%d author(s)", len(work.Message.Author)))
	}
	return fail(fmt.Sprintf("the file lists %d author(s), Crossref %d",
		len(c.Config.Paper.Authors), len(work.Message.Author)))
}

// rule: CC-MET-007 crossref-author-name-match
func crossrefAuthorNameMatch(c Context) Result {
	work, result := crossrefWorkFor(c, "CC-MET-007")
	if result != nil {
		return *result
	}
	if len(work.Message.Author) == 0 {
		return skip("Crossref records no authors for this DOI")
	}

	names := normaliseAll(c.Config.Paper.Authors)
	var missing []string
	for _, author := range work.Message.Author {
		if author.Family == "" {
			continue
		}
		found := false
		for _, name := range names {
			if strings.Contains(name, Normalise(author.Family)) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, strings.TrimSpace(author.Given+" "+author.Family))
		}
	}
	if len(missing) > 0 {
		return fail("author(s) Crossref lists but the file does not: " +
			strings.Join(missing, ", "))
	}
	return pass("")
}

// rule: CC-MET-008 crossref-author-orcid-match
func crossrefAuthorORCIDMatch(c Context) Result {
	work, result := crossrefWorkFor(c, "CC-MET-008")
	if result != nil {
		return *result
	}

	inFile := map[string]bool{}
	for _, author := range c.Config.Paper.Authors {
		if author.ORCID != "" {
			inFile[ORCIDDigits(author.ORCID)] = true
		}
	}

	var missing []string
	compared := 0
	for _, author := range work.Message.Author {
		if author.ORCID == "" {
			continue
		}
		compared++
		if !inFile[ORCIDDigits(author.ORCID)] {
			missing = append(missing, strings.TrimSpace(author.Given+" "+author.Family)+
				" ("+author.ORCID+")")
		}
	}
	if compared == 0 {
		return skip("Crossref records no author ORCID for this DOI")
	}
	if len(missing) > 0 {
		return fail("ORCID(s) Crossref has but the file does not: " +
			strings.Join(missing, ", "))
	}
	return pass(fmt.Sprintf("%d ORCID(s) match", compared))
}

// crossrefWorkFor fetches the work once, and returns the result the caller
// should report instead when there is nothing to compare against.
func crossrefWorkFor(c Context, rule string) (crossrefWork, *Result) {
	doi := DOIIn(c.Config.Paper.Reference)
	if doi == "" {
		result := skip("the paper reference is not a DOI")
		return crossrefWork{}, &result
	}
	if !c.Services.Enabled() {
		result := needsServices(RequiresService[rule])
		return crossrefWork{}, &result
	}

	work, err := c.Services.crossrefWorkOf(doi)
	if err != nil {
		result := skip(err.Error())
		return crossrefWork{}, &result
	}
	return work, nil
}

// crossrefWorkOf is the one request to Crossref, for the rules and for
// CrossrefWork alike.
func (s *Services) crossrefWorkOf(doi string) (crossrefWork, error) {
	var work crossrefWork
	if err := s.getJSON(s.Crossref+"/works/"+doi, nil, &work); err != nil {
		return crossrefWork{}, fmt.Errorf("could not ask Crossref about %s: %w", doi, err)
	}
	return work, nil
}

// --- helpers ---------------------------------------------------------------

var (
	doiInText   = regexp.MustCompile(`10\.[0-9]{4,}/[^\s"'<>]+`)
	orcidInText = regexp.MustCompile(`[0-9]{4}-[0-9]{4}-[0-9]{4}-[0-9]{3}[0-9Xx]`)
	spaces      = regexp.MustCompile(`\s+`)
	notLetters  = regexp.MustCompile(`[^\p{L}\p{N} ]+`)
)

// DOIIn pulls the DOI out of a reference, which may be a bare DOI, a
// doi.org URL, or a URL that carries one.
func DOIIn(reference string) string {
	match := doiInText.FindString(reference)
	return strings.TrimRight(match, ".,;)")
}

// ORCIDDigits pulls the ORCID out of however it was written: bare, as a URL,
// with or without a scheme. The check digit is uppercase, as ORCID writes it.
func ORCIDDigits(orcid string) string { return strings.ToUpper(orcidInText.FindString(orcid)) }

// Normalise makes two names or titles comparable: case, punctuation and
// repeated spaces differ between a codecheck.yml and a metadata record without
// meaning anything.
func Normalise(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = notLetters.ReplaceAllString(text, " ")
	return strings.TrimSpace(spaces.ReplaceAllString(text, " "))
}

func normaliseAll(people []Person) []string {
	names := make([]string, 0, len(people))
	for _, person := range people {
		names = append(names, Normalise(person.Name))
	}
	return names
}

// everyone is the people with an ORCID, authors and codecheckers alike.
func everyone(c Context) []Person {
	var people []Person
	for _, person := range append(append([]Person{}, c.Config.Paper.Authors...),
		c.Config.Codechecker...) {
		if strings.TrimSpace(person.ORCID) != "" {
			people = append(people, person)
		}
	}
	return people
}
