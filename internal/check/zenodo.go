package check

import (
	"fmt"
	"regexp"
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
	Metadata    struct {
		Title    string `json:"title"`
		Creators []struct {
			Name  string `json:"name"`
			ORCID string `json:"orcid"`
		} `json:"creators"`
		Relations struct {
			Version []struct {
				Index  int  `json:"index"`
				IsLast bool `json:"is_last"`
				Count  int  `json:"count"`
			} `json:"version"`
		} `json:"relations"`
	} `json:"metadata"`
}

// zenodoFile is one file of a record. Zenodo's newer API names it `key` and
// links it under `links.self`; the older one used `filename` and
// `links.download`, and records deposited years ago still answer that way.
type zenodoFile struct {
	Key      string `json:"key"`
	Filename string `json:"filename"`
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
	// A concept DOI always resolves to whichever version is current, so a
	// certificate that names one does not point at a fixed document.
	if record.ConceptDOI != "" && sameDOI(record.ConceptDOI, c.Config.Report) {
		return fail(fmt.Sprintf("'%s' is the concept DOI, which follows the latest version; "+
			"use the version-specific DOI", c.Config.Report))
	}
	return pass("")
}

// rule: CC-REP-002 report-doi-newest-version
func reportDOINewestVersion(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-002")
	if result != nil {
		return *result
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
	return fail(fmt.Sprintf("a newer version of this record has been published "+
		"(version %d of %d)", versions[0].Index+1, versions[0].Count))
}

// rule: CC-REP-003 zenodo-files-present
func zenodoFilesPresent(c Context) Result {
	record, result := zenodoRecordFor(c, "CC-REP-003")
	if result != nil {
		return *result
	}
	if len(record.Files) == 0 {
		return fail("the Zenodo record has no files, so the certificate cannot be read")
	}
	var names []string
	for _, file := range record.Files {
		names = append(names, file.name())
	}
	return pass(strings.Join(names, ", "))
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
		record.title(), certificate))
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
		}
	}
	if len(missing) > 0 {
		return fail("codechecker(s) not among the record's creators: " +
			strings.Join(missing, ", "))
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
		if digits := orcidDigits(creator.ORCID); digits != "" {
			recorded[digits] = true
		}
	}
	if len(recorded) == 0 {
		return skip("the Zenodo record carries no creator ORCID")
	}

	var missing []string
	for _, codechecker := range withORCID {
		if !recorded[orcidDigits(codechecker.ORCID)] {
			missing = append(missing, codechecker.Name+" ("+codechecker.ORCID+")")
		}
	}
	if len(missing) > 0 {
		return fail("codechecker ORCID(s) not on the record: " + strings.Join(missing, ", "))
	}
	return pass(fmt.Sprintf("%d ORCID(s) match", len(withORCID)))
}

// zenodoRecordFor fetches the record the report DOI points at, once.
func zenodoRecordFor(c Context, rule string) (zenodoRecord, *Result) {
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

	var record zenodoRecord
	url := fmt.Sprintf("%s/records/%s", c.Services.Zenodo, recordID[1])
	if err := c.Services.getJSON(url, nil, &record); err != nil {
		result := skip(fmt.Sprintf("could not read the Zenodo record %s: %s", recordID[1], err))
		return zenodoRecord{}, &result
	}
	return record, nil
}

var zenodoRecordID = regexp.MustCompile(`zenodo\.([0-9]+)`)

func sameDOI(a, b string) bool {
	return normalise(doiIn(a)) != "" && normalise(doiIn(a)) == normalise(doiIn(b))
}

// namesOverlap compares two ways of writing the same person, by the longest
// word they share, which in practice is the family name.
func namesOverlap(a, b string) bool {
	longest := ""
	for _, word := range strings.Fields(normalise(a)) {
		if len(word) > len(longest) {
			longest = word
		}
	}
	if longest == "" {
		return false
	}
	return strings.Contains(normalise(b), longest)
}
