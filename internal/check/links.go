package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/codecheckers/chekhov/config"
)

// Which of the links around a check no longer answer.
//
// A certificate is published once and read for years, so a link that is dead
// on the day it is published is a link that is dead forever. This collects the
// links from what a codechecker is about to publish - the check issue, the
// codecheck.yml, the certificate's source, and the deposit the report points at
// - and asks each whether it is still there. See codecheckers/chekhov#12.
//
// Like `check repository` it is a description, not a verdict: there is no rule
// in the catalogue about links, and a site having a bad afternoon must never
// hold up a check. So the report is advisory, and it is written to keep the
// noise down, because a list of false alarms teaches people to skip the list.
// Only "not there" is a dead link: a refusal, a rate limit, a timeout or a
// server error says the site could not be asked, and is reported as that.

// AboutLinks is the word `check` takes for this description. Deliberately not
// one of Parts, as AboutRepository is not: there is no catalogue to narrow.
const AboutLinks = "links"

// linkTimeout bounds one link. The run as a whole is bounded by the caller's
// context, and there is no cap on how many links a run asks about, so a single
// slow site must not hold up the rest. A variable only so that a test of a
// slow site does not have to be one.
var linkTimeout = 15 * time.Second

// How many links are asked at once, in all and of any one site. Enough that a
// certificate with a hundred links answers in a minute or two; few enough per
// site that the bot is not the reason a small server rate-limits.
const (
	linksInFlight = 8
	linksPerHost  = 2
)

// A Link is one URL and what it answered.
type Link struct {
	URL string
	// Found is where the link was seen, in the order first seen: a link in
	// the issue and in the certificate is one link found twice.
	Found []string
	// Outcome is ✔ it answers, ✖ it is gone, … it could not be asked, and ─
	// it was not asked at all.
	Outcome Outcome
	// Says is why, in a few words.
	Says string

	// unasked is why a link was not asked, for the report to count; host is
	// the listed host it matched, when that is why.
	unasked unasked
	host    string
}

// unasked is why a link was not asked at all.
type unasked int

const (
	asked unasked = iota
	skippedHost
	outOfTime
	offline
)

// notAsked marks a link as not asked, and says why in the link's own words.
func (link *Link) notAsked(why unasked, host string) {
	link.Outcome, link.unasked, link.host = OutcomeUnchecked, why, host
	switch why {
	case skippedHost:
		link.Says = "not asked: " + host + " refuses automated requests"
	case outOfTime:
		link.Says = "not asked: the time ran out"
	case offline:
		link.Says = "not asked: the external services are not enabled"
	}
}

// Links is what `check links` collected, and after Probe, what each answered.
type Links struct {
	// Target is what was asked about, as the bundle names it.
	Target string
	// Read are the places links were collected from.
	Read []string
	// Unread are the places that were meant to be read and could not be,
	// with why. The report says so, because "no dead links" from a run that
	// never saw the certificate is not the same answer.
	Unread []string
	Links  []*Link

	index     map[string]*Link
	mentioned []string // Zenodo and ResearchEquals DOIs, in order of mention
}

// Len is how many different links were found.
func (l *Links) Len() int { return len(l.Links) }

// A LinkSource is text to collect links from that the caller has read: the
// check issue, which the bot reads and this package does not know how to.
type LinkSource struct {
	Name string
	Text string
	// Err is why the text could not be read, which the report repeats.
	Err error
}

// CollectLinks gathers the links around a check, without asking any of them.
// The caller decides from Len whether to say that this will take a while, and
// then calls Probe.
//
// ctx bounds the reading of the deposits, the only part that asks a service
// rather than reading what the caller already has.
func CollectLinks(ctx context.Context, c Context, issue LinkSource) *Links {
	links := &Links{Target: c.Label, index: map[string]*Link{}}
	if c.Bundle != nil {
		links.Target = c.Bundle.Describe()
	}

	if issue.Err != nil {
		links.unread(issue.Name, issue.Err.Error())
	} else if issue.Name != "" {
		links.addText(issue.Text, issue.Name)
		links.Read = append(links.Read, issue.Name)
	}

	links.addConfiguration(c)
	links.addCertificate(c)
	links.addDepositions(ctx, c)
	return links
}

