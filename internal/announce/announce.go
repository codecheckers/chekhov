// Package announce composes the toot about a published CODECHECK.
//
// What the toot says is read from the published certificate, index.json next
// to its landing page, and who is mentioned from the register's persons.csv,
// venues.csv and the codechecker lists (codecheckers/register#217). A missing
// file or column is not an error: nobody is mentioned, and the preview says
// who could not be. Posting is internal/mastodon's business, not this
// package's.
package announce

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/mastodon"
)

// A Person is an author or a codechecker as the certificate names them.
type Person = check.Person

// A Certificate is what a toot is about.
type Certificate struct {
	ID string
	// Page is the landing page the certificate was read from, ending in a
	// slash; the page images are next to it.
	Page           string
	PaperTitle     string
	PaperReference string
	Report         string
	Venue          string
	Authors        []Person
	Codecheckers   []Person
}

// Load reads a published certificate and the accounts of the people and the
// venue it names.
func Load(services *check.Services, settings *config.Settings, id string) (Certificate, Directory, error) {
	page := settings.CertificateURL(id)
	raw, err := services.FetchFile(page + "index.json")
	if err != nil {
		return Certificate{}, Directory{}, fmt.Errorf("certificate %s is not published, or not yet: %w", id, err)
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
		return Certificate{}, Directory{}, fmt.Errorf("%sindex.json could not be read: %w", page, err)
	}
	if published.Certificate.ID != id {
		return Certificate{}, Directory{}, fmt.Errorf("%sindex.json is certificate %q, not %s",
			page, published.Certificate.ID, id)
	}

	certificate := Certificate{
		ID:             id,
		Page:           page,
		PaperTitle:     oneLine(published.Paper.Title),
		PaperReference: strings.TrimSpace(published.Paper.Reference),
		Report:         strings.TrimSpace(published.Codecheck.Report),
		Venue:          strings.TrimSpace(published.Codecheck.Venue),
		Authors:        published.Paper.Authors,
		Codecheckers:   published.Codecheck.Codecheckers,
	}

	directory := Directory{venues: map[string]Venue{}}
	// persons.csv first: the register's own record of a person wins over the
	// lists people fill in about themselves.
	for _, source := range append([]string{services.RegisterFile("persons.csv")}, settings.CodecheckerLists()...) {
		rows, err := services.CSV(source)
		if err != nil {
			continue
		}
		for _, row := range rows {
			directory.addPerson(firstOf(row, "orcid", "ORCID"), row["name"], row["fediverse"])
		}
	}
	if rows, err := services.CSV(services.RegisterFile("venues.csv")); err == nil {
		for _, row := range rows {
			directory.addVenue(row["name"], row["fediverse"], row["hashtags"])
		}
	}
	return certificate, directory, nil
}

// Directory finds the fediverse account of a person or a venue.
type Directory struct {
	people []entry
	venues map[string]Venue
}

type entry struct {
	orcid, name, handle string
}

// A Venue is what venues.csv says about announcing.
type Venue struct {
	Handle   string
	Hashtags []string
}

func (d *Directory) addPerson(orcid, name, handle string) {
	handle = Handle(handle)
	if handle == "" {
		return
	}
	d.people = append(d.people, entry{orcid: check.ORCIDDigits(orcid), name: check.Normalise(name), handle: handle})
}

func (d *Directory) addVenue(name, handle, hashtags string) {
	venue := Venue{Handle: Handle(handle)}
	for _, tag := range strings.Split(hashtags, ";") {
		if tag = strings.TrimLeft(strings.TrimSpace(tag), "#"); tag != "" {
			venue.Hashtags = append(venue.Hashtags, tag)
		}
	}
	if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
		d.venues[name] = venue
	}
}

// Person finds a person's account: by ORCID, and by name only when the
// certificate gives no ORCID. Institutional rows have NA there, and a name is
// all there is to go by.
func (d Directory) Person(person Person) string {
	if orcid := check.ORCIDDigits(person.ORCID); orcid != "" {
		for _, known := range d.people {
			if known.orcid == orcid {
				return known.handle
			}
		}
		return ""
	}
	name := check.Normalise(person.Name)
	if name == "" {
		return ""
	}
	for _, known := range d.people {
		if known.name == name {
			return known.handle
		}
	}
	return ""
}

// Venue finds what venues.csv says about a venue, by its short name.
func (d Directory) Venue(name string) Venue {
	return d.venues[strings.ToLower(strings.TrimSpace(name))]
}

var handlePattern = regexp.MustCompile(`^@?([A-Za-z0-9_.-]+)@([A-Za-z0-9.-]+\.[A-Za-z]{2,})$`)

// Handle normalises an account to @user@instance, or returns "" for anything
// that is not one. A profile URL or a bare @user is not guessed at: a wrong
// guess mentions a stranger. The codecheck R package reads the column the
// same way (fediverse_handle()).
func Handle(value string) string {
	match := handlePattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return ""
	}
	return "@" + match[1] + "@" + strings.ToLower(match[2])
}

// A Toot is a composed announcement.
type Toot struct {
	Text string
	// Mentioned are the accounts the toot names.
	Mentioned []string
	// Unmatched are the people, and the venue, with no account on record.
	Unmatched []string
	// Defused says the mentions are written without their @, so that nobody is
	// notified.
	Defused bool
}

// Limits are what a toot must fit.
type Limits struct {
	// Characters is the most a toot may have.
	Characters int
	// PerURL is what a link counts as, whatever its length.
	PerURL int
}

