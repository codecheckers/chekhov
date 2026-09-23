package check

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/config"
)

// Services is the outside world: the APIs and files a check needs when it
// cannot reach a verdict from the codecheck.yml alone.
//
// It is nil in the offline test suite, and then every check that needs it
// skips with "external services not enabled". That is deliberate: a check that
// silently passes when it could not run is worse than one that says it did not
// run. Enable it with CHEKHOV_INTEGRATION=1.
type Services struct {
	// Access is everything a deployment configures. It is a field rather than
	// a scattering of them so that Fresh can copy it whole: the list used to
	// be written out by hand, and a field added above and forgotten there was
	// silently empty for every command a deployment ran - which is what
	// happened to the OpenAlex base URL.
	Access

	once     sync.Once
	register *registerData
	loadErr  error

	cache sync.Map // url -> *cachedResponse
}

// Access is how this deployment reaches the outside world: the client, the
// credentials, which source to ask about a paper, and where each service
// lives. Everything configured, and nothing a run accumulates.
type Access struct {
	HTTP *http.Client

	// GitHubToken lifts the rate limit on the GitHub API. The public endpoints
	// this bot uses work without one, more slowly.
	GitHubToken string

	// Metadata is where to ask what a paper is, "openalex" or "crossref", and
	// Mailto is the address OpenAlex asks for in return for its polite pool.
	// Empty Metadata means OpenAlex: a deployment that says nothing gets the
	// source that answers.
	Metadata string
	Mailto   string

	// Register is the repository the register lives in, as owner/repo. The
	// register-wide rules read register.csv and venues.csv from it.
	Register string

	// Base URLs of the services, so that a sandbox, a mirror or a test server
	// can be used instead. Empty means the real one, see defaults().
	Zenodo        string // https://zenodo.org/api
	ZenodoSandbox string // https://sandbox.zenodo.org/api
	ORCID         string // https://pub.orcid.org/v3.0
	Crossref      string // https://api.crossref.org
	OpenAlex      string // https://api.openalex.org
	GitHub        string // https://api.github.com
	RawContent    string // https://raw.githubusercontent.com
	GitLab        string // https://gitlab.com
	OSF           string // https://api.osf.io/v2
}

// ServicesFromEnv builds the services from the environment, or returns nil
// when external services are not enabled.
//
//	CHEKHOV_INTEGRATION=1              enable them
//	CHEKHOV_GH_ACCESS_TOKEN=...        optional, lifts the GitHub rate limit
//	CHEKHOV_TARGET_REPO=owner/repo     the register to read; the default is in
//	                                   config/settings-development.yml, which is
//	                                   the one place that names it
func ServicesFromEnv() *Services {
	if os.Getenv("CHEKHOV_INTEGRATION") == "" {
		return nil
	}
	return Online()
}

// Online builds services that reach the real world, for a caller that has
// decided to go online rather than reading it out of the environment - the
// `--online` flag of the check command.
func Online() *Services {
	services := &Services{Access: Access{
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		GitHubToken: os.Getenv("CHEKHOV_GH_ACCESS_TOKEN"),
		Register:    targetRepository(),
	}}
	if settings, err := config.Current(); err == nil {
		services.Metadata = settings.MetadataSource()
		services.Mailto = settings.MetadataMailto()
	}
	services.defaults()
	return services
}

// targetRepository is the register the bot works on, from the settings file,
// which is the one place that names it. A settings file that cannot be read is
// not a reason to guess: fall back to the testing register, never to the real
// one.
func targetRepository() string {
	settings, err := config.Current()
	if err != nil {
		return "codecheckers/testing-dev-register"
	}
	return settings.TargetRepository()
}

// defaults fills in whatever a caller left blank, so that a hand-built
// Services - a test pointing two of the five at a stub server, say - still
// reaches the real ones for the rest.
func (s *Services) defaults() {
	for _, field := range []struct {
		value *string
		url   string
	}{
		{&s.Zenodo, "https://zenodo.org/api"},
		{&s.ZenodoSandbox, "https://sandbox.zenodo.org/api"},
		{&s.ORCID, "https://pub.orcid.org/v3.0"},
		{&s.Crossref, "https://api.crossref.org"},
		{&s.OpenAlex, "https://api.openalex.org"},
		{&s.GitHub, "https://api.github.com"},
		{&s.RawContent, "https://raw.githubusercontent.com"},
		{&s.GitLab, "https://gitlab.com"},
		{&s.OSF, "https://api.osf.io/v2"},
	} {
		if *field.value == "" {
			*field.value = field.url
		}
		*field.value = strings.TrimSuffix(*field.value, "/")
	}
}

