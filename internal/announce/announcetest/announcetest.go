// Package announcetest publishes the fake certificate 1970-001 on a test
// server, for the tests of announce and of the bot that runs it.
//
// It is imported by tests only.
package announcetest

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// index is the certificate as the codecheck R package publishes it, a copy of
// the testing register's docs/1970-001/index.json.
//
//go:embed index.json
var index []byte

// Paths the published data is served at.
const (
	Index         = "/certs/1970-001/index.json"
	Persons       = "/raw/codecheckers/testing/HEAD/persons.csv"
	Venues        = "/raw/codecheckers/testing/HEAD/venues.csv"
	Codecheckers  = "/lists/codecheckers.csv"
	Institutional = "/lists/institutional.csv"
)

// A Published certificate, and what a command needs to read it.
type Published struct {
	*testserver.Server
	Services *check.Services
	Settings *config.Settings
}

// New publishes 1970-001 with its register data and no page images; add them
// with Pages.
//
// The data covers every way a person is matched or not: an ORCID in
// persons.csv, an ORCID whose first list row has a malformed account and whose
// second has one, a name without ORCID, and people with no account at all.
func New(t testing.TB) *Published {
	t.Helper()
	server := testserver.New(t)
	settings := &config.Settings{}
	settings.Chekhov.Env.Certificates = server.URL + "/certs/{id}"
	settings.Chekhov.Env.CodecheckerLists = []string{server.At(Codecheckers), server.At(Institutional)}
	settings.Chekhov.Mastodon.Visibility = "direct"

	server.Bytes(Index, index)
	server.Text(Persons,
		"orcid,wikidata,fediverse\n0000-0000-0000-0001,,\n0000-0002-1825-0097,,@carberry@example.social\n")
	server.Text(Venues,
		"name,longname,label,fediverse,hashtags\nFAKE PeerJ Computer Science,FAKE,journal,@venue@example.social,FAKETesting;#OpenScience\n")
	server.Text(Codecheckers,
		"name,handle,ORCID,fediverse\nFake Codechecker,@fake,0000-0000-0000-0002,https://example.social/@not-a-handle\nSomeone Else,@else,0000-0000-0000-0002,@else@example.social\n")
	server.Text(Institutional,
		"name,handle,ORCID,institution,fediverse\nFake Institutional Codechecker,@inst,NA,Lab,inst@example.social\n")

	return &Published{
		Server:   server,
		Services: &check.Services{HTTP: server.Client(), Register: "codecheckers/testing", RawContent: server.URL + "/raw"},
		Settings: settings,
	}
}

// Pages publishes cert_1.png to cert_n.png, each a little darker than the one
// before, so that no two frames are the same.
func (p *Published) Pages(t testing.TB, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		p.Bytes(PagePath(i), page(t, uint8(255-i*40)))
	}
}

// PagePath is where page n of 1970-001 is served.
func PagePath(n int) string { return fmt.Sprintf("/certs/1970-001/cert_%d.png", n) }

// page is a small certificate-like page: grey lines of text on white.
func page(t testing.TB, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 620, 877))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := 100; y < 750; y += 30 {
		for x := 50; x < 550; x++ {
			img.SetGray(x, y, color.Gray{Y: shade})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
