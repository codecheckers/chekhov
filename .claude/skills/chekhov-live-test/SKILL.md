---
name: chekhov-live-test
description: How to deploy chekhov-dev and run a live test on the testing register - when a feature counts as done, one test issue per change set, posting commands one at a time, and how to read a rendered reply. Use after pushing a change, or when asked to deploy or live-test.
---

<!-- Source: own rule; close on commit, reopen on a failed live run, 2026-11-02. -->
**A feature is done when the deployment has answered it.** Offline tests prove
the bot decides correctly; they cannot prove GitHub accepts the write or that
the reply renders the way it reads in a test. So the live run is part of the
change, not a queue kept beside it: deploy, run it on the testing register,
fix what it turns up, and say in the issue what the deployment answered.
**The commit closes the issue anyway** (`closes #N`), with the background
comment beside it, and the live run follows the push. **When the live run
fails, reopen the issue** and move it back to In Progress, saying so in the
reply; the fix closes it again. Reopening needs no go-ahead, because the
user decided this approach; the comment saying what failed is drafted first,
as every comment is. What cannot be run - it needs an account nobody wants to
create - waits in chekhov#63 rather than in a file.

**Deploying** is from a second checkout, so that the platform builds a commit
and never a working tree: in `~/git/codecheck/chekhov-deploy`,
`git pull --ff-only` (from the working checkout, commits only) and
`runway app deploy source -y`. That clone's `runway` remote is the
`chekhov-dev` app, which is where every live test goes; wait for
`https://chekhov-dev.pqapp.dev/healthz` to report the new version before the
first command; see `docs/deployment.md`. **Production** is the `chekhov` app
at `https://chekhov.pqapp.dev`, on `codecheckers/register` since 2026-10-30
(#37), deployed from `~/git/codecheck/chekhov-deploy-prod` - and only on an
explicit go-ahead for that deploy, as for a push: it is the bot every
codechecker talks to.

**One live test issue per change set.** Live testing happens on a new issue in
the testing register, named for what is being tried and listing what each
command should answer. A single long-running issue buries the run that matters
under a hundred earlier comments, and a reply cannot be read against what was
expected of it. Link the chekhov issues from the test issue, and the test issue
from the closing comment, so the proof is one click away. Its first line is
prose, never a command: the bot answers an opened issue too.

**Post the commands one at a time, and wait for each reply.** The bot answers
in a goroutine, so a burst of comments comes back out of order: eight commands
posted in eight seconds produced replies interleaved with the next commands,
and matching each answer to its question meant reading timestamps. Leave enough
time for the reply to land - a `check` that asks OpenAlex and Zenodo takes
longer than `rules` - and read it before posting the next one. A staged reply
is rewritten as its stages answer, so it is finished when it has stopped
changing, not when it first appears. The thread is the record, and it has to be
readable by somebody who was not there.

**Judge a reply by how GitHub renders it**, not by its source: the comment's
HTML is `gh api repos/<repo>/issues/comments/<id> -H 'Accept:
application/vnd.github.html+json' --jq .body_html`. A `|` in a table cell ends
the cell even inside a code span, and a line straight after a list is part of
its last item; both looked right in the source and wrong on the issue (#21).

**What to point a test at.** The `1970-*` rows of the testing register name
made-up repositories, so a certificate alone has no configuration to read;
point at a bundle in `testing-dev-register/testdata/`, one per behaviour, each
a copy of `testdata/valid-2.0/` with the one line that matters changed. A
bundle that stands for a row not yet in the register carries the next
`1970-NNN` after the last row: `CC-REG-002` measures the jump against
`register.csv` alone, where any other year starts from nothing.
