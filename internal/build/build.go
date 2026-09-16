// Package build says which build is running.
//
// The commit is not injected by the platform: runway pins the buildpack's
// build flags, so -ldflags never reaches the compiler there. Go stamps the
// revision into the binary by itself when it is built from a checkout, which
// is the same answer without a deployment having to cooperate.
package build

import "runtime/debug"

// Stamp fills in whatever was not injected with -ldflags from Go's own build
// information, and returns the version and the commit to report.
func Stamp(version, commit string) (string, string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version, commit
	}

	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}

	if commit == "" {
		commit = revision
	}
	if version == "" || version == "dev" {
		if short := shorten(revision); short != "" {
			version = short
			// A binary built from a dirty tree is not the commit it names, and
			// a bug report that says so saves an afternoon.
			if modified == "true" {
				version += "+dirty"
			}
		}
	}
	return version, commit
}

func shorten(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