// DefaultLimits are Mastodon's defaults. Toots are kept to them even where an
// instance allows more, so that they read the same when boosted elsewhere;
// see LimitsFor.
var DefaultLimits = Limits{Characters: 500, PerURL: 23}

// LimitsFor are the limits a toot is composed to on an instance: the
// instance's own, but never more characters than DefaultLimits.
func LimitsFor(instance mastodon.Limits) Limits {
	limits := DefaultLimits
	if instance.MaxCharacters > 0 {
		limits.Characters = min(instance.MaxCharacters, DefaultLimits.Characters)
	}
	if instance.CharactersPerURL > 0 {
		limits.PerURL = instance.CharactersPerURL
	}
	return limits
}

// Compose writes the toot. With direct visibility every mention is defused
// (@user@instance becomes user@instance): otherwise a development toot would
// be a direct message to real authors.
func Compose(certificate Certificate, directory Directory, limits Limits, visibility string) (Toot, error) {
	toot := Toot{Defused: visibility == "direct"}
	people := func(persons []Person) []string {
		named := make([]string, 0, len(persons))
		for _, person := range persons {
			name := strings.TrimSpace(person.Name)
			if handle := directory.Person(person); handle != "" {
				toot.Mentioned = append(toot.Mentioned, handle)
				named = append(named, fmt.Sprintf("%s (%s)", name, handle))
			} else {
				toot.Unmatched = append(toot.Unmatched, name)
				named = append(named, name)
			}
		}
		return named
	}

	authors := people(certificate.Authors)
	codecheckers := people(certificate.Codecheckers)
	venue := directory.Venue(certificate.Venue)
	venueLine := certificate.Venue
	if venue.Handle != "" {
		toot.Mentioned = append(toot.Mentioned, venue.Handle)
		venueLine += " " + venue.Handle
	} else if certificate.Venue != "" {
		toot.Unmatched = append(toot.Unmatched, "venue "+certificate.Venue)
	}
	hashtags := []string{"#CODECHECK"}
	for _, tag := range venue.Hashtags {
		hashtags = append(hashtags, "#"+tag)
	}

	render := func(authors []string, title string) string {
		lines := []string{"🎉 New CODECHECK certificate " + certificate.ID}
		add := func(prefix, value string) {
			if strings.TrimSpace(value) != "" {
				lines = append(lines, prefix+value)
			}
		}
		add("📄 ", title)
		add("✍️ ", strings.Join(authors, ", "))
		add("🔍 Codechecked by ", strings.Join(codecheckers, ", "))
		add("🏛️ ", venueLine)
		add("🔗 Certificate: ", certificate.Report)
		add("📰 Paper: ", certificate.PaperReference)
		add("🌐 ", certificate.Page)
		lines = append(lines, strings.Join(hashtags, " "))
		text := strings.Join(lines, "\n")
		if toot.Defused {
			// The one place mentions are taken out, so that an @ in a title
			// is defused as surely as an account from persons.csv.
			text = defuse(text)
		}
		return text
	}

	// Too long: the author list goes first, then the title.
	title := certificate.PaperTitle
	toot.Text = render(authors, title)
	if Length(toot.Text, limits.PerURL) > limits.Characters && len(authors) > 1 {
		authors = []string{authors[0] + " et al."}
		toot.Text = render(authors, title)
	}
	if over := Length(toot.Text, limits.PerURL) - limits.Characters; over > 0 {
		keep := utf8.RuneCountInString(title) - over - 1
		if keep < 20 {
			return toot, fmt.Errorf("the toot is %d characters over the limit of %d even with a shortened title",
				over, limits.Characters)
		}
		title = strings.TrimSpace(string([]rune(title)[:keep])) + "…"
		toot.Text = render(authors, title)
	}
	return toot, nil
}

var (
	urlPattern = regexp.MustCompile(`https?://\S+`)
	// A mention counts as its local part only: @user@instance is @user.
	mentionPattern = regexp.MustCompile(`@([A-Za-z0-9_]+)@[A-Za-z0-9.-]+[A-Za-z0-9]`)
	// Mastodon reads a mention after anything but a letter, a digit, an
	// underscore, a slash or an equals sign: "Re:@alice" and "“@alice" notify
	// alice as surely as " @alice". Every such @ is dropped, however many.
	atWordPattern = regexp.MustCompile(`(^|[^\p{L}\p{N}_/=])@+([\p{L}\p{N}_])`)
)

// Length counts a toot the way Mastodon does: in characters, each link as a
// fixed number, and a mention by its local part.
func Length(text string, perURL int) int {
	links := len(urlPattern.FindAllString(text, -1))
	text = urlPattern.ReplaceAllString(text, "")
	text = mentionPattern.ReplaceAllString(text, "@$1")
	return utf8.RuneCountInString(text) + links*perURL
}

func defuse(text string) string {
	// Matches cannot overlap, so "@a @b" needs a second pass when the
	// separator is taken by the first match.
	for {
		next := atWordPattern.ReplaceAllString(text, "$1$2")
		if next == text {
			return text
		}
		text = next
	}
}

// Announced finds a status that already announced the certificate, by its
// identifier or its landing page, and returns its URL.
func Announced(statuses []mastodon.Posted, certificate Certificate) (string, bool) {
	for _, status := range statuses {
		if strings.Contains(status.Content, certificate.ID) || strings.Contains(status.Content, certificate.Page) {
			return status.URL, true
		}
	}
	return "", false
}

func firstOf(row map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(row[key]); value != "" {
			return value
		}
	}
	return ""
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
