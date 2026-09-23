// Package build says which version of the bot is running.
//
// One constant, bumped by hand in the commit that earns it. It is the only
// version this bot reports: /healthz, `@chekhovbot version` and the
// development footer all read it, so a deployment and a local build answer the
// same way. revision.go is the commit the build was made from, which is a
// different question and is only answered when a build can prove it.
//
// Go's own answer is used for the commit and not for the version. Since Go
// 1.24 `go build` stamps the module version from the VCS tag, free and correct
// wherever .git is present - and the deployment's builder has none, which a
// build log settled on 2026-09-21: the git buildpack, which detects on finding
// a .git, does not run there. So the stamp is empty exactly where the question
// is asked, and a version that is sometimes a semantic version and sometimes a
// pseudo-version is worse than one that is always the same shape. revision.go
// reads the commit from that same stamp and reports nothing when there is
// none. See chekhov#4 and docs/deployment.md.
package build

// Version is the version of the bot, as semantic versioning applies it to what
// somebody talking to the bot can see: a new command or a new thing a reply
// reports is a minor bump, a fix or a wording change a patch, a command
// removed or renamed a major one.
//
// Bumped in the commit that earns it, beside the CHANGELOG.md entry - a
// version bumped later describes a build nobody can point at.
// TestVersionMatchesTheChangelog holds the two together.
const Version = "0.4.4"

// Shorten is a commit as it is shown to a reader: long enough to be unique in
// this repository, short enough to read. Used for the commit this build was
// made from, and for the register commit the validation rules came from.
func Shorten(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