// reset forgets the responses seen so far. One run of the bot asks the same
// question once; a test that reuses the services across cases has to be able
// to ask again.
func (s *Services) reset() {
	s.cache = sync.Map{}
	s.once = sync.Once{}
	s.register = nil
	s.loadErr = nil
}

// RegisterFile is the URL of a file in the register the bot works on.
func (s *Services) RegisterFile(name string) string {
	return s.rawURL(s.Register, "HEAD", name)
}

// Fresh is a copy that has seen nothing yet: the same access, an empty cache.
// The cache is meant for one run; a deployment that keeps one Services for its
// whole life gives each command a fresh copy. A nil or disabled Services stays
// as it is.
//
// The whole of Access goes across, so adding a field to it cannot leave a
// deployment quietly talking to nowhere.
func (s *Services) Fresh() *Services {
	if !s.Enabled() {
		return s
	}
	return &Services{Access: s.Access}
}

// IsCertificateID reports whether a word is a certificate identifier, YYYY-NNN.
func IsCertificateID(word string) bool { return certificateID.MatchString(word) }

// Enabled reports whether checks may reach out.
func (s *Services) Enabled() bool { return s != nil && s.HTTP != nil }

// needsServices is the skip every service-dependent check returns when it
// cannot reach out, naming what it would have needed.
func needsServices(service string) Result {
	return skip("external services not enabled, this check needs " + service)
}

// RequiresService maps a rule to the outside thing its check needs. A rule
// listed here skips in the offline suite and runs in the integration suite,
// which is what the two exhaustive fixtures assert.
var RequiresService = map[string]string{
	"CC-CFG-012": "the report URL",
	"CC-MET-002": "the ORCID API",
	"CC-MET-003": "the ORCID API",
	"CC-MET-004": "the paper reference",
	"CC-MET-005": "the paper's metadata source",
	"CC-MET-006": "the paper's metadata source",
	"CC-MET-007": "the paper's metadata source",
	"CC-MET-008": "the paper's metadata source",
	"CC-MET-009": "the reference-other entries",
	"CC-BUN-004": "the checked repository",
	"CC-REP-001": "the Zenodo API",
	"CC-REP-002": "the Zenodo API",
	"CC-REP-003": "the Zenodo API",
	"CC-REP-004": "the Zenodo API",
	"CC-REP-005": "the Zenodo API",
	"CC-REP-006": "the Zenodo API",
	"CC-REG-001": "register.csv",
	"CC-REG-002": "register.csv",
	"CC-REG-003": "register.csv",
	"CC-REG-004": "register.csv",
	"CC-REG-005": "venues.csv",
	"CC-REG-006": "the GitHub API",
	"CC-REG-007": "the GitHub API",
}

type cachedResponse struct {
	status int
	body   []byte
	err    error
}

// get fetches a URL once per run. A codecheck.yml names the same ORCID or the
// same record several times, and the checks are separate functions by design,
// so without this the bot would ask the same question repeatedly.
func (s *Services) get(url string, header map[string]string) (int, []byte, error) {
	return s.request(http.MethodGet, url, header)
}

// exists asks only whether something is there. It is a HEAD, so a manifest of
// two dozen figures does not download two dozen figures - and does not keep
// them in the cache for the rest of the run.
//
// Not every server answers a HEAD: some reply 405, some proxies drop it, and a
// recorded cassette may only hold the GET. Any of those falls back to a GET,
// which is what the check was doing before.
func (s *Services) exists(url string) (int, error) {
	status, _, err := s.request(http.MethodHead, url, nil)
	if err != nil || status == http.StatusMethodNotAllowed {
		status, _, err = s.get(url, nil)
	}
	return status, err
}

