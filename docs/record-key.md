# The key the bot signs a check's roles with

The per-check roles — handling editor, assigned codechecker, authors — live in
a comment the bot posted on the checks issue, because the bot has no storage
but the issues themselves. See [`../CLAUDE.md`](../CLAUDE.md) and
codecheckers/chekhov#18. The certificate identifier `set certificate` reserves
lives there too, as `"certificate":"YYYY-NNN"` inside `roles`, and a
`**Certificate**` line under the table (codecheckers/chekhov#20).

A comment by the bot cannot be edited by somebody without write access to the
repository. It **can** be edited by anyone who has it, and GitHub keeps the bot
as the author:

```
PATCH /repos/codecheckers/testing-dev-register/issues/comments/<id>   (as a collaborator)
→ 200, body changed, author still "chekhovbot"
```

On the register that is the editors and the codecheckers — the people whose
roles the record decides. So the bot signs what it writes, and checks the
signature when it reads it back. This is detection, not prevention: the edit
still happens, and the bot notices instead of acting on it.

## Where each half of the key lives

```sh
go run ./cmd/chekhov record-key
```

The command generates the pair in memory and **prints** it. It writes nothing:
no file, no keychain, no repository. What happens to each half is a decision,
not a default.

| Half | Made | Kept | Who can read it |
|---|---|---|---|
| private (`CHEKHOV_RECORD_KEY=...`) | on the machine that runs the command, once | the deployment's configuration only — `runway app config set` | whoever can read that app's configuration: the same people who hold the bot's GitHub token |
| public (`public key: ...`) | the same moment | [`record-keys.pub`](record-keys.pub) in this repository, and the register's site | everybody, by design |

The private half is **never** committed, never in `.env.example`, never in a
reply and never in a log — `/healthz` reports the *public* key, and only in
development. `.env` carries it for a local run and is ignored by git. If it
leaks, treat it as a leaked token: make a new pair, set it, restart, and see
"Rotation" below, which is not as reassuring as it sounds.

## Where the public key is published

In [`record-keys.pub`](record-keys.pub), one line per register.

**Not in the register itself**, and that is the point. The people who can edit
a roles comment — anyone with write access to the register — could otherwise
re-sign a record with a key of their own and update the published key in the
same breath, and the two would agree. The anchor has to sit somewhere with a
different set of writers and a visible history:

| Where | Why |
|---|---|
| `codecheckers/chekhov/docs/record-keys.pub` | the implementation repository, whose write access is narrower than the register's, and where every change to the file is a commit somebody can see |
| the register's website, `codecheck.org.uk` | a citable URL for a reader who is verifying a certificate rather than reading source |
| the register's Zenodo deposition | immutable and timestamped: a key published there cannot be quietly changed afterwards, which is what makes an old record checkable years later |

A record names the key that signed it, so a reader of an old certificate can
tell *which* published key to check against after a rotation. That naming
proves nothing by itself — a forger would name their own key — which is exactly
why the list above, and not the record, is the trust anchor.

Ed25519, not an HMAC, for that second row: a shared secret would mean only the
bot can check its own work, which is a strange arrangement for a project whose
purpose is independently verifiable records.

```sh
runway app config set -a chekhov CHEKHOV_RECORD_KEY=...
runway app restart -a chekhov      # config alone does not restart, see deployment.md
```

`GET /healthz` reports the public key in development, which is the quickest way
to see which key a deployment is signing with.

## What the signature covers

**The bytes of the record, exactly as they stand in the comment.** This is what
the bot writes, in full — `TestTheRecordHasTheDocumentedShape` keeps this file
and the code in step:

```
<!-- chekhov:roles {"v":1,"check":"codecheckers/register#42","key":"9M8SoHGUZ2PC+Ujpxj2mVwFEJfcQJX1TgH7K1dHxrXE=","roles":{"handling_editor":"nuest","assigned_codechecker":"a-codechecker","authors":["an-author"]}} -->
<!-- chekhov:sig b/FiuZlKy4RtkczF3EJWFdFzEh1Z5HmhBGA3KzoKqOImvyLxXdZnuWzPYARz4did4MOwb0mllEsRN26a+FOeBw== -->

**Roles on this check**

| Role | Who |
|---|---|
| handling editor | `@nuest` |
| assigned codechecker | `@a-codechecker` |
| author | `@an-author` |

I read the record at the top, not this table. It is signed.
```

The signed message is the **first line in full** — from `<!-- ` through ` -->`,
markers included — and nothing else. Not a re-encoding of what it decodes to: a
signature over a re-marshal covers an *equivalence class* of records rather
than the one a reader sees, so duplicate JSON keys, unknown fields and
different spellings of a handle would all verify, and an independent verifier
would have to reproduce Go's JSON encoder to check anything.

Verifying a record is therefore three steps, and needs nothing but
[`record-keys.pub`](record-keys.pub):

1. Take `key` from the record and find it in `record-keys.pub`. A key that is
   not there is not this bot's, whatever the signature says — that lookup is
   the trust, not the record's own claim about itself.
2. `ed25519.Verify(key, firstLineBytes, base64decode(signatureLine))`.
3. Check that `check` is the repository and issue you are actually reading.

The check identity is inside the payload deliberately: a signature over the
roles alone would let a valid record be lifted from one issue and pasted into
another, where it would verify perfectly and be wrong. The bot compares it
case-insensitively, as GitHub's names are.

**The table below the record is not signed** — it is generated from the record,
and the bot checks that the whole comment is the one it would write from what
it just read. An edited table is therefore an edited record, even when the
signed line is untouched; otherwise a false set of roles could be displayed
under a genuine signature indefinitely.

## When a record does not verify

The bot says what it knows and no more: **the roles record was edited after I
wrote it**, and the reason — the signature does not match, it carries none, the
comment around it was changed, or it names a key that is not accepted. It does
*not* say who edited it: it cannot tell, and a rotation that drops a key from
the accepted list, or a record written by another deployment, produce the same
evidence without anybody having touched anything.

It refuses to change the roles of that check, and keeps showing what the record
claims — hiding it helps nobody, and the reply is where an
editor finds out that it was edited at all.

An editor who is content with what it says adopts it:

```
@chekhovbot accept roles
```

That re-signs the record as it stands and writes `accepted_by` and
`accepted_at` **into the signed payload**, so adopting an edit is part of the
record rather than a reset nobody can see afterwards. It is editor-only, like
`assign`.

A record that names a key which is not in the accepted list is refused with
that said — `it names a key I do not accept` — rather than as a bare bad
signature, because the likeliest cause is a key rotated out of the list rather
than an attack.

## Rotation

`CHEKHOV_RECORD_KEYS_RETIRED` takes a comma-separated list of public keys that
should still verify. A rotated key therefore does not invalidate every record
ever written; records are re-signed with the current key the next time they
change.

This is rotation for **hygiene, not for compromise**: a retired key goes on
verifying, so records forged with a leaked key stay valid. Recovering from a
leak means dropping the leaked key from the list — and then every record it
signed reads as edited, and has to be adopted.

## Unsigned records

A deployment **with** a key refuses a record that carries no signature, exactly
as it refuses one whose signature is wrong. Anything else would make deleting
the signature an easier forgery than making one: an attacker would edit the
roles, drop the `sig` line, and the bot would not only believe it but re-sign
it on the next change, laundering the forgery into its own signature.

So there is no legacy path. A record written before a key existed is read as
"edited after I wrote it", and an editor adopts it with `accept roles` — or deletes
the comment and lets the bot start again.

A deployment **without** a key writes unsigned records and reads them: the
comment says so, and so does every `roles` reply. It cannot tell whether a
record has been edited, which is the state everything was in before this
existed, so it is a development convenience and not a configuration for the
real register.

## What this does not cover

**Deletion.** Anyone with write access can delete the roles comment; the bot
then sees a check with no roles and starts a fresh record, signature and all.
The defence there is the audit trail — every change also posted as its own
comment, appended and never edited — which is the remaining piece of
codecheckers/chekhov#18.

It also protects the *record*, not the bot's other comments. Only the record
is load-bearing.
