package check

import (
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Where the files that go with a codecheck.yml are.
//
// One idea of the bundle, whether the configuration was read from a directory
// on disk or fetched from a repository, and the only thing that knows how to
// reach the files of one service or another - the configuration itself is read
// through it too. Before this there were two ideas of a bundle, a local
// directory for the rules about the bundle and a repository specification
// grafted onto a URL for the manifest, so every rule about the bundle skipped
// for a repository the bot had just read a configuration out of. Four skips in
// a row read as a broken bot rather than as a check that could not run. See
// codecheckers/chekhov#17.
//
// Paths are relative to the codecheck.yml and written with forward slashes, as
// the specification writes them; the empty path is the directory the
// configuration itself is in.
type Bundle interface {
	// Read returns the contents of one file of the bundle.
	Read(path string) ([]byte, error)
	// Exists reports whether a path is in the bundle.
	Exists(path string) (bool, error)
	// List names what is in one directory of the bundle.
	List(dir string) ([]BundleEntry, error)
	// Licence is the licence the bundle states, empty when it states none.
	//
	// How a bundle states one depends on where it is. A git repository
	// carries a LICENSE file; an archive says so in its metadata, where there
	// is no file to look for. CC-BUN-005 asks whether the repository under
	// check *states* a licence, so each bundle is asked in its own terms.
	Licence() (string, error)
	// Describe says where the bundle is, for a finding to name.
	Describe() string
}

// listingPageSize is how many entries to ask a paging API for at a time.
const listingPageSize = 100

// BundleEntry is one file or directory of a bundle.
type BundleEntry struct {
	Name  string
	IsDir bool
	// Size is the file in bytes, or SizeUnknown when the listing does not
	// say - GitLab's tree gives names and nothing else. Zero is a real
	// answer: an empty file is not an unmeasured one.
	Size int64
}

// SizeUnknown is a BundleEntry the listing gave no size for.
const SizeUnknown int64 = -1

// A counter is a bundle that can say how big it is without being walked a
// directory at a time.
//
// Counting and listing are different questions, and three of the four sources
// answer them with different endpoints. Listing one directory is what the
// manifest rules need, and it stays exactly as it was; counting the whole
// bundle is what `check repository` needs, and asking for it a directory at a
// time costs one request per directory for an answer the platform will give in
// one. A bundle that has no cheaper shape simply does not implement this, and
// is walked. See codecheckers/chekhov#46.
type counter interface {
	Count() (Tally, error)
}

// A Tally is how much a bundle holds.
//
// Embedded in Description, so that a source which answers in one request and a
// source which has to be walked fill in the same fields by the same rules,
// rather than two shapes of the same numbers with a copy between them.
type Tally struct {
	Files   int
	Bytes   int64
	Unsized int
	// Partial says the count is a floor rather than a total: the platform
	// truncated its answer, or the bundle is larger than a walk will read.
	Partial bool
}

// add puts one file into the tally, with the one rule about sizes: a listing
// that gives none is counted as unsized rather than as nothing. An empty file
// is not an unmeasured one.
func (t *Tally) add(size int64) {
	t.Files++
	if size == SizeUnknown {
		t.Unsized++
		return
	}
	t.Bytes += size
}

// full reports whether a walk has counted as much as a description reads.
// Only the walk asks: a source that answers in one request has already paid
// for the whole answer, and reporting "more than two thousand" when it knows
// the number would be throwing away what was bought.
func (t *Tally) full() bool { return t.Files >= measureFileLimit }

// errNoServices is what a repository bundle answers when the outside world is
// switched off. The rules turn it into their own skip, so that "could not
// look" never reads as "looked and found nothing".
var errNoServices = errors.New("external services not enabled")

// errNoSuchDirectory is a directory the bundle does not have. A manifest entry
// under it is absent, which is a finding about that entry rather than a reason
// to give up on the whole manifest.
var errNoSuchDirectory = errors.New("no such directory in the bundle")

// bundleOf resolves what somebody wrote to the repository it names and the
// bundle its files are in.
//
// The one place that does it: reading a configuration out of a repository and
// describing the repository itself both start here, so a target cannot be
// understood one way by `check` and another by `check repository`.
func bundleOf(target string, services *Services) (RepositorySpec, Bundle, error) {
	// Resolved before the services are asked for, so that a target nobody can
	// make sense of is answered as such rather than as an outage.
	spec, err := ResolveTarget(target, services)
	if err != nil {
		return RepositorySpec{}, nil, err
	}
	if !services.Enabled() {
		return spec, nil, fmt.Errorf("reading %s needs the external services", spec)
	}
	bundle := bundleFor(spec, services)
	if bundle == nil {
		return spec, nil, fmt.Errorf("unsupported repository type %q", spec.Type)
	}
	return spec, bundle, nil
}

// bundleFor is the bundle of a repository, nil for a kind of repository this
// bot cannot read. Which service to ask is decided here, once, rather than at
// every call.
func bundleFor(spec RepositorySpec, services *Services) Bundle {
	switch spec.Type {
	case "github":
		bundle := &githubBundle{}
		bundle.of(spec, services, bundle.tree)
		return bundle
	case "gitlab":
		bundle := &gitlabBundle{}
		bundle.of(spec, services, bundle.tree)
		return bundle
	case "osf":
		bundle := &osfBundle{}
		bundle.of(spec, services, bundle.tree)
		return bundle
	case "zenodo", "zenodo-sandbox":
		bundle := &zenodoBundle{}
		bundle.of(spec, services, bundle.tree)
		return bundle
	default:
		return nil
	}
}

// --- reading a directory once ----------------------------------------------

// memo remembers what a directory held. Three rules ask about the root of the
// bundle and a manifest asks about a file at a time, and none of them should
// cost a second listing - for a repository that is a second parse of a
// response that may run to hundreds of kilobytes.
//
// A failure is not remembered: it is usually a service having a bad moment,
// and the response cache already keeps the reader from asking twice.
type memo struct {
	readDir func(dir string) ([]BundleEntry, error)
	mu      sync.Mutex
	dirs    map[string][]BundleEntry
}

func (m *memo) List(dir string) ([]BundleEntry, error) {
	dir = strings.Trim(dir, "/")
	m.mu.Lock()
	defer m.mu.Unlock()
	if entries, remembered := m.dirs[dir]; remembered {
		return entries, nil
	}
	entries, err := m.readDir(dir)
	if err != nil {
		return nil, err
	}
	if m.dirs == nil {
		m.dirs = map[string][]BundleEntry{}
	}
	m.dirs[dir] = entries
	return entries, nil
}

// --- a directory on disk ---------------------------------------------------

type localBundle struct {
	memo
	dir string
}

func newLocalBundle(dir string) *localBundle {
	bundle := &localBundle{dir: dir}
	bundle.readDir = bundle.entries
	return bundle
}

func (b *localBundle) Describe() string { return b.dir }

func (b *localBundle) Read(name string) ([]byte, error) { return os.ReadFile(b.join(name)) }

func (b *localBundle) Exists(name string) (bool, error) {
	info, err := os.Stat(b.join(name))
	if err == nil {
		// A directory is not the file the manifest names, and a repository
		// bundle would answer 404 for it: one bundle, one verdict.
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (b *localBundle) Licence() (string, error) { return licenceFileIn(b) }

func (b *localBundle) entries(dir string) ([]BundleEntry, error) {
	entries, err := os.ReadDir(b.join(dir))
	if err != nil {
		return nil, err
	}
	listing := make([]BundleEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		listing = append(listing, fileEntry(entry.Name(), entry.IsDir(), sizeOf(info, err)))
	}
	return listing, nil
}

func (b *localBundle) join(name string) string {
	return filepath.Join(b.dir, filepath.FromSlash(strings.Trim(name, "/")))
}

// --- a repository ----------------------------------------------------------

// repoBundle is what the four repository kinds share: where they are, how to
// reach the outside world, and the listing they have already read. The
// sub-directory of an `org/repo|path` specification is part of the bundle's own
// idea of where it is, through RepositorySpec.path, so no check has to
// remember it.
type repoBundle struct {
	memo
	spec     RepositorySpec
	services *Services
}

func (b *repoBundle) of(spec RepositorySpec, services *Services, readDir func(string) ([]BundleEntry, error)) {
	b.spec, b.services, b.readDir = spec, services, readDir
}

func (b *repoBundle) Describe() string { return b.spec.String() }

// available reports that the outside world can be asked, because a repository
// bundle is nothing without it.
func (b *repoBundle) available() error {
	if !b.services.Enabled() {
		return errNoServices
	}
	return nil
}

// existsInListing answers for the services that hold files rather than a tree
// at a URL: what is there is what the listing says is there.
func existsInListing(bundle Bundle, name string) (bool, error) {
	dir, file := path.Split(strings.Trim(name, "/"))
	entries, err := bundle.List(dir)
	if errors.Is(err, errNoSuchDirectory) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name == file {
			return true, nil
		}
	}
	return false, nil
}

// --- GitHub ---

type githubBundle struct{ repoBundle }

func (b *githubBundle) url(name string) string {
	return b.spec.within(b.spec.rawBase(b.services), name)
}

func (b *githubBundle) Read(name string) ([]byte, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	return b.services.FetchFile(b.url(name))
}

func (b *githubBundle) Exists(name string) (bool, error) {
	if err := b.available(); err != nil {
		return false, err
	}
	return rawExists(b.services, b.url(name))
}

func (b *githubBundle) Licence() (string, error) { return licenceFileIn(b) }

func (b *githubBundle) tree(dir string) ([]BundleEntry, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	// The contents API answers a whole directory at once, up to a thousand
	// entries; beyond that GitHub asks for the git tree API instead, which no
	// CODECHECK bundle has needed yet.
	url := fmt.Sprintf("%s/repos/%s/contents/%s?ref=HEAD",
		b.services.GitHub, b.spec.Path, escapePathSegments(b.spec.path(dir)))
	var listing []treeEntry
	if err := b.services.github(url, &listing); err != nil {
		return nil, err
	}
	return entriesOf(listing, "dir", true), nil
}

// Count reads the bundle's tree in one request.
//
// The contents API above answers one directory, which is the right call when a
// rule asks about one directory; counting a bundle through it costs a request
// per directory, up to the two hundred the walk allows. The git tree API gives
// every path and every blob size in a single response.
//
// The tree-ish names the bundle rather than the repository - `HEAD:codecheck`
// for an `org/repo|codecheck` target - so a bundle in a sub-directory does not
// fetch the whole repository to discard most of it, the paths come back
// relative to the bundle, and `truncated` is a statement about this bundle
// rather than about something elsewhere in the repository. That is exactly
// what Partial means: this count is a floor.
//
// Nothing is clamped here. The walk stops at a limit because each directory
// past it costs another request; one response has already been paid for
// whole, and answering "more than two thousand files" when the number is in
// hand would be throwing away what was bought.
func (b *githubBundle) Count() (Tally, error) {
	if err := b.available(); err != nil {
		return Tally{}, err
	}
	var tree struct {
		Tree []struct {
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	url := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1",
		b.services.GitHub, b.spec.Path, b.treeish())
	if err := b.services.github(url, &tree); err != nil {
		return Tally{}, err
	}

	tally := Tally{Partial: tree.Truncated}
	for _, item := range tree.Tree {
		// Everything that is not a directory is a file, which is what the
		// per-directory listing this replaces says too - entriesOf reads
		// anything but a `dir` as a file. A submodule counts as the one entry
		// it is rather than as the repository behind it, which is not in this
		// bundle; a description must not depend on which endpoint answered.
		if item.Type != "tree" {
			tally.add(item.Size)
		}
	}
	return tally, nil
}

// treeish names the bundle's own tree: the commit for a repository, and the
// sub-directory inside it for an `org/repo|path` target.
func (b *githubBundle) treeish() string {
	if inside := b.spec.path(""); inside != "" {
		return "HEAD:" + escapePathSegments(inside)
	}
	return "HEAD"
}

// --- GitLab ---

type gitlabBundle struct {
	repoBundle
	// branch is the default branch that answered, remembered so that a
	// master-era project is not probed for main once per manifest file.
	branch string
}

func (b *gitlabBundle) branches() []string {
	if b.branch != "" {
		return []string{b.branch}
	}
	return gitLabBranches
}

func (b *gitlabBundle) url(branch, name string) string {
	return b.spec.within(b.spec.gitLabRawBase(b.services, branch), name) + "?inline=false"
}

func (b *gitlabBundle) Read(name string) ([]byte, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	var lastErr error
	for _, branch := range b.branches() {
		raw, err := b.services.FetchFile(b.url(branch, name))
		if err == nil {
			b.branch = branch
			return raw, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (b *gitlabBundle) Exists(name string) (bool, error) {
	if err := b.available(); err != nil {
		return false, err
	}
	var lastErr error
	for _, branch := range b.branches() {
		found, err := rawExists(b.services, b.url(branch, name))
		switch {
		case err != nil:
			lastErr = err
		case found:
			b.branch = branch
			return true, nil
		}
	}
	return false, lastErr
}

func (b *gitlabBundle) Licence() (string, error) { return licenceFileIn(b) }

func (b *gitlabBundle) tree(dir string) ([]BundleEntry, error) {
	listing, whole, err := b.treePages(b.spec.path(dir), false)
	if err != nil {
		return nil, err
	}
	if !whole {
		// A listing cut short is worse than no listing: a manifest file past
		// the cut would read as a file the codechecker forgot to commit. A
		// rule that cannot see the whole directory has not looked at it.
		return nil, fmt.Errorf("the listing of %s in %s is longer than %d entries",
			b.spec.path(dir), b.spec, maxTreePages*listingPageSize)
	}
	// GitLab's tree gives names and no sizes.
	return entriesOf(listing, "tree", false), nil
}

// Count reads the whole tree, one paginated walk rather than one per
// directory.
//
// `recursive` is the only difference from tree above, and it collapses the
// per-directory loop into the pagination that was there anyway. Still no
// sizes, so every file counts as unsized: that is the honest answer, and the
// one the walk gave too.
func (b *gitlabBundle) Count() (Tally, error) {
	listing, whole, err := b.treePages(b.spec.path(""), true)
	if err != nil {
		return Tally{}, err
	}
	var tally Tally
	for _, entry := range entriesOf(listing, "tree", false) {
		if !entry.IsDir {
			tally.add(entry.Size)
		}
	}
	// Counting is the one question a short answer can still be useful for:
	// "more than this" is the same judgement the number was wanted for.
	tally.Partial = !whole
	return tally, nil
}

// maxTreePages bounds a paginated tree. GitLab spends a request per page, so
// this is a limit on what a listing may cost, in the unit it costs it in -
// unlike the walk's limits, which bound a description's reading.
const maxTreePages = 200

// treePages reads a GitLab tree, a page at a time, and says whether it reached
// the end.
//
// One page at a time because a data-heavy bundle with more than a hundred
// entries would otherwise hide its codecheck/ directory, and a hidden one
// reads as a finding rather than as a skip. One function because listing a
// directory and counting the bundle are the same request with one parameter
// changed, and two copies of it would drift.
//
// Reaching the page bound is reported rather than hidden, because the two
// callers answer it differently: a count says it is a floor, and a listing
// refuses, since a rule that cannot see the whole directory has not looked at
// it. A last page that is full is not proof there is nothing after it, so it
// counts as not having reached the end.
func (b *gitlabBundle) treePages(path string, recursive bool) ([]treeEntry, bool, error) {
	if err := b.available(); err != nil {
		return nil, false, err
	}
	var listing []treeEntry
	for page := 1; page <= maxTreePages; page++ {
		url := fmt.Sprintf("%s/api/v4/projects/%s/repository/tree?path=%s&per_page=%d&page=%d",
			b.services.GitLab, neturl.QueryEscape(b.spec.Path),
			neturl.QueryEscape(path), listingPageSize, page)
		if recursive {
			url += "&recursive=true"
		}
		var batch []treeEntry
		if err := b.services.getJSON(url, nil, &batch); err != nil {
			return nil, false, err
		}
		listing = append(listing, batch...)
		if len(batch) < listingPageSize {
			return listing, true, nil
		}
	}
	return listing, false, nil
}

// --- OSF ---

type osfBundle struct {
	repoBundle
	// hrefs is where each directory's own listing is, learned from the
	// listing of the folder above it. OSF names no paths, so without this
	// every directory is reached by following the node root down again: the
	// response cache spares the network, but each cached body is unmarshalled
	// afresh, so a walk three deep re-parses every ancestor's pages once per
	// directory. The parent listing already carries the child's href, and
	// remembering it makes descending cost one request from the parent rather
	// than a re-walk. Keyed by the path inside the repository, as memo is.
	//
	// Its own lock rather than memo's: listing is reached both through
	// memo.List, which holds that lock across the read, and directly from
	// item, which does not - so taking memo's here would deadlock on one path
	// and guard nothing on the other.
	mu    sync.Mutex
	hrefs map[string]string
}

// remember records where a directory's own listing is, so that the next
// descent starts from it rather than from the node root.
func (b *osfBundle) remember(dir, href string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hrefs == nil {
		b.hrefs = map[string]string{}
	}
	b.hrefs[dir] = href
}

// from is the deepest listing already known on the way to a directory: its own
// href when it has been reached before, otherwise the nearest ancestor's, and
// the node root when none is known. The index is how many of the segments that
// href has already accounted for, so the caller follows the rest.
func (b *osfBundle) from(segments []string) (href string, reached int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(segments); i > 0; i-- {
		if known, ok := b.hrefs[strings.Join(segments[:i], "/")]; ok {
			return known, i
		}
	}
	return b.spec.osfRoot(b.services), 0
}

func (b *osfBundle) Read(name string) ([]byte, error) {
	item, found, err := b.item(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("no %s in OSF node %s", name, b.spec.Path)
	}
	return b.services.FetchFile(item.Links.Download)
}

func (b *osfBundle) Exists(name string) (bool, error) { return existsInListing(b, name) }

// Licence reads the node's licence, which OSF keeps as its own resource rather
// than as a file. A node that has not chosen one carries the licence named
// "No license", which states nothing.
func (b *osfBundle) Licence() (string, error) {
	if err := b.available(); err != nil {
		return "", err
	}
	var node struct {
		Data struct {
			Relationships struct {
				License struct {
					Links struct {
						Related struct {
							Href string `json:"href"`
						} `json:"related"`
					} `json:"links"`
				} `json:"license"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := b.services.getJSON(fmt.Sprintf("%s/nodes/%s/", b.services.OSF, b.spec.Path), nil, &node); err != nil {
		return "", fmt.Errorf("could not read OSF node %s: %w", b.spec.Path, err)
	}

	href := node.Data.Relationships.License.Links.Related.Href
	if href == "" {
		return licenceFileIn(b)
	}
	var licence struct {
		Data struct {
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := b.services.getJSON(href, nil, &licence); err != nil {
		return "", fmt.Errorf("could not read the licence of OSF node %s: %w", b.spec.Path, err)
	}
	if name := licence.Data.Attributes.Name; name != "" && !strings.EqualFold(name, noOSFLicence) {
		return name, nil
	}
	return licenceFileIn(b)
}

// noOSFLicence is what OSF calls a node that has chosen no licence.
const noOSFLicence = "No license"

func (b *osfBundle) tree(dir string) ([]BundleEntry, error) {
	listing, err := b.listing(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]BundleEntry, 0, len(listing.Data))
	for _, item := range listing.Data {
		entries = append(entries, fileEntry(item.Attributes.Name, item.folder(), item.Attributes.Size))
	}
	return entries, nil
}

// item finds one file of the node, for its download link.
func (b *osfBundle) item(name string) (osfItem, bool, error) {
	dir, file := path.Split(strings.Trim(name, "/"))
	listing, err := b.listing(dir)
	if err != nil {
		return osfItem{}, false, err
	}
	for _, item := range listing.Data {
		if item.Attributes.Name == file {
			return item, true, nil
		}
	}
	return osfItem{}, false, nil
}

// listing walks to one directory of the node. OSF names no paths: a directory
// is reached by following the listing of the folder above it, one segment at a
// time.
func (b *osfBundle) listing(dir string) (osfListing, error) {
	if err := b.available(); err != nil {
		return osfListing{}, err
	}
	// Start from the deepest listing already reached on the way here, which
	// for the common descent - a directory whose parent was just listed - is
	// the parent itself, and so costs one request rather than a walk from the
	// node root. FieldsFunc rather than Split: the root is no segments at all
	// rather than one empty one.
	segments := strings.FieldsFunc(b.spec.path(dir), func(r rune) bool { return r == '/' })
	href, reached := b.from(segments)
	listing, err := b.services.osfListing(href, b.spec.Path)
	if err != nil {
		return listing, err
	}

	for _, segment := range segments[reached:] {
		next := ""
		for _, item := range listing.Data {
			if item.Attributes.Name == segment && item.folder() {
				next = item.Relationships.Files.Links.Related.Href
			}
		}
		if next == "" {
			return osfListing{}, fmt.Errorf("%w: no %s in OSF node %s",
				errNoSuchDirectory, segment, b.spec.Path)
		}
		reached++
		b.remember(strings.Join(segments[:reached], "/"), next)
		if listing, err = b.services.osfListing(next, b.spec.Path); err != nil {
			return listing, err
		}
	}
	return listing, nil
}

// --- Zenodo ---

type zenodoBundle struct{ repoBundle }

func (b *zenodoBundle) Read(name string) ([]byte, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	record, err := b.services.zenodoRecordOf(b.spec)
	if err != nil {
		return nil, err
	}
	wanted := b.spec.path(name)
	for _, file := range record.Files {
		if file.name() == wanted {
			if link := file.downloadLink(); link != "" {
				return b.services.FetchFile(link)
			}
		}
	}
	return nil, fmt.Errorf("no %s in Zenodo record %s", name, b.spec.Path)
}

func (b *zenodoBundle) Exists(name string) (bool, error) { return existsInListing(b, name) }

// Licence reads the record's own licence. A deposit has no LICENSE file to
// find unless the whole bundle was uploaded as files, so the metadata is where
// it says so; the file is still looked for when the metadata says nothing.
func (b *zenodoBundle) Licence() (string, error) {
	if err := b.available(); err != nil {
		return "", err
	}
	record, err := b.services.zenodoRecordOf(b.spec)
	if err != nil {
		return "", err
	}
	if licence := record.licence(); licence != "" {
		return licence, nil
	}
	return licenceFileIn(b)
}

// Count folds the record's files into a tally in one pass.
//
// A Zenodo record is flat and is fetched whole, so the walk costs no requests
// - but it re-derives the prefixes of every file once per directory, and
// re-unmarshals the cached record body each time it does. Counting the files
// the record already lists is the same answer without either.
func (b *zenodoBundle) Count() (Tally, error) {
	if err := b.available(); err != nil {
		return Tally{}, err
	}
	record, err := b.services.zenodoRecordOf(b.spec)
	if err != nil {
		return Tally{}, err
	}

	prefix := b.spec.path("")
	if prefix != "" {
		prefix += "/"
	}
	var tally Tally
	for _, file := range record.Files {
		if _, under := strings.CutPrefix(file.name(), prefix); under {
			tally.add(file.Size)
		}
	}
	return tally, nil
}

// tree lists a record's files. A record is flat, so a directory is whatever
// prefix the file names share.
func (b *zenodoBundle) tree(dir string) ([]BundleEntry, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	record, err := b.services.zenodoRecordOf(b.spec)
	if err != nil {
		return nil, err
	}

	prefix := b.spec.path(dir)
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]bool{}
	var entries []BundleEntry
	for _, file := range record.Files {
		name, under := strings.CutPrefix(file.name(), prefix)
		if !under {
			continue
		}
		child, _, nested := strings.Cut(name, "/")
		if seen[child] {
			continue
		}
		seen[child] = true
		entries = append(entries, fileEntry(child, nested, file.Size))
	}
	return entries, nil
}

// --- shared ----------------------------------------------------------------

// treeEntry is one entry of a git platform's directory listing. GitHub and
// GitLab answer the same two fields, and disagree only on the word for a
// directory.
type treeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// entriesOf reads a listing. sized says whether this platform's listing
// carries file sizes at all: GitHub's does, GitLab's does not, and reading
// GitLab's absent size as a zero would report a gigabyte of data as nothing.
func entriesOf(listing []treeEntry, directory string, sized bool) []BundleEntry {
	entries := make([]BundleEntry, 0, len(listing))
	for _, item := range listing {
		size := SizeUnknown
		if sized {
			size = item.Size
		}
		entries = append(entries, fileEntry(item.Name, item.Type == directory, size))
	}
	return entries
}

// fileEntry is one listing entry with the one rule about sizes applied: a
// directory has no size of its own, whatever the listing said about it.
func fileEntry(name string, isDir bool, size int64) BundleEntry {
	if isDir {
		size = SizeUnknown
	}
	return BundleEntry{Name: name, IsDir: isDir, Size: size}
}

// sizeOf is what a directory entry on disk weighs, unknown when it could not
// be stat'ed.
func sizeOf(info os.FileInfo, err error) int64 {
	if err != nil {
		return SizeUnknown
	}
	return info.Size()
}

// licenceFileIn is how a git repository states its licence: a file at the root
// of the bundle, named the way everyone names it.
func licenceFileIn(bundle Bundle) (string, error) {
	entries, err := bundle.List("")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir && licenceFile.MatchString(entry.Name) {
			return entry.Name, nil
		}
	}
	return "", nil
}

// rawExists reports whether a file is served at a URL, a HEAD where it can.
//
// Only "not there" is absence. A rate limit, a robots policy or a server
// having a bad moment says nothing about whether the file exists, and must
// never be reported as a manifest file the codechecker forgot to commit.
func rawExists(services *Services, url string) (bool, error) {
	status, err := services.exists(url)
	if err != nil {
		return false, fmt.Errorf("could not reach %s: %w", url, err)
	}
	switch {
	case status < 400:
		return true, nil
	case status == http.StatusNotFound, status == http.StatusGone:
		return false, nil
	default:
		return false, fmt.Errorf("%s answers %d, which blocks the check rather than failing it",
			url, status)
	}
}

// escapePathSegments escapes each segment of a path, leaving the separators
// alone: a path is not one URL segment.
func escapePathSegments(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = neturl.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