// FetchFile reads one file, and says which URL failed rather than only that
// something did.
func (s *Services) FetchFile(url string) ([]byte, error) {
	status, body, err := s.get(url, nil)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", url, err)
	}
	if status != http.StatusOK {
		return nil, &StatusError{URL: url, Status: status}
	}
	return body, nil
}

// StatusError is a file that answered something other than 200, so that a
// caller can tell "not there" from "not now".
type StatusError struct {
	URL    string
	Status int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s answered %d", e.URL, e.Status) }

// IsNotFound reports whether an error from FetchFile says the file does not
// exist, as opposed to a server or a network that failed.
func IsNotFound(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && (status.Status == http.StatusNotFound || status.Status == http.StatusGone)
}

func (s *Services) request(method, url string, header map[string]string) (int, []byte, error) {
	// The Accept header is part of the key: the same URL answers JSON or text
	// depending on what was asked for.
	key := method + " " + url + " " + header["Accept"]
	if cached, ok := s.cache.Load(key); ok {
		response := cached.(*cachedResponse)
		return response.status, response.body, response.err
	}

	status, body, err := s.fetch(method, url, header)
	s.cache.Store(key, &cachedResponse{status: status, body: body, err: err})
	return status, body, err
}

func (s *Services) fetch(method, url string, header map[string]string) (int, []byte, error) {
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("User-Agent",
		"chekhov/dev (+https://github.com/codecheckers/chekhov; the CODECHECK register bot)")
	for key, value := range header {
		request.Header.Set(key, value)
	}

	response, err := s.HTTP.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	return response.StatusCode, body, err
}

// getJSON fetches and decodes, and reports a non-200 as an error so that every
// caller does not repeat the same three lines.
func (s *Services) getJSON(url string, header map[string]string, into any) error {
	if header == nil {
		header = map[string]string{}
	}
	if _, ok := header["Accept"]; !ok {
		header["Accept"] = "application/json"
	}

	status, body, err := s.get(url, header)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		// Typed, as FetchFile's failure already was, so that a caller can tell
		// a 404 from an outage through IsNotFound. Its words are the ones this
		// returned before, because they reach a reply.
		return &StatusError{URL: url, Status: status}
	}
	return json.Unmarshal(body, into)
}

// rawURL is where a repository serves its files as plain bytes, at a ref: one
// file when path names one, the directory the files hang off when it is empty.
//
// Written out by hand in three places before it was written here, and the
// third is why the trailing slash is trimmed: a configured RawContent ending
// in "/" gave one caller a doubled slash, which raw.githubusercontent answers
// 404 for - reported by the rules as a missing file rather than as an error.
func (s *Services) rawURL(repository, ref, path string) string {
	base := strings.TrimSuffix(s.RawContent, "/") + "/" + repository + "/" + ref
	if path == "" {
		return base
	}
	return base + "/" + path
}

func (s *Services) github(url string, into any) error {
	header := map[string]string{"Accept": "application/vnd.github+json"}
	if s.GitHubToken != "" {
		header["Authorization"] = "Bearer " + s.GitHubToken
	}
	return s.getJSON(url, header, into)
}

// resolves reports whether a URL answers, as a check result. A connection
// failure is a skip rather than a failure: an unreachable server says nothing
// about the codecheck.yml.
func (s *Services) resolves(url string) Result {
	status, _, err := s.get(url, nil)
	if err != nil {
		return skip(fmt.Sprintf("could not reach '%s': %s", url, err))
	}
	// A publisher that blocks robots, a rate limit, or a server having a bad
	// moment says nothing about whether the reference is right. Only "not
	// there" is a failure.
	switch {
	case status == 401, status == 403, status == 405, status == 429:
		return skip(fmt.Sprintf("'%s' answers %d, which blocks the check rather than failing it",
			url, status))
	case status >= 500:
		return skip(fmt.Sprintf("'%s' answers %d, so the server could not say", url, status))
	case status >= 400:
		return fail(fmt.Sprintf("'%s' answers %d", url, status))
	}
	return pass("")
}

// resolution is what resolving several URLs said, with the reason for each one
// that did not: a 404 and a server that could not be asked send the
// codechecker to do different things, so "does not resolve" alone is not
// enough.
type resolution struct {
	resolved   int
	unresolved []string // why each URL that does not resolve does not
	failed     []int    // which of the URLs those were, by position
	unreached  []string // why each URL that could not be asked could not
}

