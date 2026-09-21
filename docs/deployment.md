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

So if this builder has no `.git`, the deployment reports no commit, and the fix
is at build time rather than at runtime: `BP_GO_BUILD_LDFLAGS`, which the Go
buildpack does read.

**Whether this platform can stamp the commit properly is an open question, not
a settled no.** This file used to say `-ldflags` and the VCS stamp were both
impossible here. Neither claim was tested:
[`paketo-buildpacks/go-build`](https://github.com/paketo-buildpacks/go-build)
does read `BP_GO_BUILD_LDFLAGS`, and it never passes `-buildvcs=false`, so
Go would stamp the revision if `.git` reached the build. Whether it does is
undocumented - runway says only that it "builds from git, not from your working
tree".

Runway's Go buildpack order includes
[`paketo-buildpacks/git`](https://github.com/paketo-buildpacks/git), which only
runs when it finds a `.git` directory - so whether it ran is itself the answer
to whether `.git` is there. One command:

```sh
runway app exec -a chekhov      # then, in the shell: printenv REVISION
```

A SHA means `.git` reaches the build, and the bot's own VCS stamp should then be
there too - check with `chekhov version` in the same shell, which prints the
commit when there is one. Empty means it does not, and the question becomes one
for runway: their own Go stack ships a buildpack that is dead code without it.
Either way the bot does not read `REVISION` itself, for the reason above. See
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
`git`, and go-git does not understand a **multi-pack-index**. Background
maintenance writes one - `git maintenance`, or a client like GitKraken, which
leaves a `.git/gk` directory and `loose-*.pack` files beside it - and from then
on every deploy fails with `object not found` before anything is pushed, while
the platform happily goes on serving the build it received last. `-l debug`
shows it resolving `refs/heads/main` and then giving up.

Check with `ls .git/objects/pack/`: a `multi-pack-index` file is the symptom.

```sh
git multi-pack-index expire --object-dir=.git/objects   # drop the index
git repack -ad                                          # one pack again
git maintenance unregister                              # and stop it coming back
```

Deploying from a fresh clone works too, and is the quicker way out when a
deployment is waiting, but it is a second checkout to keep in step - prefer
repacking the one you work in.

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

## Redeploying

```sh
git commit ...           # the platform builds a commit, not a working tree
runway app deploy source -y
```

A redeploy replaces the process. Commands in flight are given twenty seconds to
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
