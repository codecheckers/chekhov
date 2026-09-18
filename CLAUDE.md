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
- Never edit a bundled rule file by hand. The tests compare `provenance.json`
  against what is bundled, so a hand edit shows up as a failure.
- A rule's **severity comes from the rule file**, never from the code. The same
  check function serves every specification version that has the rule: a
  requirement hardened in 2.0 is reported more loudly, not checked differently.
  `strict` escalates warnings to errors and never the other way round.
- The one exception is `referenceRules` in `internal/check/run.go`, the
  cross-area group behind `check references`. It is hand-written because the
  rule files have no way to express it yet, guarded by a test, and it goes away
  when codecheckers/register#216 lands. Do not add a second such list.

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
`GET /healthz` for everything else. No state, no database. The container runs
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

**Development says more than production.** `CHEKHOV_ENV` picks the settings
file and, through `command.Deployment.Development()`, how much a deployment
talks about itself: in development every comment carries a footer with the
build, the commit and the register, and `/healthz` adds the commit, the rules
provenance, the token expiry and whether the services are reachable. In
production the footer is gone and `/healthz` says only status, version, bot,
register and environment - it is unauthenticated, and the rest is nobody's
business. Anything new that names a build, a path or a credential belongs
behind that check.

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
`Report.Subset`: the rule areas, plus `references`, which crosses two areas
because the catalogue separates the form of a reference from what resolving it
says. A narrowed report says so in its heading, so that "0 failed" cannot be
read as "this file is fine".

## Where things stand

Commit `b63d81b` carries the first implementation: the rule catalogue, the
checks, the offline and integration suites, the CLI. `02ddbfc` added reading a
configuration out of a repository. The listener, the reply path and the first
three conversational commands came after that. Read `CHANGELOG.md` for what
exists; read the issues for what does not.

| Issue | State |
|---|---|
| #7 `check codecheck.yml` | Done but for line numbers in the report |
| #9 metadata, #10 bundle, #11 references | Commands exist; small criteria left (ORCID checksum digit, bundle size, the certificate's own references) |
| #8 repository, #12 links | Not started |
| #1 commands, #2 hello, #3 unknown-command hint | Implemented; they close once the deployment has answered a real comment |
| #4 version SHA and target register | `version` and `/healthz` report both; the SHA needs the `-ldflags` of a real build |
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
internal/announce/  the toot about a certificate: data, mentions, length, the GIF
internal/suggest/   finding a codechecker: the lists, the evidence, the ranking
internal/testserver/ the offline stub server and its local-only client, for tests
internal/rules/     the rule catalogue, embedded from the register
internal/check/     one check function per rule, the runner and the formatting
  config.go         the checks that need only the file and its bundle
  external.go       the checks that ask Crossref, ORCID or a repository
  zenodo.go         the certificate's archive record
  register.go       the register-wide rules and the checks issue
  services.go       the outside world: base URLs, cache, register.csv
  source.go         reading a codecheck.yml from github::, gitlab::, osf::, zenodo::
  bundle.go         the files around it, on disk or in the repository
internal/command/   the command registry, the parser, and the reply bodies
  data/quotes.yml   Chekhov quotes from Wikiquote, for `thanks`
testdata/           codecheck.yml fixtures, valid and failing, one per directory
testdata/cassettes/ recorded service responses, replayed offline
scripts/            maintenance scripts
```

A fixture is a **directory** with a `codecheck.yml` in it, because some rules
are about the file's name and the bundle around it.

## Changelog

`CHANGELOG.md` follows Keep a Changelog. Add to `## [Unreleased]` under
`Added`, `Changed`, `Fixed` or `Removed`, one line per user-visible change,
with the issue reference. Not the implementation, not the reasoning: those
belong in the code comments and the commit message.
