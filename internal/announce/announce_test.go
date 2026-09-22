package announce

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/announce/announcetest"
	"github.com/codecheckers/chekhov/internal/mastodon"
)

var update = flag.Bool("update", false, "rewrite the golden toots")

type published struct{ *announcetest.Published }

func newPublished(t *testing.T) published { return published{announcetest.New(t)} }

func (p published) load(t *testing.T) (Certificate, Directory) {
	t.Helper()
	certificate, directory, err := Load(p.Services, p.Settings, "1970-001")
	if err != nil {
		t.Fatal(err)
	}
	return certificate, directory
}

// golden compares a toot with its file, the test server's address written as
// https://certificates.example so that the file does not change with the port.
func (p published) golden(t *testing.T, name, got string) {
	t.Helper()
	got = strings.ReplaceAll(got, p.URL, "https://certificates.example")
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if got != string(want) {
		t.Errorf("the toot differs from %s:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func TestComposePublic(t *testing.T) {
	p := newPublished(t)
	certificate, directory := p.load(t)

	toot, err := Compose(certificate, directory, DefaultLimits, "public")
	if err != nil {
		t.Fatal(err)
	}
	p.golden(t, "1970-001-public.toot", toot.Text)

	// The malformed account in the first list is skipped, and the ORCID match
	// takes the next row that has one; an ORCID-less person is matched by name.
	wantMentioned := []string{"@carberry@example.social", "@else@example.social", "@inst@example.social", "@venue@example.social"}
	if strings.Join(toot.Mentioned, " ") != strings.Join(wantMentioned, " ") {
		t.Errorf("mentioned = %v, want %v", toot.Mentioned, wantMentioned)
	}
	wantUnmatched := []string{"Fake Author Without ORCID", "Fake Author Without Handle"}
	if strings.Join(toot.Unmatched, "|") != strings.Join(wantUnmatched, "|") {
		t.Errorf("unmatched = %v, want %v", toot.Unmatched, wantUnmatched)
	}
}

// A direct toot in development must not notify anybody it names.
func TestComposeDirectDefusesEveryMention(t *testing.T) {
	p := newPublished(t)
	certificate, directory := p.load(t)
	certificate.PaperTitle = "@home is where the code is"

	toot, err := Compose(certificate, directory, DefaultLimits, "direct")
	if err != nil {
		t.Fatal(err)
	}
	p.golden(t, "1970-001-direct.toot", toot.Text)
	if !toot.Defused {
		t.Error("the toot does not say it is defused")
	}
	for _, line := range strings.Split(toot.Text, "\n") {
		for _, word := range strings.Fields(strings.Trim(line, "()")) {
			if strings.HasPrefix(strings.TrimLeft(word, "("), "@") {
				t.Errorf("a mention survived: %q in %q", word, line)
			}
		}
	}
}

func TestMissingFilesMentionNobody(t *testing.T) {
	p := newPublished(t)
	for _, path := range []string{announcetest.Persons, announcetest.Venues, announcetest.Codecheckers, announcetest.Institutional} {
		p.Remove(path)
	}
	certificate, directory := p.load(t)

	toot, err := Compose(certificate, directory, DefaultLimits, "public")
	if err != nil {
		t.Fatal(err)
	}
	if len(toot.Mentioned) != 0 {
		t.Errorf("mentioned %v with no data", toot.Mentioned)
	}
	if len(toot.Unmatched) != 6 {
		t.Errorf("unmatched = %v, want the five people and the venue", toot.Unmatched)
	}
	if strings.Contains(toot.Text, "@") {
		t.Errorf("the toot has an @ with no data: %s", toot.Text)
	}
}

func TestAnUnpublishedCertificateIsAnError(t *testing.T) {
	p := newPublished(t)
	if _, _, err := Load(p.Services, p.Settings, "1970-002"); err == nil {
		t.Fatal("an unpublished certificate loaded")
	}
}

// The register's own record of a person wins over the lists people fill in
// about themselves. CSVs keeps the order of the sources, whichever answers
// first; this holds Load to putting persons.csv ahead of the lists.
func TestPersonsWinsOverTheLists(t *testing.T) {
	p := newPublished(t)
	const orcid = "0000-0000-0000-0009"
	p.Text(announcetest.Codecheckers,
		"name,handle,ORCID,fediverse\nA Codechecker,@a,"+orcid+",@list@example.social\n")
	p.Text(announcetest.Persons, "orcid,wikidata,fediverse\n"+orcid+",,@register@example.social\n")

	_, directory := p.load(t)
	if got := directory.Person(Person{ORCID: orcid}); got != "@register@example.social" {
		t.Errorf("the account is %q, want persons.csv's @register@example.social", got)
	}
}

func TestLongToots(t *testing.T) {
	p := newPublished(t)
	certificate, directory := p.load(t)

	for i := range 40 {
		certificate.Authors = append(certificate.Authors, Person{Name: fmt.Sprintf("Author Number %d", i)})
	}
	toot, err := Compose(certificate, directory, DefaultLimits, "public")
	if err != nil {
		t.Fatal(err)
	}
	if n := Length(toot.Text, 23); n > 500 {
		t.Errorf("the toot is %d characters", n)
	}
	if !strings.Contains(toot.Text, "Josiah Carberry (@carberry@example.social) et al.") {
		t.Errorf("the authors were not shortened first:\n%s", toot.Text)
	}
	if strings.Contains(toot.Text, "…") {
		t.Errorf("the title was cut although et al. was enough:\n%s", toot.Text)
	}

	certificate.PaperTitle = strings.Repeat("A very long title ", 30)
	toot, err = Compose(certificate, directory, DefaultLimits, "public")
	if err != nil {
		t.Fatal(err)
	}
	if n := Length(toot.Text, 23); n > 500 {
		t.Errorf("the toot is %d characters", n)
	}
	if !strings.Contains(toot.Text, "…") {
		t.Errorf("the title was not cut:\n%s", toot.Text)
	}
}

func TestLengthCountsTheWayMastodonDoes(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"hello", 5},
		{"🎉 ok", 4},
		{"see https://doi.org/10.5281/zenodo.14279041 now", 4 + 23 + 4},
		{"hi @someone@example.social", 3 + 8},
	}
	for _, c := range cases {
		if got := Length(c.text, 23); got != c.want {
			t.Errorf("Length(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestHandle(t *testing.T) {
	for value, want := range map[string]string{
		"@codecheck@fediscience.org":         "@codecheck@fediscience.org",
		" codecheck@FediScience.org":         "@codecheck@fediscience.org",
		"https://fediscience.org/@codecheck": "",
		"@codecheck":                         "",
		"NA":                                 "",
	} {
		if got := Handle(value); got != want {
			t.Errorf("Handle(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestAnnounced(t *testing.T) {
	certificate := Certificate{ID: "1970-001", Page: "https://example.org/1970-001/"}
	statuses := []mastodon.Posted{
		{URL: "https://example.social/1", Content: "<p>something else</p>"},
		{URL: "https://example.social/2", Content: "<p>New CODECHECK certificate 1970-001</p>"},
	}
	if url, found := Announced(statuses, certificate); !found || url != "https://example.social/2" {
		t.Errorf("Announced = %q, %v", url, found)
	}
	if _, found := Announced(statuses[:1], certificate); found {
		t.Error("an unrelated status counted as the announcement")
	}
}

func TestGIFHasAtMostFourFrames(t *testing.T) {
	p := newPublished(t)
	p.Pages(t, 5)
	certificate, _ := p.load(t)

	attachment, err := GIF(p.Services, certificate, 0)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(attachment.GIF))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Image) != 4 || attachment.Frames != 4 {
		t.Errorf("%d frames, want 4", len(decoded.Image))
	}
	if width := decoded.Config.Width; width != 800 {
		t.Errorf("width = %d, want 800", width)
	}
	if decoded.Delay[0] != 200 {
		t.Errorf("delay = %d, want 200", decoded.Delay[0])
	}

	// Over a limit, it gets narrower and then shorter before it gives up.
	small, err := GIF(p.Services, certificate, int64(len(attachment.GIF)-1))
	if err != nil {
		t.Fatal(err)
	}
	if config, err := gif.DecodeConfig(bytes.NewReader(small.GIF)); err != nil || config.Width != 600 {
		t.Errorf("width = %d (%v), want the narrower fallback", config.Width, err)
	}
	if _, err := GIF(p.Services, certificate, 10); err == nil {
		t.Error("an attachment over any limit was not refused")
	}
}

func TestGIFNeedsAPage(t *testing.T) {
	p := newPublished(t)
	certificate, _ := p.load(t)
	if _, err := GIF(p.Services, certificate, 0); err == nil {
		t.Fatal("a certificate without page images gave an animation")
	}
}

// The pages are fetched together, but the animation still ends at the first
// missing page and keeps their order.
func TestGIFStopsAtTheFirstMissingPage(t *testing.T) {
	p := newPublished(t)
	p.Pages(t, 4)
	p.Remove(announcetest.PagePath(3))
	certificate, _ := p.load(t)

	attachment, err := GIF(p.Services, certificate, 0)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Frames != 2 {
		t.Errorf("%d frames, want the 2 before the gap", attachment.Frames)
	}
}

// Mastodon reads a mention after almost any character, so every one of these
// would notify alice from a direct toot.
func TestDefuseRemovesEveryMention(t *testing.T) {
	for _, text := range []string{
		"@alice@example.org", "Re:@alice@example.org", "“@alice@example.org”", "(@alice)", "a @@alice b",
		"@alice @bob", "x,@alice;@bob",
	} {
		if got := defuse(text); regexp.MustCompile(`(^|[^\p{L}\p{N}_/=])@+[\p{L}\p{N}_]`).MatchString(got) {
			t.Errorf("defuse(%q) = %q still mentions someone", text, got)
		}
	}
	// Not mentions, left alone: an address, and a URL path.
	for _, text := range []string{"mail me at alice@example.org", "https://example.social/@alice"} {
		if got := defuse(text); got != text {
			t.Errorf("defuse(%q) = %q, want it unchanged", text, got)
		}
	}
}

// A page that failed to load is an error, not the end of the certificate.
func TestGIFReportsAPageThatFailed(t *testing.T) {
	p := newPublished(t)
	p.Pages(t, 3)
	p.Status(announcetest.PagePath(2), 503)
	certificate, _ := p.load(t)

	if _, err := GIF(p.Services, certificate, 0); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want the 503 of page 2", err)
	}
}

// A page far taller than any real certificate's is refused before it is
// scaled, rather than risking the deployment's memory limit; see
// codecheckers/chekhov#32.
func TestGIFRefusesATallPage(t *testing.T) {
	p := newPublished(t)
	tall := image.NewGray(image.Rect(0, 0, 100, maxPageHeight+1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, tall); err != nil {
		t.Fatal(err)
	}
	p.Bytes(announcetest.PagePath(1), buf.Bytes())
	certificate, _ := p.load(t)

	_, err := GIF(p.Services, certificate, 0)
	if err == nil || !strings.Contains(err.Error(), "px tall") {
		t.Errorf("error = %v, want the page refused as too tall", err)
	}
}
