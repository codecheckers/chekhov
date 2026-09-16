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
	// Describe says where the bundle is, for a finding to name.
	Describe() string
}

// listingPageSize is how many entries to ask a paging API for at a time.
const listingPageSize = 100

// BundleEntry is one file or directory of a bundle.
type BundleEntry struct {
	Name  string
	IsDir bool
}

// errNoServices is what a repository bundle answers when the outside world is
// switched off. The rules turn it into their own skip, so that "could not
// look" never reads as "looked and found nothing".
var errNoServices = errors.New("external services not enabled")

// errNoSuchDirectory is a directory the bundle does not have. A manifest entry
// under it is absent, which is a finding about that entry rather than a reason
// to give up on the whole manifest.
var errNoSuchDirectory = errors.New("no such directory in the bundle")

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

func (b *localBundle) entries(dir string) ([]BundleEntry, error) {
	entries, err := os.ReadDir(b.join(dir))
	if err != nil {
		return nil, err
	}
	listing := make([]BundleEntry, 0, len(entries))
	for _, entry := range entries {
		listing = append(listing, BundleEntry{Name: entry.Name(), IsDir: entry.IsDir()})
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
	return b.services.fetchFile(b.url(name))
}

func (b *githubBundle) Exists(name string) (bool, error) {
	if err := b.available(); err != nil {
		return false, err
	}
	return rawExists(b.services, b.url(name))
}

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
	return entriesOf(listing, "dir"), nil
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
		raw, err := b.services.fetchFile(b.url(branch, name))
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

func (b *gitlabBundle) tree(dir string) ([]BundleEntry, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	// A page at a time: a data-heavy bundle with more than a hundred entries
	// would otherwise hide its codecheck/ directory, and a hidden one now
	// reads as a finding rather than as a skip.
	var listing []treeEntry
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/api/v4/projects/%s/repository/tree?path=%s&per_page=%d&page=%d",
			b.services.GitLab, neturl.QueryEscape(b.spec.Path),
			neturl.QueryEscape(b.spec.path(dir)), listingPageSize, page)
		var batch []treeEntry
		if err := b.services.getJSON(url, nil, &batch); err != nil {
			return nil, err
		}
		listing = append(listing, batch...)
		if len(batch) < listingPageSize {
			break
		}
	}
	return entriesOf(listing, "tree"), nil
}

// --- OSF ---

type osfBundle struct{ repoBundle }

func (b *osfBundle) Read(name string) ([]byte, error) {
	item, found, err := b.item(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("no %s in OSF node %s", name, b.spec.Path)
	}
	return b.services.fetchFile(item.Links.Download)
}

func (b *osfBundle) Exists(name string) (bool, error) { return existsInListing(b, name) }

func (b *osfBundle) tree(dir string) ([]BundleEntry, error) {
	listing, err := b.listing(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]BundleEntry, 0, len(listing.Data))
	for _, item := range listing.Data {
		entries = append(entries, BundleEntry{
			Name:  item.Attributes.Name,
			IsDir: item.folder(),
		})
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
	listing, err := b.services.osfListing(b.spec.osfRoot(b.services), b.spec.Path)
	if err != nil {
		return listing, err
	}
	for _, segment := range strings.Split(b.spec.path(dir), "/") {
		if segment == "" {
			continue
		}
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
				return b.services.fetchFile(link)
			}
		}
	}
	return nil, fmt.Errorf("no %s in Zenodo record %s", name, b.spec.Path)
}

func (b *zenodoBundle) Exists(name string) (bool, error) { return existsInListing(b, name) }

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
		entries = append(entries, BundleEntry{Name: child, IsDir: nested})
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
}

func entriesOf(listing []treeEntry, directory string) []BundleEntry {
	entries := make([]BundleEntry, 0, len(listing))
	for _, item := range listing {
		entries = append(entries, BundleEntry{Name: item.Name, IsDir: item.Type == directory})
	}
	return entries
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
