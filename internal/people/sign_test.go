package people

import (
	"context"

	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"github.com/codecheckers/chekhov/internal/command"
	"strings"
	"testing"
	"time"
)

// The point of the whole exercise: an edit by somebody with write access -
// which GitHub allows, keeping the bot as the comment's author - is noticed.
func TestAnEditedRecordIsNotedAsNotTheBotsOwn(t *testing.T) {
	honest := comment(t, signer(t), "codecheckers/testing", 1,
		Record{AssignedCodechecker: "an-honest-codechecker"})
	// A collaborator edits the block, as they may: the handles change, the
	// signature does not.
	edited := strings.Replace(honest, "an-honest-codechecker", "mallory", 1)
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: edited}}}

	reading, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !errors.Is(reading.Tampered, ErrTampered) {
		t.Errorf("an edited record was accepted: %+v", reading)
	}
	// What it says is still reported: hiding it helps nobody.
	if reading.Record.AssignedCodechecker != "mallory" {
		t.Errorf("the reading does not say what the record now claims: %+v", reading.Record)
	}
}

// A record the bot did not write grants nothing and changes nothing.
func TestATamperedRecordRefusesEveryChange(t *testing.T) {
	forged := comment(t, newKey(), "codecheckers/testing", 1, Record{HandlingEditor: "mallory"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: forged}}}
	store := checks(t, issue)

	_, err := store.Update(context.Background(), "codecheckers/testing", 1,
		func(record Record) (Record, error) {
			record, _, err := record.Grant(command.RoleAuthor, "an-author")
			return record, err
		})
	if !errors.Is(err, ErrTampered) {
		t.Errorf("a record signed by somebody else was written to: %v", err)
	}
	if len(issue.edited) != 0 {
		t.Error("the forged record was edited anyway")
	}
}

// A signature covers which check it belongs to, so a valid record cannot be
// lifted from one issue into another.
func TestARecordCannotBeMovedToAnotherCheck(t *testing.T) {
	elsewhere := comment(t, signer(t), "codecheckers/testing", 7,
		Record{HandlingEditor: "nuest", AssignedCodechecker: "a-codechecker"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: elsewhere}}}

	reading, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !errors.Is(reading.Tampered, ErrTampered) {
		t.Error("a record from another check was accepted here")
	}
	if !strings.Contains(reading.Tampered.Error(), "#7") {
		t.Errorf("the complaint does not say where it came from: %v", reading.Tampered)
	}
}

// Stripping the signature must not be an easier forgery than making one.
//
// An attacker with write access edits the roles and deletes the `sig` line. If
// that read as "an old record, not signed yet", the bot would act on it and
// sign it on the next change, laundering the forgery into its own signature.
func TestAnUnsignedRecordIsRefusedByABotThatSigns(t *testing.T) {
	unsigned, err := NewSigner("")
	if err != nil {
		t.Fatal(err)
	}
	old := comment(t, unsigned, "codecheckers/testing", 1, Record{AssignedCodechecker: "a-codechecker"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: old}}}
	ctx := context.Background()

	// A bot that signs will not touch it: deleting a signature has to be as
	// hard as forging one, or it is the easier forgery.
	signing := checks(t, issue)
	reading, err := signing.Read(ctx, "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !errors.Is(reading.Tampered, ErrTampered) {
		t.Errorf("a bot with a key accepted an unsigned record: %+v", reading)
	}

	// A bot without a key reads it, and says it cannot tell.
	keyless := &Checks{Comments: issue, Bot: "chekhovbot", Signer: unsigned}
	reading, err = keyless.Read(ctx, "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if reading.Tampered != nil || !reading.Unsigned || reading.Signed() {
		t.Errorf("a keyless bot read an unsigned record as %+v", reading)
	}
}

// An editor adopts an edited record, and who adopted it becomes part of what
// is signed - not an invisible reset.
func TestAcceptingAdoptsAndRecordsWhoDid(t *testing.T) {
	forged := comment(t, newKey(), "codecheckers/testing", 1, Record{HandlingEditor: "nuest"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: forged}}}
	store := checks(t, issue)
	ctx := context.Background()

	when := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reading, err := store.Accept(ctx, "codecheckers/testing", 1, "@NuEsT", when)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if reading.AcceptedBy != "nuest" || !strings.HasPrefix(reading.AcceptedAt, "2026-09-18") {
		t.Errorf("the adoption was not recorded: %+v", reading)
	}

	after, err := store.Read(ctx, "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Tampered != nil || !after.Signed() {
		t.Errorf("the adopted record is still refused: %+v", after)
	}
	if after.AcceptedBy != "nuest" {
		t.Errorf("who adopted it did not survive: %+v", after)
	}
	// And it is in the comment, where a reader of the issue sees it.
	if !strings.Contains(issue.edited[1], "nuest") {
		t.Errorf("the comment does not say who adopted it: %s", issue.edited[1])
	}
}

// Accepting a record the bot wrote itself does nothing, rather than adding a
// note that somebody adopted what nobody had touched.
func TestAcceptingAnHonestRecordChangesNothing(t *testing.T) {
	honest := comment(t, signer(t), "codecheckers/testing", 1, Record{HandlingEditor: "nuest"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: honest}}}

	_, err := checks(t, issue).Accept(context.Background(), "codecheckers/testing", 1, "nuest", time.Now())
	if !errors.Is(err, ErrUnchanged) {
		t.Errorf("an untouched record was adopted: %v", err)
	}
	if len(issue.edited) != 0 {
		t.Error("the record was written to anyway")
	}
}

