// Package check runs the CODECHECK validation rules against a codecheck.yml.
//
// One function per rule, and the function knows nothing about specification
// versions: which rules run comes from the rule file of the version the
// configuration declares, and how loudly a failure is reported comes from that
// rule's severity. A requirement hardened in 2.0 is the same check reported
// more loudly, not a second code path.
//
// This mirrors the codecheck R package function for function, under the same
// rule identifiers, so that the two implementations can be diffed.
package check

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Status of one check. A check that cannot reach a verdict returns StatusSkip:
// "could not check" is not a failed check.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Result of running one check.
type Result struct {
	Status Status
	Detail string
}

func pass(detail string) Result { return Result{Status: StatusPass, Detail: detail} }
func fail(detail string) Result { return Result{Status: StatusFail, Detail: detail} }
func skip(detail string) Result { return Result{Status: StatusSkip, Detail: detail} }

// A Func checks one rule against the file under validation.
type Func func(Context) Result

// Checks maps a rule identifier to the function that checks it. A rule missing
// here is not checked, and the tests fail unless it is listed in
// NotImplemented.
var Checks = map[string]Func{
	"CC-CFG-001": yamlParses,
	"CC-CFG-002": explicitDocument,
	"CC-CFG-003": fileNameAndLocation,
	"CC-CFG-004": manifestPresent,
	"CC-CFG-005": manifestItemFile,
	"CC-CFG-006": manifestPathRelative,
	"CC-CFG-007": manifestItemComment,
	"CC-CFG-008": codecheckerPresent,
	"CC-CFG-009": codecheckerName,
	"CC-CFG-010": codecheckerORCID,
	"CC-CFG-011": reportPresent,
	"CC-CFG-013": reportDOINotPlaceholder,
	"CC-CFG-014": versionPresent,
	"CC-CFG-015": versionKnown,
	"CC-CFG-016": paperPresent,
	"CC-CFG-017": paperTitle,
	"CC-CFG-018": paperAuthors,
	"CC-CFG-019": paperAuthorName,
	"CC-CFG-020": paperAuthorORCID,
	"CC-CFG-021": paperReference,
	"CC-CFG-022": referenceNotBarePDF,
	"CC-CFG-023": noPlaceholderValues,
	"CC-CFG-024": summaryPresent,
	"CC-CFG-025": certificatePresent,
	"CC-CFG-026": certificateIDFormat,
	"CC-CFG-027": referenceIsURL,
	"CC-CFG-028": referencePrefersDOI,
	"CC-CFG-029": referenceOtherIsList,
	"CC-CFG-030": referenceOtherItemForm,
	"CC-CFG-031": referencePDFIsArchived,
	"CC-BUN-002": codecheckDirectoryPresent,
	"CC-BUN-003": reportFilePresent,
	"CC-BUN-005": licencePresent,
	"CC-MET-001": orcidFormat,

	// These need the outside world, see services.go and RequiresService: they
	// skip with what they would have needed unless the services are enabled.
	"CC-CFG-012": reportResolvable,
	"CC-MET-002": orcidResolves,
	"CC-MET-003": orcidNameMatch,
	"CC-MET-004": paperReferenceResolves,
	"CC-MET-005": paperTitleMatch,
	"CC-MET-006": paperAuthorCountMatch,
	"CC-MET-007": paperAuthorNameMatch,
	"CC-MET-008": paperAuthorORCIDMatch,
	"CC-MET-009": referenceOtherResolves,
	"CC-BUN-001": manifestFilesExist,
	"CC-BUN-004": repositoryReachable,
	"CC-REP-001": reportDOIVersionSpecific,
	"CC-REP-002": reportDOINewestVersion,
	"CC-REP-003": zenodoFilesPresent,
	"CC-REP-004": zenodoCertificateIDMatch,
	"CC-REP-005": zenodoCodecheckerNamesMatch,
	"CC-REP-006": zenodoCodecheckerORCIDsMatch,
	"CC-REG-001": certificateUnique,
	"CC-REG-002": certificateIDSequence,
	"CC-REG-003": repositorySpecFormat,
	"CC-REG-004": typeKnown,
	"CC-REG-005": venueKnown,
	"CC-REG-006": issueExists,
	"CC-REG-007": issueReferencesCertificate,
}

// NotImplemented are the rules nothing checks yet, each with the reason. It is
// empty: every rule of both specification versions has a check. The map stays
// because TestEveryRuleAccountedFor reads it, so a rule added to the register
// that nobody has got to yet has somewhere to be recorded rather than being
// ignored.
var NotImplemented = map[string]string{}

