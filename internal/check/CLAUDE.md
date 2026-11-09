# internal/check

### Three suites, three questions

| Suite | Question | Command |
|---|---|---|
| Stubs, `stub_test.go` | does the bot decide correctly, given an answer? | `go test ./...` |
| Cassettes, `cassette_test.go` | do we read what the services really send? | `go test ./...` |
| Integration, `integration_test.go` | do the services still answer the way we assume? | `CHEKHOV_INTEGRATION=1 go test -run Integration ./internal/check/ ./internal/build/` |

One integration test writes: `sandbox_test.go` makes, audits and deletes a
draft on the Zenodo sandbox, and runs only with `CHEKHOV_ZENODO_SANDBOX_TOKEN`
set (`docs/zenodo-sandbox-token.md`). The token is for development; the bot
reads a draft by its share link and never holds one.

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
and empty now, which is why `codecheckers suggest` matched on languages alone
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

A fixture is a **directory** with a `codecheck.yml` in it, because some rules
are about the file's name and the bundle around it.
