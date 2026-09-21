package build

import "runtime/debug"

// Which commit this binary was built from, when the toolchain stamped one.
//
// This is deliberately not the version. The version is a constant somebody
// bumps; the revision is a fact about the build, reported beside it where a
// reader is developing the thing and wants to know exactly what answered.
//
// One source, and it is the only one that cannot be wrong: since Go 1.24
// `go build` reads the commit out of the checkout itself. Nothing hands it
// over, so nothing can hand over the wrong one.
//
// That rules out the obvious alternatives. A `CHEKHOV_COMMIT` set by hand
// beside a deploy was the old answer and the reason this stopped reporting a
// commit at all: configuration is a separate lifecycle from the build - a
// config change does not rebuild - so it outlives the binary it describes,
// and on 2026-09-21 it named a commit four behind the running code for a
// morning. `REVISION`, which paketo-buildpacks/git sets into the image when
// it finds a .git, is a build product but is *also* an environment variable:
// the same name a deployment's configuration can set, with no way to tell the
// two apart. Reading it would report a hand-set value as though the build had
// proved it, which is the first failure with a better disguise. It is moot
// besides: the deployment's builder has no .git at all, so that buildpack
// never runs there - a build log settled it on 2026-09-21. The deployment
// therefore reports no commit, which is the honest answer, and the remaining
// way to give it one is at build time (`BP_GO_BUILD_LDFLAGS`, which the Go
// buildpack does read) rather than at runtime. See chekhov#4 and
// docs/deployment.md -> The version.

// Uncommitted is what to say about a build whose tree had changes in it. One
// phrase, because the terminal and the posted reply should not describe the
// same state in two wordings.
const Uncommitted = "with uncommitted changes"

// Revision is the commit this binary was built from, and whether the tree had
// uncommitted changes. Empty when the toolchain stamped nothing, which is the
// honest answer for `go run`, for `go test`, and for a builder without a .git.
func Revision() (commit string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			commit = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	return commit, dirty
}
