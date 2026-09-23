# Deployment

The bot runs on [runway.horse](https://runway.horse), against the **testing
register**. Everything below assumes that; pointing a deployment at
`codecheckers/register` is a separate, deliberate act, and begins with writing
`config/settings-production.yml`, which does not exist.

## Why runway.horse

The constraint from [register#209](https://github.com/codecheckers/register/issues/209)
is a free tier that does not sleep: a codechecker who waits thirty seconds for a
cold start assumes the bot is broken. Runway's free plan is 0.5 CPU, 128 MB RAM,
2 GB storage, up to six apps, and says plainly that it imposes no sleeps. A Go
binary of some 15 MB with no runtime to boot fits that with room to spare.

The alternatives and why not: render.com's free web services sleep; fly.io needs
a card on file; a GitHub Actions cron cannot answer a webhook within the ten
seconds GitHub allows; a container on CODECHECK infrastructure means owning
uptime and TLS for a bot that is still an experiment.

## Once

```sh
bash <(curl -s https://www.runway.horse/install.sh)   # installs ~/.local/bin/runway
runway login
runway local key setup                                # the deploy key for this machine
```

## Creating the app

From a checkout of this repository:

```sh
runway app create chekhov -y
runway app config set -a chekhov BP_GO_TARGETS=./cmd/chekhov
runway app config set -a chekhov CHEKHOV_ENV=development
runway app config set -a chekhov CHEKHOV_TARGET_REPO=codecheckers/testing-dev-register
runway app config set -a chekhov CHEKHOV_BOT_GH_USER=chekhovbot
runway app config set -a chekhov CHEKHOV_GH_ACCESS_TOKEN=...   # see github-token.md
runway app config set -a chekhov CHEKHOV_GH_SECRET_TOKEN=...   # openssl rand -hex 32
runway app config set -a chekhov CHEKHOV_MASTODON_TOKEN=...    # optional, see "Announcing" below
runway app config set -a chekhov GOMEMLIMIT=96MiB             # the free plan has 128 MB, see "Memory" below
runway app config set -a chekhov CHEKHOV_RECORD_KEY=...       # `chekhov record-key`, see record-key.md
runway app config set -a chekhov CHEKHOV_ADMIN_ISSUE=...      # optional, see "Watching it" below
# CHEKHOV_MASTODON_ACCOUNT / CHEKHOV_MASTODON_INSTANCE only to post elsewhere than @codecheck@fediscience.org
runway app deploy source -y
```

There is no Dockerfile: the Go buildpack detects the module and builds what
`BP_GO_TARGETS` names.

**The `Procfile` is what starts the bot.** Without one, runway runs the built
binary as a single `web` process *with no command line arguments*, and
`chekhov` with no arguments is a tool that prints its usage and exits - which
the platform reads as a crash loop. The file says:

```txt
web: chekhov serve
```

`web`, `init` and `worker` are the process types runway knows; as soon as a
Procfile exists it is the complete list, nothing is added implicitly.

The tool is in the container either way, so a live deployment can be asked
questions directly:

```sh
runway app exec -a chekhov -- chekhov version
runway app exec -a chekhov -- chekhov check github::codecheckers/Piccolo-2020
```

The binary listens on `$PORT`, which the platform sets. `chekhov serve --addr`
overrides it for local runs.

**Changing configuration does not restart the app.** `runway app config set`
stores the value; the running process keeps what it started with. Follow it
with `runway app restart -a chekhov` whenever the new value has to take effect
now - a rotated token above all, where the symptom is 401 on everything and no
reply for anyone to notice. A deploy restarts on its own.

### The version

`@chekhovbot version` and `/healthz` report one version, the
`internal/build.Version` constant. Nothing about the deployment decides it: the
string a local build reports is the string the deployment reports, because
there is only one place it can come from. Bumping it is a commit in this
repository, described in `CLAUDE.md` -> The version, and a test holds it to the
newest heading in `CHANGELOG.md`.

That is a deliberate retreat from stamping the commit, and a temporary one. A
`CHEKHOV_COMMIT` configuration variable was the channel that worked: read at
runtime, written by hand, and on 2026-09-21 it named a commit four behind the
running code for a morning without anything noticing. **Nothing reads it now**
- if a deployment still carries `CHEKHOV_COMMIT` or `CHEKHOV_VERSION`, they do
nothing and are worth removing so that the next person does not set them and
wonder why the version did not move:

```sh
runway app config unset -a chekhov CHEKHOV_COMMIT CHEKHOV_VERSION
```

A development deployment also reports the commit the build was made from -
**when the toolchain stamped one**. That is the only source read, because it is
the only one nothing can hand over wrongly: `go build` reads the commit out of
the checkout itself. `REVISION`, which `paketo-buildpacks/git` sets into the
image when it finds a `.git`, looked like the answer and is not: it is an
environment variable, so a deployment's own configuration can set a variable of
that name, and nothing distinguishes the two. Reading it would report a hand-set
value as though the build had proved it - the 2026-09-21 failure with a better
disguise.

**This builder has no `.git`, and the deployment therefore reports no commit.**
Settled on 2026-09-21 by reading the build log of a real deploy, which this
file had twice guessed at instead:

```
[builder] Paketo Buildpack for Go Distribution 2.10.27
[builder] Paketo Buildpack for Go Mod Vendor 1.1.38
[builder] Paketo Buildpack for Go Build 2.4.38
[builder]     Running 'go build -o … -buildmode pie -trimpath ./cmd/chekhov'
[builder] Paketo Buildpack for Procfile 5.13.7
```

Two things there are conclusive.
[`paketo-buildpacks/git`](https://github.com/paketo-buildpacks/git) **is not in
the list**, and it detects on finding a `.git` directory - so there is none,
which also closes the `REVISION` route for good. And the `go build` line
carries no `-buildvcs=false`, so Go's automatic stamping would have written the
revision had there been a repository to read. `/healthz` on that release
reports `version 0.1.1` with no `commit` field, which is the design working: no
commit rather than a guess.

The platform does know the commit - it tags the image `chekhov:git-369b08c6` -
it simply does not hand it to the builder.

What remains is the one channel the buildpack reads:

```sh
runway app config set -a chekhov "BP_GO_BUILD_LDFLAGS=-X main.commit=$(git rev-parse HEAD)"
```

[`paketo-buildpacks/go-build`](https://github.com/paketo-buildpacks/go-build)
adds `-ldflags` to the compiler's command line only when that variable is set,
which is why the line above shows none. The catch is the one `CHEKHOV_COMMIT`
failed on: somebody has to set it beside each deploy. It is better than
`CHEKHOV_COMMIT` was - injected at build time, so the value cannot outlive the
binary it describes - and it is still a step that can be skipped, which is why
it is not done here yet. See
[chekhov#4](https://github.com/codecheckers/chekhov/issues/4).

### SSH keys, and the snap

`runway app deploy source` is a `git push` to the remote the CLI adds, so the
platform builds a **commit**: anything uncommitted is not deployed.

The CLI is a snap and cannot read hidden directories, which makes two things
fail confusingly. `runway local key setup` and `runway key add ~/.ssh/...` say
"couldn't discover a key to use" - pass it a copy of the public key from a
non-hidden path. And the ssh-agent socket under `/run/user/.../keyring/ssh` is
unreachable, so authentication fails before the push; the way round is a
passphrase-less key the CLI is pointed at directly (runway supports no other
kind):

```sh
ssh-keygen -t ed25519 -N '' -C 'chekhov deploy key' -f ~/runway-keys/chekhov_deploy
cp ~/runway-keys/chekhov_deploy.pub ./deploy-key.pub
runway key add ./deploy-key.pub chekhov-deploy -y && rm deploy-key.pub
runway local key set ~/runway-keys/chekhov_deploy
```

The snap has a private `/tmp`, so a checkout under `/tmp` is not a git
repository as far as the CLI is concerned - it answers "Deployments are only
possible from a git repo". Deploy from somewhere in the home directory.

### `object not found`, with nothing deployed

`runway app deploy source` reads the repository with go-git rather than with
`git`, and go-git only looks for packs named `pack-*.pack`. The `loose-objects`
task of `git maintenance` packs loose objects into **`loose-*.pack`** files
instead, and an object that is only there is, to go-git, not there at all
([go-git#2345](https://github.com/go-git/go-git/issues/2345), open; the fixes,
[#2390](https://github.com/go-git/go-git/pull/2390) and
[#2346](https://github.com/go-git/go-git/pull/2346), are not released). From
then on every deploy fails with `object not found` before anything is pushed,
while the platform happily goes on serving the build it received last.
`-l debug` shows it resolving `refs/heads/main` and then giving up.

Check with `ls .git/objects/pack/`: a `loose-*.pack` is the symptom. A
`multi-pack-index` usually sits beside it, written by the same maintenance run,
and this file used to name that as the cause; go-git reads each pack's own
`.idx` and never the multi-pack-index, so it is a bystander.

```sh
git multi-pack-index expire --object-dir=.git/objects   # drop the index
git repack -ad                                          # one pack-*.pack again
git commit-graph write --reachable                      # forget what the repack dropped
```

The repack drops commits nothing reaches any more - a deleted branch, say - and
the commit-graph still lists them, which `git fsck` reports as a commit it
cannot read; rewriting the graph from what is reachable clears that.

**GitKraken writes them back within the hour**, whatever git is configured to
do. Opening a repository in a tab starts a background `git maintenance run
--task=commit-graph --task=loose-objects --task=geometric-repack` (or
`incremental-repack`) with GitKraken's own bundled git, fifteen seconds later
and at most once an hour per repository. No preference turns it off - that is
read from GitKraken's application bundle, as its documentation mentions only
the manual "Perform Repo Maintenance". Because the tasks are named explicitly,
git ignores `maintenance.loose-objects.enabled`; `core.multiPackIndex false`
stops only the index, which does not matter here; and `git maintenance
unregister` does nothing, since the repository is not registered - it exits 5.
So while the checkout you work in is open in GitKraken, a repack buys a deploy
or two, not a fix.

### Deploying from a clone of its own

The way out that does not depend on GitKraken is a clone that is used for
deploying and **never opened in GitKraken**. It needs `--no-local`, or it
hardlinks the same packs, and the `runway` remote, which is how the CLI knows
the app. Once:

```sh
git clone --no-local --branch main ~/git/codecheck/chekhov ~/git/codecheck/chekhov-deploy
git -C ~/git/codecheck/chekhov-deploy remote add runway ssh://git@deploy.runway.horse:2222/chekhov.git
```

Every deploy, after committing in the working checkout:

```sh
cd ~/git/codecheck/chekhov-deploy
git pull --ff-only                  # from the working checkout, commits only
runway app deploy source -y
```

It pulls from the local checkout, so what it deploys is what is committed
there, pushed or not - the same as deploying from the working checkout, which
the platform builds from a commit rather than a working tree either way.

Keep it under your home directory, not `/tmp`: the CLI is a snap, a snap has a
`/tmp` of its own, and a clone there answers "Deployments are only possible
from a git repo".

## The webhook

On `codecheckers/testing-dev-register`: Settings → Webhooks → Add webhook.

| Field | Value |
|---|---|
| Payload URL | `https://<app>.runway.horse/dispatch` |
| Content type | `application/json` |
| Secret | the same value as `CHEKHOV_GH_SECRET_TOKEN` |
| Events | **Issues** and **Issue comments** only |

GitHub's *Recent Deliveries* tab on that page is the first place to look when
something is wrong: it shows the request, the response, and has a **Redeliver**
button, which is how a failed delivery is replayed without writing another
comment.

## Announcing

`@chekhovbot announce` posts as `CHEKHOV_MASTODON_ACCOUNT` on
`CHEKHOV_MASTODON_INSTANCE` (by default `codecheck` on fediscience.org), and
needs `CHEKHOV_MASTODON_TOKEN` of that account. Without it announcing is switched off: an editor
still gets the preview, and `confirm` says it will not post. `/healthz` in
development says whether it is configured, for which account and with which
visibility. [`mastodon-token.md`](mastodon-token.md) has the application's
scopes, where the token is kept, and its rotation.

The settings file decides the visibility. `settings-development.yml` says
`direct`, a test asserts it, and with `direct` every mention in the toot is
written without its `@`, so a development toot notifies nobody. A production
settings file has to choose `public` or `unlisted` on purpose.

Where the toot comes from is in the `env:` block: `certificates`, the URL of a
published certificate, and `codechecker_lists`. `persons.csv` and `venues.csv`
are read from the target repository. In development all of it is the testing
register's fake certificate `1970-001`.

`@chekhovbot follow` uses the same account, instance and token, plus
`mastodon.follow` in the settings file - `false` in development, a test
asserts it - because following an account and requesting it for a public
collection are not softened by `direct` visibility the way a toot's mentions
are. `/healthz`
in development reports `announce.follow`, true only when both a token is
configured and the settings file turns it on. A production settings file
turns it on deliberately, once the token has the extra scopes
[`mastodon-token.md`](mastodon-token.md) lists for it.

## Memory

The free plan kills the process above 128 MB, and a restart loses the command
in flight without a word on the issue. Building the certificate animation is
the heavy part: each page is scaled with a float buffer of about 45 MB, one
page at a time. Without a limit, Go lets garbage pile up past 150 MB before it
collects; `GOMEMLIMIT=96MiB` makes it collect in time, and a local preview of
`1970-001` then peaks at about 73 MB.

A real certificate's pages are no larger than the fake one: the register
renders every page at a fixed `CONFIG$CERT_DPI <- 72`, whatever the source
`cert.pdf`'s own size or colour depth - so a big PDF is not a proxy for a big
raster page. Previewing `2026-019`, the largest `cert.pdf` in the register
(15.6 MB over 15 pages), on the deployment itself peaked at 103 MB: safe, 25 MB
under the kill limit, but well above the 73 MB the local figure predicts. The
gap is the container's own overhead, not the certificate's - a local preview
before trusting a change here should read the deployment's own
`runway app stats`, not just its own process. See
[chekhov#32](https://github.com/codecheckers/chekhov/issues/32).

## The deployment as it stands

| | |
|---|---|
| App | `chekhov` on runway.horse, owner `chekhovbot`, plan `free-launch`, region `ber1` |
| URL | <https://chekhov.pqapp.dev> |
| Webhook | `codecheckers/testing-dev-register`, hook `680342187`, `issues` and `issue_comment` |
| Token | fine-grained, `chekhovbot`, one repository, expires 2027-09-17 |

A config change does **not** rebuild: the image is built from a pushed commit,
so a new `BP_GO_BUILD_FLAGS` only takes effect with the next deploy of new
source.

## Checking that it works

```sh
curl https://<app>.runway.horse/healthz
```

In development it answers with the version, the commit, the bot account, the
register it works on, the environment, whether external services are on, which
register commit the rules came from, and — once it has posted anything — when
the token expires. A development instance is meant to be impossible to mistake
for a production one at a glance.

`/healthz` is unauthenticated, so a production deployment (`CHEKHOV_ENV`)
reports only status, version, bot, register and environment, and its comments
carry no footer naming the build.

```sh
curl https://<app>.runway.horse/nudges
```

`/nudges` is the other read-only endpoint: when the process started, what the
nightly sweep of the register found (issues walked, replies posted, follow-ups
left waiting, how many problems), and when the next sweep is due. It is
unauthenticated too, so a problem is a *number* everywhere but development —
the words name the person the follow-up is about.

The sweep is a goroutine in this process, not a scheduled job: the reply path
and the token are already here, and a second copy of either is a second thing
to rotate. The history is in memory and a deploy loses it, which is the same
bargain as the rest of the bot having no database. The weekly **Nightly
sweep** workflow reads this endpoint without any credential and fails when no
sweep has run in 36 hours *and* the process is older than that — a timer
cannot report its own death, so something outside the process has to look.

Then, on an issue of the testing register:

```txt
@chekhovbot hello
@chekhovbot commands
@chekhovbot frobnicate
```

## Watching it

```sh
runway app logs -a chekhov                    # keeps tailing through a deploy
runway app logs -a chekhov --no-build --limit 50   # runtime only
```

Every answered command logs the command, the issue, the author, the identifier
of the comment it posted and how long it took. A refused delivery logs why: a
bad signature, the wrong repository, the bot's own comment. A failed post logs
the command, the issue and what GitHub said.

Nobody watches the logs, so a command that **panics** is also reported on an
issue: `CHEKHOV_ADMIN_ISSUE` names one in the target repository, and the bot
posts the command, the issue it was asked on, who asked and the stack trace
there. Open an issue for it once, subscribe to it, and set its number; nobody
is mentioned in the report, so subscribing is how you hear of one. The person
who asked still gets an apology on their own issue; on the admin issue itself
the report is the answer. Unset or `0`, the trace is
in the log only. In the target repository because that is the one repository
the reply path writes to, so in production the trace is as public as the
register. It covers a panic and nothing else: a kill from the memory limit
above ends the process before any of this runs. See
[chekhov#38](https://github.com/codecheckers/chekhov/issues/38).

The trace itself is **development's alone**: in production the target
repository is the public register, and a trace names the build's paths, its
packages and its line numbers, so the report there carries the panic value -
which is what a command was given, and already on the issue - and says the
trace is in the log. Every other fact about the machine is reported the same
way; see `/healthz` above.

The nightly sweep, the team load at startup and the goroutines a command asks
Mastodon on are reported there too. A panic in
any of them would otherwise end the process: they are goroutines of their own,
which the guard around a command cannot reach. The first two run for the timer
rather than for somebody, so the admin issue is the only place anyone hears of
them, and a failed sweep is counted by `/nudges` like any other - a sweep that
keeps panicking shows up as well as one that has stopped. The third is a
command's own, so the person who asked is told that step failed. See
[chekhov#50](https://github.com/codecheckers/chekhov/issues/50).

## Redeploying

```sh
git commit ...           # the platform builds a commit, not a working tree
runway app deploy source -y
```

From a checkout that is open in GitKraken, deploy from the clone of its own
instead - see "`object not found`" above. A redeploy replaces the process. Commands in flight are given twenty seconds to
finish.

## When it falls over

1. `curl https://<app>.runway.horse/healthz` — is it up, and which version?
   The version moves only when somebody bumps the constant, so it says which
   release is running rather than which commit. A development deployment also
   reports `commit` when the build carried a stamp, which is the field that
   answers "is this the code I pushed"; production reports neither, so there
   the answer comes from `runway app logs` and the build log.
2. GitHub's *Recent Deliveries* — did the event arrive, and what was answered?
   A 401 there means the secret on the platform and the secret on the webhook
   have drifted apart.
3. `runway app logs` — what did it do with the delivery?
4. A `401 Bad credentials` in the log means the token expired or was revoked,
   and a `403 Resource not accessible by personal access token` means it lacks
   a permission - Issues: read and write is what posting a comment needs. See
   [`github-token.md`](github-token.md).
5. Redeliver the event from GitHub once the cause is fixed. The bot is not
   idempotent — a redelivery posts another comment — which is fine for a
   listing and worth thinking about for anything that changes the register.

Access: the runway account is Daniel's; the bot account is `chekhovbot`, whose
credentials live with the CODECHECK organisation.
