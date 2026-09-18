# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Every change to a check's roles is its own record in the thread: the reply
  that confirms an assignment also says who asked for it and when, so the issue
  keeps the history while the roles record keeps the current state - and a
  deleted record can be reconstructed from the thread
  (codecheckers/chekhov#18).

- The bot signs the roles record it keeps in each check's issue, and refuses to
  change one it did not write: a comment by the bot can be edited by anyone
  with write access to the repository, which is what this notices. The
  signature covers the record's bytes as they stand in the comment, the
  repository and the issue, so a record cannot be rewritten or moved between
  checks; the table under it is checked against the record, so an edited table
  is an edited record; and a record with no signature is refused as firmly as
  one with a wrong signature, because otherwise deleting a signature would be
  the easier forgery. `@chekhovbot accept roles` lets an editor adopt an edited
  record, recording who adopted it inside the signed payload
  (codecheckers/chekhov#41).
- `chekhov record-key` makes the signing key: the private half goes into the
  deployment's configuration and nowhere else, the public half into
  `docs/record-keys.pub`, so a roles record can be verified without asking the
  bot. A record names the key that signed it, so a reader knows which published
  key to check it against after a rotation (codecheckers/chekhov#41).

- `@chekhovbot assign @user as codechecker|author|handling editor`, `remove`
  the same way, and `roles`, which says who holds which role on this check and
  where each role comes from. The per-check roles are kept in a comment the bot
  posted and edits in place, because the bot has no storage but the issue
  itself (codecheckers/chekhov#18).
- A role that conflicts is refused before anything is recorded, and only a
  member of the editors team can be made the handling editor
  (codecheckers/chekhov#18).
- Assigning a codechecker also sets the issue's assignee, so the role is
  visible where people look for it (codecheckers/chekhov#18).

- Standing roles come from the organisation's GitHub teams rather than a list
  of handles in the settings file: membership is read with the token's
  *Members: read* permission and held in memory for a day, refreshed lazily on
  read and rebuilt at startup. A team that cannot be read has no members, so
  the editor commands refuse rather than open (codecheckers/chekhov#18).
- A `handling editor` role, the editor looking after one check, which only a
  member of the editors team can be given (codecheckers/chekhov#18).
- Roles that cannot be held at once are refused before they are granted: the
  assigned codechecker may not be an author of the paper, and neither may the
  handling editor. Being an editor and a codechecker at the same time is no
  conflict - most editors check papers (codecheckers/chekhov#18).
- The bot knows a person may hold several roles at once: `editor`,
  `codechecker` from the organisation's teams, and `assigned codechecker` and
  `author` once a check records them. A refusal says which role the command
  needs rather than always saying "editors" (codecheckers/chekhov#18).
- `@chekhovbot refresh teams`, for an editor who has just changed a team and
  will not wait for the day's expiry; it reports what each team now holds
  (codecheckers/chekhov#18).
- `/healthz` reports how old each cached membership list is, in development
  (codecheckers/chekhov#18).

- `@chekhovbot thanks` answers with a quote from the bot's namesake, credited to
  the work it is from and linking to the Wikiquote page the collection was
  taken from. Hidden from the `commands` listing (codecheckers/chekhov#5).
- `@chekhovbot follow <certificate>` previews following the fediverse accounts
  a certificate names - who is matched, who `@codecheck` already follows,
  who has no account on record - and `follow <certificate> confirm` follows
  whoever is missing and requests everyone matched for public Mastodon
  collections on `@codecheck`, split by role: Codecheckers, Authors, Venues,
  created on first use. A collection needs the account's consent (Mastodon
  reports a request as "pending" until accepted) and is capped at 25 members,
  so past that the oldest is evicted to make room - a curated, rotating
  sample, not an exhaustive membership record. An account Mastodon refuses to
  request (most commonly: it does not follow `@codecheck` back, which its own
  feature-approval policy requires) is not a hard failure - the run continues,
  and the account is sent a private message asking it to follow back, since a
  GitHub reply never reaches the person it is about. That message has no
  cross-run duplicate protection (only Mastodon's own hour-long idempotency
  window, matching `announce`'s toot): the bot keeps no state, so an account
  still not eligible gets asked again on every later `confirm`. Editors only;
  switched off in development by `mastodon.follow: false`, since following,
  requesting collection membership and messaging an account are public acts a
  development deployment must not perform on a real account
  (codecheckers/chekhov#31).
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

- The bot's replies state facts and stop: who changed a role and when, without
  a sentence explaining what the comment is for. A record that does not verify
  says what happened to it - "the roles record was edited after I wrote it" -
  rather than naming an actor the bot cannot identify
  (codecheckers/chekhov#18, codecheckers/chekhov#41).

- `@chekhovbot refresh teams` says which copy each read replaced, so an editor
  can tell whether the membership in hand already had their change in it
  (codecheckers/chekhov#18).
- The settings file no longer carries buffy's `only: editors` under a
  responder. Nothing read it, and two places must not be able to disagree about
  who may run a command: the command registry decides, alone
  (codecheckers/chekhov#18).

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

- Nothing the bot posts can be mistaken for the roles record: a record is the
  marker on the first line of a comment the bot wrote, and every reply is
  defused before it is posted, so a stranger cannot have the bot quote a
  forged record back into the issue (codecheckers/chekhov#18).

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
- A command that panics is answered with an apology and logged, rather than
  crashing the whole process and losing every other command in flight on a
  shared deployment (codecheckers/chekhov#32).
- `announce` refuses a certificate page taller than 1754 px rather than
  risking the deployment's 128 MB memory limit while scaling it - reproduced
  live: an oversized page got the deployment OOM-killed with no reply ever
  posted (codecheckers/chekhov#32).

### Notes

- The rule identifiers are shared with the `codecheck` R package: the same rule
  has the same identifier in both, so the two implementations can be compared
  by grepping for an identifier rather than by reading both codebases.
- All 58 rules for specification 2.0 are checked: 34 from the file and its
  bundle alone, 24 by asking an external service. `RequiresService` in
  `internal/check/services.go` says which rule needs what.

[register#209]: https://github.com/codecheckers/register/issues/209
