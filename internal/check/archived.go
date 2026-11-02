package check

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Whether the copy of a checked repository has been archived.
//
// Once a certificate is in the register, the editor archives the copy of the
// repository in the CODECHECK organisation, so that it stays readable but no
// longer changes under the certificate. It is the last step of the editor
// guide and the easiest to forget. The bot does not archive anything itself -
// that needs administration rights on every repository in the organisation -
// so it reads whether it has been done, and reminds. See
// codecheckers/chekhov#27.
//
// The R package asks the same question of the whole register in
// check_repository_archived(), as a warning outside the rule catalogue; this
// is the same question asked while the check is still fresh.

// ErrArchiveUnknown is a copy whose archived status cannot be read without a
// credential the bot does not have: GitLab leaves `archived` out of what it
// tells an anonymous request. See codecheckers/chekhov#65.
var ErrArchiveUnknown = errors.New("GitLab does not say whether a project is archived without a token")

// IsCodecheckCopy reports whether a repository is a copy in the CODECHECK
// organisation on GitHub or group on GitLab - the only repositories whose
// archiving is the editor's to do. A repository elsewhere, an OSF project and
// a Zenodo record are somebody else's, and not asked about.
func IsCodecheckCopy(spec RepositorySpec) bool {
	switch spec.Type {
	case "github":
		return ownedBy(spec.Path, gitHubOrganisation)
	case "gitlab":
		// cdchck/community-codechecks/...: the group is the first segment,
		// whatever subgroups follow.
		return ownedBy(spec.Path, gitLabGroup)
	}
	return false
}

// Copy is the repository a copy's archiving is about: the spec without the
// directory inside it, because a repository is archived whole - and several
// certificates can live in one, each in its own directory.
func (r RepositorySpec) Copy() RepositorySpec {
	return RepositorySpec{Type: r.Type, Path: r.Path}
}

// Archived reports whether a copy in the CODECHECK organisation is archived.
//
// One public read of the repository, needing no permission beyond what every
// other read here has. An error is "could not tell", never "not archived": a
// reminder sent because GitHub had a bad minute is a reminder about nothing.
func (s *Services) Archived(spec RepositorySpec) (bool, error) {
	if !s.Enabled() {
		return false, fmt.Errorf("reading whether %s is archived needs the external services", spec.Copy())
	}
	if !IsCodecheckCopy(spec) {
		return false, fmt.Errorf("%s is not a copy in %s/ or %s/, so archiving it is not the register's to ask",
			spec.Copy(), gitHubOrganisation, gitLabGroup)
	}
	if spec.Type == "gitlab" {
		return false, fmt.Errorf("%s: %w", spec.Copy(), ErrArchiveUnknown)
	}

	var repository struct {
		Archived *bool `json:"archived"`
	}
	if err := s.github(s.GitHub+"/repos/"+spec.Path, &repository); err != nil {
		return false, fmt.Errorf("could not read whether %s is archived: %w", spec.Copy(), err)
	}
	if repository.Archived == nil {
		// An answer without the field is not an answer of "no".
		return false, fmt.Errorf("GitHub did not say whether %s is archived", spec.Copy())
	}
	return *repository.Archived, nil
}

// RowOfIssue is the register's row for a checks issue, and whether there is
// one. A row on the default branch is how the bot sees that a certificate has
// been registered: the pull request adding it was merged, or an editor added
// it by hand, and either way the check is finished.
func (s *Services) RowOfIssue(issue int) (RegisterRow, bool, error) {
	if !s.Enabled() {
		return RegisterRow{}, false, fmt.Errorf("looking for the row of #%d needs the register", issue)
	}
	register, err := s.registerCSV()
	if err != nil {
		return RegisterRow{}, false, fmt.Errorf("could not read the register: %w", err)
	}
	for _, entry := range register.entries {
		if number, err := strconv.Atoi(strings.TrimSpace(entry.Issue)); err == nil && number == issue {
			return entry, true, nil
		}
	}
	return RegisterRow{}, false, nil
}
