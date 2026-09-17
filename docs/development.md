# Development setup

**Rule: development and testing run against the testing register, never the
production register.**

| | Repository | Notes |
|---|---|---|
| Development / testing | [`codecheckers/testing-dev-register`](https://github.com/codecheckers/testing-dev-register) | Fake data, certificates `1970-0XX`, safe to break |
| Production | [`codecheckers/register`](https://github.com/codecheckers/register) | Do not point a dev deployment at this |

## The testing register

Public repo, mirrors the production layout:

- `register.csv` with the real column set —
  `Certificate,Repository,Type,Venue,Issue` — filled with obviously fake rows
  (`1970-001`, `github::testuser/FAKE-ml-classification-demo`, venues prefixed
  `FAKE`).
- `docs/<certificate>/index.html` per entry, published to GitHub Pages by
  `.github/workflows/static.yml` on push to `main`.
- `.github/ISSUE_TEMPLATE/buddy-exchange-request.md`.

Because certificates are dated 1970 and every venue is prefixed `FAKE`, test
output is impossible to confuse with a real CODECHECK.

## Configuring the bot against it

1. Copy `.env.example` to `.env` and fill in the token and webhook secret.
   `CHEKHOV_TARGET_REPO` defaults to `codecheckers/testing-dev-register`.
2. Check `config/settings-development.yml` — `target_repository` is the single
   switch between testing and production. Nothing else should name a repo.
3. Bot account: `chekhovbot` is a member of the `codecheckers` organisation and
   needs a **fine-grained** token scoped to the testing register alone —
   resource owner `codecheckers`, one repository, Issues read and write. See
   [`github-token.md`](github-token.md) for how to create it, what the token
   currently in use actually has, and how to rotate it. A classic token with
   `public_repo` would reach the production register too, which is the reason
   not to use one.
4. Optionally, `CHEKHOV_MASTODON_TOKEN` for `announce`: an application on the
   `codecheck` account with five scopes, see
   [`mastodon-token.md`](mastodon-token.md). Development toots are `direct`.

## Webhook on the testing register

Settings → Webhooks → Add webhook on `codecheckers/testing-dev-register`:

- Payload URL: `<deployment-url>/dispatch`
- Content type: `application/json`
- Secret: the value of `CHEKHOV_GH_SECRET_TOKEN` (`openssl rand -hex 32`)
- Events: **Issues** and **Issue comments** only

For local development the deployment URL has to be a public tunnel — smee.io or
`gh webhook forward` — since GitHub has to reach the listener.

## Command convention

Following the Open Journals bots: the command is on the **first line** of the
comment, and only the first command in a comment is interpreted. Free text may
follow on later lines.

## Engine

A fresh implementation in **Go**, decided in
[register#209](https://github.com/codecheckers/register/issues/209): a learning
goal, plus the freedom to deploy anywhere later, and a binary small enough for
every candidate free tier. The configuration in `config/` still mirrors buffy's
schema, so the responder names and the target repository stay recognisable to
anyone who knows the Open Journals bots.

Layout, conventions and the rule identifiers shared with the `codecheck` R
package are in [`CLAUDE.md`](../CLAUDE.md).

The deployment is on runway.horse, against the testing register; see
[`deployment.md`](deployment.md) for how it is built, configured and watched,
and [`github-token.md`](github-token.md) for the credential it runs with.

## Running the bot locally

```sh
export CHEKHOV_GH_ACCESS_TOKEN=... CHEKHOV_GH_SECRET_TOKEN=...
go run ./cmd/chekhov serve --addr :8099
curl localhost:8099/healthz
```

To receive real deliveries, forward them to that port with a public tunnel:

```sh
gh webhook forward --repo=codecheckers/testing-dev-register \
  --events=issues,issue_comment --url=http://localhost:8099/dispatch
```

Without a tunnel, `chekhov comment` renders the same answer the bot would post,
through the same code path:

```sh
echo '@chekhovbot commands' | go run ./cmd/chekhov comment -
```
