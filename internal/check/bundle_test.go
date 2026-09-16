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

// A bundle states its licence the way its platform does: a git repository in a
// file, an archive in its metadata, where there is no file to find.
func TestABundleStatesItsLicenceItsOwnWay(t *testing.T) {
	stub := newStub(t)
	stub.json("/github/repos/codecheckers/demo/contents/", `[
	  {"name": "LICENSE", "type": "file"},
	  {"name": "licences", "type": "dir"}
	]`)
	// The current API answers an object, the older one a plain string, and a
	// record deposited last month a rights list. All three are in the register.
	stub.json("/zenodo/records/1", `{"metadata": {"license": {"id": "cc-by-4.0"}}}`)
	stub.json("/zenodo/records/2", `{"metadata": {"license": "CC-BY-4.0"}}`)
	stub.json("/zenodo/records/3", `{"metadata": {"rights": [{"id": "mit"}]}}`)
	stub.json("/zenodo/records/4", `{"files": [{"key": "codecheck.yml"}], "metadata": {}}`)
	stub.json("/osf/nodes/ab12c/", fmt.Sprintf(`{"data": {"relationships": {"license":
	  {"links": {"related": {"href": %q}}}}}}`, stub.url("/osf/licenses/cc-by")))
	stub.json("/osf/licenses/cc-by", `{"data": {"attributes": {"name": "CC-By Attribution 4.0 International"}}}`)
	// A node that has chosen nothing carries the licence called "No license",
	// which states nothing at all.
	stub.json("/osf/nodes/none1/", fmt.Sprintf(`{"data": {"relationships": {"license":
	  {"links": {"related": {"href": %q}}}}}}`, stub.url("/osf/licenses/no-license")))
	stub.json("/osf/licenses/no-license", `{"data": {"attributes": {"name": "No license"}}}`)
	stub.json("/osf/nodes/none1/files/osfstorage/", `{"data": [], "links": {}}`)

	for _, test := range []struct {
		spec    string
		licence string
	}{
		{spec: "github::codecheckers/demo", licence: "LICENSE"},
		{spec: "zenodo::1", licence: "cc-by-4.0"},
		{spec: "zenodo::2", licence: "CC-BY-4.0"},
		{spec: "zenodo::3", licence: "mit"},
		{spec: "zenodo::4", licence: ""},
		{spec: "osf::ab12c", licence: "CC-By Attribution 4.0 International"},
		{spec: "osf::none1", licence: ""},
	} {
		spec, err := ParseRepositorySpec(test.spec)
		if err != nil {
			t.Fatalf("%s: %v", test.spec, err)
		}
		licence, err := bundleFor(spec, stub.services).Licence()
		if err != nil {
			t.Errorf("%s: %v", test.spec, err)
		}
		if licence != test.licence {
			t.Errorf("%s states %q, want %q", test.spec, licence, test.licence)
		}
	}
}

// A local bundle still reads the file, which is what the codecheck R package
// does and what a repository on disk has.
func TestALocalBundleStatesItsLicenceInAFile(t *testing.T) {
	licence, err := newLocalBundle("../../testdata/valid-2.0").Licence()
	if err != nil {
		t.Fatalf("licence: %v", err)
	}
	if licence != "LICENSE" {
		t.Errorf("the licence file was found as %q", licence)
	}
}
