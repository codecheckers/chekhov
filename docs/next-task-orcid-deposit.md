# ORCID deposit: first rough plan

## Context

A codechecker gets little lasting credit for a CODECHECK beyond the certificate
itself. ORCID has a **peer review** section meant for this kind of work. The bot
should put the check there on the codechecker's behalf, and only after the
codechecker has granted permission on ORCID itself. The first wiring targets
`sandbox.orcid.org`, where the bot account already has a **sandbox Member API
client** (client ID and secret with `/activities/update`). There is also a
debugging mode: it runs the whole authorisation, but instead of writing to ORCID
it posts what it would have sent on the issue, both raw and formatted.

Two corrections to the brief, from the ORCID API:
- **The .env holds no ORCID user or password.** The bot authenticates as a
  *client* (client ID and client secret). The person's own permission arrives as
  an OAuth access token from the three-legged flow. The sandbox account's login
  is only used to manage the client registration in Developer Tools.
- **A peer review needs a group** (`review-group-id`). The group must exist
  before the first deposit. It is registered once, with a two-legged
  client-credentials token (scope `/group-id-record/update`), and its id then
  goes into the settings.

## The flow

```
codechecker: @chekhovbot orcid deposit [certificate]
bot (reply): "Authorise me on ORCID: <sandbox.orcid.org/oauth/authorize?...&state=S>"
             (dev: also says it is a dry run)
codechecker → ORCID login + consent → redirect to  GET /orcid/callback?code=C&state=S
bot:  verify S (signature, expiry)            → else 400, nothing read
      POST /oauth/token (code → access_token, orcid iD, name)
      orcid iD == a codechecker ORCID in the certificate's codecheck.yml?  → else refuse
      already deposited? (GET /v3.0/{iD}/peer-reviews, match certificate DOI) → else skip
      deposit: POST /v3.0/{iD}/peer-review     | dry run: post payload as comment
      reply on the issue (put-code / link, or the dry-run payload)
      forget the token
browser: small HTML page, "done, see <issue link>"
```

### Design decisions

- **Stay stateless, as the bot already is ("No state, no database").** Every
  piece of pending state (repository, issue number, GitHub login, certificate,
  expiry, nonce) travels inside `state`, signed by the bot. The callback trusts
  only what verifies. It uses the access token straight away and then drops it.
  So the only "runtime storage" is the lifetime of one callback request. There
  is no store to lose on restart or to leak. If refresh or later updates are
  wanted, an in-memory map keyed by ORCID iD can come later. Nothing is written
  to disk.
  - Signing key: a new HMAC secret, `CHEKHOV_ORCID_STATE_KEY`. Reusing the
    Ed25519 record key would be possible, but that key's job is the roles
    record, and keeping the two apart makes rotation simpler.
  - Expiry of about 1 hour. A replayed `state` is harmless because the ORCID
    `code` is single-use and the deposit is de-duplicated.
- **Who: the assigned codechecker only.** The registry entry gets
  `Role: RoleAssignedCodechecker` (`internal/command/roles.go`), so the command
  is absent from everybody else's listing.
- **The deposit binds a GitHub person to an ORCID person.** The link is posted
  in a public issue, so anyone could click it. The callback therefore compares
  the ORCID iD returned by the token exchange with the `codechecker[].ORCID`
  entries of the certificate's `codecheck.yml` (read through `check.Load` /
  `check.ResolveTarget`). On a mismatch it refuses and says so on the issue. As
  a result nobody can claim a check they did not do, even with a valid link.
- **The certificate comes from the argument, or else from the issue.** A
  missing argument is resolved through `register.csv`'s Issue column.
- **Dry run is a setting, not a flag in the comment.** It goes in
  `settings-development.yml` as `orcid.deposit: false`, following the pattern of
  `mastodon.follow: false`, with a test asserting that the development settings
  never deposit. Without a client ID at all, the command still previews the
  payload directly in its reply, with no OAuth round trip, the same way
  `announce` previews without a token.

### The peer-review payload (v3.0)

| Field | Value |
|---|---|
| `reviewer-role` | `reviewer` |
| `review-identifiers` | certificate DOI (Zenodo), relationship `self` |
| `review-url` | certificate page, from the `certificates` setting |
| `review-type` | `evaluation` or `review` (open question, see below) |
| `review-completion-date` | certificate date |
| `review-group-id` | from settings (the registered CODECHECK group) |
| `subject-external-identifier` | paper DOI, relationship `self` |
| `subject-name/title`, `subject-type`, `subject-url` | paper metadata via `Services.Work` (OpenAlex) |
| `subject-container-name` | venue |
| `convening-organization` | from settings: name, city, country, ROR id |

`announce` already collects certificate data from the certificate page's
`index.json` (`internal/announce`). Reuse that collection code rather than
writing it a second time.

