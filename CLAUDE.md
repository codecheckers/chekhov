# CLAUDE.md

## Overview

Chekhov is the CODECHECK register bot, a fresh implementation in Go. It is
controlled by mentions in the checks issue: the command goes on the **first
line** of a comment, one command per comment, following the convention of the
Open Journals bots. See `README.md` for the commands and `docs/features.md` for
the full feature list with priorities.

## Committing

**Never commit. Stage changes with `git add` and propose a commit message; the
user commits.** This holds even in auto-accept mode and even when the change is
trivial or the message was agreed beforehand. The same applies to pushing and
to anything that publishes: opening issues or pull requests, posting comments,
deploying.

## The testing register

**Development and testing run against the testing register, never the
production one.**

| | Repository |
|---|---|
| Development, testing | `codecheckers/testing-dev-register` |
| Production | `codecheckers/register` |

`config/settings-development.yml` is the single place that names the target
repository. Nothing else should name a repo. See `docs/development.md`.

## Build and test

```sh
go build ./...
go test ./...
go vet ./...
gofmt -l .          # must print nothing
go run ./cmd/chekhov check testdata/valid-2.0/codecheck.yml
go run ./cmd/chekhov check --online testdata/valid-2.0/codecheck.yml  # asks the services
```

The fast suite runs in a second and needs no network: the rules are embedded
and every fixture is on disk. Keep it that way.

A check that needs the outside world asks for it through `Context.Services`,
and returns `needsServices(...)` when they are not enabled, so a run without
them reports "not checked" rather than "fine". Record the rule in
`RequiresService` with what it needs, and give it **both** kinds of test below.

### Three suites, three questions

| Suite | Question | Command |
|---|---|---|
| Stubs, `stub_test.go` | does the bot decide correctly, given an answer? | `go test ./...` |
| Cassettes, `cassette_test.go` | do we read what the services really send? | `go test ./...` |
| Integration, `integration_test.go` | do the services still answer the way we assume? | `CHEKHOV_INTEGRATION=1 go test -run Integration ./internal/check/` |

The first two are offline and run on every push; the third runs weekly and on
request, because a third-party API having a bad day must never turn a pull
request red.

**Stubs** serve every service from one `httptest` server, and their transport
refuses any request that is not addressed to it, so an offline test cannot
quietly reach the internet. Add a case to `stubCases` naming the rule, the
route it changes and the outcome it expects. The cases worth writing are the
ones real data never shows: a concept DOI, a superseded record, a name that
does not match, a 403, a 429, a 5xx, a skip that must not look like a pass.

**Cassettes** are recorded from the real services with
[go-vcr](https://github.com/dnaeon/go-vcr) and replayed from
`testdata/cassettes/`:

```sh
CHEKHOV_RECORD=1 go test -run Cassette ./internal/check/
```

Recording overwrites the cassettes and needs network; commit the result. The
`BeforeSaveHook` drops `Authorization` and throws away bodies the checks do not
parse, so a cassette stays safe to commit and small enough to read. `go test`
with no cassette present skips rather than reaching out.

`TestOfflineCoverageOfServiceRules` fails when a rule in `RequiresService` has
neither a stub case nor a cassette assertion, so a new rule with an API behind
it fails the **fast** suite, not only the weekly one.

Two fixtures carry the rest of the coverage: `testdata/exhaustive-2.0/` fills
in every node any 2.0 rule looks at, so offline the only skips are the service
rules, and `testdata/integration/` holds real published CODECHECKs, split by
the service they need.

## The validation rules

The rules are **not defined here**. They are maintained in the register, one
file per version of the configuration file specification, with the identifier
scheme in
<https://github.com/codecheckers/register/blob/master/RULES.md>.

- `internal/rules/data/*.yml` are embedded copies. Refresh them with
  `scripts/update-rules.sh` (add a path to a local register checkout to copy
  from there), which also rewrites `provenance.json` with the register commit
  the files came from. Commit the refreshed files.
- Never edit a bundled rule file by hand. The tests compare `provenance.json`
  against what is bundled, so a hand edit shows up as a failure.
- A rule's **severity comes from the rule file**, never from the code. The same
  check function serves every specification version that has the rule: a
  requirement hardened in 2.0 is reported more loudly, not checked differently.
  `strict` escalates warnings to errors and never the other way round.

### One function per rule

`internal/check/config.go` holds one function per rule, tagged with its
identifier:

```go
// rule: CC-CFG-026 certificate-id-format
func certificateIDFormat(c Context) Result { ... }
```

Register it in `Checks`. Every rule is either in `Checks` or in
`NotImplemented` with a reason, and `TestEveryRuleAccountedFor` fails when that
stops being true, so a rule added to the register cannot be ignored silently.

A check returns `pass`, `fail` or `skip`. **`skip` means "could not check"** -
an API that is down, a file that is not on disk, a server answering 403, 429 or
5xx - and is never a failure.

Paths in the manifest are **relative to the `codecheck.yml`**, as the
specification requires. Do not go looking for a file anywhere else: the copies
under `codecheck/outputs/` are what the R package archives after a check, not
where the manifest says the file is.

### The R package is the sibling implementation

The same rules are implemented in the `codecheck` R package
(`../codecheck/R/rules_checks.R`) under the same identifiers. When changing a
check, look at what the other implementation does for that identifier: the
point of the shared numbering is that `grep CC-CFG-016` finds both. Differences
are fine when deliberate, and worth a comment when they are.

## Reporting

- A rule reported **on its own** carries the rule's description, because an
  identifier alone tells a reader nothing.
- Where **several** identifiers are listed or counted, the identifier and the
  finding are enough; descriptions would bury the list.
- Outcomes have symbols, in the terminal and in the posted comment: `✔` passed,
  `✖` failed, `⚠` warning, `ℹ` note, `…` skipped, `─` not checked.
- The reply the bot posts lists **only the rules that need attention**. A table
  of every passing rule buries the few lines a codechecker has to act on.

## Reading a configuration

`check.FromFile` reads a path; `check.FromRepository` reads a repository the
way `register.csv` names one (`github::org/repo`, with `|sub/dir` when the
configuration is not at the root, plus `gitlab::`, `osf::` and `zenodo::`),
mirroring `get_codecheck_yml_*` in the R package. A repository has no bundle on
disk, so the rules about the directory around the file say they could not look,
while the manifest files are checked through the repository.

A check command can be narrowed to one part of the catalogue with
`Report.Subset`: the rule areas, plus `references`, which crosses two areas
because the catalogue separates the form of a reference from what resolving it
says. A narrowed report says so in its heading, so that "0 failed" cannot be
read as "this file is fine".

## Layout

```
cmd/chekhov/        command line entry point
internal/rules/     the rule catalogue, embedded from the register
internal/check/     one check function per rule, the runner and the formatting
internal/command/   parsing the commands people write in a comment
testdata/           codecheck.yml fixtures, valid and failing, one per directory
scripts/            maintenance scripts
```

A fixture is a **directory** with a `codecheck.yml` in it, because some rules
are about the file's name and the bundle around it.

## Changelog

`CHANGELOG.md` follows Keep a Changelog. Add to `## [Unreleased]` under
`Added`, `Changed`, `Fixed` or `Removed`, one line per user-visible change,
with the issue reference. Not the implementation, not the reasoning: those
belong in the code comments and the commit message.
