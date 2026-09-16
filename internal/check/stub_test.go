package check

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The offline half of the coverage for the checks that need external services.
//
// The cassette tests replay what the real services answered, which proves the
// payloads are read correctly. These stubs do the other half: the answers a
// published CODECHECK almost never gives - a concept DOI, a superseded record,
// a name that does not match, a publisher blocking robots - and the skips that
// must not be mistaken for passes.
//
// Every case names the rule it exercises, and TestOfflineCoverageOfServiceRules
// fails when a rule that needs a service has no case here and none in the
// cassette tests.

// stub is a fake of every service, on one test server. Each service gets its
// own path prefix so that the routes cannot collide.
//
// Routes live in a map rather than in a ServeMux, so that a case can serve
// everything a certificate in good order gets and then replace the one route it
// is about; a ServeMux panics on the second registration of a path.
type stub struct {
	server   *httptest.Server
	routes   map[string]http.HandlerFunc
	services *Services
}

func newStub(t *testing.T) *stub {
	t.Helper()

	server := &stub{routes: map[string]http.HandlerFunc{}}
	server.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if handler, ok := server.routes[r.URL.Path]; ok {
				handler(w, r)
				return
			}
			http.NotFound(w, r)
		}))
	t.Cleanup(server.server.Close)

	url := server.server.URL
	client := server.server.Client()
	// An offline test that reaches the real internet is not an offline test.
	// Anything not addressed to the stub fails, and says which URL it was.
	client.Transport = localOnly{host: server.server.Listener.Addr().String(), base: client.Transport}
	server.services = &Services{
		HTTP:          client,
		Register:      "codecheckers/testing",
		Zenodo:        url + "/zenodo",
		ORCID:         url + "/orcid",
		Crossref:      url + "/crossref",
		GitHub:        url + "/github",
		RawContent:    url + "/raw",
		GitLab:        url + "/gitlab",
		OSF:           url + "/osf",
		ZenodoSandbox: url + "/zenodo-sandbox",
	}
	return server
}

func (s *stub) handle(path string, handler http.HandlerFunc) {
	s.routes[path] = handler
}

// url is the stub's address for a path, for a fixture that has to carry a link
// to one of its own routes.
func (s *stub) url(path string) string { return s.server.URL + path }

// localOnly keeps the offline suite offline.
type localOnly struct {
	host string
	base http.RoundTripper
}

func (l localOnly) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != l.host {
		return nil, fmt.Errorf("offline test tried to reach %s; point it at the stub server",
			request.URL)
	}
	return l.base.RoundTrip(request)
}

func (s *stub) json(path, body string) {
	s.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
}

func (s *stub) text(path, body string) {
	s.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	})
}

func (s *stub) status(path string, code int) {
	s.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	})
}

// register serves a register.csv, and venues.csv when venues is not empty.
func (s *stub) register(registerCSV, venuesCSV string) {
	s.text("/raw/codecheckers/testing/HEAD/register.csv", registerCSV)
	if venuesCSV == "" {
		s.status("/raw/codecheckers/testing/HEAD/venues.csv", http.StatusNotFound)
		return
	}
	s.text("/raw/codecheckers/testing/HEAD/venues.csv", venuesCSV)
}

