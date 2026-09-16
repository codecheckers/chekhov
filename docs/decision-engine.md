# Engine decision: adopt buffy vs. new implementation

Input to <https://github.com/codecheckers/register/issues/209>. Facts gathered
2026-09-04.

## Case A — adopt buffy

### Licensing

**MIT** (`openjournals/buffy`, 29 stars, 22 forks, active: last push 2026-07-08).
No practical limit: fork, modify, relicense, run closed — the only obligation is
to keep the copyright notice and licence text. `codecheckers/chekhov` is already
MIT, so a fork is a drop-in; add a CODECHECK copyright line and keep the Open
Journals one. **The constraint is maintenance, not law.**

### Has anyone else done it?

Yes — and the way they did it is the interesting part.

| Fork | Commits ahead of upstream | What that means |
|---|---|---|
| `ropensci-org/buffy` | **0 ahead**, 5 behind | Pure YAML configuration |
| `pyOpenSci/buffy` | **0 ahead**, 84 behind | Pure configuration |
| `ReScience/buffy` | **0 ahead**, 93 behind | Pure configuration |
| `scipy-conference/buffy` | 68 ahead (branch `scipy`) | Light customisation |
| `neurolibre/roboneuro-neo` | **309 ahead**, 132 behind | Heavy fork, new feature set |

Two findings:

1. **Three peer organisations run buffy with zero code changes.** rOpenSci
   (`@ropensci-review-bot`), pyOpenSci and — most relevant to us — **ReScience**,
   a reproducibility venue, all needed only configuration. Note pyOpenSci is a
   *Python* organisation that still chose to run someone else's Ruby rather than
   rewrite it.
2. **NeuroLibre is the "completely new features" fork you asked about.**
   `roboneuro-neo` is 309 commits ahead with ~20 custom responders of its own
   (`zenodo_upload_*`, `binder_build`, `sync_myst`, `preprint_server_status`,
   `coar_responder`, …) under a namespaced `app/responders/neurolibre/`
   directory. Buffy is explicitly built for this: `docs/custom_responder.md`, a
   namespaced responders directory, and a `Gemfile_custom` hook so downstream
   dependencies don't touch the core Gemfile.

So both routes are proven: configure-only for CODECHECK's four commands, with a
documented escape hatch to custom responders (Zenodo interaction is exactly what
NeuroLibre already solved — worth reading their Zenodo responders regardless of
which engine we pick).

### What buffy costs to run

`Procfile`:

```
web:    bundle exec puma -C ./puma-config.rb
worker: bundle exec sidekiq -t 45 -r ./app/lib/workers.rb
```

That is **three processes**: Puma (Sinatra), Sidekiq, and the Redis that Sidekiq
requires. Plus a fairly heavy gem set — `octokit`, `sidekiq`, `bibtex-ruby`,
`serrano`, `github-linguist`, `licensee`.

## The constraint that breaks both cases

The issue requires a free tier and "a very small footprint". Measured against
the two candidate platforms, that requirement fights a persistent webhook server
in *any* language:

**Render free**
- Spins down after **15 minutes** without inbound traffic; ~**1 minute** to spin
  back up.
- GitHub's webhook delivery timeout is **10 seconds**, and repository webhooks
  are not retried automatically.
- ⇒ **The first command after any quiet period is silently dropped.** For a bot
  that gets a handful of commands a week, that is the normal case, not the edge
  case. This is a platform problem, not a Ruby problem.
- **No background workers on the free tier** — only static sites, web services,
  Postgres and Key Value. Buffy's `worker` process has nowhere to run.
- Free Key Value exists but is not persisted to disk; data is lost on restart.
- 750 free instance hours/month per workspace.

**runway.horse free**
- €0, 0.5 CPU, **128 MB RAM**, 2 GB storage, up to 6 apps.
- "We currently don't impose any limits (like sleeps) on free apps" — **no
  spin-down**, which solves Render's fatal problem.
- But 128 MB will not hold Puma + Sidekiq + Redis. A Ruby web process alone
  typically sits in the 100 MB+ range before the worker exists. (Measure before
  trusting this, but the direction is not in doubt.)

**⇒ buffy and the free tier are mutually exclusive.** Buffy needs ~€7–15/month
(runway Dev/Start, or Render Starter) or an institutional VM. That is not a
large sum, and "one person expenses €90/year" may simply be the right answer —
but it must be a decision, not a discovery made after the code is written.

## Case B — new implementation

### What we would actually be reimplementing

Buffy's core is a command router: parse the first line of an issue comment,
check the author's team membership, dispatch to a responder, post a reply. The
four CODECHECK commands (`welcome`, `assign`, `check codecheck.yml`, `register`)
are all plain GitHub API calls plus one YAML validation and one CSV edit + PR.
This is genuinely small — days, not months. What we would *not* get for free is
buffy's accumulated edge-case handling: labels, reminders/scheduling, team
lookups, checklists, templating, invitation flows.

