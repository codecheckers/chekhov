# CLAUDE.md

## Overview

Chekhov is the CODECHECK register bot, a fresh implementation in Go. It is
controlled by mentions in the checks issue: the command goes on the **first
line** of a comment, one command per comment, following the convention of the
Open Journals bots. See `README.md` for the commands and `docs/features.md` for
the full feature list with priorities.

## Committing and publishing

**Never commit. Stage changes with `git add` and propose a commit message; the
user commits.** This holds even in auto-accept mode and even when the change is
trivial or the message was agreed beforehand. The same applies to pushing and
to deploying.

**A commit message is one line.** What changed, and the issue it closes:
`count a bundle in one request instead of one per directory, closes #46`. No
body, no paragraphs, no bullet list of what the reviewers said. The history is
read as a list, and a list of essays is not one - `git log --oneline` is how
somebody finds the commit that broke something, and a subject line that has to
compete with forty lines underneath it is a worse subject line. The reasoning
belongs in the code comments, next to the code it is about, which is where the
next person will actually be looking when they need it.

One line means one line: no trailers either, including `Co-Authored-By` and
`Claude-Session`. A harness that asks for them is asking about its own
bookkeeping, and this repository's history is not the place to keep it.

**When the background is crucial, offer to post it to the issue.** Some of it
is worth keeping and belongs nowhere in the source: a measurement that decided
between two shapes, a thing that turned out not to be true, a limit and why it
is that number. Say so in the reply, offer a comment on the issue the commit
closes, and wait for the go-ahead as for any other public comment. The issue is
where somebody goes to ask why this is the way it is, it is one click from the
commit that closes it, and putting it there costs nothing to everybody who only
wanted to know what changed.

**Before proposing a commit message, run `/simplify`.** The code is ready means
the code has been read once more for reuse, duplication, waste and
special-casing, and the findings applied. It is cheap, it runs on the diff that
is about to be committed, and the commit message is the last honest moment to
notice that a helper already existed.

**For a change of any size, run `/code-review` as well.** The rule of thumb is
**more than ~150 changed lines of non-test Go**, counted with
`git diff --stat` minus `_test.go` files and `testdata/`. In Go a feature drags
its tests behind it, so a raw diff of 400 lines is often 150 lines of behaviour;
counting the tests would make every change "serious" and the habit would die.

Below that line, review anyway when the change:

- adds a package or moves a boundary between them,
- touches anything that verifies, signs, or carries a credential - the webhook
  signature, the token, the reply path,
- changes what a rule decides, or how a verdict is reported,
- is the first use of a new dependency.

`/code-review ultra` (the multi-agent cloud review) is yours to trigger, not
mine; the ordinary `/code-review` is the one that belongs in this loop.

**Merging a worktree branch: cherry-pick it, do not rebase it.** Work on an
issue often happens in a `.claude/worktrees/` copy driven by another session,
and that session still has the branch checked out. Rebasing it rewrites the
branch under somebody else's working tree; `git cherry-pick <sha>` onto main
gives the same linear history, leaves their branch alone - behind main, theirs
to delete or reset - and keeps the original author and message. Wait until the
other session says the branch is finished: staged-but-uncommitted work is not
a branch to merge, because that session follows the rule above and its user
does the committing.

Two things conflict every time, and both resolve the same way:
`internal/build.Version` and the `CHANGELOG.md` heading. The branch was
numbered against the main it left, so the number it wrote is stale: give it
**the next number in sequence from current main**, of the size its own change
earns - see The version - and move its changelog section to match, leaving the
entry text as its author wrote it. Merge a patch before a minor when both are
waiting, or the patch has no number left to take. Build, vet, gofmt and the
suite run before each `cherry-pick --continue`, not only at the end, so a bad
resolution is caught on the commit that caused it.

**Issues and comments: draft first, in the session.** Put the full text of an
issue - title, labels, body - in the reply, not only in a scratchpad file, and
wait for the go-ahead before `gh issue create`. The wording is reviewed before
it is public. Write the body to a file as well, because `--body-file` is what
`gh` reads, but what the reply shows is what is being approved.

