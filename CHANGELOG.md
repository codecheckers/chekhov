# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
- `chekhov check --online` runs the checks that need external services from the
  command line.
- CI: `Tests` runs the fast, offline suite on every push and pull request;
  `Integration tests` runs the external-service suite weekly and on request,
  and re-records the cassettes to catch a service changing its answers.
- `chekhov` command line tool: `check` validates a file and exits non-zero on a
  failed rule, `comment` shows the reply to a comment, `rules` lists the rules
  and which of them this bot checks, `version` reports the build and the
  register commit the rules came from.

### Fixed

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
