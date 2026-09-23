package check

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The certificate identifier as a sequence: which identifiers are taken, which
// one comes next, and how far a new one jumps. CC-REG-002 and the bot's
// `next certificate` and `set certificate` share it, so that the rule and the
// command cannot disagree about what a gap is.

// MaxCertificateJump is how far a new identifier may run past the year's
// previous one. An identifier is reserved when a check starts, and a check
// that is abandoned leaves its number unused, so a year's sequence has gaps;
// a jump beyond this is more likely a typo. The R package's
// CERTIFICATE_ID_MAX_JUMP has the same value.
const MaxCertificateJump = 9

// A Claim is one identifier somebody holds: a row of register.csv, or the
// title of an issue labelled `id assigned`.
type Claim struct {
	ID string
	// Issue is the issue whose title claims the identifier; 0 for a row of
	// register.csv.
	Issue int
}

// InRegister reports whether the claim is a row of register.csv.
func (c Claim) InRegister() bool { return c.Issue == 0 }

// certificateInTitle finds an identifier in free text, or a range of them:
// both "2026-004/2026-017" and "(2025-008 - 2025-017)" occur in the register's
// issue titles.
var certificateInTitle = regexp.MustCompile(`\b(\d{4})-(\d{3})\b(?:\s*[/\-–—]\s*(\d{4})-(\d{3})\b)?`)

// TitleCertificates is every identifier an issue title claims, a range
// expanded into its members. A range across years, or one that runs
// backwards, claims its two ends and nothing between them.
func TitleCertificates(title string) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, match := range certificateInTitle.FindAllStringSubmatch(title, -1) {
		start := match[1] + "-" + match[2]
		if match[3] == "" {
			add(start)
			continue
		}
		end := match[3] + "-" + match[4]
		from, _ := strconv.Atoi(match[2])
		to, _ := strconv.Atoi(match[4])
		if match[1] != match[3] || to < from {
			add(start)
			add(end)
			continue
		}
		for number := from; number <= to; number++ {
			add(fmt.Sprintf("%s-%03d", match[1], number))
		}
	}
	return ids
}

// TitleMentions is the identifiers of a title as it writes them, a range kept
// a range - for telling a person what a title says, where TitleCertificates
// is for counting what it claims.
func TitleMentions(title string) []string {
	return certificateInTitle.FindAllString(title, -1)
}

// TitleCertificate is the identifier a check's title carries in its own
// place, after the last `|` of `Author names | YYYY-NNN`.
func TitleCertificate(title string) (string, bool) {
	at := strings.LastIndex(title, "|")
	if at < 0 {
		return "", false
	}
	id := strings.TrimSpace(title[at+1:])
	return id, certificateID.MatchString(id)
}

// TitleWithCertificate is the title with the identifier in its place: the one
// after the last `|` replaced, or appended when there is none.
func TitleWithCertificate(title, id string) string {
	if _, ok := TitleCertificate(title); ok {
		return title[:strings.LastIndex(title, "|")] + "| " + id
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return id
	}
	return title + " | " + id
}

// certificateNumber splits a well-formed identifier into year and number.
func certificateNumber(id string) (year string, number int, ok bool) {
	if !certificateID.MatchString(id) {
		return "", 0, false
	}
	number, err := strconv.Atoi(id[5:])
	return id[:4], number, err == nil
}

// NextCertificate is the identifier after the highest one claimed in the
// year, and the claim that holds the highest. A gap is never filled: the
// number may belong to a check that was abandoned, and reusing it would give
// two checks the same identifier in somebody's notes. found is false when
// nothing is claimed in the year yet.
func NextCertificate(year int, claims []Claim) (next string, highest Claim, found bool) {
	prefix := fmt.Sprintf("%04d", year)
	top := 0
	for _, claim := range claims {
		y, number, ok := certificateNumber(claim.ID)
		if !ok || y != prefix {
			continue
		}
		if !found || number > top || number == top && claim.InRegister() && !highest.InRegister() {
			top, highest, found = number, claim, true
		}
	}
	return fmt.Sprintf("%s-%03d", prefix, top+1), highest, found
}

// CertificateJump is how far id runs past the year's previous identifier, the
// highest claimed number below it, as the R package measures it; previous is
// that claim, and found is false when id is the first of its year. A
// malformed id jumps nowhere.
func CertificateJump(id string, claims []Claim) (previous Claim, gap int, found bool) {
	year, number, ok := certificateNumber(id)
	if !ok {
		return Claim{}, 0, false
	}
	below := 0
	for _, claim := range claims {
		y, other, ok := certificateNumber(claim.ID)
		if ok && y == year && other < number && other > below {
			below, previous, found = other, claim, true
		}
	}
	return previous, number - below, found
}

// CertificateTaken finds a claim on id: a row of register.csv first, because
// that is the claim nobody can argue with, then an issue.
func CertificateTaken(id string, claims []Claim) (Claim, bool) {
	var other Claim
	found := false
	for _, claim := range claims {
		if claim.ID != id {
			continue
		}
		if claim.InRegister() {
			return claim, true
		}
		if !found {
			other, found = claim, true
		}
	}
	return other, found
}

// Certificates is the Certificate column of register.csv, one claim per row.
func (s *Services) Certificates() ([]Claim, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("reading the register needs the services")
	}
	register, err := s.registerCSV()
	if err != nil {
		return nil, err
	}
	return register.claims(), nil
}

func (d *registerData) claims() []Claim {
	claims := make([]Claim, 0, len(d.entries))
	for _, entry := range d.entries {
		if entry.Certificate != "" {
			claims = append(claims, Claim{ID: entry.Certificate})
		}
	}
	return claims
}
