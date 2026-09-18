# Chekhov

The CODECHECK register management bot.

> If the research paper has code, it must run.

`@chekhovbot` assists the repeated tasks around a CODECHECK: welcoming
codecheckers, assigning them, validating `codecheck.yml` early, and opening the
pull request against `register.csv`. It is controlled by mentions in the checks
issue in [`codecheckers/register`](https://github.com/codecheckers/register),
following the convention established by the Open Journals bots: **the command
goes on the first line of the comment, one command per comment.**

```bash
@chekhovbot commands
@chekhovbot assign @user as codechecker
@chekhovbot suggest codecheckers
@chekhovbot check codecheck.yml
@chekhovbot register
```

Tracking issue: <https://github.com/codecheckers/register/issues/209>

## The name

Anton Chekhov (1860-1904): a pun on *check*, and on Chekhov's gun — if the gun
is on the wall in Act I it must fire in Act III; if the paper has code, it must
run. (Star Trek's Chekov has one `h`.)

## Status

The bot answers `commands`, `hello` and an unknown command on the **testing
register**, and validates a `codecheck.yml` when a comment names the repository
to read it from. Editors can announce a published certificate on Mastodon with
`@chekhovbot announce <certificate>`, which previews the toot, and
`@chekhovbot announce <certificate> confirm`, which posts it; in development
the toot is a direct message with every mention defused. `@chekhovbot
codecheckers` says where the codechecker lists are, and an editor can ask
`@chekhovbot suggest codecheckers` for candidates, named without notifying
them. It runs against
[`codecheckers/testing-dev-register`](https://github.com/codecheckers/testing-dev-register)
only; the production register is not configured anywhere. See
[`docs/deployment.md`](docs/deployment.md).

```bash
go run ./cmd/chekhov serve                          # the bot: /dispatch and /healthz
echo '@chekhovbot commands' | go run ./cmd/chekhov comment -   # the same answer, offline
echo '@chekhovbot announce 1970-001' | go run ./cmd/chekhov comment --as nuest --online -  # an editor's preview
```

```bash
go build ./...
go run ./cmd/chekhov check path/to/codecheck.yml   # the report, non-zero on failure
go run ./cmd/chekhov check --online path/to/codecheck.yml     # also asks OpenAlex, ORCID, Zenodo
go run ./cmd/chekhov check github::codecheckers/Piccolo-2020  # read it from the repository
go run ./cmd/chekhov check codecheckers/Piccolo-2020          # the same, platform inferred
go run ./cmd/chekhov check 2020-001                 # the certificate, looked up in the register
go run ./cmd/chekhov check bundle github::codecheckers/demo   # one part of the catalogue
go run ./cmd/chekhov check --markdown path/to/codecheck.yml   # the reply the bot posts
go run ./cmd/chekhov rules                          # the rules, and which are checked
```

## The validation rules

The rules are maintained in the register, not here, one file per version of the
configuration file specification:
<https://github.com/codecheckers/register/blob/master/RULES.md>. Chekhov embeds
a copy, refreshed with `scripts/update-rules.sh`.

The same identifiers are used by the
[`codecheck` R package](https://github.com/codecheckers/codecheck), so the same
rule is `CC-CFG-016` in both implementations, and the two can be compared by
grepping for an identifier. Which rules apply to a file follows from the
specification version it declares, and a rule's severity comes from the rule
file rather than from either codebase.

## Development

Everything is exercised against the **testing register**, never the real one:
<https://github.com/codecheckers/testing-dev-register>. See
[`docs/development.md`](docs/development.md) for the full setup.

```bash
go test ./...                                                    # fast, offline
CHEKHOV_INTEGRATION=1 go test -run Integration ./internal/check/ # external services
```

The fast suite needs no network and covers every rule, including the ones that
ask an external service: recorded cassettes replay what OpenAlex, ORCID, Zenodo,
GitHub and the register really answered, and stubbed servers cover the answers a
published CODECHECK never gives. CI runs it on every push and pull request.

The integration suite asks the live services the same questions, to catch the
day one of them changes its answer. It reads only, and CI runs it weekly.

## Licence

Code MIT (see `LICENSE`). Graphics in `logo/` are CC BY 4.0, matching the rest
of the CODECHECK branding. The Chekhov quotes in
`internal/command/data/quotes.yml` come from
[Wikiquote](https://en.wikiquote.org/wiki/Anton_Chekhov) and are CC BY-SA 4.0;
every reply that uses one links back to the page.
