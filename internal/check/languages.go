package check

import (
	"errors"
	"fmt"
	neturl "net/url"
	"sort"
	"strings"
)

// What a repository is written in.
//
// The first thing an editor wants to know about a repository under check is
// whether they can find somebody who can run it, and that is a question about
// languages. GitHub and GitLab both count the bytes per language for a
// repository and will say so without a token; OSF and Zenodo hold a deposit of
// files and know nothing about what is in them.
//
// This is the seed of codecheckers/chekhov#8, which reports the languages, the
// licence and the size of the repository under check. `suggest codecheckers`
// needs the languages before #8 lands, so they are written here, once, for
// both to use.

// ErrNoLanguages is a source that cannot answer the question at all, as
// opposed to one that could not be reached. An archive deposit has no language
// statistics, and never will.
var ErrNoLanguages = errors.New("this kind of repository does not report languages")

// Languages are the languages of a repository, most bytes first.
//
// A repository that answers nothing is not an error: an empty repository, or
// one holding only data, has no languages, and that is worth reporting as it
// is.
func (s *Services) Languages(spec RepositorySpec) ([]string, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("reading the languages of %s needs the external services", spec)
	}

	var url string
	switch spec.Type {
	case "github":
		url = s.GitHub + "/repos/" + spec.Path + "/languages"
	case "gitlab":
		url = s.GitLab + "/api/v4/projects/" + neturl.PathEscape(spec.Path) + "/languages"
	default:
		return nil, fmt.Errorf("%s: %w", spec, ErrNoLanguages)
	}

	// Both APIs answer an object of language to share, the shares being bytes
	// on GitHub and percentages on GitLab. Which of the two does not matter:
	// only the order does.
	shares := map[string]float64{}
	var err error
	if spec.Type == "github" {
		// The GitHub API wants its own Accept header, and takes the token.
		err = s.github(url, &shares)
	} else {
		err = s.getJSON(url, nil, &shares)
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the languages of %s: %w", spec, err)
	}

	languages := make([]string, 0, len(shares))
	for language := range shares {
		if language = strings.TrimSpace(language); language != "" {
			languages = append(languages, language)
		}
	}
	sort.Slice(languages, func(i, j int) bool {
		if shares[languages[i]] != shares[languages[j]] {
			return shares[languages[i]] > shares[languages[j]]
		}
		return languages[i] < languages[j]
	})
	return languages, nil
}