### Language evaluation

| | Fit for the footprint constraint | Contributor pool in this community | Notes |
|---|---|---|---|
| **Ruby** | Poor as buffy ships (3 processes); fine as a single small Sinatra app | Weak for app code, though the org already runs Ruby via Jekyll and bundler | Only rational if we adopt buffy |
| **Python** | Good — one process, ~40–70 MB with FastAPI/Flask | **Strongest.** Matches the maintainers' stack; `codecheck-py` already exists; every codechecker can read it | `PyGithub`/`httpx`; `ruamel.yaml` for `codecheck.yml`; stdlib `csv` for the register |
| **Go** | **Best** — static binary, ~10–20 MB RSS, millisecond cold start; comfortable even in 128 MB | Weakest. Realistically a bus factor of one in this community | `go-github` is excellent |
| **Rust** | Excellent runtime, worst development speed | Weakest | Not justified for a comment router |
| **JS/TS** | Fair (~60–80 MB) | Moderate | `probot` is GitHub's own bot framework, but it is oriented at GitHub Apps, and we deliberately chose a bot *user account* for mention autocomplete |

**Critical read:** Go wins the resource argument, Python wins the argument that
actually matters. A register bot handles maybe tens of events a week — the
performance difference is irrelevant, while the difference between "any
codechecker can fix a responder" and "only Daniel can" decides whether this
thing survives. Choose Python unless the 128 MB tier turns out to be genuinely
tight, and measure before believing that it is.

## Option C — no server at all (GitHub Actions)

Worth putting on the table because it dissolves the constraint rather than
negotiating with it. An `issue_comment`-triggered workflow in the register repo
runs a Python script that does exactly what a responder would do.

- **Cost: zero, permanently.** Actions minutes are free and unmetered on public
  repositories. No hosting account, no platform migration risk, no bill anybody
  has to keep paying after the funding ends.
- **No cold-start failure mode** — the fatal Render problem disappears, because
  there is nothing to keep awake.
- Reminders are a `schedule:` workflow; secrets are repository secrets.
- Costs: ~10–25 s latency per command; no in-memory state between commands; a
  workflow file must live in `codecheckers/register` itself; and heavy or
  long-running work is awkward.
- The bot still posts as `@chekhovbot` if the workflow uses the bot's PAT rather
  than `GITHUB_TOKEN`.

Buffy has no equivalent — it is a server by design.

## Recommendation

1. **Write the responder logic in Python**, with the command grammar copied from
   buffy (first line, one command per comment) so the interaction feels familiar
   to anyone who has used JOSS or rOpenSci.
2. **Ship v1 on GitHub Actions** against `codecheckers/testing-dev-register`.
   Free forever, no platform decision needed, and it gets the four commands in
   front of real codecheckers fastest.
3. **Keep the logic transport-agnostic** — a `handle_comment(payload) ->
   actions` core called by either an Actions entrypoint or a small ASGI app — so
   moving to runway.horse (€0 at 128 MB, no sleeps) is a deployment change, not
   a rewrite. Do not build on Render free: the spin-down versus 10 s webhook
   timeout is a permanent, unfixable defect for this workload.
4. **Read NeuroLibre's Zenodo responders** before writing ours; that is the part
   of buffy's ecosystem with the most transferable value for CODECHECK.
5. **Revisit buffy** if we find ourselves wanting reminders, checklists,
   invitations and templating — at that point paying €7–15/month for a proven
   codebase beats reimplementing it, and the fork route is well-trodden.

The honest summary: buffy is the safer engineering choice and the worse fit for
the stated budget; a Python implementation on Actions is the better fit for the
budget and asks us to own ~500 lines we would otherwise inherit.

## Decision (2026-09-04)

**Own implementation in Go.** Reasons given: the maintainer wants to learn Go,
and a single static binary keeps every deployment option open — including hosts
with far more capability than a free tier, without a rewrite.

Consequences to keep in view:
- Go's footprint (~10-20 MB RSS, millisecond start) fits every candidate host,
  so the platform choice stops being urgent. runway.horse free (128 MB, no
  sleeps) works; Render free still does not, because of spin-down vs. GitHub's
  10 s webhook timeout.
- The contributor-pool argument from the evaluation above stands unresolved:
  Go is the least common language among codecheckers. Mitigate with small,
  well-named packages, thorough docs and a low-ceremony contribution path.
- Keep the core transport-agnostic — `handleComment(payload) -> []Action` — so
  the same code serves a webhook server, a GitHub Actions entrypoint, or a CLI.
- Likely dependencies: `google/go-github`, `gopkg.in/yaml.v3`, stdlib
  `net/http` + `crypto/hmac` for webhook verification. No framework needed.
