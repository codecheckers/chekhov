# internal/bot

## The bot

<!-- Source: own rule; who may ask (#70) and share link deletion added 2026-11-26. -->

`bot.Serve` is the whole deployment: `POST /dispatch` for GitHub's deliveries,
`GET /help` (the commands as a page, generated from
the registry), `GET /healthz` and `GET /nudges` for everything else. No state, no database. The container runs
it through the `Procfile` (`web: chekhov serve`), because a buildpack otherwise
runs the built binary with **no arguments**, and the tool answers that with its
usage - which the platform reads as a crash loop.

- **Verify, then read.** The signature is checked against
  `CHEKHOV_GH_SECRET_TOKEN` before the body is parsed, in
  `internal/bot/webhook.go`. An unsigned delivery is not data the bot has any
  business reading.
- **Answer, then work.** The delivery gets a 202 and the command runs in a
  goroutine: GitHub times out at ten seconds, and a `check` that asks Crossref
  and Zenodo takes longer.
- **A record that does not verify decides nothing.** `check start` and
  `check finish` act on what the record says - who may start the check, which
  repository to run the rules against - so `command.about` refuses a reading
  whose signature does not hold, as every other reader of the record does.
  `status` is the exception on purpose: it reports rather than acts, and
  showing what an edited record says, with the provenance beside it, is how an
  editor finds out it was edited.
- **A slow reply is written more than once.** The commands about the state of a
  check wrap the checks that already exist, and those take a minute; GitHub has
  its 202 long before then, but the person who typed the command is looking at
  an issue with nothing on it. So `internal/bot/progress.go` posts the reply as
  soon as its first stage answers and **rewrites the same comment** as the rest
  come in - one comment, not two, as a reply to a role change already is. A
  reply path that cannot edit posts once at the end, which is the ordinary path
  and needs no special case. Every body goes through `Server.bodyOf`, so the
  rule that nothing the bot posts may look like a record is in one place; the
  follow-up record goes only on the finished body, because a record saying
  something is outstanding must not exist while the command is still deciding.
- **One write at a time, three seconds apart.** Writes to GitHub and to
  Mastodon each queue on the client's `httpretry.Pacer`, one client per
  service per deployment, so every command of every editor shares the queue:
  a burst of comments from a young account is what GitHub's spam detection
  hid `@chekhovbot` for, after a live test. A rate limit holds the whole
  queue; reads are not held up. Never build a second write queue: a client
  for another repository comes from `Client.For`, which shares this one's
  Pacer (the codechecker lists, #49). Tests set `WriteInterval` to 0.
- Three guards, in this order: the repository is the configured target, the
  author is not the bot itself, the event is an opened issue or a created
  comment. Everything else is a 204 with a log line, never an error.
- **Then who may ask** (`gate.go`), once per comment, in `actEach` and
  `Preview`: cheapest first - the event's opener and assignees (not on a pull
  request), the cached teams, one request about the organisation, then the
  issue's comments for the record, its allowlist, or whom a registration on the
  lists is for. It fails closed. `answer` asks only about roles, so **a new way
  of running a command has to ask the gate too.** A refusal is posted once per
  account and issue, and one that could not read everything once more besides
  (`command.TurnedAwayOpening` is how either is known again, so do not reword
  the starts), never notifies, and is no answer to anything else:
  `latestRowAnswered` and `justSaid` skip it. A command whose mention of somebody
  is not their taking part is marked `AsksAbout` in the registry. A share link in a refused comment
  is still deleted, and explained at most `refusals.share_link_replies` times
  per account and issue, counted from the bot's own comments. **A link that
  could not be deleted is still up**: its warning is counted apart, and no
  setting or unreadable listing silences the first. The sweeps do not ask the
  gate.
- **`deleteShareLink` leaves only a comment whose links Zenodo shows nothing
  for** (`opensNothing`, which trusts only a token that is plainly
  whole): **every doubt deletes**, because a real link left
  up costs more than a fake one removed. Zenodo hides a draft from a token
  that does not open it, and ignores the token on a published record, so there
  no request can show a link genuine.
- **A share link is dealt with once per comment**, in `actEach` before any
  command is run or quoted, and through the path of the repository the comment
  is in (`posterFor`). `command.Defuse` cuts the token out of whatever the bot
  posts, and a record that keeps somebody's text has to cut it too.
- **A deployment does not read its own disk.** `Server.LocalPaths` is off, so a
  path in a comment is not a target; only a `github::owner/repo` spec is. The
  command line preview turns it on, because there the path is the user's own.
- `internal/github` is the only code that writes to GitHub, and it refuses any
  repository but the configured one before making a request - in each method,
  and again in `do`; a new write endpoint has to be added to `guard.go` too,
  on purpose. The one GraphQL write, turning the row's pull request into a
  draft and back (#49), is let through by its query and a single `id`, and
  the guard cannot tell which repository a node id belongs to:
  `SetRowPullRequestDraft` reads the pull request by number from the lists
  repository first and takes the id from it, so never pass it an id from
  anywhere else.
- `chekhov comment` renders its answer through the same `bot.Preview` the
  listener uses, so what you read on the command line is what would be posted.

<!-- Source: own rule, #48; closed checks added 2026-11-02 (#27). -->
**What a reply leaves outstanding lives in the thread.** A signed follow-up
record (`internal/followup`) says what kind, who it is about and when it was
asked; a sweep walks the open issues nightly, and the closed ones changed in
the last ninety days for the follow-ups that outlive a check, and the open
registrations on the codechecker lists through the lists' own client (#49) - a goroutine in the deployment,
not a second job with a second token - and hands each record to the handler
for its kind. A handler decides by **looking at the world**, never by
bookkeeping: when the thing is done it says nothing, which is what makes the
sweep safe to run twice. The exceptions are marks of what was **said**,
signed in the record, never of what was done: `Settled` for an end confirmed
out loud, and `Told` for the one-off thing a kind says, said - an
inclusion's invitation waiting to be accepted (#49), a join said while the
team could not be done, after which only the team is said again (#69). A record is only a record because **the bot wrote
it** - the sweep reads its own comments and nobody else's, as
`people.Checks.Read` does, because a deployment with no record key signs
nothing and anybody can comment on an issue of the register. `GET /nudges` reports the sweeps for the weekly
workflow that exists only to notice the goroutine has stopped; it is
unauthenticated, so it counts problems rather than naming people.

`docs/deployment.md` has the platform, the webhook settings and what to do when
it falls over; `docs/github-token.md` has the bot's credential and its
rotation, `docs/mastodon-token.md` the announcing account's.
