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
runway app create                                     # name it chekhov
runway app config set BP_GO_TARGETS=./cmd/chekhov
runway app config set BP_GO_BUILD_LDFLAGS="-X main.version=$(git describe --tags --always) -X main.commit=$(git rev-parse HEAD)"
runway app config set CHEKHOV_ENV=development
runway app config set CHEKHOV_TARGET_REPO=codecheckers/testing-dev-register
runway app config set CHEKHOV_GH_ACCESS_TOKEN=...     # see github-token.md
runway app config set CHEKHOV_GH_SECRET_TOKEN=...     # openssl rand -hex 32
runway app deploy
runway app open
```

There is no Dockerfile: the Go buildpack detects the module and builds the
target `BP_GO_TARGETS` names. The `-ldflags` are what make `@chekhovbot version`
and `/healthz` honest about which commit is answering; if the buildpack ever
refuses them, the fallback is a two-stage Dockerfile, which the platform also
accepts.

The binary listens on `$PORT`, which the platform sets. `chekhov serve --addr`
overrides it for local runs.

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

## Checking that it works

```sh
curl https://<app>.runway.horse/healthz
```

It answers with the version, the commit, the bot account, the register it works
on, the environment, whether external services are on, which register commit the
rules came from, and — once it has posted anything — when the token expires. A
development instance is meant to be impossible to mistake for a production one
at a glance.

Then, on an issue of the testing register:

```
@chekhovbot hello
@chekhovbot commands
@chekhovbot frobnicate
```

## Watching it

```sh
runway app logs          # keeps tailing through a deploy
```

Every answered command logs the command, the issue, the author, the identifier
of the comment it posted and how long it took. A refused delivery logs why: a
bad signature, the wrong repository, the bot's own comment. A failed post logs
the command, the issue and what GitHub said.

## Redeploying

```sh
git push                 # the repository is the source; deploy from a checkout
runway app deploy
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
4. A `401 Bad credentials` in the log means the token expired or was revoked;
   see [`github-token.md`](github-token.md) for rotation.
5. Redeliver the event from GitHub once the cause is fixed. The bot is not
   idempotent — a redelivery posts another comment — which is fine for a
   listing and worth thinking about for anything that changes the register.

Access: the runway account is Daniel's; the bot account is `chekhovbot`, whose
credentials live with the CODECHECK organisation.