**Never write a plain `@mention` of an account that is not on the list below.**
A handle in a comment or an issue body notifies whoever owns it, and the
plausible-looking ones are taken: `@somebody`, `@mallory`, `@an-author`,
`@an-intruder`, `@nobody` and `@octocat` are all real accounts, and real people
were notified by test comments before this was written down.

| May be mentioned | |
|---|---|
| `@nuest` | the maintainer, who is running the test |
| `@chekhovbot` | the bot itself, which the command is addressed to |

The one exception is `command.OutsideReply`, which asks the organisation's
owners to invite somebody: notifying them is the whole point of that reply, and
the handles are the organisation's current owners read from the organisation,
never written down. **A development deployment must not ask them**, because a
test may not notify people who did not ask to be in it - `teams.owners` in the
settings names who to ask instead, the shipped development and live-test files
name the maintainer, and the reply says when it used that list rather than the
organisation's. `command.notifying` is the only function that writes a
handle so that it notifies, and it is named to be hard to confuse with
`command.mentions`, which writes one in backticks so that it does not.

For anybody else in a test, **check first** and use a handle that does not
exist:

```sh
gh api users/<handle> --silent || echo "free to use"
```

`@a-codechecker` and `@a-handling-editor` are free at the time of writing and
read well in a transcript. Backticks do not help in a command - the bot parses
the first line, and `` `@x` `` is not a handle - so the handle in a command has
to be one nobody owns. In prose, and in the bot's own replies, a handle in
backticks renders without notifying, which is why the reply bodies write them
that way.

If a test genuinely needs a real account - checking that an assignee sticks,
say - **ask first**, every time.

**One live test issue per change set.** Live testing happens on a new issue in
the testing register, named for what is being tried and listing what each
command should answer. A single long-running issue buries the run that matters
under a hundred earlier comments, and a reply cannot be read against what was
expected of it. Link the chekhov issues from the test issue, and the test issue
from the closing comment, so the proof is one click away.

**Post the commands one at a time, and wait for each reply.** The bot answers
in a goroutine, so a burst of comments comes back out of order: eight commands
posted in eight seconds produced replies interleaved with the next commands,
and matching each answer to its question meant reading timestamps. Leave enough
time for the reply to land - a `check` that asks OpenAlex and Zenodo takes
longer than `rules` - and read it before posting the next one. The thread is
the record, and it has to be readable by somebody who was not there.

