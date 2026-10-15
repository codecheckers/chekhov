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

### #1, #2, #3 — `commands`, `hello`, the unknown-command hint

- [ ] `@chekhovbot hello` is answered on a real issue of the testing register
- [ ] `@chekhovbot commands` lists the groups, and lists nothing an editor-only
      command should show a passer-by
- [ ] `@chekhovbot registr` suggests `register`; `@chekhovbot polish the
      certificate` suggests nothing

### #7 — `check codecheck.yml`

- [ ] a real check of a real repository is answered in the thread, with the
      findings carrying their line numbers
- [ ] a configuration declaring an unknown specification version is refused
      rather than checked against the newest

### #20 — the certificate identifier

The core was answered on testing-dev-register#209 against 0.8.0 and 0.10.0,
before the account was hidden; those replies are hidden with it, so tick them
only once they show again. What 0.9.0 added was never run: that is
testing-dev-register#207, written and waiting, with the numbers as they stood
after #209 took `2026-090` - check them against the register before step 2.

- [ ] `@chekhovbot next certificate` counts `register.csv` and the issues
      labelled `id assigned`, and lists the unlabelled titles it did not count
      - a range as its title writes it (`2026-004/2026-017`), not spelled out
- [ ] `@chekhovbot set certificate` reserves the next one: the record, the
      title and the label all change, and the record comment opens
      `<!-- chekhov:record `, not `chekhov:roles`
- [ ] `@chekhovbot set certificate` again changes nothing and says so: no
      title, no label, no "set by" line
- [ ] `@chekhovbot set certificate` on a number another issue holds is refused,
      naming that issue and `CC-REG-001`
- [ ] a jump of more than 9 is refused with `CC-REG-002` and the `anyway`
      command, and `… anyway` sets it and says the jump was confirmed
- [ ] `@chekhovbot roles` afterwards shows the identifier

### #52 — the request guard, and one write at a time

Merged as `0f75049` (0.12.1) and `bc166dd` (0.12.2). The guard itself is
proved offline - a refused write never reaches a server - and has nothing to
show live beyond every other test still working. The queue has:

- [ ] two commands posted back to back on one issue - `hello`, then
      `version` - are answered **at least three seconds apart** by the
      comments' timestamps, and both are answered
- [ ] a staged reply (`check links` on more than five links, or `check
      finish`) still arrives and is rewritten, its edits paced like any write
- [ ] reading is **not** held up behind the queue: `status` on another issue
      answers promptly while a burst of writes is still going out
- [ ] `/healthz` and the development footer report `0.12.2`

### #11 — `check metadata references`

testing-dev-register#210 says "All six answered as expected under 0.10.0",
but its thread shows no replies: they were posted after the account was
hidden. Rerun it once the account is back, rather than count it as done.

- [ ] the six commands of testing-dev-register#210 are answered in the thread,
      as that issue lists them

### #18, #47 — roles, and the invitation

- [ ] `@chekhovbot assign @a-codechecker as codechecker` records the role,
      assigns the issue, and puts a member into the codecheckers team
- [ ] somebody outside the organisation produces the `OutsideReply`, and a
      **development** deployment asks `teams.owners` rather than the
      organisation's real owners - the reply says which list it used
- [ ] a record edited by hand is refused, and `@chekhovbot accept record`
      adopts it, writing who adopted it into the signed block

### #12 — `check links`

Merged as `42b5d33` (0.12.0). #12 closes once these have been answered.

- [ ] `@chekhovbot check links` with no target answers "I need to be told what
      to check", as every other `check` does
- [ ] `@chekhovbot check links codecheckers/Piccolo-2020` - more than five
      links, so a "Checking N links …" comment appears at once and is
      **rewritten** with the answer, leaving one comment in the thread, with
      the development footer still under it. The answer reads from the issue,
      `codecheck.yml`, `codecheck/piccolo2020-codecheck.Rmd` and Zenodo record
      3674056, and lists `codeocean.com` as "refuses automated requests
      (403)", not as gone
