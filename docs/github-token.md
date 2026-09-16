# The bot's GitHub token

`@chekhovbot` needs one credential to speak: a **fine-grained personal access
token** belonging to the `chekhovbot` account and scoped to the testing
register. The webhook secret (`CHEKHOV_GH_SECRET_TOKEN`) is a different thing
and is covered at the end.

## Why fine-grained rather than classic

A classic token's smallest useful scope is `public_repo`, which grants write to
**every** public repository the account can write to — `codecheckers/register`
included. A fine-grained token names one repository, so pointing a deployment at
the production register fails at the token rather than at the configuration.
That turns "development and testing run against the testing register, never the
production one" from a convention into something enforced.

The cost is that a fine-grained token expires — one year at the most, there is
no "never" — and that an organisation-owned one may need an owner's approval.

## Creating it

Signed in as `chekhovbot`: Settings → Developer settings → Personal access
tokens → Fine-grained tokens → Generate new token.

| Field | Value |
|---|---|
| Token name | `chekhov-dev` (say what it is for; the name is all future-you sees) |
| Resource owner | **`codecheckers`** — *not* `chekhovbot`. A user-owned token reaches only that user's own repositories, and the registers belong to the organisation |
| Expiration | 90 days for development. The maximum is 366 days |
| Repository access | Only select repositories → **`codecheckers/testing-dev-register`** |

Permissions, under Repository permissions:

| Permission | Level | Needed for |
|---|---|---|
| Metadata | Read | Mandatory, selected automatically |
| Issues | **Read and write** | Posting the reply; later labels and assignees |
| Contents | Read | Reading `register.csv` and `venues.csv` through the API |
| Pull requests | Read and write | Only for `@chekhovbot register`, which opens the PR against `register.csv` |

Organisation permissions: **none**. Reading team membership would need
organisation-wide *Members: read*, which is not repository-scoped; the editors
are listed by handle in `config/settings-development.yml` instead.

If the resource owner is the organisation, the request may land in
Organization settings → Third-party Access → Personal access tokens for an
owner to approve. Check there when a freshly created token answers 404 to
everything: a token the organisation has not approved is issued and sees
nothing.

## What the token in use actually has

The token currently in `.env` was created with **Issues, Contents and Pull
requests all at read and write**. That is wider than today's commands need:
`commands`, `hello` and the unknown-command reply post an issue comment and
nothing else, and the `check` command only reads. The extra rights are for the
commands that come later — labels and assignees (Issues: write, in use already
by comments), the register pull request (Contents and Pull requests: write).

It is not worth regenerating a narrower token now and a wider one in a month.
What matters is that it is still one repository: the production register is out
of reach either way.

## Account prerequisites

- `chekhovbot` is an active member of the `codecheckers` organisation.
- Its permission on `codecheckers/testing-dev-register` is `read`. That is
  enough to comment on a public repository, which is all the current commands
  do. Labels, assignees and pushing a branch need `write`, so that has to be
  raised before those commands land.
- 2FA on the account, as the organisation requires.

## Rotation

The token expires. GitHub returns the date in the
`github-authentication-token-expiration` response header, and the bot logs a
warning when it is within a fortnight, but do not rely on reading logs:

1. Generate a replacement with the same settings as above.
2. `runway app config set CHEKHOV_GH_ACCESS_TOKEN=...`, which redeploys.
3. Check `GET /healthz`, which reports the new expiry date.
4. Delete the old token.

See [`deployment.md`](deployment.md) for where the deployment lives.

## The webhook secret

`CHEKHOV_GH_SECRET_TOKEN` is not a GitHub token: it is a shared secret you
invent, used to sign the webhook deliveries so the bot can tell GitHub's
requests from anyone else's.

```sh
openssl rand -hex 32
```

The same value goes into `.env` (locally), into the platform's configuration
(deployed), and into the webhook's *Secret* field on the testing register. A
delivery whose `X-Hub-Signature-256` does not verify is answered 401 and never
parsed.

## Never commit a token

`.env` is in `.gitignore`. `.env.example` carries the variable names and no
values. A token that reaches a commit is burnt: revoke it, do not try to remove
it from the history.