// run validates a codecheck.yml against the stubbed services and returns one
// rule's result. The %s in the configuration is the test server's URL, for the
// rules whose subject is a URL taken from the file itself.
func (s *stub) run(t *testing.T, configuration, rule string) RuleResult {
	t.Helper()
	s.services.reset()

	context := FromBytes([]byte(strings.ReplaceAll(configuration, "{{server}}", s.server.URL)))
	report, err := Run(context.WithServices(s.services), "2.0", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return resultFor(t, report, rule)
}

// --- the configurations the cases are built from ---------------------------

// A complete certificate. Every case takes this and changes the one thing it
// is about, so that a failure is about that thing and not about the fixture.
//
// The URLs point at the stub server rather than at doi.org and github.com,
// because the rules that resolve a URL take it from the file: a real-looking
// URL here would be a real request. The DOI and the repository are still
// recognisable inside them, which is what the checks parse out.
const stubConfig = `---
version: https://codecheck.org.uk/spec/config/2.0/
manifest:
  - file: figure1.png
    comment: manuscript Figure 1
paper:
  title: "A paper whose code runs"
  authors:
    - name: Josiah Carberry
      ORCID: 0000-0002-1825-0097
  reference: "{{server}}/doi/10.5555/preprint.1"
codechecker:
  - name: Stephen J. Eglen
    ORCID: 0000-0001-8607-8025
report: "{{server}}/doi/10.5281/zenodo.1234567"
summary: All outputs reproduced.
repository: "{{server}}/github.com/codecheckers/demo"
certificate: 2026-001
`

// with returns the configuration with one line replaced, named by its key.
func with(key, line string) string {
	var out []string
	for _, existing := range strings.Split(stubConfig, "\n") {
		if strings.HasPrefix(existing, key+":") {
			if line != "" {
				out = append(out, line)
			}
			continue
		}
		out = append(out, existing)
	}
	return strings.Join(out, "\n")
}

// withReferenceOther adds a further reference, which 2.0 introduced and no
// published certificate carries yet.
func withReferenceOther(entry string) string {
	const reference = `  reference: "{{server}}/doi/10.5555/preprint.1"`
	return strings.Replace(stubConfig, reference,
		reference+"\n  reference-other:\n    - \""+entry+"\"", 1)
}

const (
	zenodoRecordJSON = `{
	  "id": 1234567,
	  "doi": "10.5281/zenodo.1234567",
	  "conceptdoi": "10.5281/zenodo.1234566",
	  "files": [{"key": "CODECHECK_certificate_2026-001.pdf"}],
	  "metadata": {
	    "title": "CODECHECK Certificate 2026-001",
	    "creators": [{"name": "Eglen, Stephen J.", "orcid": "0000-0001-8607-8025"}],
	    "relations": {"version": [{"index": 0, "is_last": true, "count": 1}]}
	  }
	}`
	orcidPersonJSON = `{"name": {"given-names": {"value": "Stephen"}, "family-name": {"value": "Eglen"}}}`
	carberryJSON    = `{"name": {"given-names": {"value": "Josiah"}, "family-name": {"value": "Carberry"}}}`
	crossrefJSON    = `{"message": {
	  "title": ["A paper whose code runs"],
	  "author": [{"given": "Josiah", "family": "Carberry", "ORCID": "https://orcid.org/0000-0002-1825-0097"}]
	}}`
	registerRow = "Certificate,Repository,Type,Venue,Issue\n" +
		"2026-001,github::codecheckers/demo,journal,GigaScience,42\n"
	venuesRow = "name,longname\nGigaScience,GigaScience\n"
	issueJSON = `{"number": 42, "title": "Carberry | 2026-001", "state": "closed"}`
)

// serveEverythingWell wires the answers a certificate in good order gets, so a
// case only has to override the one route it is about.
func (s *stub) serveEverythingWell() {
	s.json("/zenodo/records/1234567", zenodoRecordJSON)
	s.json("/orcid/0000-0001-8607-8025/person", orcidPersonJSON)
	s.json("/orcid/0000-0002-1825-0097/person", carberryJSON)
	s.json("/crossref/works/10.5555/preprint.1", crossrefJSON)
	s.json("/github/repos/codecheckers/testing/issues/42", issueJSON)
	s.text("/raw/codecheckers/demo/HEAD/figure1.png", "PNG")
	s.text("/doi/10.5281/zenodo.1234567", "the certificate")
	s.text("/doi/10.5555/preprint.1", "the paper")
	s.text("/github.com/codecheckers/demo", "the repository")
	s.register(registerRow, venuesRow)
}

// --- the cases -------------------------------------------------------------

type stubCase struct {
	name string
	rule string
	// configuration defaults to stubConfig when empty.
	configuration string
	routes        func(*stub)
	want          Outcome
	detail        string // a fragment the detail has to contain
}

var stubCases = []stubCase{
	// --- Zenodo ---
	{
		name:          "the report names the concept DOI, which follows the latest version",
		rule:          "CC-REP-001",
		configuration: with("report", `report: "{{server}}/doi/10.5281/zenodo.1234566"`),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/zenodo/records/1234566", zenodoRecordJSON)
			s.text("/doi/10.5281/zenodo.1234566", "the certificate")
		},
		want:   OutcomeWarning,
		detail: "concept DOI",
	},
	{
		name: "a newer version of the record has been published",
		rule: "CC-REP-002",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/zenodo/records/1234567", strings.Replace(zenodoRecordJSON,
				`{"index": 0, "is_last": true, "count": 1}`,
				`{"index": 0, "is_last": false, "count": 3}`, 1))
		},
		want:   OutcomeWarning,
		detail: "newer version",
	},
	{
		name: "the record has no files, so the certificate cannot be read",
		rule: "CC-REP-003",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/zenodo/records/1234567", strings.Replace(zenodoRecordJSON,
				`"files": [{"key": "CODECHECK_certificate_2026-001.pdf"}],`, `"files": [],`, 1))
		},
		want:   OutcomeWarning,
		detail: "no files",
	},
	{
		name: "the record title does not carry the certificate identifier",
		rule: "CC-REP-004",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/zenodo/records/1234567", strings.Replace(zenodoRecordJSON,
				"CODECHECK Certificate 2026-001", "A certificate", 1))
		},
		want:   OutcomeWarning,
		detail: "does not carry the certificate identifier",
	},
	{
		name: "the record names someone else as creator",
		rule: "CC-REP-005",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/zenodo/records/1234567", strings.Replace(zenodoRecordJSON,
				`"name": "Eglen, Stephen J."`, `"name": "Lovelace, Ada"`, 1))
		},
		want:   OutcomeWarning,
		detail: "not among the record's creators",
	},
	{
		name: "the creator's ORCID is not the codechecker's",
		rule: "CC-REP-006",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/zenodo/records/1234567", strings.Replace(zenodoRecordJSON,
				`"orcid": "0000-0001-8607-8025"`, `"orcid": "0000-0002-1825-0097"`, 1))
		},
		want:   OutcomeWarning,
		detail: "not on the record",
	},
	{
		name: "the record is not there",
		rule: "CC-REP-003",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/zenodo/records/1234567", http.StatusNotFound)
		},
		want:   OutcomeSkipped,
		detail: "could not read the Zenodo record",
	},
	{
		name: "Zenodo answers something that is not JSON",
		rule: "CC-REP-004",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.text("/zenodo/records/1234567", "<html>maintenance</html>")
		},
		want:   OutcomeSkipped,
		detail: "could not read the Zenodo record",
	},

	// --- Crossref ---
	{
		name: "Crossref has a different title",
		rule: "CC-MET-005",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/crossref/works/10.5555/preprint.1", strings.Replace(crossrefJSON,
				"A paper whose code runs", "A quite different paper", 1))
		},
		want:   OutcomeWarning,
		detail: "Crossref says",
	},
	{
		name: "Crossref lists more authors than the file",
		rule: "CC-MET-006",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/crossref/works/10.5555/preprint.1", strings.Replace(crossrefJSON,
				`"author": [`, `"author": [{"given": "Ada", "family": "Lovelace"}, `, 1))
		},
		want:   OutcomeWarning,
		detail: "the file lists 1 author(s), Crossref 2",
	},
	{
		name: "Crossref names an author the file does not",
		rule: "CC-MET-007",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/crossref/works/10.5555/preprint.1", strings.Replace(crossrefJSON,
				`"family": "Carberry"`, `"family": "Lovelace"`, 1))
		},
		want:   OutcomeWarning,
		detail: "Lovelace",
	},
	{
		name: "Crossref has an author ORCID the file lacks",
		rule: "CC-MET-008",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/crossref/works/10.5555/preprint.1", strings.Replace(crossrefJSON,
				"0000-0002-1825-0097", "0000-0002-1825-0098", 1))
		},
		want:   OutcomeWarning,
		detail: "ORCID(s) Crossref has but the file does not",
	},
	{
		name: "Crossref does not know the DOI",
		rule: "CC-MET-005",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/crossref/works/10.5555/preprint.1", http.StatusNotFound)
		},
		want:   OutcomeSkipped,
		detail: "could not ask Crossref",
	},

	// --- ORCID ---
	{
		name: "an ORCID that does not resolve",
		rule: "CC-MET-002",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/orcid/0000-0001-8607-8025/person", http.StatusNotFound)
		},
		want:   OutcomeWarning,
		detail: "0000-0001-8607-8025",
	},
	{
		name: "the ORCID record names someone else",
		rule: "CC-MET-003",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/orcid/0000-0001-8607-8025/person", strings.Replace(orcidPersonJSON,
				"Eglen", "Lovelace", 1))
		},
		want:   OutcomeWarning,
		detail: "is Stephen Lovelace at ORCID",
	},
	{
		name: "no ORCID record can be read at all",
		rule: "CC-MET-003",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/orcid/0000-0001-8607-8025/person", http.StatusInternalServerError)
			s.status("/orcid/0000-0002-1825-0097/person", http.StatusInternalServerError)
		},
		want:   OutcomeSkipped,
		detail: "no ORCID record could be read",
	},

	// --- the register ---
	{
		name: "the certificate identifier is in the register twice",
		rule: "CC-REG-001",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.register(registerRow+"2026-001,github::codecheckers/other,journal,GigaScience,43\n",
				venuesRow)
		},
		want:   OutcomeError,
		detail: "appears 2 times",
	},
	{
		name:          "the certificate number leaves a gap in the year",
		rule:          "CC-REG-002",
		configuration: with("certificate", "certificate: 2026-009"),
		routes: func(s *stub) {
			s.serveEverythingWell()
			// The register stops at 2026-002, so 2026-009 skips seven numbers.
			s.register("Certificate,Repository,Type,Venue,Issue\n"+
				"2026-001,github::codecheckers/demo,journal,GigaScience,41\n"+
				"2026-002,github::codecheckers/demo,journal,GigaScience,42\n", venuesRow)
		},
		want:   OutcomeError,
		detail: "leaves a gap",
	},
	{
		name: "the Repository column has no known prefix",
		rule: "CC-REG-003",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.register("Certificate,Repository,Type,Venue,Issue\n"+
				"2026-001,https://github.com/codecheckers/demo,journal,GigaScience,42\n", venuesRow)
		},
		want:   OutcomeError,
		detail: "malformed repository specification",
	},
	{
		name: "the Type column is not one of the four",
		rule: "CC-REG-004",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.register(strings.Replace(registerRow, ",journal,", ",magazine,", 1), venuesRow)
		},
		want:   OutcomeError,
		detail: "is not one of journal",
	},
	{
		name: "the venue is not listed in venues.csv",
		rule: "CC-REG-005",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.register(registerRow, "name,longname\nSomewhere else,Somewhere else\n")
		},
		want:   OutcomeWarning,
		detail: "is not listed in venues.csv",
	},
	{
		name: "the register carries no venues.csv",
		rule: "CC-REG-005",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.register(registerRow, "")
		},
		want:   OutcomeSkipped,
		detail: "no venues.csv",
	},
	{
		name:          "the certificate is not in the register yet, which is the normal case mid-check",
		rule:          "CC-REG-003",
		configuration: with("certificate", "certificate: 2026-777"),
		routes: func(s *stub) {
			s.serveEverythingWell()
		},
		want:   OutcomeSkipped,
		detail: "not in register.csv yet",
	},
	{
		name: "register.csv cannot be read",
		rule: "CC-REG-001",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/raw/codecheckers/testing/HEAD/register.csv", http.StatusNotFound)
		},
		want:   OutcomeSkipped,
		detail: "could not read the register",
	},

	// --- the checks issue ---
	{
		name: "the issue the register names is gone",
		rule: "CC-REG-006",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/github/repos/codecheckers/testing/issues/42", http.StatusNotFound)
		},
		want:   OutcomeWarning,
		detail: "could not be read",
	},
	{
		name: "the issue title does not carry the certificate identifier",
		rule: "CC-REG-007",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.json("/github/repos/codecheckers/testing/issues/42",
				`{"number": 42, "title": "Carberry, a paper", "state": "open"}`)
		},
		want:   OutcomeWarning,
		detail: "does not carry 2026-001",
	},
	{
		name: "the register names no issue for this certificate",
		rule: "CC-REG-006",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.register("Certificate,Repository,Type,Venue,Issue\n"+
				"2026-001,github::codecheckers/demo,journal,GigaScience,\n", venuesRow)
		},
		want:   OutcomeSkipped,
		detail: "names no checks issue",
	},

	// --- the repository under check ---
	{
		name: "a manifest file is not at the path the manifest declares",
		rule: "CC-BUN-001",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/raw/codecheckers/demo/HEAD/figure1.png", http.StatusNotFound)
		},
		want:   OutcomeError,
		detail: "figure1.png",
	},
	{
		name: "the manifest file is only archived under codecheck/outputs, which is not where it was declared",
		rule: "CC-BUN-001",
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/raw/codecheckers/demo/HEAD/figure1.png", http.StatusNotFound)
			s.text("/raw/codecheckers/demo/HEAD/codecheck/outputs/figure1.png", "PNG")
		},
		want:   OutcomeError,
		detail: "figure1.png",
	},
	{
		name:          "the repository does not answer",
		rule:          "CC-BUN-004",
		configuration: with("repository", `repository: "{{server}}/gone"`),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/gone", http.StatusNotFound)
		},
		want:   OutcomeError,
		detail: "do not answer",
	},

	// --- URLs taken from the file ---
	{
		name:          "the report URL is not there",
		rule:          "CC-CFG-012",
		configuration: with("report", `report: "{{server}}/missing-report"`),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/missing-report", http.StatusNotFound)
		},
		want:   OutcomeInfo,
		detail: "answers 404",
	},
	{
		name:          "the publisher blocks robots, which is not the same as a broken reference",
		rule:          "CC-MET-004",
		configuration: with("  reference", `  reference: "{{server}}/blocked"`),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/blocked", http.StatusForbidden)
		},
		want:   OutcomeSkipped,
		detail: "blocks the check rather than failing it",
	},
	{
		name:          "a rate limit is a block, not a failure",
		rule:          "CC-MET-004",
		configuration: with("  reference", `  reference: "{{server}}/slow-down"`),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/slow-down", http.StatusTooManyRequests)
		},
		want:   OutcomeSkipped,
		detail: "blocks the check rather than failing it",
	},
	{
		name:          "a further reference that does not resolve",
		rule:          "CC-MET-009",
		configuration: withReferenceOther("{{server}}/gone-reference"),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/gone-reference", http.StatusNotFound)
		},
		want:   OutcomeWarning,
		detail: "do not resolve",
	},
	{
		name:          "a server having a bad moment is not a broken reference",
		rule:          "CC-CFG-012",
		configuration: with("report", `report: "{{server}}/unavailable"`),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.status("/unavailable", http.StatusServiceUnavailable)
		},
		want:   OutcomeSkipped,
		detail: "could not say",
	},
	{
		name:          "a further reference that resolves",
		rule:          "CC-MET-009",
		configuration: withReferenceOther("{{server}}/version-of-record"),
		routes: func(s *stub) {
			s.serveEverythingWell()
			s.text("/version-of-record", "the version of record")
		},
		want:   OutcomeOK,
		detail: "1 entry(s) resolve",
	},
}

