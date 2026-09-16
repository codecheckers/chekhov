package check

import (
	"fmt"
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
// context to validate it in.
//
// There is no bundle on disk, so the rules about the directory around the file
// say they could not look. What can be checked remotely - the manifest files,
// through the repository - is checked through Services.
func FromRepository(spec string, services *Services) (Context, error) {
	if !services.Enabled() {
		return Context{}, fmt.Errorf("reading %s needs the external services", spec)
	}
	parsed, err := ParseRepositorySpec(spec)
	if err != nil {
		return Context{}, err
	}

	raw, err := fetchConfiguration(parsed, services)
	if err != nil {
		return Context{}, err
	}

	context := FromBytes(raw)
	// The file was asked for by name, so the name and location rule has its
	// answer; the bundle rules have not, and skip.
	context.Path = "codecheck.yml"
	context.Label = spec
	context.Services = services
	context.RepositorySpec = parsed

	// Only a file that says nothing about its version needs dating, and dating
	// it costs a request - so ask only then.
	if SpecVersionNamed(context.Config.Version) == "" && context.Config.CheckTime == "" {
		context.Modified = lastModified(parsed, services)
	}
	return context, nil
}

// lastModified is when the configuration was last changed at its source, zero
// when that cannot be established. See SpecVersion.
func lastModified(spec RepositorySpec, services *Services) time.Time {
	switch spec.Type {
	case "github":
		var commits []struct {
			Commit struct {
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		}
		path := "codecheck.yml"
		if spec.SubPath != "" {
			path = spec.SubPath + "/codecheck.yml"
		}
		url := fmt.Sprintf("%s/repos/%s/commits?path=%s&per_page=1", services.GitHub, spec.Path, path)
		if err := services.github(url, &commits); err != nil || len(commits) == 0 {
			return time.Time{}
		}
		return commits[0].Commit.Committer.Date
	case "zenodo", "zenodo-sandbox":
		api := services.Zenodo
		if spec.Type == "zenodo-sandbox" {
			api = services.ZenodoSandbox
		}
		var record struct {
			Created   time.Time `json:"created"`
			Published string    `json:"publication_date"`
		}
		if err := services.getJSON(fmt.Sprintf("%s/records/%s", api, spec.Path), nil, &record); err != nil {
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

func fetchConfiguration(spec RepositorySpec, services *Services) ([]byte, error) {
	switch spec.Type {
	case "github":
		base := services.RawContent + "/" + spec.Path + "/HEAD"
		return services.fetchFile(spec.within(base, "codecheck.yml"))
	case "gitlab":
		// GitLab has no HEAD alias, so the default branch has to be named.
		// main first, then master, which is what the older projects use.
		var lastErr error
		for _, branch := range []string{"main", "master"} {
			base := services.GitLab + "/" + spec.Path + "/-/raw/" + branch
			raw, err := services.fetchFile(spec.within(base, "codecheck.yml") + "?inline=false")
			if err == nil {
				return raw, nil
			}
			lastErr = err
		}
		return nil, lastErr
	case "osf":
		return fetchOSFConfiguration(spec, services)
	case "zenodo", "zenodo-sandbox":
		return fetchZenodoConfiguration(spec, services)
	default:
		return nil, fmt.Errorf("unsupported repository type %q", spec.Type)
	}
}

// within puts a path where the specification says it is: relative to the
// codecheck.yml, which for a repository whose configuration sits in a
// sub-directory means relative to that directory.
//
// The one place that knows about the sub-directory, used both to fetch the
// configuration and to look for the manifest files beside it.
func (r RepositorySpec) within(base, file string) string {
	// Not path.Join: these are URLs, and it would collapse the // of the
	// scheme.
	parts := []string{strings.TrimSuffix(base, "/")}
	if r.SubPath != "" {
		parts = append(parts, strings.Trim(r.SubPath, "/"))
	}
	return strings.Join(append(parts, strings.TrimPrefix(file, "/")), "/")
}

type osfFiles struct {
	Data []struct {
		Attributes struct {
			Name string `json:"name"`
		} `json:"attributes"`
		Links struct {
			Download string `json:"download"`
		} `json:"links"`
	} `json:"data"`
}

func fetchOSFConfiguration(spec RepositorySpec, services *Services) ([]byte, error) {
	var listing osfFiles
	url := fmt.Sprintf("%s/nodes/%s/files/osfstorage/", services.OSF, spec.Path)
	if err := services.getJSON(url, nil, &listing); err != nil {
		return nil, fmt.Errorf("could not list the files of OSF node %s: %w", spec.Path, err)
	}
	for _, file := range listing.Data {
		if file.Attributes.Name == "codecheck.yml" {
			return services.fetchFile(file.Links.Download)
		}
	}
	return nil, fmt.Errorf("no codecheck.yml in OSF node %s", spec.Path)
}

func fetchZenodoConfiguration(spec RepositorySpec, services *Services) ([]byte, error) {
	api := services.Zenodo
	if spec.Type == "zenodo-sandbox" {
		api = services.ZenodoSandbox
	}

	var record zenodoRecord
	url := fmt.Sprintf("%s/records/%s", api, spec.Path)
	if err := services.getJSON(url, nil, &record); err != nil {
		return nil, fmt.Errorf("could not read Zenodo record %s: %w", spec.Path, err)
	}
	for _, file := range record.Files {
		if file.name() == "codecheck.yml" {
			if link := file.downloadLink(); link != "" {
				return services.fetchFile(link)
			}
		}
	}
	return nil, fmt.Errorf("no codecheck.yml in Zenodo record %s", spec.Path)
}

// URL is where a human can see this repository. The register names a
// repository in a form nobody can click; a report that says which repository
// was read should let the reader open it.
func (r RepositorySpec) URL() string {
	switch r.Type {
	case "github":
		url := "https://github.com/" + r.Path
		if r.SubPath != "" {
			url += "/tree/HEAD/" + r.SubPath
		}
		return url
	case "gitlab":
		url := "https://gitlab.com/" + r.Path
		if r.SubPath != "" {
			url += "/-/tree/HEAD/" + r.SubPath
		}
		return url
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

// SourceURL is where the configuration under check came from, empty for a file
// on disk.
func (c Context) SourceURL() string { return c.RepositorySpec.URL() }
