# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.4] - 2026-09-23

### Fixed

- A panic in the nightly sweep, or in a follow-up handler it calls, now costs
  that night's walk rather than the whole process: it is recovered, reported
  on the admin issue, and counted by `GET /nudges`, and the timer still fires
  the next night. The team load at startup, and the goroutines a command asks
  Mastodon on, are guarded the same way - the last of these tells the person
  who asked that the step failed (codecheckers/chekhov#50).
- The stack trace in a panic report is development's alone. In production the
  admin issue is on the public register, and a trace names the build's paths,
  packages and line numbers: the report there carries the panic value and says
  the trace is in the log (codecheckers/chekhov#50).

## [0.4.3] - 2026-09-23

### Fixed

- `assign` no longer reports a failure for somebody who is in the team
  already. An organisation owner is a maintainer of every team they are in,
  and GitHub shows this bot no membership for them at all, so the add was the
  first the bot heard of it and the `maintainer` it got back read as a
  surprise: the reply said it could not put them in the team, when nothing was
  wrong and nothing had changed. It now says they are in it as a maintainer,
  a role the bot did not set and will not change. Somebody already in the team
  is not written to at all, which also means a `member` write can no longer
  take a maintainer's role away (codecheckers/chekhov#47).

## [0.4.2] - 2026-09-23

### Added

- A command that panics is reported with its stack trace on the admin issue
  named by `CHEKHOV_ADMIN_ISSUE`, as well as with the apology on the issue it
  was asked on (codecheckers/chekhov#38).

## [0.4.1] - 2026-09-23

### Fixed

- A reply that GitHub answers with a server error close to the hourly
  rate-limit reset is retried after the usual pause, instead of waiting for the
  reset (codecheckers/chekhov#33).
- A Mastodon request refused with a 403 that says when to try again is retried
  once, the way GitHub's already were (codecheckers/chekhov#33).
- When GitHub or Mastodon asks for a pause of more than two minutes, the reply
  says at once that the request did not go out, rather than falling silent
  until the pause is over (codecheckers/chekhov#33).

## [0.4.0] - 2026-09-23

### Added

- `check` says which line of `codecheck.yml` each finding is about: after the
  identifier in the terminal, and in a Line column of the reply, linked to that
  line when the file came from GitHub or GitLab (codecheckers/chekhov#7).

### Changed

- A `codecheck.yml` that declares a specification version this build does not
  know - a newer one, a malformed one, or an empty `version` - is refused
  rather than checked against a guess. The reply names the version it
  declared and the versions this build knows, and says the build is behind the
  register if the file means a newer one. It used to say the file named no
  version at all (codecheckers/chekhov#7).

## [0.3.1] - 2026-09-23

### Changed

- `announce`, `follow`, `codecheckers` and `suggest codecheckers` read the
  register's lists side by side, and `announce` asks Mastodon while it reads
  the certificate, about a second faster (#34).

## [0.3.0] - 2026-09-22

### Added

- Follow-up records: a reply that leaves something outstanding says so in a
  signed block on its own comment, and a later walk of the register picks it
  up. The bot has no database, so the state is the thread - and the record is
  signed for the reason the roles record is, because a repository collaborator
  can edit a bot comment and an edited record would send the sweep after
  somebody else. `internal/followup` owns it and the handlers are keyed by
  kind, so a new kind of follow-up is a handler rather than a change to the
  walk (codecheckers/chekhov#48).
- `@chekhovbot nudge` (editors) walks every open issue of the register, reads
  the follow-up records and acts on them. The first kind is the invitation the
  owners were asked for: still not a member after three days, and the owners
  are asked again; a member now, and the bot does what the ask promised - the
  team the check implies, the role, the issue's assignee - and says the row in
  the codechecker list is still a person's job. A handler decides by looking at
  the organisation rather than by bookkeeping, so a second sweep the same day
  says nothing twice - and a role that went to somebody else while the
  invitation was outstanding stays with them. The owners are asked at most
  four times before the bot says so and goes quiet
  (codecheckers/chekhov#48).
- The same sweep runs nightly in the deployment itself, which needs no second
  copy of the token, and `GET /nudges` reports what it has found: the process
  start, the last three weeks of sweeps and when the next is due. It is
  unauthenticated, so it counts problems rather than naming the people they
  are about, except in development. The weekly **Nightly sweep** workflow reads
  it without a credential and fails when no sweep has run in 36 hours and the
  process is older than that - a timer cannot report its own death
  (codecheckers/chekhov#48).

## [0.2.1] - 2026-09-22

### Fixed

- `assign` does the team half even when the role is unchanged. Re-running it
  to move somebody into another team means the team, and the reply said
  "already the assigned codechecker" and stopped - silently ignoring what the
  editor asked for. Found by the live test on
  codecheckers/testing-dev-register#202 (codecheckers/chekhov#47).

## [0.2.0] - 2026-09-22

### Changed

- `@chekhovbot invite @user` is removed, and `assign` does the work instead.
  GitHub splits team membership by authority: a team maintainer may add
  somebody already in the organisation, and only an organisation owner may
  bring somebody in from outside - so a bot that could invite a stranger would
  have to be an owner, which reaches membership everywhere, billing and
  repository deletion. `assign @user as codechecker` now asks whether the
  person is in the organisation: if they are not, the role is **not** recorded
  and the reply asks the organisation's owners, mentioning them, to invite
  them - "I will handle the rest". If they are, the bot puts them in the team
  the check implies and says so (codecheckers/chekhov#47).
- `teams.owners` names who to ask when somebody has to be invited to the
  organisation, instead of reading the organisation's owners. That reply is the
  only place the bot writes a plain `@mention`, so a development deployment
  asks whoever is testing it rather than notifying the real owners, and the
  reply says when it did that (codecheckers/chekhov#47).
- Which team that is follows from the check rather than from the person: a
  check carrying the `institution` label sends a codechecker to the
  institutional team, which `teams.institutional` names; an editor can say
  otherwise in either direction with `assign @user as codechecker in <team>`.
  The teams the bot may add to are an allow-list in `config/`, and a settings
  file is refused at load when it contains the editors team - the bot must not
  be able to grant the permission its own editor commands are gated on - or
  when a named team is missing from the list, which would leave the team half
  of `assign` silently doing nothing (codecheckers/chekhov#47).

## [0.1.1] - 2026-09-21

A patch rather than a minor bump: nothing a codechecker can see changes, because
everything below is behind the development check.

### Added

- A development deployment, and a locally built binary previewing a reply,
  report the commit the build was made from - in `@chekhovbot version`, in the
  footer and on `/healthz` - and say when the tree had uncommitted changes, so
  a binary that is not the commit it names says so. The only source read is the
  stamp `go build` makes from the checkout since Go 1.24, which the toolchain
  reads rather than being handed: `go run`, `go test` and a builder without a
  `.git` report no commit rather than a guess. The hand-set `CHEKHOV_COMMIT`
  that started this is not coming back, and neither is the `REVISION` an
  environment a deployment's own configuration can set
  (codecheckers/chekhov#4).

## [0.1.0] - 2026-09-21

Everything below is what the development deployment runs. It is given a version
so that a reply naming one can be traced to a changelog entry; the register bot
has not been released against the production register yet, which is
codecheckers/chekhov#37.

The version is a constant, `internal/build.Version`, and it is the only thing
the bot reports about which build is running. It used to report a commit as
well, through three channels - `-ldflags`, the toolchain's VCS stamp and a
`CHEKHOV_COMMIT` configuration variable - which could each name a different
one, and on the deployment the variable named a commit four behind the running
code for a morning. The commit is gone from `/healthz`, from
`@chekhovbot version` and from the development footer, and so are
`CHEKHOV_VERSION` and `CHEKHOV_COMMIT`. Reporting the commit properly is still
wanted and still open: codecheckers/chekhov#4.

### Changed

- OpenAlex replaces Crossref as the source for what a paper is. Of ten article
  DOIs from the register, Crossref knew seven and OpenAlex nine (the arXiv DOIs
  are DataCite, which Crossref does not hold); OpenAlex carried an abstract for
  nine against four, ORCIDs for twenty authors of thirty-two against seven of
  twenty-seven, and a subject for every record, while Crossref's `subject`
  field came back empty on all seven. `CC-MET-005` to `CC-MET-008` read it too,
  so they keep their names and change where they look. Crossref is still
  implemented and one setting away, `chekhov.metadata.source`
  (codecheckers/chekhov#24).
- `suggest codecheckers` now has a subject to match on: what OpenAlex says a
  paper is about goes into the fields it ranks by, which in ten real checks had
  matched on languages alone (codecheckers/chekhov#24).
- The bundled rule files are refreshed from the register at `101d109e`: the
  four paper rules and `CC-MET-004` now point at the R package's
  `validate_codecheck_yml_metadata`, which replaced
  `validate_codecheck_yml_crossref` when codecheckers/codecheck#92 landed.
  Names, descriptions, severities and rule counts are unchanged, so nothing a
  check reports changes (codecheckers/register#220).
- A `check_time` written as `2019-02-14T10:00:00+0000` now dates a
  configuration that names no specification version, where the form was
  previously not understood and the file fell back to the newest
  specification. One list of timestamp forms serves the rules and the
  provenance record (codecheckers/chekhov#43).

### Added

- `@chekhovbot invite @user` adds somebody to the `codecheckers` team, for an
  editor who has found a codechecker outside it. It says whether GitHub sent an
  invitation or the membership was immediate, tells somebody already in the
  team from somebody in the team but not on a codechecker list, and refuses
  with the reason when the token may not manage membership - never silently,
  and never as though an invitation had been sent. The row in the codechecker
  list stays a person's job, and the reply says so. Adding to any other team,
  asking for any role but plain `member`, and the organisation-role endpoint
  next door are all refused in code and covered by tests. A settings file that
  names the editors and codecheckers teams the same is refused at load, because
  the guard that keeps inviting out of the editors team compares their names
  (codecheckers/chekhov#42).
- `@chekhovbot rules` says which rule catalogue this build judges by and
  whether it is the register's current one, naming the bundled commit, the
  register's commit and the day the bundle was taken - so "behind" can be told
  from "the same day, a different file". A register that cannot be asked is
  reported as "could not check", never as agreement, and so is one file that
  could not be read while the other could. What the register's copy *says*
  decides the verdict, compared against the md5 the refresh recorded, so a
  whitespace fix or a revert is not reported as a bundle that has fallen
  behind. The weekly workflow runs the same comparison through
  `chekhov rules --check` and fails when it has (codecheckers/chekhov#43).
- `@chekhovbot check repository` describes the repository under check before a
  codechecker is assigned: the languages, the licence it states, whether there
  is a `codecheck.yml`, and how many files and bytes the bundle holds, with a
  word when it is large enough to be awkward to check. It is a description
  rather than a verdict - nothing in it passes or fails - and a fact the source
  cannot give is reported as "could not check" rather than as an absence
  (codecheckers/chekhov#8).
- `@chekhovbot codecheckers` says where the lists of codecheckers are, linking
  each one and naming how many entries it has rather than copying it into the
  issue, where it would go out of date (codecheckers/chekhov#24).
- `@chekhovbot suggest codecheckers` proposes at most five codecheckers for a
  check, ranked by the languages and fields they declare against what the check
  is about: a repository, the paper's DOI, a certificate identifier, or the
  abstract or availability statement pasted under the command. Authors of the
  paper, anyone already on this check and anyone with an open check are left
  out and named as such, and every handle is written so that asking notifies
  nobody. A share counts for what it distinguishes - two thirds of the
  community declare R or Python - and a shortlist nothing separates says so
  rather than sending every check to the same five people
  (codecheckers/chekhov#24).

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
