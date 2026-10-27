package check

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The certificate's archive record on Zenodo. The rules are the CODECHECK
// community curation policy written down:
// https://zenodo.org/communities/codecheck/curation-policy

type zenodoRecord struct {
	ID          int64        `json:"id"`
	DOI         string       `json:"doi"`
	ConceptDOI  string       `json:"conceptdoi"`
	ConceptRecI int64        `json:"conceptrecid,string"`
	Title       string       `json:"title"`
	Created     time.Time    `json:"created"`
	Published   string       `json:"publication_date"`
	Files       []zenodoFile `json:"files"`
	// draft says the record is an unpublished draft, read by `check zenodo`,
	// whose DOI is only reserved. Never in a response, see asRecord.
	draft    bool
	Metadata struct {
		Title string `json:"title"`
		// License is an object with an id on the current API and a plain
		// string on the older one, so it is read as it comes, see licence.
		License  json.RawMessage `json:"license"`
		Rights   []zenodoRight   `json:"rights"`
		Creators []zenodoCreator `json:"creators"`
		// What the record says in prose and in its links, read by
		// `check links`: the description is HTML, the notes plain text.
		Description          string             `json:"description"`
		Notes                string             `json:"notes"`
		RelatedIdentifiers   []zenodoIdentifier `json:"related_identifiers"`
		AlternateIdentifiers []zenodoIdentifier `json:"alternate_identifiers"`
		// References are strings on the records seen so far; read as they
		// come, so that an object in their place is walked rather than
		// failing the whole record.
		References json.RawMessage `json:"references"`
		Relations  struct {
			Version []zenodoVersion `json:"version"`
		} `json:"relations"`
	} `json:"metadata"`
}

type zenodoRight struct {
	ID string `json:"id"`
}

type zenodoCreator struct {
	Name  string `json:"name"`
	ORCID string `json:"orcid"`
}

type zenodoVersion struct {
	Index  int  `json:"index"`
	IsLast bool `json:"is_last"`
	Count  int  `json:"count"`
}

// zenodoIdentifier is one identifier a record relates itself to, with the
// scheme that says how to read it: a URL, a DOI, an ORCID.
type zenodoIdentifier struct {
	Identifier string `json:"identifier"`
	Scheme     string `json:"scheme"`
}

// licence is the licence the record states, empty when it states none.
//
// Zenodo has written it three ways over the years: metadata.license as an
// object with an id, metadata.license as a plain string, and metadata.rights
// as a list. A record deposited in 2019 answers differently from one deposited
// last month, and both are in the register.
func (r zenodoRecord) licence() string {
	var asObject struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Metadata.License, &asObject); err == nil && asObject.ID != "" {
		return asObject.ID
	}
	var asString string
	if err := json.Unmarshal(r.Metadata.License, &asString); err == nil && asString != "" {
		return asString
	}
	for _, right := range r.Metadata.Rights {
		if right.ID != "" {
			return right.ID
		}
	}
	return ""
}

