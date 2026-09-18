# Chekhov feature list

Sources: [register#209](https://github.com/codecheckers/register/issues/209);
all 27 buffy responders (`openjournals/buffy/app/responders`); the fork
enhancements in `neurolibre/roboneuro-neo` (309 commits ahead, ~20 custom
responders) and `scipy-conference/buffy`.

**Engine decision: own implementation in Go.** Learning goal plus freedom to
deploy anywhere later; the measured footprint of a Go binary (~10-20 MB) fits
every candidate host, including runway.horse free (128 MB, no sleeps).

Priorities: `MVP` = needed for the bot to be useful at all; then `high`,
`medium`, `low`.

## A. Bot basics and command plumbing

Issues opened in `codecheckers/chekhov`; `version` and `thanks` promoted to MVP
for completeness of the conversational set.

| Command | Effect | Prio | Issue |
|---|---|---|---|
| `@chekhovbot commands` / `help` | List available commands, filtered by the caller's role | MVP | #1 |
| `@chekhovbot hello` | Liveness check; bot replies it is awake | MVP | #2 |
| `@chekhovbot <unknown>` | Reply that the command is unknown, suggest `commands` | MVP | #3 |
| `@chekhovbot version` | Report bot version, commit and **which register it targets** | MVP | #4 |
| `@chekhovbot thanks` | Friendly acknowledgement: a quote from the namesake | MVP | #5 |
| `@chekhovbot goodbye` | Closing pleasantry when a check ends | low | #6 |

## B. People, roles and assignment

| Command | Effect | Prio |
|---|---|---|
| `@chekhovbot assign @user as codechecker` | Set the codechecker, assign the issue, record it | MVP |
| `@chekhovbot remove @user as codechecker` | Unassign and clear the recorded codechecker | MVP |
| `@chekhovbot assign @user as editor` | Set the editor handling this check | high |
| `@chekhovbot codecheckers` | Link the lists of registered codecheckers, rather than copy them | high |
| `@chekhovbot suggest codecheckers` | Propose candidates by declared expertise vs. the repository, the DOI, or a pasted abstract | high |
| `@chekhovbot invite @user` | Send an org/team invitation to a new codechecker | medium |
| `@chekhovbot list team <name>` | List members of a codecheckers team | medium |
| `@chekhovbot add/remove assignee @user` | Manage GitHub assignees without role semantics | medium |
| `@chekhovbot availability @user` | Show whether a codechecker is currently accepting work | low |

## C. Register and certificate

| Command | Effect | Prio |
|---|---|---|
| `@chekhovbot register` | Open a PR adding the row to `register.csv` | MVP |
| `@chekhovbot set certificate <id>` | Reserve/record the certificate identifier for this check | MVP |
| `@chekhovbot set venue <name>` / `set type <type>` | Record register metadata on the issue | high |
| `@chekhovbot set repository <url>` | Record the code repository under check | high |
| `@chekhovbot check register` | Validate the row against `register.csv` rules before the PR | high |
| `@chekhovbot next certificate` | Suggest the next free certificate id for the year | medium |
| `@chekhovbot show entry <id>` | Print an existing register entry | low |

## D. Validation and early checks

| Command | Effect | Prio | Issue |
|---|---|---|---|
| `@chekhovbot check codecheck.yml` | Validate the config against the spec, report problems in-thread | MVP | #7 |
| `@chekhovbot check repository` | Report languages, licence, size of the repo under check | high | #8 |
| `@chekhovbot check metadata` | Verify ORCIDs, DOIs, author and venue metadata resolve | high | #9 |
| `@chekhovbot check bundle` | Verify the CODECHECK bundle contents (report, manifest, outputs) | high | #10 |
| `@chekhovbot check references` | Resolve the paper's DOI/references via the metadata source | medium | #11 |
| `@chekhovbot check links` | Report dead links in the issue and the bundle | low | #12 |

**Open design question across this group:** the R package `codecheck` already
implements most of these rules (`R/validation.R`:
`validate_codecheck_yml_crossref`, `validate_codecheck_yml_orcid`,
`validate_contents_references`, `is_doi_placeholder`,
`is_placeholder_certificate`, `validate_certificate_for_rendering`;
`R/configuration.R`: `get_codecheck_yml_{github,gitlab,osf,zenodo}`).
Reimplementing them in Go risks two validators drifting apart. Decide once,
for the whole group, before writing #7.

## E. Zenodo and archiving

| Command | Effect | Prio |
|---|---|---|
| `@chekhovbot check zenodo <id>` | Validate the Zenodo record's metadata and files | high |
| `@chekhovbot create deposit` | Create a Zenodo draft deposit for the certificate | medium |
| `@chekhovbot upload bundle` | Upload the bundle to the Zenodo draft | medium |
| `@chekhovbot reserve doi` | Reserve the DOI so it can be printed in the report | medium |
| `@chekhovbot publish deposit` | Publish the Zenodo deposit (irreversible, editors only) | medium |
| `@chekhovbot zenodo status` | Report deposit state, files and DOI | medium |

## F. Workflow state

| Command | Effect | Prio |
|---|---|---|
| `@chekhovbot start check` | Move the issue into the checking state, apply labels, post the checklist | high |
| `@chekhovbot checklist` | Post/refresh the codechecker's checklist comment | high |
| `@chekhovbot status` | Summarise this check: role assignments, metadata, open items | high |
| `@chekhovbot finish check` | Mark the check complete, apply labels, prompt for the register PR | high |
| `@chekhovbot label <command>` | Apply/remove configured label sets (buffy `label_command`) | medium |
| `@chekhovbot close` | Close the issue with a templated message | low |

## G. Reminders and nudges

| Command | Effect | Prio |
|---|---|---|
| `@chekhovbot remind @user in two weeks` | Schedule a reminder comment (buffy `reminders`) | medium |
| `@chekhovbot remind me when the deposit is published` | Event-based rather than time-based reminder | low |
| (automatic) stale check nudge | Nudge issues untouched for N days | medium |
| (automatic) ping editors | Alert editors when a check is unassigned for N days | low |

## H. Onboarding and community

| Command | Effect | Prio |
|---|---|---|
| (automatic) welcome | Greet a new codechecker on their first issue, link the guide | MVP |
| `@chekhovbot onboard @user` | Run the onboarding checklist for a new codechecker | medium |
| `@chekhovbot buddy exchange` | Open/track a buddy-exchange request (see testing register template) | low |
| `@chekhovbot docs <topic>` | Link the relevant part of the CODECHECK guide | low |

## I. Integrations and automation

| Command | Effect | Prio |
|---|---|---|
| `@chekhovbot rerender register` | Trigger the register site rebuild after a merge | medium |
| `@chekhovbot run <workflow>` | Dispatch a GitHub Actions workflow (buffy `github_action`) | medium |
| `@chekhovbot binder` | Trigger a Binder build of the repo under check (NeuroLibre `binder_build`) | low |
| `@chekhovbot call <service>` | Generic external service call (buffy `external_service`) | low |
| `@chekhovbot stats` | Report register statistics | low |
| `@chekhovbot announce <certificate> [confirm]` | Toot about a published certificate, mentioning its authors, codecheckers and venue; preview first (editors, #23) | medium |
| `@chekhovbot follow <certificate> [confirm]` | Follow the fediverse accounts a certificate names, and curate public, consent-gated Codecheckers/Authors/Venues Mastodon collections on `@codecheck` (capped at 25, oldest evicted); preview first (editors, #31) | medium |

## Proposed MVP

`commands`, `hello`, unknown-command reply, automatic `welcome`,
`assign/remove codechecker`, `check codecheck.yml`, `set certificate`,
`register`. Everything else follows once those work end to end against
`codecheckers/testing-dev-register`.
