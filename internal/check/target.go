package check

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// What a person may write as the target of a check.
//
// register.csv has to name four platforms in one column, so it writes
// `type::path`. Nobody should have to know that to ask the bot a question, so
// a target written without a platform is read as the one it can only be, and a
// certificate identifier is looked up in the register. The report names the
// repository it actually read, as a link, so a wrong guess is visible in the
// answer rather than hidden in it. See codecheckers/chekhov#19 and #13.

// gitLabGroup is the CODECHECK group on GitLab, as register.csv writes it in
// every gitlab:: row. Everything else that looks like `owner/repo` is read as
// GitHub, which is what a codechecker almost always means.
const gitLabGroup = "cdchck"

var (
	// ownerRepo is `owner/repo`, the form both git platforms use.
	ownerRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// osfNode is an OSF identifier: five characters, letters and digits.
	osfNode = regexp.MustCompile(`^[A-Za-z0-9]{5}$`)
	// zenodoNumber is a Zenodo record identifier: digits only.
	zenodoNumber = regexp.MustCompile(`^[0-9]+$`)
)

// ResolveTarget turns what a person wrote into the repository to read. It is
// the only entry point: a full `type::path` always wins, a certificate
// identifier is looked up, and anything else has its platform inferred.
//
// ParseRepositorySpec stays strict, because CC-REG-003 validates the
// register's own Repository column with it, where a missing platform is a
// finding rather than a shortcut.
func ResolveTarget(target string, services *Services) (RepositorySpec, error) {
	target = strings.TrimSpace(target)
	switch {
	case target == "":
		return RepositorySpec{}, fmt.Errorf("no target to check")
	case IsRepositorySpec(target):
		return ParseRepositorySpec(target)
	case certificateID.MatchString(target):
		return repositoryOfCertificate(target, services)
	}

	// The sub-directory is written the same way whatever the platform, so the
	// platform is decided by what comes before the pipe.
	path, subPath, _ := strings.Cut(target, "|")
	spec := RepositorySpec{Path: path, SubPath: strings.Trim(subPath, "/")}
	switch {
	case ownerRepo.MatchString(path):
		spec.Type = "github"
		if owner, project, _ := strings.Cut(path, "/"); strings.EqualFold(owner, gitLabGroup) {
			// The group is matched however it was written, but GitLab serves
			// it lowercase: keeping the caller's capitalisation would 404.
			spec.Type, spec.Path = "gitlab", gitLabGroup+"/"+project
		}
	case zenodoNumber.MatchString(path):
		spec.Type = "zenodo"
	case osfNode.MatchString(path):
		spec.Type = "osf"
	default:
		return RepositorySpec{}, fmt.Errorf(
			"I cannot tell what %q is; name a repository as one of %s with its path (github::owner/repo), "+
				"or give a certificate identifier like 2020-001",
			target, strings.Join(withSuffix(knownRepositoryTypes, "::"), ", "))
	}
	return spec, nil
}

// repositoryOfCertificate looks a certificate identifier up in the register
// the bot works on, which is the only thing that knows where a finished
// CODECHECK lives.
func repositoryOfCertificate(certificate string, services *Services) (RepositorySpec, error) {
	if !services.Enabled() {
		return RepositorySpec{}, fmt.Errorf("looking %s up needs the register", certificate)
	}
	register, err := services.registerCSV()
	if err != nil {
		return RepositorySpec{}, fmt.Errorf("could not read the register: %w", err)
	}
	entry, found := register.entryFor(certificate)
	if !found {
		return RepositorySpec{}, fmt.Errorf("%s is not in the register (%s)", certificate, services.Register)
	}
	if strings.TrimSpace(entry.Repository) == "" {
		return RepositorySpec{}, fmt.Errorf("%s has no repository in the register (%s)", certificate, services.Register)
	}
	// Read the way a person's target is read, so that a row missing its
	// platform can still be checked - being malformed is CC-REG-003's finding
	// to report, not a reason to refuse to look. Without services, because a
	// row naming a certificate would otherwise send us round again.
	return ResolveTarget(entry.Repository, nil)
}

// IsLocalPath reports whether a target is meant as a file on disk.
//
// What it looks like decides, and only then whether it is there: a mistyped
// path is answered with "no such codecheck.yml" rather than with a lecture
// about repository forms. Only the command line asks - a deployment does not
// read its own disk, so there a target is always a repository.
func IsLocalPath(target string) bool {
	if target == "" || IsRepositorySpec(target) {
		return false
	}
	switch {
	case strings.HasSuffix(target, ".yml"), strings.HasSuffix(target, ".yaml"):
		return true
	case strings.HasPrefix(target, "/"), strings.HasPrefix(target, "./"),
		strings.HasPrefix(target, "../"), strings.HasPrefix(target, "~"):
		return true
	}
	// Whatever is actually there is the caller's own, directory included: a
	// fixture is a directory with a codecheck.yml in it, and "that is a
	// directory" is a better answer than a request to GitHub.
	_, err := os.Stat(target)
	return err == nil
}

// ServicesFor answers the one policy question a caller at a terminal has:
// reading a repository means fetching it, so naming one is asking to go
// online, whatever --online said. A deployment brings its own services.
func ServicesFor(target string, online bool) *Services {
	if online || !IsLocalPath(target) {
		return Online()
	}
	return nil
}

// Load reads the target of a check command: a file on disk when paths are
// allowed and it is one, otherwise a repository.
//
// The one place that decides which of the two a target is, so that the command
// line and the bot cannot answer that question differently.
func Load(target string, localPaths bool, services *Services) (Context, error) {
	if localPaths && IsLocalPath(target) {
		context, err := FromFile(target)
		if err != nil || !services.Enabled() {
			return context, err
		}
		return context.WithServices(services), nil
	}
	return FromRepository(target, services)
}
