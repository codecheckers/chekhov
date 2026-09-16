package check

import (
	"fmt"
	"strconv"
	"strings"
)

// The register-wide rules. These are not about a codecheck.yml on its own but
// about the row it gets in register.csv, so they need the register.

// knownRepositoryTypes are the prefixes the Repository column may use.
var knownRepositoryTypes = []string{"github", "gitlab", "osf", "zenodo", "zenodo-sandbox"}

// knownVenueTypes are the values the Type column may take.
var knownVenueTypes = []string{"journal", "conference", "community", "institution"}

// rule: CC-REG-001 certificate-unique
func certificateUnique(c Context) Result {
	certificate := strings.TrimSpace(c.Config.Certificate)
	if certificate == "" {
		return skip("no certificate identifier to look up")
	}
	register, result := registerFor(c, "CC-REG-001")
	if result != nil {
		return *result
	}

	occurrences := 0
	for _, entry := range register.entries {
		if entry.Certificate == certificate {
			occurrences++
		}
	}
	if occurrences > 1 {
		return fail(fmt.Sprintf("%s appears %d times in register.csv", certificate, occurrences))
	}
	return pass(fmt.Sprintf("%s appears %d time(s) in register.csv", certificate, occurrences))
}

// rule: CC-REG-002 certificate-id-sequence
func certificateIDSequence(c Context) Result {
	certificate := strings.TrimSpace(c.Config.Certificate)
	if !certificateID.MatchString(certificate) {
		// The form is CC-CFG-026's business; without it there is no sequence
		// to speak of.
		return skip("the certificate identifier is not of the form YYYY-NNN")
	}
	register, result := registerFor(c, "CC-REG-002")
	if result != nil {
		return *result
	}

	year, number := certificate[:4], certificate[5:]
	position, err := strconv.Atoi(number)
	if err != nil {
		return skip("the certificate number is not a number")
	}

	highest := 0
	for _, entry := range register.entries {
		if !strings.HasPrefix(entry.Certificate, year+"-") {
			continue
		}
		if other, err := strconv.Atoi(entry.Certificate[5:]); err == nil && other > highest {
			highest = other
		}
	}

	// A certificate continues the year's sequence: it is one of the numbers
	// already given out, or the next one. A gap means a number was skipped.
	if position <= highest+1 {
		return pass(fmt.Sprintf("%s, the highest in %s is %03d", certificate, year, highest))
	}
	return fail(fmt.Sprintf("%s leaves a gap: the highest identifier in %s is %s-%03d",
		certificate, year, year, highest))
}

// rule: CC-REG-003 repository-spec-format
func repositorySpecFormat(c Context) Result {
	entry, result := registerEntryFor(c, "CC-REG-003")
	if result != nil {
		return *result
	}
	if entry.Repository == "" {
		return fail("the register entry has no Repository")
	}

	for _, known := range knownRepositoryTypes {
		if strings.HasPrefix(entry.Repository, known+"::") {
			return pass(entry.Repository)
		}
	}
	return fail(fmt.Sprintf("'%s' does not start with a known prefix (%s)",
		entry.Repository, strings.Join(withSuffix(knownRepositoryTypes, "::"), " ")))
}

// rule: CC-REG-004 type-known
func typeKnown(c Context) Result {
	entry, result := registerEntryFor(c, "CC-REG-004")
	if result != nil {
		return *result
	}
	for _, known := range knownVenueTypes {
		if strings.EqualFold(entry.Type, known) {
			return pass(entry.Type)
		}
	}
	return fail(fmt.Sprintf("'%s' is not one of %s", entry.Type,
		strings.Join(knownVenueTypes, ", ")))
}

// rule: CC-REG-005 venue-known
func venueKnown(c Context) Result {
	entry, result := registerEntryFor(c, "CC-REG-005")
	if result != nil {
		return *result
	}
	register, _ := c.Services.registerCSV()
	if len(register.venues) == 0 {
		return skip("the register carries no venues.csv to compare against")
	}
	if entry.Venue == "" {
		return fail("the register entry has no Venue")
	}
	if register.venues[strings.ToLower(entry.Venue)] {
		return pass(entry.Venue)
	}
	return fail(fmt.Sprintf("'%s' is not listed in venues.csv", entry.Venue))
}

// --- the checks issue ------------------------------------------------------

type gitHubIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

// rule: CC-REG-006 issue-exists
func issueExists(c Context) Result {
	issue, result := issueFor(c, "CC-REG-006")
	if result != nil {
		return *result
	}
	return pass(fmt.Sprintf("issue #%d, %s", issue.Number, issue.State))
}

// rule: CC-REG-007 issue-references-certificate
func issueReferencesCertificate(c Context) Result {
	certificate := strings.TrimSpace(c.Config.Certificate)
	issue, result := issueFor(c, "CC-REG-007")
	if result != nil {
		return *result
	}
	if strings.Contains(issue.Title, certificate) {
		return pass(issue.Title)
	}
	return fail(fmt.Sprintf("issue #%d is titled '%s', which does not carry %s",
		issue.Number, issue.Title, certificate))
}

// issueFor reads the checks issue named in the register entry.
func issueFor(c Context, rule string) (gitHubIssue, *Result) {
	entry, result := registerEntryFor(c, rule)
	if result != nil {
		return gitHubIssue{}, result
	}
	number := strings.TrimSpace(entry.Issue)
	if number == "" {
		skipped := skip("the register entry names no checks issue")
		return gitHubIssue{}, &skipped
	}

	var issue gitHubIssue
	url := fmt.Sprintf("%s/repos/%s/issues/%s", c.Services.GitHub, c.Services.Register, number)
	if err := c.Services.github(url, &issue); err != nil {
		failed := fail(fmt.Sprintf("issue #%s of %s could not be read: %s",
			number, c.Services.Register, err))
		return gitHubIssue{}, &failed
	}
	return issue, nil
}

// --- helpers ---------------------------------------------------------------

// registerFor loads the register, or returns what the caller should report.
func registerFor(c Context, rule string) (*registerData, *Result) {
	if !c.Services.Enabled() {
		result := needsServices(RequiresService[rule])
		return nil, &result
	}
	register, err := c.Services.registerCSV()
	if err != nil {
		result := skip(fmt.Sprintf("could not read the register: %s", err))
		return nil, &result
	}
	return register, nil
}

// registerEntryFor finds this certificate's row. A certificate that is not in
// the register yet is the normal case while a check is under way, and its row
// cannot be judged before it exists.
func registerEntryFor(c Context, rule string) (registerEntry, *Result) {
	certificate := strings.TrimSpace(c.Config.Certificate)
	if certificate == "" {
		result := skip("no certificate identifier to look up")
		return registerEntry{}, &result
	}
	register, result := registerFor(c, rule)
	if result != nil {
		return registerEntry{}, result
	}

	entry, found := register.entryFor(certificate)
	if !found {
		skipped := skip(fmt.Sprintf("%s is not in register.csv yet", certificate))
		return registerEntry{}, &skipped
	}
	return entry, nil
}

func withSuffix(values []string, suffix string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value+suffix)
	}
	return out
}
