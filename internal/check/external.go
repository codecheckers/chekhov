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
		return fail(fmt.Sprintf("'%s' is not a resolvable URL", report)).at(c.Config.Line("report"))
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-CFG-012"])
	}
	return c.Services.resolves(report).at(c.Config.Line("report"))
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
	return c.Services.resolves(reference).at(c.Config.Line("paper.reference"))
}

// rule: CC-MET-009 reference-other-resolves
func referenceOtherResolves(c Context) Result {
	if !c.Config.Paper.ReferenceOtherIsList || len(c.Config.Paper.ReferenceOther) == 0 {
		return skip("no reference-other sequence")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-MET-009"])
	}

	var urls, paths []string
	for i, entry := range c.Config.Paper.ReferenceOther {
		entry = strings.TrimSpace(entry)
		if !bareURL.MatchString(entry) {
			// Not a URL is a finding about the form, CC-CFG-030's to report.
			continue
		}
		urls = append(urls, entry)
		paths = append(paths, referenceOtherPath(i))
	}
	if len(urls) == 0 {
		return skip("no reference-other entry is a URL, so there is nothing to resolve")
	}

	// The R package differs, deliberately not followed: its url_resolves fails
	// on a 403, 429 or 5xx, it passes when some entries could not be reached
	// as long as none failed, and it lists the URLs without their reasons.
	resolution := c.Services.resolveAll(urls)
	var lines []int
	for _, i := range resolution.failed {
		lines = append(lines, c.Config.Line(paths[i]))
	}
	return resolution.verdict("reference-other entries that do not resolve", lines,
		fmt.Sprintf("%d entry(s) resolve", resolution.resolved))
}

