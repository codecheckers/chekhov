# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `@chekhovbot follow <certificate>` previews following the fediverse accounts
  a certificate names - who is matched, who `@codecheck` already follows,
  who has no account on record - and `follow <certificate> confirm` follows
  whoever is missing and requests everyone matched for public Mastodon
  collections on `@codecheck`, split by role: Codecheckers, Authors, Venues,
  created on first use. A collection needs the account's consent (Mastodon
  reports a request as "pending" until accepted) and is capped at 25 members,
  so past that the oldest is evicted to make room - a curated, rotating
  sample, not an exhaustive membership record. Editors only; switched off in
  development by `mastodon.follow: false`, since following and requesting
  collection membership are public acts a development deployment must not
  perform on a real account (codecheckers/chekhov#31).
- `@chekhovbot announce <certificate>` previews a toot about a published
  certificate - its text, the certificate pages as an animated GIF, who is
  mentioned and who has no fediverse account on record - and
  `announce <certificate> confirm` posts it to Mastodon, once. Editors only; in
  development the toot is a direct message with every mention defused
  (codecheckers/chekhov#23).
- `chekhov comment --as <handle> --online` previews a command as someone else,
  with the services it needs.
- The rules about the bundle work for a repository, not only for a directory on
  disk: `codecheck/`, the certificate report and the licence are read over the
  same service the configuration came from, and the manifest is checked for
  GitLab, OSF and Zenodo targets as well as GitHub
  (codecheckers/chekhov#17).
- `check` accepts a target without its platform: `owner/repo` is read as
  GitHub, `chchck/...` as GitLab, five characters as an OSF node and digits as
  a Zenodo record, and an ambiguous target is refused with the forms spelled
  out (codecheckers/chekhov#19).
- `check` accepts a certificate identifier, `2020-001`, and reads the
  repository it names out of the register (codecheckers/chekhov#13). A target
  that could not be read says which repository it resolved to.
- The bot listens: `chekhov serve` receives GitHub's `issues` and
  `issue_comment` deliveries on `/dispatch`, verifies the signature before it
  reads the body, answers immediately and runs the command afterwards
  (codecheckers/chekhov#14).
- The bot answers: one reply path posts the comment as the bot account, with
  truncation at GitHub's limit, one retry, and a footer naming the build and
  the register (codecheckers/chekhov#15).
- `@chekhovbot commands`, and `help` for the same thing: the commands the asker
  may run, grouped, generated from the command registry
  (codecheckers/chekhov#1).
- `@chekhovbot version`: which build is answering, with a link to the commit,
  which register it works on, and which register commit the validation rules
  came from (codecheckers/chekhov#4).
- `@chekhovbot hello`: the liveness check, naming the build and the register it
  works on (codecheckers/chekhov#2).
- A comment the bot cannot read is answered rather than ignored, suggesting the
  command it was probably meant to be (codecheckers/chekhov#3).
- `GET /healthz` reports the version, commit, bot account, register,
  environment, the register commit the rules came from and when the token
  expires, so a development deployment cannot be mistaken for the real one.
- Deployment on runway.horse against the testing register: the Go buildpack
  builds `cmd/chekhov` and the `Procfile` starts `chekhov serve`, and `docs/deployment.md` says
  how to create the app, configure the webhook, read the logs and recover
  (codecheckers/chekhov#16).
- `docs/github-token.md` documents the bot's fine-grained GitHub token: what it
  may do, why it is not a classic token, and how to rotate it.
- `@chekhovbot check` validates a `codecheck.yml` against the CODECHECK
  validation rules and replies with the rules that need attention. First
  command of the bot ([register#209]).
- The validation rules are embedded from the register, one set per version of
  the configuration file specification, with `internal/rules/data/provenance.json`
  recording which register commit they came from.
- `scripts/update-rules.sh` refreshes the embedded rules, from GitHub or from a
  local register checkout.
- `testdata/exhaustive-2.0/` is a maximal configuration that fills in every node
  any 2.0 rule looks at, so every implemented check reaches a verdict and none
  of them skips.
- Every rule of both specification versions now has a check. The 24 that need
  Crossref, ORCID, Zenodo, the GitHub API or `register.csv` skip with what they
  would have needed unless `CHEKHOV_INTEGRATION=1` enables external services.
- `testdata/integration/` holds fixtures split by the service they need, three
  real published CODECHECKs between them, and the integration suite asserts
  that together they make every service-dependent rule reach a verdict.
- Offline coverage for the checks that need an external service: recorded
  cassettes (go-vcr) replay what Crossref, ORCID, Zenodo, GitHub and the
  register answered, and stubbed servers cover the failure paths, so the fast
  suite exercises every rule.
- The report says where the configuration was read from, as a link the reader
  can follow, and how the specification version was chosen: declared, dated or
  assumed.
- `chekhov check --online` runs the checks that need external services from the
  command line.
- A `codecheck.yml` can be read from a repository the way `register.csv` names
  one: `github::org/repo`, `github::org/repo|sub/dir`, `gitlab::group/project`,
  `osf::<id>` and `zenodo::<id>` (codecheckers/chekhov#7).
- `check` can be narrowed to one part of the catalogue: `config`, `metadata`,
  `bundle`, `references`, `report` or `register`, which is what the bot's
  `@chekhovbot check bundle` asks for (codecheckers/chekhov#9, #10, #11).
- CI: `Tests` runs the fast, offline suite on every push and pull request;
  `Integration tests` runs the external-service suite weekly and on request,
  and re-records the cassettes to catch a service changing its answers.
- `chekhov` command line tool: `check` validates a file and exits non-zero on a
  failed rule, `comment` shows the reply to a comment, `rules` lists the rules
  and which of them this bot checks, `version` reports the build, the register
  it targets and the register commit the rules came from, and `serve` runs the
  bot.

### Changed

- A repository that answers 403, 429 or 5xx for a manifest file makes the check
  skip rather than report the file as missing; only "not there" is absence
  (codecheckers/chekhov#17).
- `CC-BUN-001` checks the manifest against the bundle the configuration was
  read from, which for a file on disk means the files beside it rather than a
  repository it names - the same thing the `codecheck` R package checks, and no
  longer a rule that needs the network (codecheckers/chekhov#17).

- The build stamp is found rather than injected: `-ldflags` if a release was
  built by hand, then `CHEKHOV_COMMIT` from the deployment's configuration,
  then Go's own VCS stamp, which a binary built from a checkout always carries
  and which marks a dirty tree as such.
- A deployment says less in production: the comment footer naming the build and
  the register is a development thing, and `/healthz`, which is
  unauthenticated, reports the commit, the rules provenance and the token
  expiry only in development.
- `config/settings-development.yml` is read by the bot rather than only
  documenting its intent: it is the single place naming the target repository,
  the bot account and the editors. The ERB interpolation buffy uses became
  `${VAR:-default}`, which Go can read.

### Fixed

- A deployment answers every command from what is published now: responses
  were cached for the life of the process, so `check` could report on a
  `register.csv` or a repository as it was hours earlier.
- The GitLab shortcut reads `cdchck/`, the group `register.csv` actually names;
  `chchck/` was a typo that could never match a CODECHECK project
  (codecheckers/chekhov#19).
- `CC-BUN-005` asks a bundle how it states its licence rather than looking only
  for a file: an OSF node and a Zenodo record state one in their metadata, and
  were reported as stating none (codecheckers/chekhov#17).

- A `403` from GitHub is retried only when it is a rate limit. A token without
  the right permission answers 403 too, and asking again never helped.
- `CC-BUN-001` manifest-files-exist now looks only where the specification says
  a manifest file is, relative to the `codecheck.yml`. It used to accept a copy
  under `codecheck/outputs/`, which is where the R package archives outputs
  after a check, and so tolerated manifest paths the specification calls wrong.
- A reference or report answering 401, 403, 405, 429 or a 5xx is reported as
  blocked rather than broken: a publisher refusing robots says nothing about
  whether the reference is right.

### Notes

- The rule identifiers are shared with the `codecheck` R package: the same rule
  has the same identifier in both, so the two implementations can be compared
  by grepping for an identifier rather than by reading both codebases.
- All 58 rules for specification 2.0 are checked: 34 from the file and its
  bundle alone, 24 by asking an external service. `RequiresService` in
  `internal/check/services.go` says which rule needs what.

[register#209]: https://github.com/codecheckers/register/issues/209