## Pieces

1. **Step 0, outside the code:** in sandbox Developer Tools, register the
   redirect URI `https://<dev deployment>/orcid/callback`. ORCID matches it
   exactly and requires https. For a local run, check whether sandbox accepts
   `http://127.0.0.1`. Then register the CODECHECK peer-review group once,
   through a small `chekhov orcid register-group` CLI subcommand that uses the
   client-credentials token, and put the returned group id in the settings.
2. **`internal/orcid/`**, the only code that talks to ORCID, the same idea as
   `internal/mastodon`:
   `AuthorizeURL(state)`, `Exchange(code) (Token{Access, ORCID, Name})`,
   `PeerReviews(token, iD)`, `AddPeerReview(token, iD, PeerReview) (putCode)`,
   `RegisterGroup(...)`, and a `PeerReview` struct with JSON tags. Base URLs
   come from the settings, never from a literal.
3. **`internal/orcid/state.go`**: sign and verify `state`, with the expiry
   check.
4. **`config/settings.go` and the YAML:** an `orcid:` block holding `site`
   (`https://sandbox.orcid.org`), `api` (`https://api.sandbox.orcid.org/v3.0`),
   `callback` (public URL), `group_id`, `convening_organization`, and `deposit`
   (`false` in development). The secrets come only from the environment.
5. **`.env.example`:** `CHEKHOV_ORCID_CLIENT_ID`,
   `CHEKHOV_ORCID_CLIENT_SECRET`, `CHEKHOV_ORCID_STATE_KEY`. Without them,
   `orcid deposit` previews only.
6. **The command:** a registry entry in `internal/command/registry.go` (name
   `orcid`, usage `orcid deposit [certificate]`, group People, role assigned
   codechecker), a handler in `internal/bot/` next to `announce`, and reply
   bodies in `internal/command/reply.go`.
7. **The callback route:** `GET /orcid/callback` in `Server.Handler()`
   (`internal/bot/bot.go`), beside `/dispatch` and `/healthz`. It verifies
   `state` before it reads anything else. The work then runs in a goroutine
   after the HTML answer, following the same "answer, then work" rule as
   `/dispatch`. Replies go through `internal/github`, so the configured-repo
   fence applies here as well. The repository inside `state` must equal the
   target repository.
8. **The dry-run comment:** a heading ("Dry run: this is what I would have sent
   to ORCID for `0000-…`"), a formatted table of the fields, and then the raw
   request in a fenced `json` block, with method and URL, where the
   `Authorization` value is shown as `Bearer <redacted>`. The token never
   appears in a comment or a log line.
9. **`/healthz` (development only):** whether ORCID is configured, the sandbox
   or production site, and the deposit mode. Never a secret.
10. **Docs, version, changelog:** a new `docs/orcid.md` covering the client,
    the redirect URI, group registration and rotation. A minor version bump,
    because this is a new command. Commands in `README.md`. Changelog entry
    under the new version heading.

## Tests (offline, as usual)

- `internal/orcid`: stub ORCID in `internal/testserver`, covering token
  exchange, the peer-review POST (201 with a `Location` put-code), 401, 409
  duplicate, and 5xx.
- `state`: round trip, tampering, expiry, and the wrong repository.
- The bot: `orcid deposit` is refused and absent from the listing for anyone but
  the assigned codechecker. A mismatched ORCID iD refuses. An existing deposit
  is skipped. Dry run posts the payload, redacts the token, and sends no POST to
  `/peer-review`. The development settings have `deposit: false`.
- A payload golden file for one fixture certificate.

## Verification

1. `go build ./... && go test ./... && go vet ./... && gofmt -l .`
2. `echo '@chekhovbot orcid deposit 1970-001' | go run ./cmd/chekhov comment -`
   previews the payload.
3. Live test on a new issue in `testing-dev-register`, following the CLAUDE.md
   routine: one command at a time, using a sandbox ORCID test account that
   appears as the codechecker in the fixture's `codecheck.yml`. Expected
   results: dry-run comment first, then with `deposit: true` a real peer review
   on the sandbox record, then a second run that reports "already deposited".
   Finally, a sandbox account that is not the codechecker is refused.
4. `/code-review` before the commit is proposed. This change touches
   credentials and a new public endpoint, so it qualifies.

## Open questions

- `review-type`: `evaluation` or `review`? And which organisation convenes a
  CODECHECK: CODECHECK itself (does it have a ROR id?) or the venue?
- Group: one CODECHECK group, or the venue's ISSN (`issn:…` groups need no
  registration) when the venue has one?
- Which CODECHECK is this, when several codecheckers share one certificate?
  Each runs the command for themselves, and the ORCID match picks out their own
  entry.
- Before any code: draft a chekhov issue for this and put it on the board.
  Both need your go-ahead.