func (l *Links) unread(what, why string) {
	l.Unread = append(l.Unread, what+": "+why)
}

// add records one URL seen in one place.
func (l *Links) add(url, where string) {
	if link, seen := l.index[url]; seen {
		if !slices.Contains(link.Found, where) {
			link.Found = append(link.Found, where)
		}
		return
	}
	link := &Link{URL: url, Found: []string{where}}
	l.index[url] = link
	l.Links = append(l.Links, link)
}

// addText records every link in a piece of text, and every Zenodo or
// ResearchEquals DOI it mentions, linked or not: "the report is
// 10.5281/zenodo.123" names the deposit as surely as a link to it does.
func (l *Links) addText(text, where string) {
	for _, url := range linksIn(text) {
		l.add(url, where)
	}
	for _, doi := range depositDOI.FindAllString(text, -1) {
		doi = strings.ToLower(doi)
		if !slices.Contains(l.mentioned, doi) {
			l.mentioned = append(l.mentioned, doi)
		}
	}
}

// addDOI records an identifier written as a DOI rather than a link, which is
// how codecheck.yml and a deposit's related identifiers mostly write them.
func (l *Links) addDOI(text, where string) {
	trimmed := strings.TrimSpace(text)
	if doi := DOIIn(trimmed); doi != "" && doi == strings.TrimPrefix(trimmed, "doi:") {
		l.add("https://doi.org/"+doi, where)
	}
	l.addText(text, where)
}

// --- finding links in text --------------------------------------------------

var (
	// urlInText is a URL in prose, Markdown, HTML or LaTeX. It stops at the
	// characters that end one in all four - whitespace, quotes, angle
	// brackets, backticks, and the braces of \url{...} - and trailing
	// punctuation is trimmed afterwards, because a sentence may end in one.
	urlInText = regexp.MustCompile("https?://[^\\s<>\"'`{}\\[\\]|\\\\^]+")
	// unescapeURLs undoes the escapes LaTeX and Markdown put into a URL.
	unescapeURLs = strings.NewReplacer(`\_`, "_", `\#`, "#", `\%`, "%", `\&`, "&")
	// depositDOI is a DOI of the two places a certificate is deposited.
	depositDOI = regexp.MustCompile(`(?i)10\.5281/zenodo\.[0-9]+|10\.53962/[a-z0-9-]+`)
)

// linksIn finds the links in a piece of text.
//
// A link that is built rather than written - `https://api.example/{id}` in a
// notebook, `https://$HOST/` in a shell line - is not a link anybody can
// follow, so one that runs into a template character is dropped rather than
// reported as dead in its truncated form.
//
// LaTeX and hand-written Markdown escape `_`, `#`, `%` and `&` in a URL, which
// are read as the characters they stand for; a backslash that is not one of
// those ends the URL in a way nobody meant, and the link is dropped rather
// than asked in a form that would answer 404.
func linksIn(text string) []string {
	text = unescapeURLs.Replace(text)
	var links []string
	for _, span := range urlInText.FindAllStringIndex(text, -1) {
		if span[1] < len(text) && strings.ContainsRune("{$\\", rune(text[span[1]])) {
			continue
		}
		url := trimURL(text[span[0]:span[1]])
		if strings.Contains(url, "$") || !worthAsking(url) {
			continue
		}
		links = append(links, url)
	}
	return links
}

// trimURL takes off what a sentence or markup put after a URL: punctuation,
// Markdown emphasis, and a closing parenthesis the URL did not open - so that
// `(see https://x.org/a)` loses it and `https://en.wikipedia.org/wiki/R_(language)`
// keeps it.
func trimURL(url string) string {
	for {
		trimmed := strings.TrimRight(url, ".,;:!?*_'\"")
		if strings.HasSuffix(trimmed, ")") && strings.Count(trimmed, ")") > strings.Count(trimmed, "(") {
			trimmed = strings.TrimSuffix(trimmed, ")")
		}
		if trimmed == url {
			return url
		}
		url = trimmed
	}
}

