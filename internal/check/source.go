package check

import (
	"fmt"
	neturl "net/url"
	"slices"
	"strings"
	"time"
)

// Where a codecheck.yml comes from.
//
// The register names a repository the way register.csv does, `type::path`, and
// the bot has to be able to read a configuration from all four kinds - a
// codechecker asks it about a repository, not about a file on the bot's disk.
// The R package does the same in get_codecheck_yml_{github,gitlab,osf,zenodo}
// (R/configuration.R), and this mirrors it.

// RepositorySpec is a parsed `type::path` from register.csv.
type RepositorySpec struct {
	Type string
	Path string
	// SubPath is the directory inside the repository that holds the
	// codecheck.yml, written as `org/repo|path` in register.csv. Manifest
	// paths are relative to the configuration file, so they are relative to
	// this directory too.
	SubPath string
}

// ParseRepositorySpec reads `github::org/repo`, `github::org/repo|sub/dir`,
// `gitlab::group/project`, `osf::<id>` or `zenodo::<id>`.
func ParseRepositorySpec(spec string) (RepositorySpec, error) {
	pieces := strings.SplitN(strings.TrimSpace(spec), "::", 2)
	if len(pieces) != 2 || pieces[0] == "" || pieces[1] == "" {
		return RepositorySpec{}, fmt.Errorf(
			"malformed repository specification %q, expected one of %s",
			spec, strings.Join(withSuffix(knownRepositoryTypes, "::"), " "))
	}

	parsed := RepositorySpec{Type: pieces[0], Path: pieces[1]}
	if !slices.Contains(knownRepositoryTypes, parsed.Type) {
		return RepositorySpec{}, fmt.Errorf("unsupported repository type %q, expected one of %s",
			parsed.Type, strings.Join(knownRepositoryTypes, ", "))
	}

	if path, subPath, found := strings.Cut(parsed.Path, "|"); found {
		parsed.Path, parsed.SubPath = path, strings.Trim(subPath, "/")
	}
	return parsed, nil
}

// IsRepositorySpec reports whether a string is a repository specification
// rather than a path to a file.
func IsRepositorySpec(candidate string) bool {
	return strings.Contains(candidate, "::")
}

// FromRepository fetches the codecheck.yml of a repository and returns the
// context to validate it in. The spec may be written in any form
// ResolveTarget accepts.
//
// The configuration is read through the repository's bundle, which then stays
// on the context: the rules about the bundle look in the same place the file
// came from, rather than saying they could not look.
func FromRepository(spec string, services *Services) (Context, error) {
	parsed, bundle, err := bundleOf(spec, services)
	if err != nil {
		return Context{}, err
	}
	raw, err := bundle.Read("codecheck.yml")
	if err != nil {
		// A target written as a shortcut or as a certificate identifier says
		// what it came to, because otherwise the reader cannot tell which
		// repository it was that answered 404.
		if resolved := parsed.String(); resolved != strings.TrimSpace(spec) {
			return Context{}, fmt.Errorf("%s: %w", resolved, err)
		}
		return Context{}, err
	}

	context := FromBytes(raw)
	// The file was asked for by name, so the name and location rule has its
	// answer.
	context.Path = "codecheck.yml"
	// The resolved form, not what was written: a target given as a shortcut or
	// as a certificate identifier should say which repository it came to.
	context.Label = parsed.String()
	context.Services = services
	context.RepositorySpec = parsed
	context.Bundle = bundle

	// Only a file that says nothing about its version needs dating, and dating
	// it costs a request - so ask only then. One that names a version this
	// build does not know is refused, not dated, see SpecVersion. The test is whether check_time
	// can actually be read, not whether it is present: a date in a form
	// nothing understands is no date at all.
	if !context.Config.HasVersion {
		if _, dated := parseCheckTime(context.Config.CheckTime); !dated {
			context.Modified = lastModified(parsed, services)
		}
	}
	return context, nil
}

// lastModified is when the configuration was last changed at its source, zero
// when that cannot be established. See SpecVersion.
func lastModified(spec RepositorySpec, services *Services) time.Time {
	switch spec.Type {
	case "github":
		_, when, err := services.lastCommit(spec.Path, spec.path("codecheck.yml"))
		if err != nil {
			return time.Time{}
		}
		return when
	case "zenodo", "zenodo-sandbox":
		record, err := services.zenodoRecordOf(spec)
		if err != nil {
			return time.Time{}
		}
		if when, err := time.Parse(time.DateOnly, record.Published); err == nil {
			return when
		}
		return record.Created
	default:
		// GitLab and OSF can say too, but nothing in the register needs it yet.
		return time.Time{}
	}
}

// lastCommit is the newest commit touching one file of a GitHub repository:
// which commit it was, and when it was made.
//
// Two callers want different halves of the same request - dating a
// configuration wants the time, and comparing the bundled rules with the
// register wants the commit - so it is asked for once, here.
func (s *Services) lastCommit(repository, file string) (string, time.Time, error) {
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	url := fmt.Sprintf("%s/repos/%s/commits?path=%s&per_page=1",
		s.GitHub, repository, neturl.QueryEscape(file))
	if err := s.github(url, &commits); err != nil {
		return "", time.Time{}, fmt.Errorf("could not ask %s about %s: %w", repository, file, err)
	}
	if len(commits) == 0 {
		return "", time.Time{}, fmt.Errorf("%s has no %s", repository, file)
	}
	return commits[0].SHA, commits[0].Commit.Committer.Date, nil
}

