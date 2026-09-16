package announce

import (
	"bytes"
	"image/gif"
	"testing"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
)

// Reads the testing register's fake certificate the way a development
// deployment does:
//
//	CHEKHOV_INTEGRATION=1 go test ./internal/announce/ -run Integration
func TestIntegrationTestingRegisterCertificate(t *testing.T) {
	services := check.ServicesFromEnv()
	if !services.Enabled() {
		t.Skip("set CHEKHOV_INTEGRATION=1 to read the published certificate")
	}
	settings, err := config.Load("development")
	if err != nil {
		t.Fatal(err)
	}

	certificate, directory, err := Load(services, settings, "1970-001")
	if err != nil {
		t.Fatal(err)
	}
	toot, err := Compose(certificate, directory, DefaultLimits, settings.Mastodon().Visibility)
	if err != nil {
		t.Fatal(err)
	}
	if len(toot.Mentioned) == 0 || len(toot.Unmatched) == 0 {
		t.Errorf("the fixture should give both mentions and people without an account: %+v", toot)
	}

	attachment, err := GIF(services, certificate, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gif.DecodeAll(bytes.NewReader(attachment.GIF)); err != nil {
		t.Fatal(err)
	}
	if attachment.Frames != maxFrames {
		t.Errorf("%d frames, want %d from the five pages published", attachment.Frames, maxFrames)
	}
	t.Logf("%d characters, %d frames, %d bytes\n%s", Length(toot.Text, 23), attachment.Frames, len(attachment.GIF), toot.Text)
}