- [ ] a check with five links or fewer is answered in one comment, no notice
      first
- [ ] a link in the issue body to a page that does not exist (a made-up path
      on `github.com`) is listed as ✖ "answers 404", found in the issue
- [ ] a link in the issue to `linkedin.com` is not asked, and is counted in
      the note on sites that refuse automated requests
- [ ] a ResearchEquals DOI mentioned in the issue (`10.53962/nsys-9a40`) has
      its deposit read: "ResearchEquals 10.53962/nsys-9a40" among the sources
- [ ] a staged reply of #22 (`check finish`) keeps the development footer
      after it is rewritten - rewrites now go through `EditReply`
- [ ] `/healthz` and the development footer report `0.12.0`

### #44 — the labels that no longer describe the check

- [ ] a check with an assigned codechecker that still carries
      `needs codechecker` is warned about, under the command's own answer
- [ ] the note appears once in a reply, never twice

### #48 — the nightly sweep

- [ ] `@chekhovbot nudge` walks the open issues and reports what it found
- [ ] a follow-up the sweep has already answered is not answered twice
- [ ] `GET /nudges` reports the sweeps without naming anybody

### #23, #31 — announcing and following

- [ ] a development deployment previews, then on `confirm` posts a `direct`
      toot for `1970-001`, and refuses a second `confirm`
- [ ] `@chekhovbot follow` previews and, with `follow: false` in the settings,
      acts on nothing

### #24 — finding a codechecker

- [ ] `@chekhovbot codecheckers` links the lists and counts them
- [ ] `@chekhovbot suggest codecheckers` with nothing named uses the issue
      itself, and says what it matched on

### #22 — workflow state

The commands, in order. Post one at a time and read each reply before the next.

- [ ] `@chekhovbot check codecheckers/<a repository>` still answers as before,
      run first so a parser regression is caught before anything new
- [ ] `@chekhovbot status` before anything: no codechecker, no target
- [ ] `@chekhovbot check start` refused, no assigned codechecker
- [ ] `@chekhovbot assign @a-codechecker as codechecker`
- [ ] `@chekhovbot check start github::codecheckers/<a repository>` - the
      labels change at once, and the repository description and the
      configuration arrive **into the same comment**, which is the staged
      reply working
- [ ] `@chekhovbot status` - the roles, the labels, the two check stages
- [ ] `@chekhovbot check finish` refused, nothing to read a configuration from
- [ ] `@chekhovbot set certificate`, then `@chekhovbot status` - the target now
      resolves from the record
- [ ] `@chekhovbot check finish <a repository with placeholder DOIs>` -
      `work in progress` off, `metadata pending` on, `CC-CFG-023` named
- [ ] `@chekhovbot check finish <a clean repository>` - both off, the venue
      type labels and `id assigned` untouched, `register` suggested

What the offline suite cannot prove, from the review of this change. Each of
these is a real-client behaviour a stub got wrong or could not reach, so the
live run is the first honest test of it:

- [ ] **The reply does not contradict itself.** On the `check start` above, the
      comment says it removed `needs codechecker` and must **not** also warn
      that the check still carries it. The offline test missed this until the
      stub stopped aliasing the issue's label slice, which the real client
      never did - so this is the one to read most carefully.
- [ ] **An edited record decides nothing.** Edit the bot's record comment by
      hand, then: `check start` and `check finish` refuse and say the record
      was edited; `status` still answers and shows the provenance, because that
      is where an editor finds out. `accept record` then unblocks the two.
- [ ] **A mistyped or misplaced subcommand is answered.** `@chekhovbot check
      fnish` suggests `check finish`; `@chekhovbot check status` suggests
      `status`; `@chekhovbot check config <a repository>` is untouched.
- [ ] **A warning is listed, not just counted.** A configuration whose only
      findings are warnings reads `⚠` with the rules under it, rather than `✔`
      because nothing failed outright.
- [ ] **A check whose configuration cannot be read** - name a repository that
      404s - moves no label, and does **not** say the register entry is ready.
