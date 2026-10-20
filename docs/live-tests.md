# Live tests still to run

The offline suite proves the bot decides correctly. It cannot prove that
GitHub accepts the write, that the reply renders the way it reads in a test, or
that a label the settings name is a label the register has. That is what a live
test is for, and until one has been run a feature is implemented rather than
done.

**The bot account was blocked on GitHub, and reads as unblocked** as of
**2026-10-15**: its profile and its earlier replies are visible to an anonymous
reader again. Only reads were checked; that a write is accepted is established
by the first live test below. This file is the queue: every live test a merged
change still owes, so that the run is a checklist rather than an archaeology of
the changelog.

Update the date above whenever the account's state is checked, so that
"unblocked" stays a fact somebody established rather than an assumption carried
forward.

## How a live test is run

The rules are in `CLAUDE.md` and are not repeated here, only the two that are
easiest to lose:

- **One issue per change set**, in `codecheckers/testing-dev-register`, named
  for what is being tried and listing what each command should answer. Link
  the chekhov issue from it, and it from the closing comment.
- **One command at a time, each reply read before the next.** The bot answers
  in a goroutine, so a burst of comments comes back out of order.

**The bot is never the one who types.** The issue and every command are posted
with `gh`, as the maintainer's own account (`gh issue create`, `gh issue
comment`); `@chekhovbot` only answers. Its token is fenced to what a reply
needs, and a test must not use it - or any other - to do the asking.

A handle used in a test must be one nobody owns -
`gh api users/<handle> --silent || echo "free to use"` - except `@nuest` and
`@chekhovbot`. `@a-codechecker` and `@a-handling-editor` were free when this
was written.

When a test has been run, tick it, and say on the chekhov issue what the
deployment answered.

## The queue

Run on testing-dev-register#222 and #223 against 0.12.2, 2026-10-19, as `@nuest`
(an editor, and here the assigned codechecker). Findings from that run that are
not a tick:

- `status` after `set certificate` cannot resolve the target: a freshly
  reserved identifier is not in `register.csv`. The record resolves only once
  it holds an identifier the register knows, which `set certificate` refuses
  to hand out; the step was run by editing the record's header and adopting it.
- A clean `check finish` tells the codechecker to run `@chekhovbot register`,
  which is not a command yet (#59, fixed in 0.12.3; see the live test below).
- The placeholders `check finish` says "the register rules pass, so the entry
  is ready to be opened" and, below it, that the check is not finished (#60,
  fixed in 0.12.3; see the live test below).
- `assign` on a check whose codechecker belongs to an institution warns that the
  `institution` label is missing, on every reply from then on.

### register#232, #57 — the labels of the checks issue, the files of the deposit

Not run yet. Against 0.13.0.

- [ ] **`@chekhovbot check register <certificate>`** for a row of the testing
      register whose issue lacks its type label warns `CC-REG-008` and names
      the labels the row wants. The testing register's `venues.csv` has one
      venue, `FAKE PeerJ Computer Science` with the label `journal`, so a
      journal row there wants `journal` alone, and so does a row whose venue
      is not in `venues.csv` - which is `CC-REG-005`'s warning, not this one's.
- [ ] After the labels are added by hand, the same command passes
      `CC-REG-008` - the issue is read afresh, not from a copy of the first run.
- [ ] **`@chekhovbot check <certificate>`** for a certificate whose Zenodo
      deposit holds `CODECHECK_certificate_YYYY-NNN.pdf` and no source warns
      `CC-REP-008` and `CC-REP-009`, and says nothing about `CC-REP-003`.

### #59, #60 — what `check finish` says next

Run on testing-dev-register#224 against 0.12.3, 2026-10-19.

- [x] **A clean `check finish`** (`testdata/valid-2.0`) names the pull request
      against `register.csv` in words, with no code block.
- [x] **A `check finish` on `testdata/placeholders`** says the check is waiting
      on metadata and `CC-CFG-023`, and says nothing about the register rules or
      the entry being ready.
- [x] **`@chekhovbot register`** is answered "I do not know the command".

### #1, #2, #3 — `commands`, `hello`, the unknown-command hint

Run on testing-dev-register#222 against 0.10.0, 2026-10-15.

- [x] `@chekhovbot hello` is answered on a real issue of the testing register
- [ ] `@chekhovbot commands` lists the groups (it does, as an editor), and lists
      nothing an editor-only command should show a passer-by - not run: it
      needs a second account that is not an editor
- [x] `@chekhovbot helo` suggests `hello`; `@chekhovbot polish the certificate`
      suggests nothing. (This read `registr` suggests `register` before, but
      `register` is a group heading and not a command, so the example could
      never have answered.)

### #7 — `check codecheck.yml`

Run on testing-dev-register#222 against 0.10.0, 2026-10-15.

- [x] a real check of a real repository is answered in the thread, with the
      findings carrying their line numbers (`check config
      codecheckers/Piccolo-2020`)
- [x] a configuration declaring an unknown specification version is refused
      rather than checked against the newest (`testdata/future-spec`)

### #20 — the certificate identifier

Run on testing-dev-register#207 against 0.10.0, 2026-10-16. The register had
moved on from the numbers in that issue's body (`2026-099` on #220 was the
highest, checked against the issue titles), so the next identifier was
`2026-100` and not `2026-091`; the refusals, `2026-025` on #130 and the jump
to `2026-082`, were unchanged. #207 now holds `2026-082`.

- [x] `@chekhovbot next certificate` counts `register.csv` and the issues
      labelled `id assigned`, and lists the unlabelled titles it did not count
      - a range as its title writes it (`2026-004/2026-017`), not spelled out
- [x] `@chekhovbot set certificate` reserves the next one: the record, the
      title and the label all change, and the record comment opens
      `<!-- chekhov:record `, not `chekhov:roles`
- [x] `@chekhovbot set certificate` again changes nothing and says so: no
      title, no label, no "set by" line
- [x] `@chekhovbot set certificate` on a number another issue holds is refused,
      naming that issue and `CC-REG-001`
- [x] a jump of more than 9 is refused with `CC-REG-002` and the `anyway`
      command, and `… anyway` sets it and says the jump was confirmed
- [x] `@chekhovbot roles` afterwards shows the identifier

### #52 — the request guard, and one write at a time

Merged as `0f75049` (0.12.1) and `bc166dd` (0.12.2). The guard itself is
proved offline - a refused write never reaches a server - and has nothing to
show live beyond every other test still working. The queue has:

- [x] two commands posted back to back on one issue - `hello`, then
      `version` - are answered **at least three seconds apart** by the
      comments' timestamps, and both are answered (4 s apart on
      testing-dev-register#222, 0.12.2, 2026-10-16)
- [x] a staged reply (`check links` on more than five links, or `check
      finish`) still arrives and is rewritten, its edits paced like any write
      (`check links codecheckers/Piccolo-2020`: the notice, then the answer in
      the same comment)
- [x] reading is **not** held up behind the queue - proved offline for both
      writing clients (`TestWritesArePacedAndQueued`,
      `TestAReadIsNotHeldUpBehindTheWrites`), not live: a read never shows in
      a thread, and its reply is a write that waits its turn by design, so a
      burst and a `status` would measure nothing
- [x] `/healthz` and the development footer report `0.12.2`

### #11 — `check metadata references`

testing-dev-register#210 says "All six answered as expected under 0.10.0",
but its thread shows no replies: they were posted after the account was
hidden. Rerun it once the account is back, rather than count it as done.

- [x] the six commands of testing-dev-register#210 are answered in the thread,
      as that issue lists them (the replies show again now the account is
      back; read 2026-10-15, they match the table in that issue)

### #18, #47 — roles, and the invitation

- [ ] `@chekhovbot assign @a-codechecker as codechecker` records the role,
      assigns the issue, and puts a member into the codecheckers team
      (postponed: needs an account that is in the organisation but not yet in
      the team)
- [ ] (postponed: needs a real GitHub account outside the organisation, which
      the maintainer does not want to create for a test) somebody outside the
      organisation produces the `OutsideReply`, and a
      **development** deployment asks `teams.owners` rather than the
      organisation's real owners - the reply says which list it used
- [x] a record edited by hand is refused, and `@chekhovbot accept record`
      adopts it, writing who adopted it into the signed block (testing-dev-register#223,
      2026-10-19: `accepted_by` and `accepted_at` are in the signed header)

### #12 — `check links`

Merged as `42b5d33` (0.12.0). #12 closes once these have been answered.

- [x] `@chekhovbot check links` with no target answers "I need to be told what
      to check", as every other `check` does
- [x] `@chekhovbot check links codecheckers/Piccolo-2020` - more than five
      links, so a "Checking N links …" comment appears at once and is
      **rewritten** with the answer, leaving one comment in the thread, with
      the development footer still under it. The answer reads from the issue,
      `codecheck.yml`, `codecheck/piccolo2020-codecheck.Rmd` and Zenodo record
      3674056, and lists `codeocean.com` as "refuses automated requests
      (403)", not as gone
- [x] a check with five links or fewer is answered in one comment, no notice
      first
- [x] a link in the issue body to a page that does not exist (a made-up path
      on `github.com`) is listed as ✖ "answers 404", found in the issue
- [x] a link in the issue to `linkedin.com` is not asked, and is counted in
      the note on sites that refuse automated requests
- [x] a ResearchEquals DOI mentioned in the issue (`10.53962/nsys-9a40`) has
      its deposit read: "ResearchEquals 10.53962/nsys-9a40" among the sources
- [x] a staged reply of #22 (`check finish`) keeps the development footer
      after it is rewritten - rewrites now go through `EditReply`
- [x] `/healthz` and the development footer report `0.12.0`

### #44 — the labels that no longer describe the check

- [x] a check with an assigned codechecker that still carries
      `needs codechecker` is warned about, under the command's own answer
- [x] the note appears once in a reply, never twice

### #48 — the nightly sweep

- [x] `@chekhovbot nudge` walks the open issues and reports what it found
      (198 open issues, nothing outstanding; testing-dev-register#222)
- [ ] (postponed, with the item above: the only follow-up kind starts with an
      outside invitation) a follow-up the sweep has already answered is not
      answered twice
- [x] `GET /nudges` reports the sweeps without naming anybody (21 nightly
      sweeps since 2026-09-23, the last 2026-10-15, no handle in the body)

### #23, #31 — announcing and following

- [x] a development deployment previews, then on `confirm` posts a `direct`
      toot for `1970-001`, and refuses a second `confirm` (testing-dev-register#223,
      2026-10-19: the toot, fediscience.org/@codecheck/117288512270117211, was
      already out from an earlier run and is not public; `announce 1970-001
      confirm` answered with the preview and "Already announced", and posted
      nothing. The first `confirm` was not repeated.)
- [x] `@chekhovbot follow` previews and, with `follow: false` in the settings,
      acts on nothing (previewed, and `confirm` too: both say following is switched off)

### #24 — finding a codechecker

- [x] `@chekhovbot codecheckers` links the lists and counts them
- [x] `@chekhovbot suggest codecheckers` says what it matched on: with a
      random production article that has an abstract (2021-001,
      `github::codecheckers/causality-review`) it matched on languages and
      left out the assigned codechecker. Naming it by certificate identifier
      does not work in the testing register, because `register.csv` there does
      not hold production identifiers. The bare form, which reads the issue
      itself, is only covered by "Nothing to match on" on #222.

### #22 — workflow state

The commands, in order. Post one at a time and read each reply before the next.

- [x] `@chekhovbot check codecheckers/<a repository>` still answers as before,
      run first so a parser regression is caught before anything new
- [x] `@chekhovbot status` before anything: no codechecker, no target
- [x] `@chekhovbot check start` refused, no assigned codechecker
- [x] `@chekhovbot assign @a-codechecker as codechecker` (as `@nuest`, who
      was assigned and recorded; the team membership was not exercised)
- [x] `@chekhovbot check start github::codecheckers/<a repository>` - the
      labels change at once, and the repository description and the
      configuration arrive **into the same comment**, which is the staged
      reply working
- [x] `@chekhovbot status` - the roles, the labels, the two check stages (with a
      certificate `register.csv` holds in the record; both stages ran and said "could
      not check", since the fixture repository does not exist)
- [x] `@chekhovbot check finish` refused, nothing to read a configuration from
- [x] `@chekhovbot set certificate`, then `@chekhovbot status` - the target now
      resolves from the record
      (testing-dev-register#223, 2026-10-19: not reachable with `set certificate`, which
      refuses an identifier `register.csv` holds. Reached by editing the record's
      header to `1970-001` and adopting it: `status` resolved it to
      `github::testuser/FAKE-ml-classification-demo`, and warned that the title
      still carried `2026-100`.)
- [x] `@chekhovbot check finish <a repository with placeholder DOIs>` -
      `work in progress` off, `metadata pending` on, `CC-CFG-023` named
- [x] `@chekhovbot check finish <a clean repository>` - both off, the venue
      type labels and `id assigned` untouched, `register` suggested

What the offline suite cannot prove, from the review of this change. Each of
these is a real-client behaviour a stub got wrong or could not reach, so the
live run is the first honest test of it:

- [x] **The reply does not contradict itself.** On the `check start` above, the
      comment says it removed `needs codechecker` and must **not** also warn
      that the check still carries it. The offline test missed this until the
      stub stopped aliasing the issue's label slice, which the real client
      never did - so this is the one to read most carefully.
- [x] **An edited record decides nothing.** Edit the bot's record comment by
      hand, then: `check start` and `check finish` refuse and say the record
      was edited; `status` still answers and shows the provenance, because that
      is where an editor finds out. `accept record` then unblocks the two.
- [x] **A mistyped or misplaced subcommand is answered.** `@chekhovbot check
      fnish` suggests `check finish`; `@chekhovbot check status` suggests
      `status`; `@chekhovbot check config <a repository>` is untouched.
- [x] **A warning is listed, not just counted.** A configuration whose only
      findings are warnings reads `⚠` with the rules under it, rather than `✔`
      because nothing failed outright.
- [x] **A check whose configuration cannot be read** - name a repository that
      404s - moves no label, and does **not** say the register entry is ready.