// worthAsking leaves out what is a link in form only: a host that is not a
// place on the internet, or the reserved example domains a template writes.
func worthAsking(url string) bool {
	host := hostOf(url)
	if !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return false
	}
	return !slices.ContainsFunc(reservedHosts, func(reserved string) bool { return onHost(host, reserved) })
}

// reservedHosts are the names set aside for examples and tests, RFC 2606.
var reservedHosts = []string{"example.com", "example.org", "example.net", "example", "invalid", "test"}

// onHost reports whether a host is the one named or under it: `linkedin.com`
// covers `www.linkedin.com`, and `test` covers every `.test` name.
func onHost(host, named string) bool {
	return host == named || strings.HasSuffix(host, "."+named)
}

// --- the codecheck.yml --------------------------------------------------------

// addConfiguration reads every value in the configuration, each with the
// line it is on, so that the report says where to fix it.
func (l *Links) addConfiguration(c Context) {
	if len(c.Raw) == 0 {
		return
	}
	name := "`" + path.Base(filepathOrDefault(c.Path)) + "`"
	l.Read = append(l.Read, name)

	var document yaml.Node
	if err := yaml.Unmarshal(c.Raw, &document); err != nil {
		// A file that does not parse still has links in it; the checks say
		// what is wrong with the YAML.
		l.addText(string(c.Raw), name)
		return
	}
	var walk func(node *yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.ScalarNode {
			l.addDOI(node.Value, fmt.Sprintf("%s line %d", name, node.Line))
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(&document)
}

func filepathOrDefault(name string) string {
	if name == "" {
		return "codecheck.yml"
	}
	return name
}

// --- the certificate ----------------------------------------------------------

// certificateSources are the names the certificate's source goes by, in the
// order they are looked for: the R package's R Markdown and Quarto templates,
// codecheck-py's notebook, and what a certificate written by hand is in.
var certificateSources = append(slices.Clone(executableCertificateSources), "codecheck.md", "codecheck.tex")

// executableCertificateSources are the sources that make the certificate when
// run; a deposit carrying more than one leaves the reader to guess which did,
// which is CC-REP-010.
var executableCertificateSources = []string{"codecheck.Rmd", "codecheck.qmd", "codecheck.ipynb"}

// certificateDirectory is where the certificate's source lives, relative to
// the codecheck.yml, as both templates put it.
const certificateDirectory = "codecheck"

// addCertificate reads the certificate's source, if the bundle has one and it
// is not too large to be worth holding in memory for its links.
//
// The size is asked of the listing before anything is downloaded: GitHub, OSF,
// Zenodo and the disk say it there. GitLab's listing does not, so there the
// file is read and a body as large as the limit - or as large as a response is
// ever read - is set aside unparsed. A notebook cut short is broken JSON, and
// half a certificate would report the links in the other half as absent.
func (l *Links) addCertificate(c Context) {
	const what = "the certificate source"
	if c.Bundle == nil {
		l.unread(what, "there is no repository to look in")
		return
	}
	entries, err := c.Bundle.List(certificateDirectory)
	switch {
	case errors.Is(err, errNoSuchDirectory), errors.Is(err, os.ErrNotExist), IsNotFound(err):
		l.unread(what, "there is no `"+certificateDirectory+"/` directory")
		return
	case err != nil:
		l.unread(what, err.Error())
		return
	}

	entry, found := certificateEntry(entries)
	if !found {
		l.unread(what, fmt.Sprintf("none of %s in `%s/`", strings.Join(certificateSources, ", "), certificateDirectory))
		return
	}

	name := certificateDirectory + "/" + entry.Name
	shown := "`" + name + "`"
	limit := c.Services.maxCertificateBytes()
	if entry.Size > limit {
		l.unread(shown, tooLarge(humanBytes(entry.Size), limit))
		return
	}
	content, err := c.Bundle.Read(name)
	if err != nil {
		l.unread(shown, err.Error())
		return
	}
	if entry.Size == SizeUnknown && int64(len(content)) >= limit {
		l.unread(shown, tooLarge("at least "+humanBytes(int64(len(content))), limit))
		return
	}

	text, err := certificateText(entry.Name, content)
	if err != nil {
		l.unread(shown, err.Error())
		return
	}
	l.addText(text, shown)
	l.Read = append(l.Read, shown)
}

// certificateEntry picks the certificate's source out of the listing: one of
// the template names, or else a source named for the check the way the
// earlier certificates were, `piccolo2020-codecheck.Rmd`.
func certificateEntry(entries []BundleEntry) (BundleEntry, bool) {
	for _, candidate := range certificateSources {
		index := slices.IndexFunc(entries, func(e BundleEntry) bool { return !e.IsDir && e.Name == candidate })
		if index >= 0 {
			return entries[index], true
		}
	}
	for _, entry := range entries {
		extension := path.Ext(entry.Name)
		if !entry.IsDir && strings.Contains(strings.ToLower(entry.Name), "codecheck") &&
			slices.Contains(certificateSources, "codecheck"+extension) {
			return entry, true
		}
	}
	return BundleEntry{}, false
}

func tooLarge(size string, limit int64) string {
	return fmt.Sprintf("it is %s, over the configured limit of %s", size, humanBytes(limit))
}

// maxCertificateBytes is the configured limit, or the settings' default when
// the services were built without them - offline, or in a test.
//
// Never more than a response is ever read: a repository bundle stops reading
// at fetchLimit, so a larger limit would let a certificate whose listed size
// passed be parsed cut short, without anything saying so.
func (s *Services) maxCertificateBytes() int64 {
	if s == nil || s.MaxCertificateBytes <= 0 {
		return min(config.DefaultMaxCertificateBytes, fetchLimit)
	}
	return min(s.MaxCertificateBytes, fetchLimit)
}

// certificateText is the prose of a certificate source: what its author
// wrote, rather than the code that produced the figures.
//
// Code is left out because a URL in code is mostly built - an API endpoint
// with a parameter on the end - and a link nobody can follow is noise. In a
// notebook the outputs are left out too: they are what the code printed.
func certificateText(name string, content []byte) (string, error) {
	switch path.Ext(name) {
	case ".ipynb":
		return notebookProse(content)
	case ".tex":
		return string(content), nil
	default:
		return withoutCodeBlocks(string(content)), nil
	}
}

// notebookProse is the text of a notebook's Markdown cells. A cell's source is
// a list of lines in the format as written, and a single string in some
// writers' output; both are read.
func notebookProse(content []byte) (string, error) {
	var notebook struct {
		Cells []struct {
			Type   string          `json:"cell_type"`
			Source json.RawMessage `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(content, &notebook); err != nil {
		return "", fmt.Errorf("it is not a notebook: %w", err)
	}
	var prose strings.Builder
	for _, cell := range notebook.Cells {
		if cell.Type != "markdown" {
			continue
		}
		var lines []string
		if err := json.Unmarshal(cell.Source, &lines); err != nil {
			var whole string
			_ = json.Unmarshal(cell.Source, &whole)
			lines = []string{whole}
		}
		prose.WriteString(strings.Join(lines, ""))
		prose.WriteString("\n")
	}
	return prose.String(), nil
}

// withoutCodeBlocks drops the fenced code of R Markdown, Quarto and Markdown:
// the chunks between ``` or ~~~ lines.
func withoutCodeBlocks(text string) string {
	var prose strings.Builder
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence == "" && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")):
			fence = trimmed[:3]
		case fence != "" && strings.HasPrefix(trimmed, fence):
			fence = ""
		case fence == "":
			prose.WriteString(line)
			prose.WriteString("\n")
		}
	}
	return prose.String()
}

// --- the deposit ----------------------------------------------------------------

// addDepositions reads the metadata of the deposits the check names: the
// Zenodo record a `zenodo::` target is, and any Zenodo or ResearchEquals DOI
// mentioned anywhere above - usually the `report:` of the codecheck.yml. A
// deposit's description and related identifiers are published with the
// certificate and are as much a part of it as its text.
//
// One level only: a DOI found in a deposit's metadata is asked whether it
// resolves, and its own deposit is not read in turn.
func (l *Links) addDepositions(ctx context.Context, c Context) {
	// A deposit is a place to read and a function that reads it; the list is
	// fixed before any is read, so a DOI one of them mentions is not added.
	type deposit struct {
		where string
		read  func() error
	}
	var deposits []deposit
	zenodo := func(spec RepositorySpec) {
		where := "Zenodo record " + spec.Path
		if spec.Type == "zenodo-sandbox" {
			where = "Zenodo sandbox record " + spec.Path
		}
		if slices.ContainsFunc(deposits, func(d deposit) bool { return d.where == where }) {
			return
		}
		deposits = append(deposits, deposit{where, func() error {
			record, err := c.Services.zenodoRecord(ctx, spec)
			if err == nil {
				l.addZenodo(record, where)
			}
			return err
		}})
	}
	if c.RepositorySpec.Type == "zenodo" || c.RepositorySpec.Type == "zenodo-sandbox" {
		zenodo(c.RepositorySpec)
	}
	for _, doi := range l.mentioned {
		if record := zenodoRecordID.FindStringSubmatch(doi); record != nil {
			zenodo(RepositorySpec{Type: "zenodo", Path: record[1]})
			continue
		}
		where := "ResearchEquals " + doi
		deposits = append(deposits, deposit{where, func() error {
			version, err := c.Services.researchEqualsVersion(ctx, doi)
			if err == nil {
				l.addText(version.Description, where)
				l.addText(version.Changelog, where)
				for _, reference := range stringsIn(version.Refs) {
					l.addDOI(reference, where)
				}
			}
			return err
		}})
	}

	for _, deposit := range deposits {
		if !c.Services.Enabled() {
			l.unread(deposit.where, "reading it needs the external services")
		} else if err := deposit.read(); err != nil {
			l.unread(deposit.where, err.Error())
		} else {
			l.Read = append(l.Read, deposit.where)
		}
	}
}

// addZenodo reads what a Zenodo record says in prose and in its identifiers.
func (l *Links) addZenodo(record zenodoRecord, where string) {
	metadata := record.Metadata
	l.addText(html.UnescapeString(metadata.Description), where)
	l.addText(metadata.Notes, where)
	for _, identifier := range slices.Concat(metadata.RelatedIdentifiers, metadata.AlternateIdentifiers) {
		switch strings.ToLower(identifier.Scheme) {
		case "url":
			l.addText(identifier.Identifier, where)
		case "doi":
			l.addDOI(identifier.Identifier, where)
		}
	}
	for _, reference := range stringsIn(metadata.References) {
		l.addText(reference, where)
	}
}

// stringsIn is every string in a piece of JSON, however it is nested: the
// references of a deposit are a list of strings on every record seen so far,
// and a list of objects would be read rather than refused.
func stringsIn(raw json.RawMessage) []string {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var found []string
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case string:
			found = append(found, value)
		case []any:
			for _, item := range value {
				walk(item)
			}
		case map[string]any:
			for _, item := range value {
				walk(item)
			}
		}
	}
	walk(value)
	return found
}

// --- asking -------------------------------------------------------------------

// Probe asks every link whether it is there, a few at a time, and stops
// asking when ctx is done: a link not reached by then is reported as not
// checked, never as dead.
func (l *Links) Probe(ctx context.Context, services *Services) {
	var (
		all   = make(chan struct{}, linksInFlight)
		hosts = map[string]chan struct{}{}
		wg    sync.WaitGroup
	)
	for _, link := range l.Links {
		if host, skipped := services.skipped(link.URL); skipped {
			link.notAsked(skippedHost, host)
			continue
		}
		if !services.Enabled() {
			link.notAsked(offline, "")
			continue
		}
		host := hostOf(link.URL)
		if hosts[host] == nil {
			hosts[host] = make(chan struct{}, linksPerHost)
		}
		wg.Add(1)
		go func(link *Link, perHost chan struct{}) {
			defer wg.Done()
			// The host's place first: a goroutine waiting for a busy host
			// must not hold one of the places every other host needs.
			if !acquire(ctx, perHost) {
				link.notAsked(outOfTime, "")
				return
			}
			defer func() { <-perHost }()
			if !acquire(ctx, all) {
				link.notAsked(outOfTime, "")
				return
			}
			defer func() { <-all }()
			services.probe(ctx, link)
		}(link, hosts[host])
	}
	wg.Wait()
}

// acquire takes a place in a semaphore, or reports that the run ended first.
func acquire(ctx context.Context, semaphore chan struct{}) bool {
	select {
	case semaphore <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func hostOf(url string) string {
	parsed, err := neturl.Parse(url)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// skipped reports whether a link is to a site the settings say not to ask,
// and which of the listed hosts it matched.
func (s *Services) skipped(url string) (string, bool) {
	if s == nil {
		return "", false
	}
	host := hostOf(url)
	for _, listed := range s.LinkSkipHosts {
		listed = strings.ToLower(strings.TrimSpace(listed))
		if listed != "" && onHost(host, listed) {
			return listed, true
		}
	}
	return "", false
}

// probe asks one link, within linkTimeout, and records what it answered.
//
// A DOI is asked of the DOI system rather than followed: the redirect lands on
// a publisher, and publishers are the sites most likely to refuse a robot, so
// following it would report the publisher's policy as the DOI's health. What
// the DOI system says - this DOI exists, and points here - is the question.
func (s *Services) probe(run context.Context, link *Link) {
	ctx, cancel := context.WithTimeout(run, linkTimeout)
	defer cancel()

	var status int
	var err error
	if doi := doiOf(link.URL); doi != "" {
		var answer handle
		answer, status, err = s.doiHandle(ctx, doi)
		switch {
		case err != nil:
		case answer.ResponseCode == handleFound:
			link.Outcome = OutcomeOK
			return
		case answer.ResponseCode == handleNotFound:
			link.Outcome, link.Says = OutcomeError, "no such DOI"
			return
		case status == http.StatusOK:
			link.Outcome, link.Says = OutcomeSkipped,
				fmt.Sprintf("the DOI system answered code %d", answer.ResponseCode)
			return
		}
	} else {
		// A HEAD first, so that a link to a dataset costs a status line.
		// Some servers answer HEAD with 405, some answer a robot's HEAD
		// with 403 and its GET with the page, some answer a HEAD 404 that a
		// GET would not - and a dead link is the one verdict this report
		// must not get wrong - and some proxies drop a HEAD, so each of those
		// is asked again as a GET. A host that does not exist or did not
		// answer in time will not do better the second time.
		status, err = s.status(ctx, http.MethodHead, link.URL)
		if slices.Contains([]int{http.StatusMethodNotAllowed, http.StatusForbidden,
			http.StatusNotFound, http.StatusGone}, status) ||
			err != nil && !noSuchHost(err) && !timedOut(err) {
			status, err = s.status(ctx, http.MethodGet, link.URL)
		}
	}
	if err != nil && run.Err() != nil {
		// The run ended, not the link: nothing is known about it.
		link.notAsked(outOfTime, "")
		return
	}
	link.Outcome, link.Says = linkOutcome(status, err)
}

// status is what a server answers, and nothing else: the body is closed
// unread. Not cached, because a link is asked once per run, and a GET of a
// multi-gigabyte file must cost the status line and not the 8 MiB that fetch
// would otherwise read and keep.
func (s *Services) status(ctx context.Context, method, url string) (int, error) {
	request, err := newRequest(ctx, method, url)
	if err != nil {
		return 0, err
	}
	response, err := s.HTTP.Do(request)
	if err != nil {
		return 0, err
	}
	response.Body.Close()
	return response.StatusCode, nil
}

func noSuchHost(err error) bool {
	var dns *net.DNSError
	return errors.As(err, &dns) && dns.IsNotFound
}

func timedOut(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout()
}

// linkOutcome turns what a link answered into what the report says of it.
//
// Only "not there" is a dead link. Services.resolves, behind the rules about
// references, fails every 4xx it does not know to be a refusal; this is
// deliberately more forgiving, because it asks every link in a certificate,
// most of them to sites nobody vetted for how they treat a robot, and a report
// that cries wolf is one nobody reads.
func linkOutcome(status int, err error) (Outcome, string) {
	switch {
	case err != nil && noSuchHost(err):
		return OutcomeError, "no such host"
	case err != nil && timedOut(err):
		return OutcomeSkipped, fmt.Sprintf("no answer within %s", linkTimeout)
	case err != nil:
		return OutcomeSkipped, "could not connect"
	case status == http.StatusNotFound, status == http.StatusGone:
		return OutcomeError, fmt.Sprintf("answers %d", status)
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return OutcomeSkipped, fmt.Sprintf("refuses automated requests (%d)", status)
	case status == http.StatusTooManyRequests:
		return OutcomeSkipped, "rate-limited (429)"
	case status >= 500:
		return OutcomeSkipped, fmt.Sprintf("server error (%d)", status)
	case status >= 400:
		return OutcomeSkipped, fmt.Sprintf("answers %d", status)
	}
	return OutcomeOK, ""
}

// doiOf is the DOI a doi.org link resolves, empty for any other link. In
// lower case, as the DOI system treats it: the same DOI written two ways is
// one question, and one entry in the response cache.
func doiOf(url string) string {
	parsed, err := neturl.Parse(url)
	if err != nil {
		return ""
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "doi.org", "dx.doi.org", "www.doi.org":
		return strings.ToLower(DOIIn(parsed.Path))
	}
	return ""
}

// handle is what the DOI system says about one DOI.
type handle struct {
	ResponseCode int `json:"responseCode"`
	Values       []struct {
		Type string `json:"type"`
		Data struct {
			Value any `json:"value"`
		} `json:"data"`
	} `json:"values"`
}

// Handle response codes, https://www.handle.net/proxy_servlet.html.
const (
	handleFound    = 1
	handleNotFound = 100
)

// target is where the DOI points, empty when it says nothing.
func (h handle) target() string {
	for _, value := range h.Values {
		if url, ok := value.Data.Value.(string); ok && value.Type == "URL" {
			return url
		}
	}
	return ""
}

// doiHandle asks the DOI system about one DOI. Cached, because the same DOI is
// asked about to read its deposit and again to report whether it resolves.
func (s *Services) doiHandle(ctx context.Context, doi string) (handle, int, error) {
	url := s.DOI + "/api/handles/" + doi + "?type=URL"
	status, body, err := s.requestContext(ctx, http.MethodGet, url, map[string]string{"Accept": "application/json"})
	if err != nil {
		return handle{}, status, err
	}
	var answer handle
	if status == http.StatusOK || status == http.StatusNotFound {
		if err := json.Unmarshal(body, &answer); err != nil {
			return handle{}, status, fmt.Errorf("the DOI system answered something that is not JSON: %w", err)
		}
	}
	return answer, status, nil
}

// --- the report -----------------------------------------------------------------

// Started is what is posted while the links are asked, when there are enough
// of them that the answer will take a while.
func (l *Links) Started() string {
	return fmt.Sprintf("Checking %d links in %s. That takes a while; "+
		"this comment will be replaced with the answer.\n", l.Len(), l.sources())
}

func (l *Links) sources() string {
	if len(l.Read) == 0 {
		return "nothing that could be read"
	}
	return strings.Join(l.Read, ", ")
}

// Markdown is the answer: the counts, then only the links that need
// attention - gone first, then the ones that could not be asked. A table of
// every link that answered would bury the few a codechecker has to fix.
func (l *Links) Markdown() string {
	var out strings.Builder
	counts := map[Outcome]int{}
	for _, link := range l.Links {
		counts[link.Outcome]++
	}
	asked := l.Len() - counts[OutcomeUnchecked]

	fmt.Fprintf(&out, "Links around `%s`, from %s: %d found", l.Target, l.sources(), l.Len())
	if l.Len() > 0 {
		fmt.Fprintf(&out, ", %d asked: %s %d answer, %s %d gone, %s %d could not say",
			asked, Symbol(OutcomeOK), counts[OutcomeOK], Symbol(OutcomeError), counts[OutcomeError],
			Symbol(OutcomeSkipped), counts[OutcomeSkipped])
	}
	out.WriteString(".\n")

	attention := slices.DeleteFunc(slices.Clone(l.Links), func(link *Link) bool {
		return link.Outcome != OutcomeError && link.Outcome != OutcomeSkipped
	})
	slices.SortStableFunc(attention, func(a, b *Link) int {
		// ✖ before …: the dead links are the ones to act on.
		return rankOf(a.Outcome) - rankOf(b.Outcome)
	})
	if len(attention) > 0 {
		out.WriteString("\n| | Link | Says | Found in |\n|---|---|---|---|\n")
		for _, link := range attention {
			fmt.Fprintf(&out, "| %s | %s | %s | %s |\n", Symbol(link.Outcome), escapePipes(link.URL),
				escapePipes(link.Says), escapePipes(foundIn(link.Found)))
		}
	} else if asked > 0 {
		out.WriteString("\nEvery link that was asked answers.\n")
	}

	if notes := l.notes(); len(notes) > 0 {
		out.WriteString("\n")
		for _, note := range notes {
			fmt.Fprintf(&out, "- %s\n", note)
		}
	}
	out.WriteString("\nThis is advice, not a verdict: a site that could not say today may answer tomorrow, " +
		"and nothing here holds up the check.\n")
	return out.String()
}

func rankOf(outcome Outcome) int {
	if outcome == OutcomeError {
		return 0
	}
	return 1
}

// foundIn names where a link was seen, the first few of them.
func foundIn(places []string) string {
	const shown = 3
	if len(places) <= shown {
		return strings.Join(places, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(places[:shown], ", "), len(places)-shown)
}

// notes are what was not asked or not read, each as a count rather than a
// list: the hosts that refuse robots are the same few on every check.
func (l *Links) notes() []string {
	var notes []string
	var hosts []string
	count := map[unasked]int{}
	for _, link := range l.Links {
		count[link.unasked]++
		if link.unasked == skippedHost && !slices.Contains(hosts, link.host) {
			hosts = append(hosts, link.host)
		}
	}
	if n := count[skippedHost]; n > 0 {
		notes = append(notes, fmt.Sprintf("%s %s not asked, to sites that refuse automated requests: %s.",
			Symbol(OutcomeUnchecked), links(n), strings.Join(hosts, ", ")))
	}
	if n := count[outOfTime]; n > 0 {
		notes = append(notes, fmt.Sprintf("%s %s not asked: the time ran out first.",
			Symbol(OutcomeUnchecked), links(n)))
	}
	if n := count[offline]; n > 0 {
		notes = append(notes, fmt.Sprintf("%s %s not asked: the external services are not enabled.",
			Symbol(OutcomeUnchecked), links(n)))
	}
	for _, unread := range l.Unread {
		notes = append(notes, "Not read: "+unread+".")
	}
	return notes
}

func links(n int) string { return fmt.Sprintf("%d link%s", n, plural(n)) }