// zenodoFile is one file of a record. Zenodo's newer API names it `key` and
// links it under `links.self`; the older one used `filename` and
// `links.download`, and records deposited years ago still answer that way.
type zenodoFile struct {
	Key      string `json:"key"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Links    struct {
		Self     string `json:"self"`
		Download string `json:"download"`
	} `json:"links"`
}

func (f zenodoFile) name() string {
	if f.Key != "" {
		return f.Key
	}
	return f.Filename
}

func (f zenodoFile) downloadLink() string {
	if f.Links.Download != "" {
		return f.Links.Download
	}
	return f.Links.Self
}

func (r zenodoRecord) title() string {
	if r.Metadata.Title != "" {
		return r.Metadata.Title
	}
	return r.Title
}

// rule: CC-REP-001 report-doi-version-specific
func reportDOIVersionSpecific(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-001")
	if result != nil {
		return *result
	}
	if record.draft {
		return skip(draftDOI)
	}
	// A concept DOI always resolves to whichever version is current, so a
	// certificate that names one does not point at a fixed document.
	if record.ConceptDOI != "" && sameDOI(record.ConceptDOI, c.Config.Report) {
		return fail(fmt.Sprintf("'%s' is the concept DOI, which follows the latest version; "+
			"use the version-specific DOI", c.Config.Report)).at(c.Config.Line("report"))
	}
	return pass("")
}

// rule: CC-REP-002 report-doi-newest-version
func reportDOINewestVersion(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-002")
	if result != nil {
		return *result
	}
	if record.draft {
		return skip(draftDOI)
	}
	versions := record.Metadata.Relations.Version
	if len(versions) == 0 {
		return skip("Zenodo records no version relation for this record")
	}
	if versions[0].IsLast {
		if versions[0].Count > 0 {
			return pass(fmt.Sprintf("version %d of %d", versions[0].Index+1, versions[0].Count))
		}
		return pass("the latest version of the record")
	}
	if versions[0].Count == 0 {
		return fail("a newer version of this record has been published").at(c.Config.Line("report"))
	}
	return fail(fmt.Sprintf("a newer version of this record has been published "+
		"(version %d of %d)", versions[0].Index+1, versions[0].Count)).at(c.Config.Line("report"))
}

// draftDOI is why the rules about the report DOI say nothing about a draft.
const draftDOI = "a draft's DOI is only reserved until it is published"

// The files of the record, CC-REP-007 to CC-REP-010, which replace CC-REP-003.
// They follow zenodo_policy_check() in the R package, which had them first.

// sourceExtensions are what CC-REP-009 accepts as the certificate's source:
// any file written in one of these, as the rule's description lists them.
var sourceExtensions = []string{".rmd", ".qmd", ".ipynb", ".docx", ".odt", ".md", ".tex"}

// rule: CC-REP-007 zenodo-certificate-pdf-present
func zenodoCertificatePDFPresent(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-007")
	if result != nil {
		return *result
	}
	if pdfs := record.filesEnding(".pdf"); len(pdfs) > 0 {
		return pass(strings.Join(pdfs, ", "))
	}
	if len(record.Files) == 0 {
		return fail("the Zenodo record has no files")
	}
	return fail("no PDF in the Zenodo record: " + strings.Join(record.fileNames(), ", "))
}

// rule: CC-REP-008 zenodo-certificate-pdf-name
func zenodoCertificatePDFName(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-008")
	if result != nil {
		return *result
	}
	pdfs := record.filesEnding(".pdf")
	if len(pdfs) == 0 {
		return skip("no PDF to look at the name of, see CC-REP-007")
	}
	if i := slices.IndexFunc(pdfs, func(pdf string) bool { return strings.EqualFold(pdf, "codecheck.pdf") }); i >= 0 {
		return pass(pdfs[i])
	}
	return fail(strings.Join(pdfs, ", ") + ", where the policy expects the certificate to be named codecheck.pdf")
}

// rule: CC-REP-009 zenodo-certificate-source-present
func zenodoCertificateSourcePresent(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-009")
	if result != nil {
		return *result
	}
	if sources := record.filesEnding(sourceExtensions...); len(sources) > 0 {
		return pass(strings.Join(sources, ", "))
	}
	return fail("the record should carry the certificate source, e.g. codecheck.Rmd")
}

// rule: CC-REP-010 zenodo-certificate-source-unambiguous
func zenodoCertificateSourceUnambiguous(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-010")
	if result != nil {
		return *result
	}
	// Only codecheck.* counts: another notebook in the deposit is the
	// workflow that was checked, not the certificate.
	var sources []string
	for _, name := range record.fileNames() {
		if ContainsFold(executableCertificateSources, name) {
			sources = append(sources, name)
		}
	}
	switch len(sources) {
	case 0:
		return pass("none of " + strings.Join(executableCertificateSources, ", "))
	case 1:
		return pass(sources[0])
	}
	return fail(strings.Join(sources, ", ") + ": keep one of " + strings.Join(executableCertificateSources, ", ") +
		", so that it is clear which made the certificate")
}

// fileNames are the names of the record's files, in the record's order.
func (r zenodoRecord) fileNames() []string {
	names := make([]string, 0, len(r.Files))
	for _, file := range r.Files {
		names = append(names, file.name())
	}
	return names
}

// filesEnding are the record's files with one of the extensions, ignoring case.
func (r zenodoRecord) filesEnding(extensions ...string) []string {
	var names []string
	for _, name := range r.fileNames() {
		if slices.Contains(extensions, strings.ToLower(path.Ext(name))) {
			names = append(names, name)
		}
	}
	return names
}

// rule: CC-REP-004 zenodo-certificate-id-match
func zenodoCertificateIDMatch(c Context) Result {
	certificate := strings.TrimSpace(c.Config.Certificate)
	if certificate == "" {
		return skip("no certificate identifier to compare")
	}
	record, result := zenodoRecordFor(c, "CC-REP-004")
	if result != nil {
		return *result
	}
	if strings.Contains(record.title(), certificate) {
		return pass(record.title())
	}
	return fail(fmt.Sprintf("the record is titled '%s', which does not carry the certificate identifier %s",
		record.title(), certificate)).at(c.Config.Line("certificate"))
}

// rule: CC-REP-005 zenodo-codechecker-names-match
func zenodoCodecheckerNamesMatch(c Context) Result {
	if len(c.Config.Codechecker) == 0 {
		return skip("no codechecker to compare")
	}
	record, result := zenodoRecordFor(c, "CC-REP-005")
	if result != nil {
		return *result
	}
	if len(record.Metadata.Creators) == 0 {
		return skip("the Zenodo record lists no creators")
	}

	var missing []string
	var lines []int
	for _, codechecker := range c.Config.Codechecker {
		found := false
		for _, creator := range record.Metadata.Creators {
			// Zenodo writes "Family, Given", the codecheck.yml usually the
			// other way round, so the comparison is by family name.
			if namesOverlap(codechecker.Name, creator.Name) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, codechecker.Name)
			lines = append(lines, c.Config.Line(codechecker.path+".name"))
		}
	}
	if len(missing) > 0 {
		return fail("codechecker(s) not among the record's creators: " +
			strings.Join(missing, ", ")).at(lines...)
	}
	return pass(fmt.Sprintf("%d codechecker(s) match", len(c.Config.Codechecker)))
}

// rule: CC-REP-006 zenodo-codechecker-orcids-match
func zenodoCodecheckerORCIDsMatch(c Context) Result {
	var withORCID []Person
	for _, codechecker := range c.Config.Codechecker {
		if strings.TrimSpace(codechecker.ORCID) != "" {
			withORCID = append(withORCID, codechecker)
		}
	}
	if len(withORCID) == 0 {
		return skip("no codechecker ORCID to compare")
	}
	record, result := zenodoRecordFor(c, "CC-REP-006")
	if result != nil {
		return *result
	}

	recorded := map[string]bool{}
	for _, creator := range record.Metadata.Creators {
		if digits := ORCIDDigits(creator.ORCID); digits != "" {
			recorded[digits] = true
		}
	}
	if len(recorded) == 0 {
		return skip("the Zenodo record carries no creator ORCID")
	}

	var missing []string
	var lines []int
	for _, codechecker := range withORCID {
		if !recorded[ORCIDDigits(codechecker.ORCID)] {
			missing = append(missing, codechecker.Name+" ("+codechecker.ORCID+")")
			lines = append(lines, c.Config.Line(codechecker.orcidPath()))
		}
	}
	if len(missing) > 0 {
		return fail("codechecker ORCID(s) not on the record: " + strings.Join(missing, ", ")).at(lines...)
	}
	return pass(fmt.Sprintf("%d ORCID(s) match", len(withORCID)))
}

// zenodoRecordFor fetches the record the report DOI points at, once, or is
// the one `check zenodo` read already, which may be a draft.
func zenodoRecordFor(c Context, rule string) (zenodoRecord, *Result) {
	if c.zenodo != nil {
		return *c.zenodo, nil
	}
	report := strings.TrimSpace(c.Config.Report)
	recordID := zenodoRecordID.FindStringSubmatch(report)
	if recordID == nil {
		result := skip("the report is not a Zenodo DOI")
		return zenodoRecord{}, &result
	}
	if !c.Services.Enabled() {
		result := needsServices(RequiresService[rule])
		return zenodoRecord{}, &result
	}

	record, err := c.Services.zenodoRecord(context.Background(), RepositorySpec{Type: "zenodo", Path: recordID[1]})
	if err != nil {
		result := skip(fmt.Sprintf("could not read the Zenodo record %s: %s", recordID[1], err))
		return zenodoRecord{}, &result
	}
	return record, nil
}

// zenodoRecord reads one record, of Zenodo or its sandbox as the spec says.
// The rules about the report and `check links` both ask, and the response
// cache makes that one request.
func (s *Services) zenodoRecord(ctx context.Context, spec RepositorySpec) (zenodoRecord, error) {
	var record zenodoRecord
	err := s.getJSONContext(ctx, fmt.Sprintf("%s/records/%s", spec.zenodoAPI(s), spec.Path), nil, &record)
	return record, err
}

var zenodoRecordID = regexp.MustCompile(`zenodo\.([0-9]+)`)

func sameDOI(a, b string) bool {
	return Normalise(DOIIn(a)) != "" && Normalise(DOIIn(a)) == Normalise(DOIIn(b))
}

// namesOverlap compares two ways of writing the same person, by the longest
// word they share, which in practice is the family name.
func namesOverlap(a, b string) bool {
	longest := ""
	for _, word := range strings.Fields(Normalise(a)) {
		if len(word) > len(longest) {
			longest = word
		}
	}
	if longest == "" {
		return false
	}
	return strings.Contains(Normalise(b), longest)
}
