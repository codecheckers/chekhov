# CLAUDE.md

## Overview

Chekhov is the CODECHECK register bot, a fresh implementation in Go. It is
controlled by mentions in the checks issue: the command goes on the **first
line** of a comment, one command per comment, following the convention of the
Open Journals bots. See `README.md` for the commands and `docs/features.md` for
the full feature list with priorities.

## Committing and publishing

**Commit and push only on explicit, individual confirmation. Stage changes
with `git add` and propose a commit message; the user commits, or says to
commit.** An explicit "commit it" for the change at hand is the only go-ahead:
auto-accept mode is not one, a message agreed beforehand is not one, and a
confirmation covers the commit it was given for, not the next one. Pushing is
the same, and separate: "commit it" is not "push it", and each push waits for
its own go-ahead.

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

<!-- Source: own rule; ArchiveReminder exception added 2026-11-02 (#27), IncludeOutsideReply 2026-11-08 (#49). -->
The one exception is `command.OutsideReply`, which asks the organisation's
owners to invite somebody - and `command.IncludeOutsideReply`, the same ask for
a registered codechecker, through the same `OutsideRequest.ask` (#49): notifying them is the whole point of that reply, and
the handles are the organisation's current owners read from the organisation,
never written down. **A development deployment must not ask them**, because a
test may not notify people who did not ask to be in it - `teams.owners` in the
settings names who to ask instead, the shipped development and live-test files
name the maintainer, and the reply says when it used that list rather than the
organisation's. The second is `command.ArchiveReminder` (#27), which the
nightly sweep posts to the handling editor recorded on the check - somebody an
editor named for that check, so in a test it is whoever the test assigned.
The third is `command.ReminderDue` (#25), which notifies the person a
`remind` named on the day it is due - somebody the command's author already
mentioned, so in a test it is the handle the test wrote; every at sign in the
note it quotes is written as `&#64;`, so that nobody else is notified twice;
a reminder may only name somebody already on the check. The fourth and fifth
are the nudges, `command.QuietNudge` to the assigned codechecker of a quiet
check and `command.QueueNudge` to the editors team about one waiting for a
codechecker - which in development is `teams.owners` again, for the same
reason; and a development deployment nudges only the issues
`nudges.only_issues` names, because the testing register is full of old
test issues whose records name people. The sixth is
`command.RegistrationInvite` (#49), the first post of a registration the bot
opens on the lists for somebody who has joined - when an `assign` asked the
owners for them, or an editor typed `include open` - naming the one person
who has to answer it; in a test, a handle the test chose, so the maintainer's.
The review `include` requests on its pull request notifies through GitHub
rather than a mention, and goes to whom `lists.reviewers` names - the editor
who ran it by default, the owners or the editors (in development
`teams.owners` for either), or named accounts - and never to the registrant. The seventh
and eighth are the sweep's on the lists: `command.ReadyToInclude` tells the
editors - `EditorsToTell`, as the queue nudge - about a complete registration
nobody has included, once; and `command.PullRequestWaiting` reminds the
owners, as `OutsideReply` asks them, of the row's pull request open a week.
`command.notifying` is the only function that writes a
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

The offline tests follow the same rule, although they notify nobody, so that
a handle copied out of a test into a live run is safe: every made-up handle in
them was checked, and those somebody owned - `a-stranger`, `an-editor`,
`a-newcomer`, `ada` and more - are now `a-made-up-…` and `made-up-…`, which
were free. A new one is checked before it is written.

If a test genuinely needs a real account - checking that an assignee sticks,
say - **ask first**, every time.

**Live testing and deploying** follow the `chekhov-live-test` skill: a feature is done when the deployment has answered it, one test issue per change set, commands posted one at a time. Production is deployed only on an explicit go-ahead for that deploy, as for a push.


<!-- Source: own rule, board status standing go-ahead added 2026-11-02. -->
**The board is part of the work.** The register development board
(<https://github.com/orgs/codecheckers/projects/2>) carries Status and Priority
for everything. When starting on an issue, check whether it is on the board; if
it is not, say so and offer to add it. Move it on at the steps that matter -
picked up, implemented, done - rather than only at the end, so that someone
looking at the board sees what is actually happening.

**Moving an issue's Status on the board needs no go-ahead; do it and say so.**
*In Progress* when work on it starts, *Done* when the commit that closes it
is pushed, and back to *In Progress* if the live run fails (see "A feature is
done when the deployment has answered it"), each mentioned in the reply as it
happens. A board that lags the work is
the failure this prevents. `gh project item-edit` does the move; the ids are
in the `register-project-board` memory.

**Ask before changing anything else on an issue or the board.** A label, a
comment, a new item, a Priority or a milestone: propose it, wait for the
go-ahead. The same rule as commits, and for the same reason - it is visible to
other people the moment it happens.

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
`config/settings-production.yml` names `codecheckers/register`, written out
rather than read from a variable, and `config.validate` refuses every other
settings file that names it (#37).

## Build and test

```sh
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
`RequiresService` with what it needs, and give it **both** kinds of test (stubs and cassettes, described in `internal/check/CLAUDE.md`).


## Changelog

`CHANGELOG.md` follows Keep a Changelog, with one departure: the entry goes
under the heading of the version being bumped to, and `## [Unreleased]` stays
empty. Because the version is bumped in the commit that earns it - see The
version above - there is never a change waiting for a release to be named, and
`TestVersionMatchesTheChangelog` fails on entries parked under `Unreleased`.

One line per user-visible change, under `Added`, `Changed`, `Fixed` or
`Removed`, with the issue reference. Not the implementation, not the reasoning:
those belong in the code comments and the commit message.

## Decisions worth keeping

- **Go 1.26**, required by `golang.org/x/image` v0.47 (go-vcr v4 needs 1.24). Both workflows pin it, and so does `BP_GO_VERSION` on both apps, or the buildpack builds with its newest Go; the weekly workflow fails once Go no longer supports it.

## Where the rest lives

Loaded when working under the directory: `internal/check/CLAUDE.md` (validation rules, test suites, reading a configuration, decisions), `internal/bot/CLAUDE.md` (the listener, follow-up records), `internal/command/CLAUDE.md` (registry, roles, the record), `internal/build/CLAUDE.md` (the version, semantic versioning rules). The R package's remaining work is written up in `docs/next-task-r-validators.md`.

## Global rules

<!-- Source: own rule, added 2026-10-23; see ~/.claude/CLAUDE.md, rule G0.2. -->

This applies only if a user-level `~/.claude/CLAUDE.md` exists; otherwise
ignore this section. That file holds rules for all projects, with IDs such as
G1.1. Whenever one of them changes what you do, say so in your reply and name
the rule, e.g. "I found X. I did not do Y but suggest Z, following global rule
G1.1."
