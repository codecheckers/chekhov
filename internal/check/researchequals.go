package check

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
)

// A certificate deposited on ResearchEquals rather than Zenodo, as read by
// `check links`. The R package reads the same records for its curation policy
// audit, see get_researchequals_version_id in ../codecheck/R/researchequals.R.

// researchEqualsVersion is one published version of a ResearchEquals module,
// as much of it as says anything in prose.
type researchEqualsVersion struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// Changelog is null on a first version.
	Changelog string `json:"changelog"`
	// Refs are the module's references. Empty on every record seen so far,
	// so read as they come rather than in a shape nobody has seen.
	Refs json.RawMessage `json:"refs"`
}

// versionID is the identifier of a version at the end of the page a
// ResearchEquals DOI points at, https://researchequals.com/versions/<uuid>.
var versionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// researchEqualsVersion reads the version a DOI names.
//
// The API is keyed by the version's identifier, which the DOI does not carry.
// The R package follows the DOI's redirect to find it; this asks the DOI
// system where the DOI points instead, which is the request `check links`
// makes anyway to learn whether the DOI resolves - so it costs nothing, and
// the page the redirect lands on is never downloaded.
func (s *Services) researchEqualsVersion(ctx context.Context, doi string) (researchEqualsVersion, error) {
	answer, status, err := s.doiHandle(ctx, doi)
	if err != nil {
		return researchEqualsVersion{}, fmt.Errorf("could not ask the DOI system: %w", err)
	}
	if answer.ResponseCode != handleFound {
		return researchEqualsVersion{}, fmt.Errorf("the DOI system does not know it (%d)", status)
	}
	id := path.Base(answer.target())
	if !versionID.MatchString(id) {
		return researchEqualsVersion{}, fmt.Errorf("it points at %s, which is not a ResearchEquals version",
			answer.target())
	}

	var version researchEqualsVersion
	err = s.getJSONContext(ctx, s.ResearchEquals+"/versions/"+id, nil, &version)
	return version, err
}