func TestStubbedServices(t *testing.T) {
	for _, testCase := range stubCases {
		t.Run(testCase.rule+" "+testCase.name, func(t *testing.T) {
			server := newStub(t)
			testCase.routes(server)

			configuration := testCase.configuration
			if configuration == "" {
				configuration = stubConfig
			}

			result := server.run(t, configuration, testCase.rule)
			if result.Outcome != testCase.want {
				t.Errorf("%s: outcome %q, want %q (%s)",
					testCase.rule, result.Outcome, testCase.want, result.Detail)
			}
			if !strings.Contains(result.Detail, testCase.detail) {
				t.Errorf("%s: detail %q does not contain %q",
					testCase.rule, result.Detail, testCase.detail)
			}
		})
	}
}

// A certificate in good order passes everything, against the same stubs. It is
// the control: without it, a case above could be passing because the fixture is
// broken rather than because the check works.
func TestStubbedServicesInGoodOrder(t *testing.T) {
	server := newStub(t)
	server.serveEverythingWell()
	server.services.reset()

	context := FromBytes([]byte(strings.ReplaceAll(stubConfig, "{{server}}", server.server.URL)))
	report, err := Run(context.WithServices(server.services), "2.0", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, result := range report.Results {
		if _, needsService := RequiresService[result.Rule.ID]; !needsService {
			continue
		}
		switch result.Rule.ID {
		case "CC-MET-009":
			// No reference-other in this configuration, so there is nothing to
			// resolve; the rule says so rather than passing.
			continue
		}
		if result.Outcome != OutcomeOK {
			t.Errorf("%s: outcome %q, want ok (%s)",
				result.Rule.ID, result.Outcome, result.Detail)
		}
	}
}

// Every rule that needs a service has to be exercised offline, by a stub case
// or by a cassette. A new rule with an API behind it then fails the fast suite,
// not only the weekly integration run.
func TestOfflineCoverageOfServiceRules(t *testing.T) {
	covered := map[string]bool{}
	for _, testCase := range stubCases {
		covered[testCase.rule] = true
	}
	for _, ids := range cassetteRules {
		for _, id := range ids {
			covered[id] = true
		}
	}

	for id, needs := range RequiresService {
		if !covered[id] {
			t.Errorf("%s needs %s and has no offline test: add a stub case or a cassette assertion",
				id, needs)
		}
	}
}