// rule: CC-BUN-004 repository-reachable
func repositoryReachable(c Context) Result {
	if len(c.Config.Repository) == 0 {
		return skip("no repository recorded")
	}
	if !c.Services.Enabled() {
		return needsServices(RequiresService["CC-BUN-004"])
	}

	// A repository that answered 403 or 503 used to pass: only a 404 was
	// counted against it (chekhov#11).
	return c.Services.resolveAll(c.Config.Repository).verdict("repository(s) that do not answer",
		[]int{c.Config.Line("repository")}, strings.Join(c.Config.Repository, ", "))
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
	var lines []int
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
			lines = append(lines, c.Config.Line(item.path+".file"))
		}
	}
	if len(missing) > 0 {
		return fail("manifest file(s) not where the configuration says: " + strings.Join(missing, ", ")).
			at(lines...)
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
	var lines []int
	for _, person := range people {
		if _, err := orcidRecord(c, person.ORCID); err != nil {
			unresolved = append(unresolved, person.ORCID)
			lines = append(lines, c.Config.Line(person.orcidPath()))
		}
	}
	if len(unresolved) > 0 {
		return fail("ORCID(s) that do not resolve: " + strings.Join(unresolved, ", ")).at(lines...)
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
	var lines []int
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
			lines = append(lines, c.Config.Line(person.path+".name"))
		}
	}
	if compared == 0 {
		return skip("no ORCID record could be read")
	}
	if len(mismatched) > 0 {
		return fail("name(s) that differ from the ORCID record: " +
			strings.Join(mismatched, "; ")).at(lines...)
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

// Work is what a metadata source says about a DOI.
//
// One shape whichever source answered, so that the rules compare the same
// fields either way and only one place knows which source was asked.
type Work struct {
	Title    string
	Journal  string
	Abstract string
	// Subjects is what the paper is about, broadest first.
	Subjects []string
	Authors  []WorkAuthor
}

// A WorkAuthor is one author as a metadata source names them.
type WorkAuthor struct {
	Name string
	// Family is the surname, which is what CC-MET-007 compares: Crossref
	// gives it, OpenAlex gives one display name and it has to be taken from
	// the end of it.
	Family string
	ORCID  string
}

// Work asks the configured source what a paper is.
//
// The one door: the rules reach it through workFor and the commands call it
// directly, so a deployment that switches source switches everything at once
// and nothing asks the other one by accident.
func (s *Services) Work(doi string) (Work, error) {
	doi = DOIIn(doi)
	if doi == "" {
		return Work{}, fmt.Errorf("that is not a DOI")
	}
	if !s.Enabled() {
		return Work{}, fmt.Errorf("asking %s needs the external services", s.MetadataSource())
	}

	if s.crossref() {
		work, err := s.crossrefWorkOf(doi)
		if err != nil {
			return Work{}, err
		}
		return work.work(), nil
	}
	work, err := s.openAlexWorkOf(doi)
	if err != nil {
		return Work{}, err
	}
	return work.work(), nil
}

// MetadataSource is where the bot asks what a paper is, named as a reply or a
// report should credit it: OpenAlex unless a deployment says otherwise.
//
// The default lives here rather than at each call, so that a Services built by
// hand - a test, a command line run - behaves like a deployment.
func (s *Services) MetadataSource() string {
	if s.crossref() {
		return "Crossref"
	}
	return "OpenAlex"
}

// crossref reports whether this deployment asked for the old source. Matched
// however it was written, because config.Load validates what a settings file
// says but a Services built by hand - a test, a command - goes around it.
func (s *Services) crossref() bool {
	return s != nil && strings.EqualFold(strings.TrimSpace(s.Metadata), "crossref")
}

// work is the described paper, in the terms every caller speaks.
func (w crossrefWork) work() Work {
	described := Work{
		Abstract: w.Message.Abstract,
		Subjects: w.Message.Subject,
	}
	if len(w.Message.Title) > 0 {
		described.Title = w.Message.Title[0]
	}
	if len(w.Message.ContainerTitle) > 0 {
		described.Journal = w.Message.ContainerTitle[0]
	}
	for _, author := range w.Message.Author {
		described.Authors = append(described.Authors, WorkAuthor{
			Name:   strings.TrimSpace(author.Given + " " + author.Family),
			Family: author.Family,
			ORCID:  ORCIDDigits(author.ORCID),
		})
	}
	return described
}

// rule: CC-MET-005 paper-title-match
//
// CC-MET-005 to 008 read whichever source config/settings names, OpenAlex by
// default; codecheckers/register#220 took the vendor out of their names for
// that reason. The `codecheck` R package still asks Crossref for the same four
// - a deliberate difference, with codecheckers/codecheck#92 open to close it.
func paperTitleMatch(c Context) Result {
	work, result := workFor(c, "CC-MET-005")
	if result != nil {
		return *result
	}
	source := c.Services.MetadataSource()
	if work.Title == "" {
		return skip(source + " records no title for this DOI")
	}
	if Normalise(work.Title) == Normalise(c.Config.Paper.Title) {
		return pass("")
	}
	return fail(fmt.Sprintf("the title is '%s' but %s says '%s'",
		c.Config.Paper.Title, source, work.Title)).at(c.Config.Line("paper.title"))
}

// rule: CC-MET-006 paper-author-count-match
func paperAuthorCountMatch(c Context) Result {
	work, result := workFor(c, "CC-MET-006")
	if result != nil {
		return *result
	}
	source := c.Services.MetadataSource()
	if len(work.Authors) == 0 {
		return skip(source + " records no authors for this DOI")
	}
	if len(work.Authors) == len(c.Config.Paper.Authors) {
		return pass(fmt.Sprintf("%d author(s)", len(work.Authors)))
	}
	return fail(fmt.Sprintf("the file lists %d author(s), %s %d",
		len(c.Config.Paper.Authors), source, len(work.Authors))).at(c.Config.Line("paper.authors"))
}

// rule: CC-MET-007 paper-author-name-match
func paperAuthorNameMatch(c Context) Result {
	work, result := workFor(c, "CC-MET-007")
	if result != nil {
		return *result
	}
	source := c.Services.MetadataSource()
	if len(work.Authors) == 0 {
		return skip(source + " records no authors for this DOI")
	}

	names := normaliseAll(c.Config.Paper.Authors)
	var missing []string
	for _, author := range work.Authors {
		if author.Family == "" {
			continue
		}
		if !namedIn(names, author) {
			missing = append(missing, author.Name)
		}
	}
	if len(missing) > 0 {
		return fail(fmt.Sprintf("author(s) %s lists but the file does not: %s",
			source, strings.Join(missing, ", "))).at(c.Config.Line("paper.authors"))
	}
	return pass("")
}

// namedIn reports whether the file lists an author the source names.
//
// Three ways, because the surname is a guess when the source gives one name
// and it has to be taken from the end of it: OpenAlex's "Josiah Carberry Jr."
// has the surname "Jr.", and a file that correctly says "Josiah Carberry"
// must not be reported as missing an author it lists. The whole name either
// way round catches that, and the middle name the file leaves out.
func namedIn(names []string, author WorkAuthor) bool {
	whole := Normalise(author.Name)
	family := Normalise(author.Family)
	for _, name := range names {
		switch {
		case family != "" && strings.Contains(name, family):
			return true
		case whole != "" && (strings.Contains(name, whole) || strings.Contains(whole, name)):
			return true
		}
	}
	return false
}

// rule: CC-MET-008 paper-author-orcid-match
func paperAuthorORCIDMatch(c Context) Result {
	work, result := workFor(c, "CC-MET-008")
	if result != nil {
		return *result
	}
	source := c.Services.MetadataSource()

	inFile := map[string]bool{}
	for _, author := range c.Config.Paper.Authors {
		if author.ORCID != "" {
			inFile[ORCIDDigits(author.ORCID)] = true
		}
	}

	var missing []string
	compared := 0
	for _, author := range work.Authors {
		if author.ORCID == "" {
			continue
		}
		compared++
		if !inFile[author.ORCID] {
			missing = append(missing, author.Name+" ("+author.ORCID+")")
		}
	}
	if compared == 0 {
		return skip(source + " records no author ORCID for this DOI")
	}
	if len(missing) > 0 {
		return fail(fmt.Sprintf("ORCID(s) %s has but the file does not: %s",
			source, strings.Join(missing, ", "))).at(c.Config.Line("paper.authors"))
	}
	return pass(fmt.Sprintf("%d ORCID(s) match", compared))
}

// workFor fetches the paper once, and returns the result the caller should
// report instead when there is nothing to compare against.
func workFor(c Context, rule string) (Work, *Result) {
	doi := DOIIn(c.Config.Paper.Reference)
	if doi == "" {
		result := skip("the paper reference is not a DOI")
		return Work{}, &result
	}
	if !c.Services.Enabled() {
		result := needsServices(RequiresService[rule])
		return Work{}, &result
	}

	work, err := c.Services.Work(doi)
	if err != nil {
		result := skip(err.Error())
		return Work{}, &result
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