// path is where a file lives inside the repository: what the manifest says,
// under the directory the configuration sits in. The one place that knows
// about the sub-directory, and the empty path is that directory itself.
func (r RepositorySpec) path(file string) string {
	return strings.Trim(r.SubPath+"/"+strings.TrimPrefix(file, "/"), "/")
}

// within is r.path as a URL under a base.
func (r RepositorySpec) within(base, file string) string {
	// Not path.Join: these are URLs, and it would collapse the // of the
	// scheme.
	base = strings.TrimSuffix(base, "/")
	if inside := r.path(file); inside != "" {
		return base + "/" + inside
	}
	return base
}

// rawBase is where GitHub serves the repository's files as plain files.
func (r RepositorySpec) rawBase(s *Services) string {
	return s.rawURL(r.Path, "HEAD", "")
}

// gitLabRawBase is the same for one branch of a GitLab project.
func (r RepositorySpec) gitLabRawBase(s *Services, branch string) string {
	return s.GitLab + "/" + r.Path + "/-/raw/" + branch
}

// gitLabBranches are the default branches to try, in order: GitLab has no HEAD
// alias, so the branch has to be named, and the older projects are on master.
var gitLabBranches = []string{"main", "master"}

// zenodoAPI is the API of the Zenodo a record lives on, sandbox or not.
func (r RepositorySpec) zenodoAPI(s *Services) string {
	if r.Type == "zenodo-sandbox" {
		return s.ZenodoSandbox
	}
	return s.Zenodo
}

// osfRoot is the storage listing of an OSF node.
func (r RepositorySpec) osfRoot(s *Services) string {
	return fmt.Sprintf("%s/nodes/%s/files/osfstorage/?page[size]=%d", s.OSF, r.Path, listingPageSize)
}

// osfListing is one OSF storage listing: the files and folders of a node, or
// of a folder inside it.
type osfListing struct {
	Data  []osfItem `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// osfItem is one file or folder of a listing, with where to download it and
// where its own listing is.
type osfItem struct {
	Attributes struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
		// Size is null for a folder, which decodes to zero and is never read:
		// only a file is measured.
		Size int64 `json:"size"`
	} `json:"attributes"`
	Links struct {
		Download string `json:"download"`
	} `json:"links"`
	Relationships struct {
		Files struct {
			Links struct {
				Related struct {
					Href string `json:"href"`
				} `json:"related"`
			} `json:"links"`
		} `json:"files"`
	} `json:"relationships"`
}

// folder reports whether the item is a directory rather than a file.
func (i osfItem) folder() bool { return i.Attributes.Kind == "folder" }

// osfListing reads a listing whole. OSF answers ten entries at a time by
// default and links to the next page; a listing read only as far as the first
// page would report the files beyond it as missing.
func (s *Services) osfListing(url, node string) (osfListing, error) {
	var whole osfListing
	for url != "" {
		var page osfListing
		if err := s.getJSON(url, nil, &page); err != nil {
			return whole, fmt.Errorf("could not list the files of OSF node %s: %w", node, err)
		}
		whole.Data = append(whole.Data, page.Data...)
		url = page.Links.Next
	}
	return whole, nil
}

// zenodoRecordOf reads the record a repository specification names.
func (s *Services) zenodoRecordOf(spec RepositorySpec) (zenodoRecord, error) {
	var record zenodoRecord
	url := fmt.Sprintf("%s/records/%s", spec.zenodoAPI(s), spec.Path)
	if err := s.getJSON(url, nil, &record); err != nil {
		return record, fmt.Errorf("could not read Zenodo record %s: %w", spec.Path, err)
	}
	return record, nil
}

// String is the `type::path` form, as register.csv writes it.
func (r RepositorySpec) String() string {
	spec := r.Type + "::" + r.Path
	if r.SubPath != "" {
		spec += "|" + r.SubPath
	}
	return spec
}

// URL is where a human can see this repository. The register names a
// repository in a form nobody can click; a report that says which repository
// was read should let the reader open it.
func (r RepositorySpec) URL() string {
	switch r.Type {
	case "github", "gitlab":
		if r.SubPath == "" {
			return r.forgePage("")
		}
		return r.within(r.forgePage("tree"), "")
	case "osf":
		return "https://osf.io/" + r.Path + "/"
	case "zenodo":
		return "https://zenodo.org/records/" + r.Path
	case "zenodo-sandbox":
		return "https://sandbox.zenodo.org/records/" + r.Path
	default:
		return ""
	}
}

// FileURL is where a human can read one file of this repository, with a line
// number to be appended as "#L12", or empty for a source whose pages cannot
// point at a line: an OSF or Zenodo file is a download, not a page.
func (r RepositorySpec) FileURL(name string) string {
	if page := r.forgePage("blob"); page != "" {
		return r.within(page, name)
	}
	return ""
}

// forgePage is a page of a GitHub or GitLab repository at HEAD: "tree" for a
// directory, "blob" for a file, and "" for the repository itself. Empty for a
// source that is not a forge.
func (r RepositorySpec) forgePage(kind string) string {
	var root, pages string
	switch r.Type {
	case "github":
		root, pages = "https://github.com/"+r.Path, "/"
	case "gitlab":
		root, pages = "https://gitlab.com/"+r.Path, "/-/"
	default:
		return ""
	}
	if kind == "" {
		return root
	}
	return root + pages + kind + "/HEAD"
}

// SourceURL is where the configuration under check came from, empty for a file
// on disk.
func (c Context) SourceURL() string { return c.RepositorySpec.URL() }

// FileURL is the configuration under check as a page a line number can be
// added to, empty for a file on disk or a source with no such page.
func (c Context) FileURL() string { return c.RepositorySpec.FileURL(c.Path) }
