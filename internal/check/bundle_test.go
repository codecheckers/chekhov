package check

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The bundle of a file on disk is the directory around it.
func TestALocalBundleReadsWhatIsBesideTheFile(t *testing.T) {
	bundle := newLocalBundle("../../testdata/valid-2.0")

	raw, err := bundle.Read("codecheck.yml")
	if err != nil || len(raw) == 0 {
		t.Fatalf("read the configuration: %v", err)
	}
	if name := codecheckDir(mustList(t, bundle, "")); name != "codecheck" {
		t.Errorf("the codecheck directory was found as %q", name)
	}

	for name, expected := range map[string]bool{
		"figure1.png":        true,
		"results/table1.csv": true,
		"nothing-here.png":   false,
		// A directory is not the file the manifest names, and a repository
		// would answer 404 for it: one bundle, one verdict.
		"results": false,
	} {
		found, err := bundle.Exists(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if found != expected {
			t.Errorf("Exists(%q) = %v", name, found)
		}
	}
}

// A directory is read once however many rules ask about it: three do, and a
// manifest asks a file at a time.
func TestABundleReadsADirectoryOnce(t *testing.T) {
	readings := 0
	remembering := &memo{readDir: func(string) ([]BundleEntry, error) {
		readings++
		return []BundleEntry{{Name: "codecheck", IsDir: true}}, nil
	}}

	for range 3 {
		if _, err := remembering.List(""); err != nil {
			t.Fatalf("list: %v", err)
		}
	}
	if readings != 1 {
		t.Errorf("the directory was read %d times", readings)
	}
}

// A server that will not answer is not a missing file. Reported as absence, a
// rate limit would tell a codechecker their manifest files are not committed.
func TestARateLimitIsNotAMissingFile(t *testing.T) {
	stub := newStub(t)
	stub.status("/raw/codecheckers/demo/HEAD/figure1.png", http.StatusTooManyRequests)
	bundle := bundleFor(RepositorySpec{Type: "github", Path: "codecheckers/demo"}, stub.services)

	found, err := bundle.Exists("figure1.png")
	if found {
		t.Error("a rate-limited file was reported as present")
	}
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("a rate limit should block the check, not answer it: %v", err)
	}
}

// A manifest path under a directory the node does not have is that one file
// missing, not a bundle nobody could look in.
func TestAnOSFFolderThatIsNotThereIsAMissingFile(t *testing.T) {
	stub := newStub(t)
	stub.json("/osf/nodes/ab12c/files/osfstorage/", `{"data": [
	  {"attributes": {"name": "codecheck.yml", "kind": "file"}}
	], "links": {}}`)
	bundle := bundleFor(RepositorySpec{Type: "osf", Path: "ab12c"}, stub.services)

	found, err := bundle.Exists("results/table1.csv")
	if err != nil {
		t.Errorf("a missing folder should be a missing file: %v", err)
	}
	if found {
		t.Error("a file under a folder that is not there was reported as present")
	}
}

// OSF answers ten entries at a time. A listing read only as far as the first
// page reports everything after it as missing.
func TestAnOSFListingIsReadWhole(t *testing.T) {
	stub := newStub(t)
	stub.json("/osf/nodes/ab12c/files/osfstorage/", fmt.Sprintf(`{"data": [
	  {"attributes": {"name": "codecheck.yml", "kind": "file"}}
	], "links": {"next": %q}}`, stub.url("/osf/nodes/ab12c/files/osfstorage/page2")))
	stub.json("/osf/nodes/ab12c/files/osfstorage/page2", `{"data": [
	  {"attributes": {"name": "codecheck", "kind": "folder"}}
	], "links": {}}`)
	bundle := bundleFor(RepositorySpec{Type: "osf", Path: "ab12c"}, stub.services)

	if name := codecheckDir(mustList(t, bundle, "")); name != "codecheck" {
		t.Errorf("the second page of the listing was not read: %q", name)
	}
}

// A Zenodo record is flat, so its directories are whatever prefix the file
// names share.
func TestZenodoDirectoriesComeFromTheFileNames(t *testing.T) {
	stub := newStub(t)
	stub.json("/zenodo/records/3674056", `{"files": [
	  {"key": "codecheck.yml"},
	  {"key": "codecheck/codecheck.pdf"},
	  {"key": "codecheck/outputs/figure1.png"}
	]}`)
	bundle := bundleFor(RepositorySpec{Type: "zenodo", Path: "3674056"}, stub.services)

	if name := codecheckDir(mustList(t, bundle, "")); name != "codecheck" {
		t.Errorf("the codecheck directory was found as %q", name)
	}
	inside := mustList(t, bundle, "codecheck")
	if len(inside) != 2 || inside[0].Name != "codecheck.pdf" || !inside[1].IsDir {
		t.Errorf("the codecheck directory holds %+v", inside)
	}
}

func mustList(t *testing.T, bundle Bundle, dir string) []BundleEntry {
	t.Helper()
	entries, err := bundle.List(dir)
	if err != nil {
		t.Fatalf("list %q: %v", dir, err)
	}
	return entries
}