// --- the file itself -------------------------------------------------------

// rule: CC-CFG-001 yaml-parses
func yamlParses(c Context) Result {
	// The encoding is checked on the raw bytes and before the parse: an invalid
	// byte inside a quoted scalar makes the YAML parser fail with a scanner
	// error rather than saying the file is not UTF-8.
	if c.Raw != nil && !utf8.Valid(c.Raw) {
		return fail("the file is not valid UTF-8 encoded")
	}
	if c.ParseError != nil {
		return fail(fmt.Sprintf("the file is not valid YAML: %s", c.ParseError))
	}
	if c.Raw == nil {
		return pass("")
	}
	return pass("valid UTF-8 YAML")
}

// rule: CC-CFG-002 explicit-document
func explicitDocument(c Context) Result {
	if c.Raw == nil {
		return skip("no file on disk to inspect")
	}
	for _, line := range strings.Split(string(c.Raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == "---" {
			return pass("")
		}
		break
	}
	return fail("the file does not start with the document marker '---'")
}

// rule: CC-CFG-003 file-name-and-location
func fileNameAndLocation(c Context) Result {
	if c.Path == "" {
		return skip("no file on disk to inspect")
	}
	if filepath.Base(c.Path) == "codecheck.yml" {
		return pass("")
	}
	return fail(fmt.Sprintf("the file is named %s, not codecheck.yml", filepath.Base(c.Path)))
}

// --- manifest --------------------------------------------------------------

// rule: CC-CFG-004 manifest-present
func manifestPresent(c Context) Result {
	// The rule is about the node being there. An empty manifest is odd but not
	// a violation, and the specification does not make it one.
	if !c.Config.HasManifest {
		return fail("no root-level manifest")
	}
	return pass(fmt.Sprintf("%d manifest item(s)", len(c.Config.Manifest)))
}

// rule: CC-CFG-005 manifest-item-file
func manifestItemFile(c Context) Result {
	if len(c.Config.Manifest) == 0 {
		return skip("no manifest to inspect")
	}
	var without []string
	for i, item := range c.Config.Manifest {
		if strings.TrimSpace(item.File) == "" {
			without = append(without, fmt.Sprint(i+1))
		}
	}
	if len(without) == 0 {
		return pass("")
	}
	return fail("manifest item(s) without a file: " + strings.Join(without, ", "))
}

// rule: CC-CFG-006 manifest-path-relative
func manifestPathRelative(c Context) Result {
	if len(c.Config.Manifest) == 0 {
		return skip("no manifest files to inspect")
	}
	// An absolute path, a Windows drive letter or a parent-directory step all
	// leave the bundle, which is what the rule is about.
	leaves := regexp.MustCompile(`^(/|~|[A-Za-z]:)|(^|/)\.\.(/|$)`)
	var outside []string
	for _, item := range c.Config.Manifest {
		if item.File != "" && leaves.MatchString(item.File) {
			outside = append(outside, item.File)
		}
	}
	if len(outside) == 0 {
		return pass("")
	}
	return fail("path(s) not relative to the bundle: " + strings.Join(outside, ", "))
}

// rule: CC-CFG-007 manifest-item-comment
func manifestItemComment(c Context) Result {
	if len(c.Config.Manifest) == 0 {
		return skip("no manifest to inspect")
	}
	without := 0
	for _, item := range c.Config.Manifest {
		if strings.TrimSpace(item.Comment) == "" {
			without++
		}
	}
	if without == 0 {
		return pass("every manifest item has a comment")
	}
	return fail(fmt.Sprintf("%d manifest item(s) have no comment to explain them", without))
}

// --- codechecker and report ------------------------------------------------

// rule: CC-CFG-008 codechecker-present
func codecheckerPresent(c Context) Result {
	if len(c.Config.Codechecker) == 0 {
		return fail("no codechecker recorded")
	}
	return pass(fmt.Sprintf("%d codechecker(s)", len(c.Config.Codechecker)))
}

// rule: CC-CFG-009 codechecker-name
func codecheckerName(c Context) Result {
	return everyPersonHas(c.Config.Codechecker, "codechecker", "name",
		func(p Person) string { return p.Name })
}

// rule: CC-CFG-010 codechecker-orcid
func codecheckerORCID(c Context) Result {
	return everyPersonHas(c.Config.Codechecker, "codechecker", "an ORCID",
		func(p Person) string { return p.ORCID })
}

// rule: CC-CFG-011 report-present
func reportPresent(c Context) Result {
	if strings.TrimSpace(c.Config.Report) == "" {
		return fail("no report identifier")
	}
	return pass(c.Config.Report)
}

// rule: CC-CFG-013 report-doi-not-placeholder
func reportDOINotPlaceholder(c Context) Result {
	report := strings.TrimSpace(c.Config.Report)
	if report == "" {
		return skip("no report identifier to inspect")
	}
	if isPlaceholder(report) {
		return fail(fmt.Sprintf("'%s' is a placeholder", report))
	}
	return pass("")
}

// --- specification version -------------------------------------------------

// rule: CC-CFG-014 version-present
func versionPresent(c Context) Result {
	if strings.TrimSpace(c.Config.Version) == "" {
		return fail("no version node; the newest specification is assumed")
	}
	return pass(c.Config.Version)
}

// rule: CC-CFG-015 version-known
func versionKnown(c Context) Result {
	version := strings.TrimSpace(c.Config.Version)
	if version == "" {
		return skip("no version node to inspect")
	}
	if SpecVersionFromURL(version) == "" {
		return fail(fmt.Sprintf("'%s' is not a published specification version", version))
	}
	return pass("")
}

// --- paper metadata --------------------------------------------------------

// rule: CC-CFG-016 paper-present
func paperPresent(c Context) Result {
	if !c.Config.HasPaper {
		return fail("no paper metadata")
	}
	return pass("")
}

// rule: CC-CFG-017 paper-title
func paperTitle(c Context) Result {
	if !c.Config.HasPaper {
		return skip("no paper metadata to inspect")
	}
	if strings.TrimSpace(c.Config.Paper.Title) == "" {
		return fail("the paper has no title")
	}
	return pass("")
}

// rule: CC-CFG-018 paper-authors
func paperAuthors(c Context) Result {
	if !c.Config.HasPaper {
		return skip("no paper metadata to inspect")
	}
	if len(c.Config.Paper.Authors) == 0 {
		return fail("the paper has no authors")
	}
	return pass(fmt.Sprintf("%d author(s)", len(c.Config.Paper.Authors)))
}

// rule: CC-CFG-019 paper-author-name
func paperAuthorName(c Context) Result {
	return everyPersonHas(c.Config.Paper.Authors, "author", "name",
		func(p Person) string { return p.Name })
}

// rule: CC-CFG-020 paper-author-orcid
func paperAuthorORCID(c Context) Result {
	return everyPersonHas(c.Config.Paper.Authors, "author", "an ORCID",
		func(p Person) string { return p.ORCID })
}

// rule: CC-CFG-021 paper-reference
func paperReference(c Context) Result {
	if strings.TrimSpace(c.Config.Paper.Reference) == "" {
		return fail("the paper has no reference")
	}
	return pass(c.Config.Paper.Reference)
}

// rule: CC-CFG-022 reference-not-bare-pdf
func referenceNotBarePDF(c Context) Result {
	reference := strings.TrimSpace(c.Config.Paper.Reference)
	if reference == "" {
		return skip("no reference to inspect")
	}
	if !isPDFLink(reference) {
		return pass("")
	}
	if isWebArchiveLink(reference) {
		// Reported by CC-CFG-031 instead, which is advisory: an archived
		// snapshot is what the specification asks for where nothing better
		// exists.
		return pass("a PDF link, but an archived one")
	}
	return fail(fmt.Sprintf("'%s' is a direct PDF link; prefer a DOI or a landing page", reference))
}

// rule: CC-CFG-031 reference-pdf-is-archived
func referencePDFIsArchived(c Context) Result {
	reference := strings.TrimSpace(c.Config.Paper.Reference)
	if reference == "" || !isPDFLink(reference) {
		return skip("the reference is not a PDF link")
	}
	if isWebArchiveLink(reference) {
		return pass("archived snapshot of a PDF")
	}
	return fail("a direct PDF link that is not archived; a Wayback Machine snapshot lasts longer")
}

// rule: CC-CFG-027 reference-is-url
func referenceIsURL(c Context) Result {
	reference := strings.TrimSpace(c.Config.Paper.Reference)
	if reference == "" {
		return skip("no reference to inspect")
	}
	if bareURL.MatchString(reference) {
		return pass("")
	}
	return fail(fmt.Sprintf("'%s' is not a bare URL; tools do not extract a URL from surrounding text", reference))
}

// rule: CC-CFG-028 reference-prefers-doi
func referencePrefersDOI(c Context) Result {
	reference := strings.TrimSpace(c.Config.Paper.Reference)
	if reference == "" {
		return skip("no reference to inspect")
	}
	if doiPattern.MatchString(reference) {
		return pass("")
	}
	return fail("the reference is not a DOI, which is preferred for long-term availability")
}

// rule: CC-CFG-029 reference-other-is-list
func referenceOtherIsList(c Context) Result {
	if !c.Config.Paper.HasReferenceOther {
		return skip("no reference-other")
	}
	if !c.Config.Paper.ReferenceOtherIsList {
		return fail("reference-other is not a sequence")
	}
	return pass(fmt.Sprintf("%d further reference(s)", len(c.Config.Paper.ReferenceOther)))
}

// rule: CC-CFG-030 reference-other-item-form
func referenceOtherItemForm(c Context) Result {
	if !c.Config.Paper.ReferenceOtherIsList {
		return skip("no reference-other sequence")
	}
	var notURL []string
	for _, entry := range c.Config.Paper.ReferenceOther {
		if !bareURL.MatchString(strings.TrimSpace(entry)) {
			notURL = append(notURL, entry)
		}
	}
	if len(notURL) == 0 {
		return pass("")
	}
	return fail("reference-other entries that are not resolvable URLs: " + strings.Join(notURL, ", "))
}

// --- certificate and summary -----------------------------------------------

// rule: CC-CFG-024 summary-present
func summaryPresent(c Context) Result {
	if strings.TrimSpace(c.Config.Summary) == "" {
		return fail("no summary of the check")
	}
	return pass("")
}

// rule: CC-CFG-025 certificate-present
func certificatePresent(c Context) Result {
	if strings.TrimSpace(c.Config.Certificate) == "" {
		return fail("no certificate identifier")
	}
	return pass(c.Config.Certificate)
}

// rule: CC-CFG-026 certificate-id-format
func certificateIDFormat(c Context) Result {
	certificate := strings.TrimSpace(c.Config.Certificate)
	if certificate == "" {
		return skip("no certificate identifier to inspect")
	}
	if certificateID.MatchString(certificate) {
		return pass("")
	}
	return fail(fmt.Sprintf("'%s' is not of the form YYYY-NNN", certificate))
}

// rule: CC-CFG-023 no-placeholder-values
func noPlaceholderValues(c Context) Result {
	var found []string
	for _, value := range c.Config.Strings {
		if isPlaceholder(value) {
			found = append(found, value)
		}
	}
	if len(found) == 0 {
		return pass("")
	}
	return fail("placeholder value(s) left in the file: " + strings.Join(found, ", "))
}

// --- metadata form ---------------------------------------------------------

// rule: CC-MET-001 orcid-format
func orcidFormat(c Context) Result {
	// Both the paper's authors and the codecheckers, because the rule is about
	// the form of an ORCID wherever it appears in the file.
	people := append(append([]Person{}, c.Config.Paper.Authors...), c.Config.Codechecker...)
	var orcids, malformed []string
	for _, person := range people {
		if strings.TrimSpace(person.ORCID) == "" {
			continue
		}
		orcids = append(orcids, person.ORCID)
		if !orcidPattern.MatchString(person.ORCID) {
			malformed = append(malformed, person.ORCID)
		}
	}
	if len(orcids) == 0 {
		return skip("no ORCID to inspect")
	}
	if len(malformed) == 0 {
		return pass(fmt.Sprintf("%d ORCID(s)", len(orcids)))
	}
	return fail("ORCIDs must be plain and without URL prefix, but found: " +
		strings.Join(malformed, ", "))
}

// --- the bundle on disk ----------------------------------------------------

// rule: CC-BUN-002 codecheck-directory-present
func codecheckDirectoryPresent(c Context) Result {
	entries, problem := bundleListing(c, "")
	if problem.Status != "" {
		return problem
	}
	if name := codecheckDir(entries); name != "" {
		return pass(name)
	}
	return fail("the bundle has no codecheck/ or .codecheck/ directory")
}

// rule: CC-BUN-003 report-file-present
func reportFilePresent(c Context) Result {
	entries, problem := bundleListing(c, "")
	if problem.Status != "" {
		return problem
	}
	name := codecheckDir(entries)
	if name == "" {
		return skip("no codecheck/ directory to look in")
	}
	inside, problem := bundleListing(c, name)
	if problem.Status != "" {
		return problem
	}

	var reports []string
	for _, entry := range inside {
		if !entry.IsDir && strings.EqualFold(filepath.Ext(entry.Name), ".pdf") {
			reports = append(reports, entry.Name)
		}
	}
	if len(reports) == 0 {
		return fail("no certificate report in the codecheck/ directory")
	}
	return pass(strings.Join(reports, ", "))
}

// rule: CC-BUN-005 licence-present
//
// The rule asks whether the repository under check states a licence, not
// whether it has a file called LICENSE: an OSF node and a Zenodo record state
// one in their metadata, and have no file to find. The codecheck R package
// looks only for the file, because it only ever sees a bundle on disk.
func licencePresent(c Context) Result {
	if c.Bundle == nil {
		return skip("no bundle to inspect")
	}
	licence, err := c.Bundle.Licence()
	if err != nil {
		if errors.Is(err, errNoServices) {
			return needsServices("the bundle under check")
		}
		return skip(fmt.Sprintf("could not read %s: %s", c.Bundle.Describe(), err))
	}
	if licence == "" {
		return fail("the repository under check states no licence")
	}
	return pass(licence)
}

// bundleListing reads one directory of the bundle, or says why it could not.
//
// A bundle that cannot be looked at is a skip, never a failure: a repository
// the services are switched off for, an API having a bad day and a directory
// that is not on disk all say nothing about the codecheck.yml. A zero Result
// means there is nothing to report and the entries can be used.
func bundleListing(c Context, dir string) ([]BundleEntry, Result) {
	if c.Bundle == nil {
		return nil, skip("no bundle to inspect")
	}
	entries, err := c.Bundle.List(dir)
	if err != nil {
		if errors.Is(err, errNoServices) {
			return nil, needsServices("the bundle under check")
		}
		where := c.Bundle.Describe()
		if dir != "" {
			where += "/" + dir
		}
		return nil, skip(fmt.Sprintf("could not read %s: %s", where, err))
	}
	return entries, Result{}
}

// codecheckDir is the name of the bundle's codecheck directory, empty when it
// has none.
func codecheckDir(entries []BundleEntry) string {
	for _, name := range []string{"codecheck", ".codecheck"} {
		for _, entry := range entries {
			if entry.IsDir && entry.Name == name {
				return name
			}
		}
	}
	return ""
}

// --- helpers shared by several checks --------------------------------------

var (
	bareURL       = regexp.MustCompile(`^https?://\S+$`)
	doiPattern    = regexp.MustCompile(`doi\.org/|^doi:|^10\.[0-9]{4,}/`)
	certificateID = regexp.MustCompile(`^[0-9]{4}-[0-9]{3}$`)
	orcidPattern  = regexp.MustCompile(`^[0-9]{4}-[0-9]{4}-[0-9]{4}-[0-9]{3}[0-9X]$`)
	pdfLink       = regexp.MustCompile(`(?i)\.pdf($|[?#])`)
	webArchive    = regexp.MustCompile(`(?i)web\.archive\.org/|archive\.ph/|webcitation\.org/`)
	placeholder   = regexp.MustCompile(`(?i)FIXME|TODO|XXXX|0000-0000-0000-0000`)
	licenceFile   = regexp.MustCompile(`(?i)^(LICEN[CS]E|COPYING)(\..*)?$`)
)

func isPDFLink(reference string) bool        { return pdfLink.MatchString(reference) }
func isWebArchiveLink(reference string) bool { return webArchive.MatchString(reference) }
func isPlaceholder(value string) bool        { return placeholder.MatchString(value) }

// everyPersonHas reports the people in a sequence that lack a field, by
// position, which is how a reader finds them in the file.
func everyPersonHas(people []Person, kind, field string, get func(Person) string) Result {
	if len(people) == 0 {
		return skip("no " + kind + " to inspect")
	}
	var without []string
	for i, person := range people {
		if strings.TrimSpace(get(person)) == "" {
			without = append(without, fmt.Sprint(i+1))
		}
	}
	if len(without) == 0 {
		return pass("")
	}
	article := "a "
	if strings.HasPrefix(field, "an ") {
		article = ""
	}
	return fail(fmt.Sprintf("%s(s) without %s%s: %s", kind, article, field,
		strings.Join(without, ", ")))
}
