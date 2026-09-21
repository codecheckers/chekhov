// Package build says which version of the bot is running.
//
// One constant, bumped by hand in the commit that earns it. It is the only
// version this bot reports: /healthz, `@chekhovbot version` and the
// development footer all read it, so a deployment and a local build answer the
// same way.
//
// Go's own answer is better and is not used yet. Since Go 1.24 `go build`
// stamps the module version from the VCS tag, free and correct wherever .git
// is present. Whether it is present in the deployment's builder is **not
// established**: this package used to assert it was not, on the strength of a
// build log, and nothing here had ever read debug.ReadBuildInfo to check.
// Until somebody does, a version that is always the same shape and always
// matches a changelog entry is worth more than a commit that is sometimes
// right. See chekhov#4, which holds the open question.
package build

// Version is the version of the bot, as semantic versioning applies it to what
// somebody talking to the bot can see: a new command or a new thing a reply
// reports is a minor bump, a fix or a wording change a patch, a command
// removed or renamed a major one.
//
// Bumped in the commit that earns it, beside the CHANGELOG.md entry - a
// version bumped later describes a build nobody can point at.
// TestVersionMatchesTheChangelog holds the two together.
const Version = "0.1.0"

// Shorten is a commit as it is shown to a reader: long enough to be unique in
// this repository, short enough to read. The bot reports no commit of its own,
// but it does report which register commit the validation rules came from.
func Shorten(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