// A retired key still verifies what it signed, so rotating a key does not
// invalidate every record ever written.
func TestARetiredKeyStillVerifies(t *testing.T) {
	retired := newKey()
	written := comment(t, retired, "codecheckers/testing", 1, Record{HandlingEditor: "nuest"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: written}}}

	current := newKey()
	store := &Checks{Comments: issue, Bot: "chekhovbot", Signer: &Signer{
		Private:  current.Private,
		Accepted: []ed25519.PublicKey{current.Private.Public().(ed25519.PublicKey), retired.Private.Public().(ed25519.PublicKey)},
	}}

	reading, err := store.Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if reading.Tampered != nil || !reading.Signed() {
		t.Errorf("a record signed with the retired key was refused: %+v", reading)
	}
}

// A deployment with no key writes unsigned records and says so, rather than
// refusing to work.
func TestWithoutAKeyRecordsAreWrittenUnsigned(t *testing.T) {
	unsigned, err := NewSigner("")
	if err != nil {
		t.Fatal(err)
	}
	if unsigned.Signs() || unsigned.PublicKey() != "" {
		t.Errorf("a signer with no key claims to sign: %+v", unsigned)
	}

	issue := &comments{}
	store := &Checks{Comments: issue, Bot: "chekhovbot", Signer: unsigned}
	ctx := context.Background()
	if _, err := store.Update(ctx, "codecheckers/testing", 1, func(record Record) (Record, error) {
		record, _, err := record.Grant(command.RoleAuthor, "an-author")
		return record, err
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	reading, err := store.Read(ctx, "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !reading.Unsigned || reading.Signed() {
		t.Errorf("a record written without a key is not marked unsigned: %+v", reading)
	}
}

// A key is read from the form it is configured in, and a key that is not one
// is refused rather than silently ignored.
func TestReadingTheKeys(t *testing.T) {
	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := base64.StdEncoding.EncodeToString(private.Seed())
	public := base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))

	signing, err := NewSigner(seed, public, "")
	if err != nil {
		t.Fatalf("read the keys: %v", err)
	}
	if !signing.Signs() || len(signing.Accepted) != 2 {
		t.Errorf("the keys were read as %+v", signing)
	}

	for _, wrong := range []string{"not base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := NewSigner(wrong); err == nil {
			t.Errorf("%q was accepted as a key", wrong)
		}
	}
}

// The table under the record is what people read, and it is not covered by the
// signature. An edited table would otherwise show a false set of roles under a
// genuine signature, indefinitely - so the whole comment has to be the one the
// bot would write.
func TestAnEditedTableIsAnEditedRecord(t *testing.T) {
	honest := comment(t, signer(t), "codecheckers/testing", 1,
		Record{AssignedCodechecker: "an-honest-codechecker"})
	// The record itself untouched; only what a reader sees.
	edited := strings.Replace(honest, "| an-honest-codechecker |", "| mallory |", 1)
	if edited == honest {
		edited = strings.Replace(honest, "an-honest-codechecker", "mallory", 2)
	}
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: edited}}}

	reading, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !errors.Is(reading.Tampered, ErrTampered) {
		t.Errorf("a comment with an edited table was accepted: %+v", reading)
	}
}

// The signature covers the bytes as they stand in the comment, so no rewriting
// of the record - spacing, key order, an added field - verifies.
func TestOnlyTheBytesAsWrittenVerify(t *testing.T) {
	honest := comment(t, signer(t), "codecheckers/testing", 1, Record{HandlingEditor: "nuest"})

	for _, rewritten := range []string{
		strings.Replace(honest, `{"v":1`, `{ "v":1`, 1),
		strings.Replace(honest, `"roles":`, `"extra":"x","roles":`, 1),
		strings.Replace(honest, `"handling_editor":"nuest"`,
			`"handling_editor":"mallory","handling_editor":"nuest"`, 1),
	} {
		issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: rewritten}}}
		reading, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
		if err != nil {
			continue // a rewrite that does not parse is refused earlier
		}
		if !errors.Is(reading.Tampered, ErrTampered) {
			t.Errorf("a rewritten record verified: %+v", reading)
		}
	}
}

// GitHub's names are case-insensitive, and a record must not become unreadable
// because a repository was written differently.
func TestTheCheckIdentityIgnoresCase(t *testing.T) {
	written := comment(t, signer(t), "CodeCheckers/Testing", 1, Record{HandlingEditor: "nuest"})
	issue := &comments{comments: []Comment{{ID: 1, Author: "chekhovbot", Body: written}}}

	reading, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if reading.Tampered != nil {
		t.Errorf("a record written for the same check in another casing was refused: %v", reading.Tampered)
	}
}

// The shape docs/record-key.md promises a verifier: one line carrying the
// record, one line carrying the signature over it, and a table below that is
// generated rather than signed.
func TestTheRecordHasTheDocumentedShape(t *testing.T) {
	body := comment(t, signer(t), "codecheckers/register", 42, Record{
		HandlingEditor: "nuest", AssignedCodechecker: "a-codechecker", Authors: []string{"an-author"},
	})
	lines := strings.Split(body, "\n")

	if !strings.HasPrefix(lines[0], marker) || !strings.HasSuffix(lines[0], markerEnd) {
		t.Errorf("the first line is not the record: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], signatureMarker) {
		t.Errorf("the second line is not the signature: %q", lines[1])
	}
	if !strings.Contains(lines[0], `"check":"codecheckers/register#42"`) {
		t.Errorf("the record does not name its check: %q", lines[0])
	}
	if !strings.Contains(lines[0], `"key":"`+signer(t).PublicKey()+`"`) {
		t.Errorf("the record does not name the key that signed it: %q", lines[0])
	}
	if t.Failed() || testing.Verbose() {
		t.Logf("a record as the bot writes it:\n%s", body)
	}
}