// resolveAll resolves every URL, see resolves.
func (s *Services) resolveAll(urls []string) resolution {
	var r resolution
	for i, url := range urls {
		switch result := s.resolves(url); result.Status {
		case StatusFail:
			r.unresolved = append(r.unresolved, result.Detail)
			r.failed = append(r.failed, i)
		case StatusSkip:
			r.unreached = append(r.unreached, result.Detail)
		default:
			r.resolved++
		}
	}
	return r
}

// verdict is the rule's result for a resolution. A URL that does not resolve
// is a finding whatever else is down, and one that could not be asked is never
// a pass, or an outage would read as "everything resolves" (chekhov#11).
func (r resolution) verdict(failing string, lines []int, passing string) Result {
	notChecked := strings.Join(r.unreached, "; ")
	switch {
	case len(r.unresolved) > 0:
		detail := failing + ": " + strings.Join(r.unresolved, "; ")
		if notChecked != "" {
			detail += "; not checked: " + notChecked
		}
		return fail(detail).at(lines...)
	case len(r.unreached) > 0:
		return skip(fmt.Sprintf("%d of %d could not be checked: %s",
			len(r.unreached), len(r.unreached)+r.resolved, notChecked))
	}
	return pass(passing)
}

// --- register.csv and venues.csv -------------------------------------------

type registerEntry struct {
	Certificate string
	Repository  string
	Type        string
	Venue       string
	Issue       string
}

type registerData struct {
	entries []registerEntry
	venues  map[string]bool
}

// registerCSV loads the register once per run.
func (s *Services) registerCSV() (*registerData, error) {
	s.once.Do(func() {
		data := &registerData{venues: map[string]bool{}}
		tables := s.CSVs(s.RegisterFile("register.csv"), s.RegisterFile("venues.csv"))
		register, venues := tables[0], tables[1]
		if register.Err != nil {
			s.loadErr = register.Err
			return
		}
		for _, row := range register.Rows {
			data.entries = append(data.entries, registerEntry{
				Certificate: row["Certificate"],
				Repository:  row["Repository"],
				Type:        row["Type"],
				Venue:       row["Venue"],
				Issue:       row["Issue"],
			})
		}

		// venues.csv is optional: the testing register may not carry one, and
		// a missing file must make the venue rule skip rather than fail.
		if venues.Err == nil {
			for _, row := range venues.Rows {
				for _, key := range []string{"Venue", "name", "Name"} {
					if value := strings.TrimSpace(row[key]); value != "" {
						data.venues[strings.ToLower(value)] = true
					}
				}
			}
		}

		s.register = data
	})
	return s.register, s.loadErr
}

// CSV reads a CSV file into one map per row, keyed by the header. Lines
// starting with # are comments, as in register.csv.
func (s *Services) CSV(url string) ([]map[string]string, error) {
	status, body, err := s.get(url, map[string]string{"Accept": "text/plain"})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &StatusError{URL: url, Status: status}
	}

	reader := csv.NewReader(strings.NewReader(string(body)))
	reader.FieldsPerRecord = -1
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s is empty", url)
	}

	header := records[0]
	var rows []map[string]string
	for _, record := range records[1:] {
		row := map[string]string{}
		for i, value := range record {
			if i < len(header) {
				row[strings.TrimSpace(header[i])] = strings.TrimSpace(value)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// A Table is one file CSVs read: its rows, or why it could not be read.
type Table struct {
	Rows []map[string]string
	Err  error
}

// CSVs reads several CSV files at once, and returns them in the order they
// were asked for, not the order they answered in: where a caller lets the
// first file win, the first file is the one it named first (#34).
func (s *Services) CSVs(urls ...string) []Table {
	tables := make([]Table, len(urls))
	var wg sync.WaitGroup
	for i, url := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := s.CSV(url)
			tables[i] = Table{Rows: rows, Err: err}
		}()
	}
	wg.Wait()
	return tables
}

// entryFor finds the register row of one certificate.
func (d *registerData) entryFor(certificate string) (registerEntry, bool) {
	for _, entry := range d.entries {
		if entry.Certificate == certificate {
			return entry, true
		}
	}
	return registerEntry{}, false
}
