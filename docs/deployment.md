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
runway app deploy source -y
```

There is no Dockerfile: the Go buildpack detects the module and builds what
`BP_GO_TARGETS` names.

**The `Procfile` is what starts the bot.** Without one, runway runs the built
binary as a single `web` process *with no command line arguments*, and
`chekhov` with no arguments is a tool that prints its usage and exits - which
the platform reads as a crash loop. The file says:

```
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

### The build stamp

`@chekhovbot version` and `/healthz` report the commit they are running, and on
this platform it has to be handed to them:

```sh
runway app config set -a chekhov CHEKHOV_COMMIT=$(git rev-parse HEAD)
runway app deploy source -y
```

Two ordinary routes are closed here. `-ldflags` never reaches the compiler,
because runway pins the buildpack's build flags - the build log says `go build
-buildmode pie -trimpath` whatever `BP_GO_BUILD_FLAGS` or
`BP_GO_BUILD_LDFLAGS` are set to. And Go's own `vcs.revision` stamp is absent,
because the builder exports the source without `.git`. The configuration is
the one channel that arrives, so set `CHEKHOV_COMMIT` in the same breath as the
deploy; `CHEKHOV_VERSION` overrides the version string when there is a release
worth naming.

Locally none of this is needed: a binary built from a checkout carries its own
revision, and says `978a0bc0+dirty` when the tree had uncommitted changes.

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

```
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
runway app config set -a chekhov BP_GO_BUILD_FLAGS="-buildmode=pie -trimpath -ldflags=\"-X main.version=... -X main.commit=...\""
runway app deploy source -y
```

A redeploy replaces the process. Commands in flight are given twenty seconds to
finish.

## When it falls over

1. `curl https://<app>.runway.horse/healthz` — is it up, and is it the build you
   think?
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
