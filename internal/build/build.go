// Package build says which build is running.
//
// Three sources, in order of how much they can be trusted:
//
//  1. -ldflags, for a release built by hand.
//  2. CHEKHOV_VERSION and CHEKHOV_COMMIT, for the deployment. runway pins the
//     buildpack's build flags, so -ldflags never reaches the compiler there,
//     and its builder exports the source without .git, so there is nothing to
//     stamp either. The configuration is the one channel that arrives, and
//     docs/deployment.md sets it in the same breath as the deploy.
//  3. Go's own VCS stamp, which every binary built from a checkout carries.
//
// A deployment that says "dev" is one where all three failed, which is better
// than one that names a commit it is not running.
package build

import (
	"os"
	"runtime/debug"
	"strings"
)

// Stamp fills in whatever -ldflags did not inject, and returns the version and
// the commit to report. Called once, at the process entry point.
func Stamp(version, commit string) (string, string) {
	revision, dirty := vcs()

	if commit == "" {
		commit = strings.TrimSpace(os.Getenv("CHEKHOV_COMMIT"))
	}
	if commit == "" {
		commit = revision
	}

	if version == "" || version == "dev" {
		if configured := strings.TrimSpace(os.Getenv("CHEKHOV_VERSION")); configured != "" {
			version = configured
		} else if short := Shorten(commit); short != "" {
			version = short
			// A binary built from a dirty tree is not the commit it names, and
			// a bug report that says so saves an afternoon.
			if commit == revision && dirty {
				version += "+dirty"
			}
		}
	}
	return version, commit
}

// vcs reads what the toolchain stamped into this binary.
func vcs() (revision string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	return revision, dirty
}

// Shorten is a commit as it is shown to a reader: long enough to be unique in
// this repository, short enough to read. One definition, because three files
// used to carry their own.
func Shorten(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
