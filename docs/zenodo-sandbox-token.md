# The Zenodo sandbox token

`check zenodo` (#26) reads an unpublished draft through the share link
somebody gives it. The bot holds **no** Zenodo credential: a deployment never
has this token, and nothing in the bot writes to Zenodo.

The token is for development. It lets the integration suite do once what the
offline fixtures can only imitate: make a draft on the
[Zenodo sandbox](https://sandbox.zenodo.org), share it, audit it the way the
bot would, and delete it (`internal/check/sandbox_test.go`). The draft chooses
the sandbox's own [CODECHECK community](https://sandbox.zenodo.org/communities/codecheck)
without being submitted to it, so nobody is asked to review a test.

## Creating it

1. Log in to <https://sandbox.zenodo.org>. It is a separate site from
   zenodo.org, with its own accounts.
2. Go to *Applications* → *Personal access tokens* → *New token*
   (<https://sandbox.zenodo.org/account/settings/applications/tokens/new/>).
3. Scopes: `deposit:write` and `deposit:actions`. Nothing else.
4. Put it in `.env` as `CHEKHOV_ZENODO_SANDBOX_TOKEN`. `.env` is gitignored.

A sandbox token cannot reach zenodo.org, which is the reason to use one: a
mistake in the test makes a draft nobody will ever see.

## Using it

```sh
set -a; . ./.env; set +a
CHEKHOV_INTEGRATION=1 go test ./internal/check/ -run Sandbox -v
```

For a live test of the deployment, keep the draft and post its share link
to the test issue:

```sh
CHEKHOV_INTEGRATION=1 CHEKHOV_ZENODO_KEEP_DRAFT=1 go test ./internal/check/ -run Sandbox -v
```

The test logs the link. Delete the draft on the sandbox afterwards, and the
comment holding the link, as the bot's reply asks.

## Rotating it

The sandbox lets a token go stale, and it answers `403 Permission denied` on
every request then: the test fails at the first request, saying so. Make a
new one as above and replace the value in `.env`. Nothing else has a copy.
