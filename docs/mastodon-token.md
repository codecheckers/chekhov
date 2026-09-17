# The Mastodon token

`@chekhovbot announce` posts as the `codecheck` account on
[fediscience.org](https://fediscience.org/@codecheck), with an access token in
`CHEKHOV_MASTODON_TOKEN`. Both are settings: `CHEKHOV_MASTODON_ACCOUNT` (the
username, without `@` or instance) and `CHEKHOV_MASTODON_INSTANCE`, with those
defaults in `config/settings-development.yml`. A test deployment can post to an
account of its own this way.

The token has to belong to the configured account. The bot reads the account's
recent toots before every post, and refuses with "the token belongs to @…" when
the token is another account's, so a token pasted into the wrong deployment
cannot post in someone else's name. Without it announcing is switched off: an editor still
gets the preview, and `confirm` says it will not post.

## Creating it

Logged in as the account the bot posts as (`codecheck` on fediscience.org
unless configured otherwise): **Preferences → Development → New application**.

| Field | Value |
|---|---|
| Application name | `chekhov` (shown under every toot the bot posts) |
| Application website | `https://github.com/codecheckers/chekhov` |
| Redirect URI | leave the default, `urn:ietf:wg:oauth:2.0:oob` |
| Scopes | exactly the ones below; untick everything else, including the top-level `read`, `write` and `follow` |

| Scope | What the bot does with it | Endpoint |
|---|---|---|
| `read:accounts` | finds the account the token belongs to | `GET /api/v1/accounts/verify_credentials` |
| `read:statuses` | reads the last ten toots and, with `direct` visibility, the conversations, to refuse a second announcement | `GET /api/v1/accounts/:id/statuses`, `GET /api/v1/conversations` |
| `write:media` | uploads the certificate animation | `POST /api/v2/media`, `GET /api/v1/media/:id` |
| `write:statuses` | posts the toot | `POST /api/v1/statuses` |
| `write:follows` | not used yet: following the accounts a certificate mentions, #31 | `POST /api/v1/accounts/:id/follow` |

The token in use has all five. #31 will also need `read:search` and
`read:follows`, to find a mentioned account on the instance and to see whether
`@codecheck` follows it already; add them when that lands.

The instance limits (`GET /api/v2/instance`) need no scope.

Submit, open the application, and copy **Your access token**. The client key
and client secret on the same page are not needed.

A token with the top-level `write` scope could also delete toots, block
accounts and change the profile. Nothing the bot does needs that, so do not
grant it.

## Where it is kept

The same places as the GitHub token, and nowhere else:

| Where | How |
|---|---|
| Locally | `CHEKHOV_MASTODON_TOKEN=` in `.env`, which is gitignored; `.env.example` has the name and no value |
| Deployed | the platform's configuration: `runway app config set -a chekhov CHEKHOV_MASTODON_TOKEN=...`, which redeploys; `CHEKHOV_MASTODON_ACCOUNT` and `CHEKHOV_MASTODON_INSTANCE` only when they differ from the defaults |
| On fediscience.org | the application page itself, where it can be read again by whoever logs in as `codecheck` |

It is never written into `config/`, a commit, an issue or a log line. The client
sends it only in the `Authorization` header, and its errors do not contain it.

To confirm the deployment has it, `GET /healthz` in development reports
`announce.configured: true` with the account and visibility. It does not show
the token.

## Rotation

Mastodon application tokens do not expire. Rotate when it may have leaked,
and when someone who could read it leaves the project:

1. On the application's page, **Regenerate access token**. The old token stops
   working immediately, so announcing is off until step 2.
2. `runway app config set -a chekhov CHEKHOV_MASTODON_TOKEN=...`, and update
   `.env`.
3. `@chekhovbot announce 1970-001` on a test issue: a preview that does not say
   "switched off" and does not fail to read the recent toots means the token
   works.

To switch announcing off altogether, delete the application on fediscience.org
(which revokes the token), or unset the variable.

## Visibility is not the token's business

The token can post publicly; the settings file decides whether the bot does.
`config/settings-development.yml` says `visibility: direct`, a test asserts it,
and `internal/mastodon` refuses any other visibility before making a request.
With `direct`, every mention is written without its `@`, so a development toot
notifies nobody. See [`deployment.md`](deployment.md#announcing).
