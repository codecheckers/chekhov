package build

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The Go release the module is built with.
//
// go.mod names the oldest release the module accepts, and the workflows pin
// the release CI tests with. Both have to be the same release, so that CI
// tests what the module claims, and that release has to be one Go still
// supports - the two newest - or the toolchain gets no security fixes. The
// first is a property of this tree and is checked offline; the second is news
// about the world and is checked weekly, in the integration workflow's own
// job. See CLAUDE.md -> Decisions worth keeping.

// goDirective is the go line of go.mod, as a release: "1.26" for go 1.26.0.
var goDirective = regexp.MustCompile(`(?m)^go (\d+\.\d+)(?:\.\d+)?\s*$`)

// goPin is a release a workflow pins for actions/setup-go.
var goPin = regexp.MustCompile(`go-version:\s*['"]?([0-9.]+)['"]?`)

// buildpackPin is the release docs/deployment.md tells the buildpack to build
// with: "1.26" for BP_GO_VERSION=1.26.*.
var buildpackPin = regexp.MustCompile(`BP_GO_VERSION=([0-9]+\.[0-9]+)\.\*`)

// moduleRelease is the Go release go.mod names.
func moduleRelease(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	found := goDirective.FindSubmatch(raw)
	if found == nil {
		t.Fatal("go.mod has no go line")
	}
	return string(found[1])
}

// Every workflow tests with the release go.mod names, so that what CI proves
// is what the module claims.
func TestTheWorkflowsPinTheReleaseGoModNames(t *testing.T) {
	want := moduleRelease(t)
	workflows, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(workflows) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	pins := 0
	for _, workflow := range workflows {
		raw, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatal(err)
		}
		for _, pin := range goPin.FindAllSubmatch(raw, -1) {
			pins++
			if got := string(pin[1]); got != want {
				t.Errorf("%s pins Go %s, and go.mod names %s", filepath.Base(workflow), got, want)
			}
		}
	}
	if pins == 0 {
		t.Error("no workflow pins a Go release")
	}
}

// The deployment builds with the release go.mod names too, so that what runs
// is what CI tested: the buildpack otherwise takes its newest Go. The pin
// lives in each app's configuration, so this holds the page that sets it.
func TestTheDeploymentDocsPinTheReleaseGoModNames(t *testing.T) {
	want := moduleRelease(t)
	raw, err := os.ReadFile("../../docs/deployment.md")
	if err != nil {
		t.Fatal(err)
	}
	pins := buildpackPin.FindAllSubmatch(raw, -1)
	if len(pins) == 0 {
		t.Fatal("docs/deployment.md sets no BP_GO_VERSION")
	}
	for _, pin := range pins {
		if got := string(pin[1]); got != want {
			t.Errorf("docs/deployment.md pins Go %s for the buildpack, and go.mod names %s", got, want)
		}
	}
}

// The release go.mod names is one Go still supports. go.dev lists exactly
// those: the stable releases it answers with are the two that get fixes.
//
// An integration test, because it asks go.dev: run weekly by its own job, so
// that a release going out of support is news there and never turns a pull
// request red.
func TestIntegrationGoReleaseIsSupported(t *testing.T) {
	if os.Getenv("CHEKHOV_INTEGRATION") == "" {
		t.Skip("asks go.dev; set CHEKHOV_INTEGRATION=1")
	}
	want := moduleRelease(t)

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get("https://go.dev/dl/?mode=json")
	if err != nil {
		t.Fatalf("could not ask go.dev which releases are supported: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("go.dev answered %s", response.Status)
	}
	var releases []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		t.Fatalf("could not read go.dev's answer: %v", err)
	}

	var supported []string
	for _, release := range releases {
		if !release.Stable {
			continue
		}
		// go1.26.10 is a patch of release 1.26.
		if strings.HasPrefix(release.Version, "go"+want+".") {
			return
		}
		supported = append(supported, release.Version)
	}
	t.Errorf("go.mod names Go %s, which Go no longer supports; go.dev lists %v. "+
		"Move go.mod and the workflows' go-version to one of those.", want, supported)
}
