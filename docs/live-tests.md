# Live tests still to run

The offline suite proves the bot decides correctly. It cannot prove that
GitHub accepts the write, that the reply renders the way it reads in a test, or
that a label the settings name is a label the register has. That is what a live
test is for, and until one has been run a feature is implemented rather than
done.

**The bot account is currently blocked on GitHub**, so nothing here can be run.
This file is the queue: every live test a merged change still owes, so that
when the account is back the run is a checklist rather than an archaeology of
the changelog.

## How a live test is run

The rules are in `CLAUDE.md` and are not repeated here, only the two that are
easiest to lose:

- **One issue per change set**, in `codecheckers/testing-dev-register`, named
  for what is being tried and listing what each command should answer. Link
  the chekhov issue from it, and it from the closing comment.
- **One command at a time, each reply read before the next.** The bot answers
  in a goroutine, so a burst of comments comes back out of order.

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

- [ ] `@chekhovbot next certificate` counts `register.csv` and the issues
      labelled `id assigned`, and lists the unlabelled titles it did not count
- [ ] `@chekhovbot set certificate` reserves the next one: the record, the
      title and the label all change
- [ ] `@chekhovbot set certificate` on a number another issue holds is refused,
      naming that issue and `CC-REG-001`
- [ ] `@chekhovbot roles` afterwards shows the identifier

### #18, #47 — roles, and the invitation

- [ ] `@chekhovbot assign @a-codechecker as codechecker` records the role,
      assigns the issue, and puts a member into the codecheckers team
- [ ] somebody outside the organisation produces the `OutsideReply`, and a
      **development** deployment asks `teams.owners` rather than the
      organisation's real owners - the reply says which list it used
- [ ] a record edited by hand is refused, and `@chekhovbot accept record`
      adopts it, writing who adopted it into the signed block

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
