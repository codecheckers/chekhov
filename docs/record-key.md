# The key the bot signs a check's roles with

The per-check roles — handling editor, assigned codechecker, authors — live in
a comment the bot posted on the checks issue, because the bot has no storage
but the issues themselves. See [`../CLAUDE.md`](../CLAUDE.md) and
codecheckers/chekhov#18.

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

## Making a key

```sh
go run ./cmd/chekhov record-key
```

It prints two things:

| | Where it goes |
|---|---|
| `CHEKHOV_RECORD_KEY=...` | the deployment's configuration, and nowhere else |
| `public key: ...` | published, so that anyone can verify a roles record |

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

**The bytes of the record, exactly as they stand in the comment.** The record
is one line:

```
<!-- chekhov:roles {"v":1,"check":"codecheckers/register#42","roles":{...}} -->
<!-- chekhov:sig <base64 Ed25519 signature over the line above, including the markers> -->
```

The signed message is that first line in full — from `<!-- ` to ` -->` — and
nothing else. Not a re-encoding of what it decodes to: a signature over a
re-marshal covers an *equivalence class* of records rather than the one a
reader sees, so duplicate JSON keys, unknown fields and different spellings of
a handle would all verify, and an independent verifier would have to reproduce
Go's JSON encoder to check anything. Verifying is therefore:

```
ed25519.Verify(publicKey, []byte(firstLine), base64decode(signatureLine))
```

The check identity is inside the payload deliberately: a signature over the
roles alone would let a valid record be lifted from one issue and pasted into
another, where it would verify perfectly and be wrong. It is compared
case-insensitively, as GitHub's names are.

**The table below the record is not signed** — it is generated from the record,
and the bot checks that the whole comment is the one it would write from what
it just read. An edited table is therefore an edited record, even when the
signed line is untouched; otherwise a false set of roles could be displayed
under a genuine signature indefinitely.

## When a record does not verify

The bot refuses to change the roles of that check, says so, and keeps showing
what the record claims — hiding it helps nobody, and the reply is where an
editor finds out that it was edited at all.

An editor who is content with what it says adopts it:

```
@chekhovbot accept roles
```

That re-signs the record as it stands and writes `accepted_by` and
`accepted_at` **into the signed block**, so adopting an edit is part of the
record rather than a reset nobody can see afterwards.

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
"not the one I wrote", and an editor adopts it with `accept roles` — or deletes
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
