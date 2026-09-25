package check

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/config"
)

// `check links` (chekhov#12): what is collected, from where, and what each
// answer is reported as. Offline: the links point at the stub server, and its
// client refuses anything else.

func urlsOf(links *Links) []string {
	var urls []string
	for _, link := range links.Links {
		urls = append(urls, link.URL)
	}
	return urls
}

func linkFor(t *testing.T, links *Links, url string) *Link {
	t.Helper()
	for _, link := range links.Links {
		if link.URL == url {
			return link
		}
	}
	t.Fatalf("%s was not collected; got %q", url, urlsOf(links))
	return nil
}

// A URL in prose ends where the prose says it does: not at the full stop, the
// parenthesis around it or the Markdown around it - but a parenthesis that is
// part of it stays.
func TestLinksInTextEndWhereTheLinkDoes(t *testing.T) {
	text := "See https://a.org/one. And (https://a.org/two), " +
		"[three](https://a.org/three) and <https://a.org/four>; " +
		"https://en.wikipedia.org/wiki/R_(programming_language) and \\url{https://a.org/five} " +
		"and **https://a.org/six**."
	want := []string{
		"https://a.org/one", "https://a.org/two", "https://a.org/three", "https://a.org/four",
		"https://en.wikipedia.org/wiki/R_(programming_language)", "https://a.org/five", "https://a.org/six",
	}
	if got := linksIn(text); !slices.Equal(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A link that is built rather than written, and a placeholder, is nothing
// anybody can follow, and reporting it as dead would be noise.
func TestLinksInTextLeaveOutWhatIsNotALink(t *testing.T) {
	text := "https://api.b.org/{id} https://$HOST/path https://example.org/x " +
		"https://www.example.com/y http://localhost:8080/ https://c.org/kept"
	if got := linksIn(text); !slices.Equal(got, []string{"https://c.org/kept"}) {
		t.Errorf("got %q, want only the real link", got)
	}
}

// LaTeX and Markdown escape characters in a URL; the link is what the escape
// stands for, and one cut short by any other backslash is not asked at all.
func TestLinksInTextAreUnescaped(t *testing.T) {
	text := `\href{https://g.org/repo\_name}{code} \url{https://g.org/page\#section} https://g.org/cut\textbf{x}`
	want := []string{"https://g.org/repo_name", "https://g.org/page#section"}
	if got := linksIn(text); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Every value of the codecheck.yml is read, a DOI written as a DOI becomes the
// link it is, and the report can say which line to fix.
func TestLinksInTheConfigurationCarryTheirLine(t *testing.T) {
	c := Context{Path: "codecheck.yml", Raw: []byte("report: 10.5281/zenodo.123\n" +
		"paper:\n  reference: https://d.org/paper\n")}
	links := CollectLinks(context.Background(), c, LinkSource{})

	if link := linkFor(t, links, "https://doi.org/10.5281/zenodo.123"); link.Found[0] != "`codecheck.yml` line 1" {
		t.Errorf("found in %q", link.Found)
	}
	if link := linkFor(t, links, "https://d.org/paper"); link.Found[0] != "`codecheck.yml` line 3" {
		t.Errorf("found in %q", link.Found)
	}
}

// One link seen in two places is one link, found twice.
func TestALinkSeenTwiceIsOneLink(t *testing.T) {
	c := Context{Path: "codecheck.yml", Raw: []byte("repository: https://e.org/repo\n")}
	links := CollectLinks(context.Background(), c, LinkSource{Name: "issue #1", Text: "the code is at https://e.org/repo."})

	if links.Len() != 1 {
		t.Fatalf("got %q, want one link", urlsOf(links))
	}
	if found := links.Links[0].Found; !slices.Equal(found, []string{"issue #1", "`codecheck.yml` line 1"}) {
		t.Errorf("found in %q", found)
	}
}

// codecheck-py's certificate is a notebook: its Markdown cells are read, as a
// list of lines or as one string, and neither the code nor what it printed is.
func TestTheNotebookCertificateIsReadForItsProse(t *testing.T) {
	c, err := FromFile(filepath.Join("..", "..", "testdata", "links-ipynb", "codecheck.yml"))
	if err != nil {
		t.Fatal(err)
	}
	links := CollectLinks(context.Background(), c, LinkSource{})

	if !slices.Contains(links.Read, "`codecheck/codecheck.ipynb`") {
		t.Fatalf("the notebook was not read: read %q, not %q", links.Read, links.Unread)
	}
	for _, want := range []string{
		"https://github.com/codecheckers/links-fixture",
		"https://doi.org/10.5555/links-fixture",
		"https://prose.links-fixture.org/notes",
	} {
		if link := linkFor(t, links, want); link.Found[0] != "`codecheck/codecheck.ipynb`" {
			t.Errorf("%s found in %q", want, link.Found)
		}
	}
	for _, url := range urlsOf(links) {
		if strings.Contains(url, "code.links-fixture") || strings.Contains(url, "output.links-fixture") {
			t.Errorf("%s is in the notebook's code or output, not its prose", url)
		}
	}
}

// An R Markdown certificate is read for its prose, not its chunks; and one
// named for the check, as the earlier ones were, is found too.
func TestTheRMarkdownCertificateIsReadForItsProse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "codecheck.yml"), "version: https://codecheck.org.uk/spec/config/1.0/\n")
	writeFile(t, filepath.Join(dir, "codecheck", "piccolo2020-codecheck.Rmd"),
		"The paper is at https://f.org/paper.\n\n```{r}\ndownload('https://f.org/data.csv')\n```\n\n"+
			"~~~\nhttps://f.org/fenced\n~~~\nAfter: https://f.org/after\n")
	c, err := FromFile(filepath.Join(dir, "codecheck.yml"))
	if err != nil {
		t.Fatal(err)
	}

	got := urlsOf(CollectLinks(context.Background(), c, LinkSource{}))
	want := []string{"https://f.org/paper", "https://f.org/after"}
	for _, url := range got {
		if strings.HasPrefix(url, "https://f.org/") && !slices.Contains(want, url) {
			t.Errorf("%s is inside a code chunk", url)
		}
	}
	for _, url := range want {
		if !slices.Contains(got, url) {
			t.Errorf("%s was not collected from the prose: %q", url, got)
		}
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sizedBundle is a bundle whose certificate listing says what the test says,
// and which records whether the certificate was downloaded.
type sizedBundle struct {
	size    int64
	content string
	reads   []string
}

func (b *sizedBundle) Read(name string) ([]byte, error) {
	b.reads = append(b.reads, name)
	return []byte(b.content), nil
}
func (b *sizedBundle) Exists(string) (bool, error) { return false, nil }
func (b *sizedBundle) List(dir string) ([]BundleEntry, error) {
	if dir != certificateDirectory {
		return nil, errNoSuchDirectory
	}
	return []BundleEntry{{Name: "codecheck.ipynb", Size: b.size}}, nil
}
func (b *sizedBundle) Licence() (string, error) { return "", nil }
func (b *sizedBundle) Describe() string         { return "github::codecheckers/large" }

// A certificate larger than the configured limit is not downloaded, when the
// listing says how large it is.
func TestALargeCertificateIsNotDownloaded(t *testing.T) {
	bundle := &sizedBundle{size: 50 << 20}
	services := &Services{Access: Access{MaxCertificateBytes: 1 << 20}}
	links := CollectLinks(context.Background(), Context{Bundle: bundle, Services: services}, LinkSource{})

	if len(bundle.reads) != 0 {
		t.Errorf("the certificate was downloaded: %q", bundle.reads)
	}
	if len(links.Unread) != 1 || !strings.Contains(links.Unread[0], "50.0 MB") ||
		!strings.Contains(links.Unread[0], "limit of 1.0 MB") {
		t.Errorf("the report does not say why the certificate was not read: %q", links.Unread)
	}
}

// Where the listing does not say - GitLab's - the file is read, and one that
// reaches the limit is set aside rather than parsed: a notebook cut short is
// broken JSON, and half a certificate would hide the links in the other half.
func TestALargeCertificateOfUnknownSizeIsNotParsed(t *testing.T) {
	bundle := &sizedBundle{size: SizeUnknown, content: `{"cells": [` + strings.Repeat(" ", 2000)}
	services := &Services{Access: Access{MaxCertificateBytes: 1000}}
	links := CollectLinks(context.Background(), Context{Bundle: bundle, Services: services}, LinkSource{})

	if len(links.Unread) != 1 || !strings.Contains(links.Unread[0], "at least") {
		t.Errorf("an oversized certificate of unknown size was not set aside: %q", links.Unread)
	}
	if slices.Contains(links.Read, "`codecheck/codecheck.ipynb`") {
		t.Error("the oversized certificate was read")
	}
}

// A limit above what a response is ever read to would let a certificate whose
// listed size passed be parsed cut short; the limit is never more than that.
func TestTheCertificateLimitIsNeverMoreThanIsRead(t *testing.T) {
	bundle := &sizedBundle{size: fetchLimit + 1}
	services := &Services{Access: Access{MaxCertificateBytes: 100 << 20}}
	links := CollectLinks(context.Background(), Context{Bundle: bundle, Services: services}, LinkSource{})

	if len(bundle.reads) != 0 || len(links.Unread) != 1 {
		t.Errorf("a certificate larger than is ever read was downloaded: %q, %q", bundle.reads, links.Unread)
	}
}

// Without a limit in the services - the command line, a test - the default
// applies, rather than no limit at all.
func TestTheCertificateLimitHasADefault(t *testing.T) {
	if got := (*Services)(nil).maxCertificateBytes(); got != config.DefaultMaxCertificateBytes {
		t.Errorf("limit = %d, want the default", got)
	}
}

// probeStub serves a link that answers each way a real site does.
func probeStub(t *testing.T) *stub {
	t.Helper()
	s := newStub(t)
	s.services.DOI = s.URL + "/doi"
	s.Status("/ok", http.StatusOK)
	s.Status("/gone", http.StatusNotFound)
	s.Status("/removed", http.StatusGone)
	s.Status("/refuses", http.StatusForbidden)
	s.Status("/busy", http.StatusTooManyRequests)
	s.Status("/down", http.StatusServiceUnavailable)
	// Answers only a GET, as some servers do.
	s.Handle("/no-head", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// Says a HEAD is not there and serves the GET, as some dynamic sites do.
	s.Handle("/head-missing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// Refuses a robot's HEAD and serves its GET, as others do.
	s.Handle("/head-refused", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	s.JSON("/doi/api/handles/10.5555/exists",
		`{"responseCode": 1, "values": [{"type": "URL", "data": {"value": "https://publisher.org/x"}}]}`)
	s.Handle("/doi/api/handles/10.5555/missing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"responseCode": 100, "handle": "10.5555/missing"}`))
	})
	return s
}

// Only "not there" is a dead link. A refusal, a rate limit and a server error
// say the site could not be asked, and each says which.
func TestEachAnswerIsReportedAsWhatItSays(t *testing.T) {
	s := probeStub(t)
	cases := map[string]struct {
		outcome Outcome
		says    string
	}{
		"/ok":           {OutcomeOK, ""},
		"/gone":         {OutcomeError, "answers 404"},
		"/removed":      {OutcomeError, "answers 410"},
		"/no-head":      {OutcomeOK, ""},
		"/head-refused": {OutcomeOK, ""},
		"/head-missing": {OutcomeOK, ""},
		"/refuses":      {OutcomeSkipped, "refuses automated requests (403)"},
		"/busy":         {OutcomeSkipped, "rate-limited (429)"},
		"/down":         {OutcomeSkipped, "server error (503)"},
	}
	links := &Links{index: map[string]*Link{}}
	for path := range cases {
		links.add(s.At(path), "the test")
	}
	links.Probe(context.Background(), s.services)

	for path, want := range cases {
		link := linkFor(t, links, s.At(path))
		if link.Outcome != want.outcome || link.Says != want.says {
			t.Errorf("%s: %s %q, want %s %q", path, link.Outcome, link.Says, want.outcome, want.says)
		}
	}
}

// A DOI is asked of the DOI system, never followed to a publisher that may
// refuse the robot: the stub has no publisher, and the DOIs still answer.
func TestADOIIsAskedOfTheDOISystem(t *testing.T) {
	s := probeStub(t)
	links := &Links{index: map[string]*Link{}}
	links.add("https://doi.org/10.5555/exists", "the test")
	links.add("https://dx.doi.org/10.5555/missing", "the test")
	links.Probe(context.Background(), s.services)

	if link := links.Links[0]; link.Outcome != OutcomeOK {
		t.Errorf("an existing DOI: %s %q", link.Outcome, link.Says)
	}
	if link := links.Links[1]; link.Outcome != OutcomeError || link.Says != "no such DOI" {
		t.Errorf("a missing DOI: %s %q", link.Outcome, link.Says)
	}
}

// A site on the settings' list is not asked at all - the stub's client would
// refuse the request, and the outcome would say "could not connect" if one
// were made - and a subdomain is covered by its host.
func TestASkippedHostIsNotAsked(t *testing.T) {
	s := probeStub(t)
	s.services.LinkSkipHosts = []string{"linkedin.com"}
	links := &Links{index: map[string]*Link{}}
	links.add("https://www.linkedin.com/in/somebody", "the test")
	links.Probe(context.Background(), s.services)

	if link := links.Links[0]; link.Outcome != OutcomeUnchecked || !strings.Contains(link.Says, "linkedin.com") {
		t.Errorf("a skipped host: %s %q", link.Outcome, link.Says)
	}
}

// A site slower than the limit for one link could not say; a link the run's
// end came before was not asked. Neither is a dead link.
func TestSlowSitesAndTheRunsEndAreNotDeadLinks(t *testing.T) {
	s := probeStub(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.Handle("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	previous := linkTimeout
	linkTimeout = 50 * time.Millisecond
	t.Cleanup(func() { linkTimeout = previous })

	links := &Links{index: map[string]*Link{}}
	links.add(s.At("/slow"), "the test")
	links.Probe(context.Background(), s.services)
	if link := links.Links[0]; link.Outcome != OutcomeSkipped || !strings.Contains(link.Says, "no answer within") {
		t.Errorf("a slow site: %s %q", link.Outcome, link.Says)
	}

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	links = &Links{index: map[string]*Link{}}
	links.add(s.At("/ok"), "the test")
	links.Probe(ended, s.services)
	if link := links.Links[0]; link.Outcome != OutcomeUnchecked || link.unasked != outOfTime {
		t.Errorf("a link after the run ended: %s %q", link.Outcome, link.Says)
	}
}

// depositStub serves a Zenodo record and a ResearchEquals version, each with
// links in the places a deposit keeps them.
func depositStub(t *testing.T) *stub {
	t.Helper()
	s := newStub(t)
	s.services.DOI = s.URL + "/doi"
	s.services.ResearchEquals = s.URL + "/researchequals"
	s.JSON("/zenodo/records/123", `{"metadata": {
		"description": "<p>Code at <a href=\"https://g.org/code?a=1&amp;b=2\">the repository</a>. See also 10.5281/zenodo.999.</p>",
		"notes": "Notes point at https://g.org/notes.",
		"related_identifiers": [
			{"identifier": "https://g.org/related", "scheme": "url"},
			{"identifier": "10.5555/reviewed", "scheme": "doi"},
			{"identifier": "0000-0002-1825-0097", "scheme": "orcid"}
		],
		"references": ["Author (2020). Title. https://g.org/reference"]
	}}`)
	s.Handle("/zenodo/records/999", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a deposit named in a deposit was read in turn")
	})
	s.JSON("/doi/api/handles/10.53962/abcd-1234", `{"responseCode": 1, "values": [
		{"type": "URL", "data": {"value": "https://researchequals.com/versions/8d0a10a6-c84b-4505-903a-f55224441a40"}}]}`)
	s.JSON("/researchequals/versions/8d0a10a6-c84b-4505-903a-f55224441a40", `{
		"description": "Reproducibility review of https://h.org/paper.",
		"changelog": null,
		"refs": ["doi:10.5555/cited", {"url": "https://h.org/ref"}]
	}`)
	return s
}

// A deposit the configuration's report names is read for its links: its
// description, notes, related identifiers and references.
func TestTheReportsZenodoDepositIsRead(t *testing.T) {
	s := depositStub(t)
	c := Context{Path: "codecheck.yml", Raw: []byte("report: https://doi.org/10.5281/zenodo.123\n"),
		Services: s.services}
	links := CollectLinks(context.Background(), c, LinkSource{})

	if !slices.Contains(links.Read, "Zenodo record 123") {
		t.Fatalf("the record was not read: %q", links.Unread)
	}
	for _, want := range []string{
		"https://g.org/code?a=1&b=2", "https://g.org/notes", "https://g.org/related",
		"https://doi.org/10.5555/reviewed", "https://g.org/reference",
	} {
		if link := linkFor(t, links, want); link.Found[0] != "Zenodo record 123" {
			t.Errorf("%s found in %q", want, link.Found)
		}
	}
}

// A `zenodo::` target is the deposit itself.
func TestAZenodoTargetIsItsOwnDeposit(t *testing.T) {
	s := depositStub(t)
	c := Context{RepositorySpec: RepositorySpec{Type: "zenodo", Path: "123"}, Services: s.services}
	if links := CollectLinks(context.Background(), c, LinkSource{}); !slices.Contains(links.Read, "Zenodo record 123") {
		t.Errorf("the target's record was not read: %q", links.Unread)
	}

	// A sandbox record is read from the sandbox.
	s.JSON("/zenodo-sandbox/records/77", `{"metadata": {"notes": "https://g.org/sandbox"}}`)
	c = Context{RepositorySpec: RepositorySpec{Type: "zenodo-sandbox", Path: "77"}, Services: s.services}
	links := CollectLinks(context.Background(), c, LinkSource{})
	if !slices.Contains(links.Read, "Zenodo sandbox record 77") {
		t.Fatalf("the sandbox record was not read: %q", links.Unread)
	}
	linkFor(t, links, "https://g.org/sandbox")
}

// A ResearchEquals DOI mentioned in the issue, not linked, is enough: the
// version is found through where the DOI system says the DOI points.
func TestAResearchEqualsDepositIsRead(t *testing.T) {
	s := depositStub(t)
	links := CollectLinks(context.Background(), Context{Services: s.services},
		LinkSource{Name: "issue #1", Text: "The certificate is 10.53962/abcd-1234."})

	if !slices.Contains(links.Read, "ResearchEquals 10.53962/abcd-1234") {
		t.Fatalf("the version was not read: %q", links.Unread)
	}
	for _, want := range []string{"https://h.org/paper", "https://doi.org/10.5555/cited", "https://h.org/ref"} {
		linkFor(t, links, want)
	}
}

// A deposit that cannot be read is said so, and the rest of the run goes on.
func TestADepositThatCannotBeReadIsNoted(t *testing.T) {
	s := depositStub(t)
	s.Status("/zenodo/records/404", http.StatusNotFound)
	c := Context{Path: "codecheck.yml", Services: s.services,
		Raw: []byte("report: 10.5281/zenodo.404\nrepository: https://g.org/repo\n")}
	links := CollectLinks(context.Background(), c, LinkSource{})

	if len(links.Unread) == 0 || !strings.HasPrefix(links.Unread[len(links.Unread)-1], "Zenodo record 404") {
		t.Errorf("the unreadable record is not noted: %q", links.Unread)
	}
	linkFor(t, links, "https://g.org/repo")
}

// Offline, a deposit is not read and not a link asked, and the report says so
// rather than reporting nothing wrong.
func TestOfflineNothingIsAsked(t *testing.T) {
	c := Context{Path: "codecheck.yml", Raw: []byte("report: 10.5281/zenodo.123\n")}
	links := CollectLinks(context.Background(), c, LinkSource{})
	links.Probe(context.Background(), nil)

	report := links.Markdown()
	for _, want := range []string{"Zenodo record 123: reading it needs the external services",
		"1 link not asked: the external services are not enabled"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
}

// The reply lists only what needs attention, the dead links first; the rest is
// counted.
func TestTheReportListsOnlyWhatNeedsAttention(t *testing.T) {
	links := &Links{Target: "github::codecheckers/demo", Read: []string{"issue #1"}, Links: []*Link{
		{URL: "https://a.org/fine", Found: []string{"issue #1"}, Outcome: OutcomeOK},
		{URL: "https://a.org/busy", Found: []string{"issue #1"}, Outcome: OutcomeSkipped, Says: "rate-limited (429)"},
		{URL: "https://a.org/gone", Found: []string{"issue #1"}, Outcome: OutcomeError, Says: "answers 404"},
		{URL: "https://x.com/a", Found: []string{"issue #1"}},
	}}
	links.Links[3].notAsked(skippedHost, "x.com")
	report := links.Markdown()

	if strings.Contains(report, "a.org/fine") {
		t.Errorf("a link that answers is listed:\n%s", report)
	}
	gone, busy := strings.Index(report, "a.org/gone"), strings.Index(report, "a.org/busy")
	if gone < 0 || busy < 0 || gone > busy {
		t.Errorf("the dead link is not listed first:\n%s", report)
	}
	for _, want := range []string{"4 found, 3 asked", "✔ 1 answer", "✖ 1 gone", "… 1 could not say",
		"1 link not asked, to sites that refuse automated requests: x.com", "not a verdict"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
}
