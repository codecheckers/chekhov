package check

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestParseRepositorySpec(t *testing.T) {
	cases := []struct {
		spec    string
		kind    string
		path    string
		subPath string
		err     bool
	}{
		{spec: "github::codecheckers/demo", kind: "github", path: "codecheckers/demo"},
		// register.csv writes a configuration in a sub-directory this way.
		{spec: "github::codecheckers/demo|paper/check", kind: "github",
			path: "codecheckers/demo", subPath: "paper/check"},
		{spec: "gitlab::group/project", kind: "gitlab", path: "group/project"},
		{spec: "osf::ab12c", kind: "osf", path: "ab12c"},
		{spec: "zenodo::3674056", kind: "zenodo", path: "3674056"},
		{spec: "zenodo-sandbox::123", kind: "zenodo-sandbox", path: "123"},
		{spec: "https://github.com/codecheckers/demo", err: true},
		{spec: "svn::codecheckers/demo", err: true},
		{spec: "github::", err: true},
		{spec: "", err: true},
	}

	for _, testCase := range cases {
		parsed, err := ParseRepositorySpec(testCase.spec)
		if testCase.err {
			if err == nil {
				t.Errorf("%q should not parse", testCase.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", testCase.spec, err)
			continue
		}
		if parsed.Type != testCase.kind || parsed.Path != testCase.path ||
			parsed.SubPath != testCase.subPath {
			t.Errorf("%q parsed as %+v", testCase.spec, parsed)
		}
	}
}

func TestIsRepositorySpec(t *testing.T) {
	if !IsRepositorySpec("github::codecheckers/demo") {
		t.Error("a repository spec should be recognised as one")
	}
	if IsRepositorySpec("testdata/valid-2.0/codecheck.yml") {
		t.Error("a path is not a repository spec")
	}
}

// Reading a configuration from each of the four kinds of repository the
// register knows.
func TestFromRepository(t *testing.T) {
	configuration := "---\ncertificate: 2026-001\n"

	cases := []struct {
		name string
		spec string
		// bundle is what the rules about the bundle can say for this source
		// now that they read it over the same service the configuration came
		// from (chekhov#17): a verdict when it can be listed, a skip when the
		// source cannot say.
		bundle Outcome
		route  func(*stub)
	}{
		{
			name:   "github",
			spec:   "github::codecheckers/demo",
			bundle: OutcomeOK,
			route: func(s *stub) {
				s.text("/raw/codecheckers/demo/HEAD/codecheck.yml", configuration)
				s.json("/github/repos/codecheckers/demo/contents/", `[
				  {"name": "codecheck", "type": "dir"},
				  {"name": "LICENSE", "type": "file"}
				]`)
			},
		},
		{
			// The bundle is the sub-directory, not the repository root: the
			// bundle knows where it is, so no check has to remember.
			name:   "github with the configuration in a sub-directory",
			spec:   "github::codecheckers/demo|paper/check",
			bundle: OutcomeOK,
			route: func(s *stub) {
				s.text("/raw/codecheckers/demo/HEAD/paper/check/codecheck.yml", configuration)
				s.json("/github/repos/codecheckers/demo/contents/paper/check", `[
				  {"name": "codecheck", "type": "dir"}
				]`)
			},
		},
		{
			name:   "gitlab on main",
			spec:   "gitlab::group/project",
			bundle: OutcomeOK,
			route: func(s *stub) {
				s.text("/gitlab/group/project/-/raw/main/codecheck.yml", configuration)
				// The stub matches the decoded path; GitLab itself takes the
				// project as one escaped segment.
				s.json("/gitlab/api/v4/projects/group/project/repository/tree", `[
				  {"name": "codecheck", "type": "tree"}
				]`)
			},
		},
		{
			// GitLab has no HEAD alias, and the older projects are on master.
			name:   "gitlab falling back to master",
			spec:   "gitlab::group/project",
			bundle: OutcomeSkipped,
			route: func(s *stub) {
				s.status("/gitlab/group/project/-/raw/main/codecheck.yml", http.StatusNotFound)
				s.text("/gitlab/group/project/-/raw/master/codecheck.yml", configuration)
			},
		},
		{
			// A record with no codecheck/ in it is a finding, where before it
			// was four skips.
			name:   "osf",
			spec:   "osf::ab12c",
			bundle: OutcomeWarning,
			route: func(s *stub) {
				s.json("/osf/nodes/ab12c/files/osfstorage/", fmt.Sprintf(`{"data": [
				  {"attributes": {"name": "README.md"}, "links": {"download": %q}},
				  {"attributes": {"name": "codecheck.yml"}, "links": {"download": %q}}
				]}`, s.url("/osf/download/README.md"), s.url("/osf/download/codecheck.yml")))
				s.text("/osf/download/codecheck.yml", configuration)
			},
		},
		{
			name:   "zenodo",
			spec:   "zenodo::3674056",
			bundle: OutcomeWarning,
			route: func(s *stub) {
				// The older records answer with filename and links.download,
				// the newer ones with key and links.self.
				s.json("/zenodo/records/3674056", fmt.Sprintf(`{"files": [
				  {"key": "codecheck.yml", "links": {"self": %q}}
				]}`, s.url("/zenodo/download/codecheck.yml")))
				s.text("/zenodo/download/codecheck.yml", configuration)
			},
		},
		{
			name:   "zenodo sandbox, an older record shape",
			spec:   "zenodo-sandbox::123",
			bundle: OutcomeWarning,
			route: func(s *stub) {
				s.json("/zenodo-sandbox/records/123", fmt.Sprintf(`{"files": [
				  {"filename": "codecheck.yml", "links": {"download": %q}}
				]}`, s.url("/zenodo-sandbox/download/codecheck.yml")))
				s.text("/zenodo-sandbox/download/codecheck.yml", configuration)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newStub(t)
			testCase.route(server)

			context, err := FromRepository(testCase.spec, server.services)
			if err != nil {
				t.Fatalf("%s: %v", testCase.spec, err)
			}
			if context.Config.Certificate != "2026-001" {
				t.Errorf("read %q as the certificate", context.Config.Certificate)
			}
			if context.Label != testCase.spec {
				t.Errorf("the report should be labelled with the spec, not %q", context.Label)
			}
			// The file was asked for by name, so that rule has its answer;
			// the bundle around it is read over the same service.
			report, err := Run(context, "2.0", false)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := resultFor(t, report, "CC-CFG-003").Outcome; got != OutcomeOK {
				t.Errorf("CC-CFG-003: outcome %q, want ok", got)
			}
			if got := resultFor(t, report, "CC-BUN-002").Outcome; got != testCase.bundle {
				t.Errorf("CC-BUN-002: outcome %q, want %q (%s)", got, testCase.bundle,
					resultFor(t, report, "CC-BUN-002").Detail)
			}
		})
	}
}

// A manifest path is relative to the codecheck.yml, so a configuration in a
// sub-directory looks for its files there.
func TestManifestPathsFollowTheSubDirectory(t *testing.T) {
	server := newStub(t)
	server.text("/raw/codecheckers/demo/HEAD/paper/check/codecheck.yml", `---
manifest:
  - file: figure1.png
    comment: Figure 1
repository: https://github.com/codecheckers/demo
certificate: 2026-001
`)
	server.text("/raw/codecheckers/demo/HEAD/paper/check/figure1.png", "PNG")

	context, err := FromRepository("github::codecheckers/demo|paper/check", server.services)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	report, err := Run(context, "2.0", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := resultFor(t, report, "CC-BUN-001").Outcome; got != OutcomeOK {
		t.Errorf("CC-BUN-001: outcome %q, want ok (%s)",
			got, resultFor(t, report, "CC-BUN-001").Detail)
	}
}

func TestFromRepositoryReportsWhatWentWrong(t *testing.T) {
	server := newStub(t)

	if _, err := FromRepository("svn::codecheckers/demo", server.services); err == nil {
		t.Error("an unknown repository type should be an error")
	}
	if _, err := FromRepository("github::codecheckers/demo", server.services); err == nil {
		t.Error("a repository without a codecheck.yml should be an error")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("the error should say what the server answered: %v", err)
	}
	if _, err := FromRepository("github::codecheckers/demo", nil); err == nil {
		t.Error("reading a repository without services should be an error")
	}
}
