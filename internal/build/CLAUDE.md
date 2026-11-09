# internal/build

## The version

**One constant, bumped by hand.** `internal/build.Version` is the version this
bot reports, and the only one: `/healthz`, `@chekhovbot version` and the
development footer all read it. No `-ldflags`, no `CHEKHOV_VERSION`. A
deployment reports the same string a local build does, because there is only
one place it can come from.

**The commit is a different question, and only the toolchain may answer it.**
`build.Revision` reads the stamp `go build` makes from the checkout and nothing
else. Not `CHEKHOV_COMMIT`, which outlived its build and started all this; and
not `REVISION` either, which a git buildpack sets into the image but which is
an environment variable a deployment's configuration can set under the same
name - reading it would report a hand-set value as though the build had proved
it. `go run`, `go test` and a builder without a `.git` report no commit, which
is the honest answer. Development-only in a reply, as every other fact about
the machine is; `chekhov version` at a terminal says it regardless, because
there it is the user's own binary. See #4.

**A change nobody outside development can see is a patch**, however much code
it took: the rules above are about what somebody talking to the bot sees, and a
field added behind the development check is not that.

Go's own answer is used for the commit and not for the version. Since Go 1.24
`go build` stamps the module version from the VCS tag - `v0.3.0`, or
`v0.0.0-20260921120735-116776f4+dirty` when untagged - which is free and
correct **wherever `.git` is present**. The deployment platform exports the
source without it, so that stamp is empty exactly where the question is asked,
and a version that is sometimes a semantic version and sometimes a pseudo-
version is worse than one that is always the same shape. `-ldflags` has the
same hole and needs the platform to co-operate. See `docs/deployment.md`.

**Semantic versioning, of the bot's surface.** What counts is what somebody
talking to the bot can see: the commands, and what a reply says.

| Bump | For |
|---|---|
| **major** | a command removed or renamed, or a reply whose meaning changes for somebody already relying on it |
| **minor** | a new command, a new argument, or a new thing a reply reports |
| **patch** | a fix, a wording change, a rule refresh, anything internal |

**The bump goes in the commit that earns it**, next to the `CHANGELOG.md`
entry, not saved up for a release: a version that is bumped later describes a
build nobody can point at. So the entry goes under the heading of the version
being bumped to, and **`## [Unreleased]` stays empty** - anything parked there
is behaviour the running version reports with no entry a reply can be traced
to. See Changelog below, which this changed.

Two checks, and it is worth knowing what each one does **not** catch:

- `TestVersionMatchesTheChangelog`, in the fast suite, compares the constant
  with the newest heading, asserts the headings are newest-first and dated, and
  fails on entries under `Unreleased`. It reads one tree, not a diff, so it
  cannot see that a commit added a command without bumping: a bullet written
  under the *existing* heading passes forever.
- The `version bump` job in `test.yml` is the one that can, because CI has the
  merge base: it fails when non-test Go changed and the `Version` line did not,
  and when the new version is not greater than the base's. A refactor that
  genuinely needs no bump says so with the `no-version-bump` label.

**Development says more than production.** `CHEKHOV_ENV` picks the settings
file and, through `command.Deployment.Development()`, how much a deployment
talks about itself: in development every comment carries a footer with the
build, the commit and the register, and `/healthz` adds the commit, the rules
provenance, the token expiry and whether the services are reachable. In
production the footer is gone and `/healthz` says only status, version, bot,
register and environment - it is unauthenticated, and the rest is nobody's
business. Anything new that names a build, a path or a credential belongs
behind that check.