**The board is part of the work.** The register development board
(<https://github.com/orgs/codecheckers/projects/2>) carries Status and Priority
for everything. When starting on an issue, check whether it is on the board; if
it is not, say so and offer to add it. Move it on at the steps that matter -
picked up, implemented, done - rather than only at the end, so that someone
looking at the board sees what is actually happening.

**Ask before changing anything on an issue or the board.** A status change, a
label, a comment, a new item: propose it, wait for the go-ahead. The same rule
as commits, and for the same reason - it is visible to other people the moment
it happens.

## Runway config

`runway app config ls`, with or without `-o json`, prints every variable's
value - secrets included - straight into whatever ran the command, which means
straight into that command's log or transcript. **Never run it in a way that
displays values.** To confirm which variables are set, pipe `-o json` directly
into a filter that only lists keys, e.g.
`runway app config ls -a <app> -o json | python3 -c "import json,sys; print(sorted(json.load(sys.stdin)))"`,
never into `head`, `cat`, or the terminal directly. To change a value,
`runway app config set` writes it without echoing it back, which is the only
reason to touch a deployment's config in the first place. If a value is ever
displayed by mistake, treat it as compromised and say so - the fix is rotating
the credential, not hoping the display goes unnoticed.

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
echo '@chekhovbot commands' | go run ./cmd/chekhov comment -          # the bot's answer
go run ./cmd/chekhov serve --addr :8099                               # the bot itself
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

**Stubs** serve every service from one `httptest` server
(`internal/testserver`, shared by every package's tests), and their transport
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
- **A build judges by what was bundled into it.** `@chekhovbot rules` and
  `chekhov rules --check` compare `provenance.json` against the register's
  current rule files; the weekly workflow runs the second and fails when the
  bundle is behind. The verdict is the file's md5, not its commit: a
  whitespace fix or a revert must not read as drift, and
  `TestProvenanceMatchesTheBundledFiles` holds the recorded md5 to the bytes
  that are actually embedded. A register that cannot be asked is "could not check",
  never agreement. The rules always come from `codecheckers/register`,
  whichever register a deployment works on - `ProvenanceRecord.SourceRepository`
  reads it out of the bundle rather than naming it again.
- Never edit a bundled rule file by hand. The tests compare `provenance.json`
  against what is bundled, so a hand edit shows up as a failure.
- A rule's **severity comes from the rule file**, never from the code. The same
  check function serves every specification version that has the rule: a
  requirement hardened in 2.0 is reported more loudly, not checked differently.
  `strict` escalates warnings to errors and never the other way round.
- The one exception is `referenceRules` in `internal/check/run.go`, the
  cross-area group behind `check metadata references`. It is hand-written
  because the rule files have no way to express it yet, guarded by a test, and
  it goes away when codecheckers/register#216 lands. Do not add a second such list.

### Where a paper's metadata comes from

`chekhov.metadata.source` in `config/` names it: **`openalex`**, or `crossref`,
which is kept and disabled rather than deleted. `Services.Work` is the one
door, and `CC-MET-005` to `CC-MET-008` go through it, so a deployment that
switches source switches the rules with it.

The measurement behind the default, on ten article DOIs from the register:
Crossref answered for seven and OpenAlex for nine - the `10.48550/arXiv.*` DOIs
are DataCite, which Crossref does not hold - with an abstract for nine against
four, author ORCIDs for twenty of thirty-two against seven of twenty-seven, and
a subject for every record against **none**: Crossref returns `subject` present
and empty now, which is why `suggest codecheckers` matched on languages alone
in ten real checks.

The four rules used to be named `crossref-*`; codecheckers/register#220 renamed
them to `paper-*` because the source is an implementation's choice, and the
identifiers - which is what both implementations key on - never moved. The R
package still asks Crossref for them, a deliberate difference with
codecheckers/codecheck#92 open to close it.

Everything a deployment configures lives in `Services.Access`, and `Fresh`
copies that struct whole. It used to write the fields out by hand, and the
OpenAlex base URL was left off the list, which left every command in a
deployment talking to nowhere. Add configuration to `Access`, never beside it;
the run state - the cache, the register read once - stays on `Services` and is
deliberately not carried over.

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

## The bot

`bot.Serve` is the whole deployment: `POST /dispatch` for GitHub's deliveries,
`GET /healthz` and `GET /nudges` for everything else. No state, no database. The container runs
it through the `Procfile` (`web: chekhov serve`), because a buildpack otherwise
runs the built binary with **no arguments**, and the tool answers that with its
usage - which the platform reads as a crash loop.

- **Verify, then read.** The signature is checked against
  `CHEKHOV_GH_SECRET_TOKEN` before the body is parsed, in
  `internal/bot/webhook.go`. An unsigned delivery is not data the bot has any
  business reading.
- **Answer, then work.** The delivery gets a 202 and the command runs in a
  goroutine: GitHub times out at ten seconds, and a `check` that asks Crossref
  and Zenodo takes longer.
- Three guards, in this order: the repository is the configured target, the
  author is not the bot itself, the event is an opened issue or a created
  comment. Everything else is a 204 with a log line, never an error.
- **A deployment does not read its own disk.** `Server.LocalPaths` is off, so a
  path in a comment is not a target; only a `github::owner/repo` spec is. The
  command line preview turns it on, because there the path is the user's own.
- `internal/github` is the only code that writes to GitHub, and it refuses any
  repository but the configured one before making a request.
- `chekhov comment` renders its answer through the same `bot.Preview` the
  listener uses, so what you read on the command line is what would be posted.

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

**What a reply leaves outstanding lives in the thread.** A signed follow-up
record (`internal/followup`) says what kind, who it is about and when it was
asked; a sweep walks the open issues nightly - a goroutine in the deployment,
not a second job with a second token - and hands each record to the handler
for its kind. A handler decides by **looking at the world**, never by
bookkeeping: when the thing is done it says nothing, which is what makes the
sweep safe to run twice. A record is only a record because **the bot wrote
it** - the sweep reads its own comments and nobody else's, as
`people.Checks.Read` does, because a deployment with no record key signs
nothing and anybody can comment on an issue of the register. `GET /nudges` reports the sweeps for the weekly
workflow that exists only to notice the goroutine has stopped; it is
unauthenticated, so it counts problems rather than naming people.

`docs/deployment.md` has the platform, the webhook settings and what to do when
it falls over; `docs/github-token.md` has the bot's credential and its
rotation, `docs/mastodon-token.md` the announcing account's.

## Commands live in the registry

`internal/command/registry.go` holds one entry per command: name, aliases,
summary, usage, group, role, hidden. The parser resolves against it and the
help listing is generated from it, so a command cannot be added and then
forgotten in the documentation - which is the whole point of a registry rather
than a `switch`.

- A command someone may not run is **absent** from the listing, not shown and
  refused.
- A person holds a *set* of roles, `command.Roles`, not one: the same person
  can be an editor and the codechecker assigned to a check. Standing roles
  (`editor`, `codechecker`) come from the organisation's GitHub teams, read
  through `internal/people` and held in memory for a day; the per-check ones
  (`handling editor`, `assigned codechecker`, `author`) belong to one issue and
  are read from it.
  The settings file names *teams*, never people, so membership is maintained in
  one place. The cache is
  the transient copy; `Teams.Refresh` rebuilds it from the persistent one, at
  startup and whenever an editor runs `refresh teams`.
- Per-check roles live in a **comment the bot posted**, found by its marker on
  the comment's first line - not by position, and never in somebody else's
  comment. A bot comment is safe from a passer-by, **not** from a repository
  collaborator, who can edit it with the bot left as the author - so **the bot
  signs the record** (Ed25519, `internal/people/sign.go`) and refuses to change
  one it did not write. The signature covers the repository and issue too, so a
  valid record cannot be lifted between checks. An editor adopts an edited
  record with `accept roles`, and who adopted it is written into the signed
  block. The private key exists only in the deployment's configuration; the
  public key is published in `docs/record-keys.pub`, deliberately **not** in
  the register - the people who could edit a record must not also be able to
  change the key it is checked against. See `docs/record-key.md`. Nothing the bot posts may look like a
  record, so `act` defuses every reply once, centrally.
- **A reply to a role change is the record of it**: it names who asked and
  when, so the thread is the history and the roles comment is the current
  state. One comment, not two.
- **Roles that conflict are refused before they are granted**, in
  `internal/command/roles.go`: the assigned codechecker may not be an author of
  the paper, nor may the handling editor - CODECHECK exists so that somebody
  other than the author runs the code. The standing roles never conflict, and
  only an editor may be made the handling editor (`Role.Requires`).
- **The owners invite; the bot does the teams.** GitHub splits this endpoint by
  authority: a **team maintainer** may add somebody already in the
  organisation, and only an **organisation owner** may bring somebody in from
  outside. So `@chekhovbot` is a maintainer of the teams it manages and must
  never be an owner. `assign` asks the organisation's current owners - read
  from the organisation, and **mentioned** so they are notified - and puts the
  person in the team itself once they have joined. See #47.
- **Team membership is fenced in code.** `internal/github/team.go` is the only
  thing that changes it: one endpoint
  (`PUT /orgs/{org}/teams/{team}/memberships/{user}`), always `role: member`
  written as a literal, no role argument on any function, the configured
  organisation only, and only a team in `teams.managed` - `config.validate`
  refuses a list containing the editors team, because the bot must not be able
  to grant the permission its own editor commands are gated on. It never
  touches `PUT /orgs/{org}/memberships/{user}`, which sets the organisation
  role. Nothing there removes anybody. `team_test.go` records every request
  that leaves and fails if any of that stops being true; add to it rather than
  trusting the comments. Its errors reach a posted comment, so every handle in
  one is in backticks.
- **Fail closed.** A team the token cannot read has no members: the editor
  commands refuse and the listing does not offer them. The opposite - an
  unreadable team meaning everyone is an editor - would hand the register to
  anyone who can type. This needs *Members: read* on the token, see
  `docs/github-token.md`.
- `config/` is the single place that names the target repository, and a test
  asserts the shipped file names the testing register.

## Reading a configuration

`check.FromFile` reads a path; `check.FromRepository` reads a repository the
way `register.csv` names one (`github::org/repo`, with `|sub/dir` when the
configuration is not at the root, plus `gitlab::`, `osf::` and `zenodo::`),
mirroring `get_codecheck_yml_*` in the R package. `check.Load` is the one place
that decides which of the two a target is, and `check.ResolveTarget` the one
place that reads what a person wrote: a platform left off is inferred
(`owner/repo` is GitHub, `chchck/...` GitLab, five characters OSF, digits
Zenodo) and a certificate identifier is looked up in `register.csv`.
`ParseRepositorySpec` stays strict, because CC-REG-003 checks the register's
own column with it and a missing prefix there is a finding, not a shortcut.
`IsLocalPath` tells a file from a repository by what the target looks like, so
that a mistyped path is answered as a missing file.

`Context.Bundle` is where the files that go with the configuration are: the
directory it was read from, or the repository it was fetched from, behind one
interface in `internal/check/bundle.go` (`Read`, `Exists`, `List`, `Licence`,
`Describe`).
The configuration itself is read through it, so how to reach a GitHub, GitLab,
OSF or Zenodo file is written once; which of the four is decided at
construction, not at every call; and a listing is read once per directory. The
rules about the bundle therefore reach a verdict for a repository, where they
used to skip for want of a directory on disk. A bundle that cannot answer is
still a skip, never a failure.

A bundle is asked in its own terms: `Licence` is a `LICENSE` file in a git
repository and the record's metadata on OSF and Zenodo, because CC-BUN-005 asks
whether the repository under check *states* a licence, not whether it has a
file of that name. The R package looks only for the file, deliberately: it only
ever sees a bundle on disk.

A check command can be narrowed to one part of the catalogue with
`Report.Subset`: the rule areas, where `metadata` also runs the rules about the
form of a reference, and `metadata references`, which runs only those and their
resolution, because the catalogue separates the form of a reference (config)
from what resolving it says (metadata). `check.Narrow` reads the two words in
either order; `check references` is gone and says so. `config` stays a part of
its own: most of it is about the file rather than the paper, and it runs
offline where `metadata` asks the world. A narrowed report says so in its
heading, so that "0 failed" cannot be read as "this file is fine".

## Where things stand

Commit `b63d81b` carries the first implementation: the rule catalogue, the
checks, the offline and integration suites, the CLI. `02ddbfc` added reading a
configuration out of a repository. The listener, the reply path and the first
three conversational commands came after that. Read `CHANGELOG.md` for what
exists; read the issues for what does not.

| Issue | State |
|---|---|
| #7 `check codecheck.yml` | Done: findings carry their line, an unknown specification version is refused. Closes once the deployment has answered a real check |
| #9 metadata, #10 bundle | Commands exist; small criteria left (ORCID checksum digit, bundle size) |
| #11 references | Done: `check metadata references` |
| #8 repository, #12 links | Not started |
| #1 commands, #2 hello, #3 unknown-command hint | Implemented; they close once the deployment has answered a real comment |
| #4 version SHA and target register | `version` and `/healthz` report the version constant and the register. The commit is deliberately not reported: open for the VCS-stamp route, which needs `.git` in the deployment's builder |
| #5 thanks, #6 goodbye | One registry entry each, not written |
| #13 check by certificate identifier, #19 target shortcuts | Done: `check.ResolveTarget` |
| #14 webhook, #15 reply path | Implemented and covered offline by recorded deliveries and a stubbed GitHub |
| #16 deployment | Documented and ready; the app itself is created by hand on runway.horse |
| #18 roles | `codechecker`, `assigned codechecker`, `author`; editors come from the GitHub team, per-check roles from a bot-owned comment |
| #17 one bundle source | Done: `Context.Bundle`, one interface per source |
| #23 announce, register#217 fediverse columns | Implemented; done once a development deployment has posted a `direct` toot for `1970-001` and refused a second confirm |
| #24 find a codechecker | `codecheckers` and `suggest codecheckers` done; `invite` split out into its own issue |
| register#216 | `tags:` in the rule files, which would delete `referenceRules` here |

The `codecheck` R package is the sibling implementation; its own remaining work
is written up in `docs/next-task-r-validators.md`, for a separate session.

## Decisions worth keeping

These came out of a review and are easy to undo by accident:

- **Narrow before running.** `RunPart` skips the rules outside the part, so
  `check bundle` costs no Crossref or Zenodo call. Filtering a finished report
  was the first attempt and was wrong.
- **Existence is a HEAD**, falling back to GET on any error or a 405. The
  manifest check was downloading every figure to read a status code, and the
  response cache was keeping them for the run.
- **The response cache is keyed by method, URL and Accept.** The same URL
  answers JSON or text depending on what was asked for.
- **A 5xx is never recorded into a cassette.** A transient outage would
  otherwise be replayed as though it were how the service answers. Recording
  says on stderr what it discarded; re-record when the service is well.
- **One parser for `type::path`.** `ParseRepositorySpec` is used by the fetch
  path and by CC-REG-003, so the format cannot be defined twice.
- **Go 1.24**, required by go-vcr v4. Both workflows pin it.

## Layout

```
cmd/chekhov/        command line entry point, and `serve`
Procfile            what the deployment runs: `web: chekhov serve`
config/             the settings file, and the code that reads it
internal/bot/       the listener: webhook, signature, dispatch, /healthz
internal/github/    the reply path: the one place that writes to GitHub
internal/people/    who holds a role: the teams cache, and the per-check record
internal/mastodon/  the one place that writes to Mastodon
internal/httpretry/ the one retry: two tries, the backoff, rate limits, for both writers
internal/announce/  the toot about a certificate: data, mentions, length, the GIF
internal/suggest/   finding a codechecker: the lists, the evidence, the ranking
internal/testserver/ the offline stub server and its local-only client, for tests
internal/rules/     the rule catalogue, embedded from the register
internal/check/     one check function per rule, the runner and the formatting
  config.go         the checks that need only the file and its bundle
  external.go       the checks that ask the metadata source, ORCID or a repository
  openalex.go       what a paper is: title, authors, and what it is about
  zenodo.go         the certificate's archive record
  register.go       the register-wide rules and the checks issue
  drift.go          are the bundled rules the register's current ones?
  services.go       the outside world: base URLs, cache, register.csv
  source.go         reading a codecheck.yml from github::, gitlab::, osf::, zenodo::
  bundle.go         the files around it, on disk or in the repository
  repository.go     what the repository under check is: a description, not a verdict
internal/command/   the command registry, the parser, and the reply bodies
  data/quotes.yml   Chekhov quotes from Wikiquote, for `thanks`
testdata/           codecheck.yml fixtures, valid and failing, one per directory
testdata/cassettes/ recorded service responses, replayed offline
scripts/            maintenance scripts
```

A fixture is a **directory** with a `codecheck.yml` in it, because some rules
are about the file's name and the bundle around it.

## Changelog

`CHANGELOG.md` follows Keep a Changelog, with one departure: the entry goes
under the heading of the version being bumped to, and `## [Unreleased]` stays
empty. Because the version is bumped in the commit that earns it - see The
version above - there is never a change waiting for a release to be named, and
`TestVersionMatchesTheChangelog` fails on entries parked under `Unreleased`.

One line per user-visible change, under `Added`, `Changed`, `Fixed` or
`Removed`, with the issue reference. Not the implementation, not the reasoning:
those belong in the code comments and the commit message.
