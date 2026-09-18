package people

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Signing what the bot records, so that it can tell when what it reads back is
// not what it wrote.
//
// A comment by the bot cannot be edited by somebody without write access to
// the repository. It can be edited by anyone who has it - on the register that
// is the editors and the codecheckers - and GitHub keeps the bot as the
// author, so the bot cannot otherwise tell an edit of its own from an edit of
// theirs. See codecheckers/chekhov#41.
//
// This is detection, not prevention. The edit still happens; the bot notices,
// says so, and refuses to act on roles it did not write until an editor
// knowingly adopts them - which is itself recorded and signed.
//
// Ed25519 rather than a shared secret, so that the public key can be published
// and anybody can verify a roles record years later without asking the bot. A
// project about independently verifiable records should not keep its own
// provenance in a form only it can check. For that to be true the signature
// has to cover the bytes in the comment rather than something reconstructed
// from them - see signedBytes.

// recordVersion is the shape of the record. It is inside the signed bytes, so
// a record cannot be replayed into a reader that reads it differently.
const recordVersion = 1

// ErrTampered is a record that no longer matches the bot's signature: it was
// edited, the signature is missing, or the comment around it was changed.
//
// Deliberately not "edited by somebody else": the bot cannot tell who, and
// there are honest causes - a key dropped from the accepted list during a
// rotation, or a record written by another deployment. What it knows is that
// the bytes are not the ones it signed, and that is what it says.
var ErrTampered = errors.New("the roles record was edited after I wrote it")

// Why is what is wrong with a record, without the sentence a reply already
// leads with: a note that repeats the sentinel reads as a stutter.
func Why(err error) string {
	if err == nil {
		return ""
	}
	if reason := strings.TrimPrefix(err.Error(), ErrTampered.Error()+": "); reason != err.Error() {
		return reason
	}
	return "the signature does not match"
}

// A Signer signs the records this bot writes and verifies the ones it reads.
//
// A Signer with no private key writes unsigned records and accepts them: that
// is a development deployment, and every reply says as much rather than the
// bot refusing to start. A deployment that *does* sign treats an unsigned
// record as tampered, because otherwise deleting a signature would be an
// easier forgery than making one.
type Signer struct {
	Private ed25519.PrivateKey
	// Accepted are the public keys a signature may verify against, the current
	// key first. More than one so that rotating a key does not invalidate
	// every record ever written.
	Accepted []ed25519.PublicKey
}

// NewSigner reads the keys from their configured form: the private key as a
// base64 seed, and any number of retired public keys beside it.
func NewSigner(private string, retired ...string) (*Signer, error) {
	signer := &Signer{}
	if strings.TrimSpace(private) != "" {
		seed, err := decodeKey(private, ed25519.SeedSize, "record key")
		if err != nil {
			return nil, err
		}
		signer.Private = ed25519.NewKeyFromSeed(seed)
		signer.Accepted = append(signer.Accepted, signer.Private.Public().(ed25519.PublicKey))
	}
	for _, key := range retired {
		if strings.TrimSpace(key) == "" {
			continue
		}
		raw, err := decodeKey(key, ed25519.PublicKeySize, "retired record key")
		if err != nil {
			return nil, err
		}
		signer.Accepted = append(signer.Accepted, ed25519.PublicKey(raw))
	}
	return signer, nil
}

// GenerateSigner makes a new key. The `chekhov record-key` command prints what
// it produces; the tests use it to sign as the bot would.
func GenerateSigner() (*Signer, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return NewSigner(base64.StdEncoding.EncodeToString(private.Seed()))
}

func decodeKey(key string, size int, what string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil {
		return nil, fmt.Errorf("the %s is not base64: %w", what, err)
	}
	if len(raw) != size {
		return nil, fmt.Errorf("the %s is %d bytes, want %d", what, len(raw), size)
	}
	return raw, nil
}

// PrivateKey is the seed to configure a deployment with, base64 as the
// environment carries it. Only `chekhov record-key` prints it.
func (s *Signer) PrivateKey() string {
	if !s.Signs() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(s.Private.Seed())
}

// PublicKey is the key that verifies what this bot writes, in the form the
// register publishes it. Empty when the bot signs nothing.
func (s *Signer) PublicKey() string {
	if !s.Signs() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(s.Private.Public().(ed25519.PublicKey))
}

// Signs reports whether records written now will carry a signature.
func (s *Signer) Signs() bool { return s != nil && s.Private != nil }

// sign returns the signature over exactly these bytes.
func (s *Signer) sign(payload []byte) string {
	if !s.Signs() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.Private, payload))
}

// Knows reports whether a public key, as a record names it, is one this bot
// accepts. A record naming an unknown key is refused with that said, rather
// than with a bare "the signature is wrong": the likeliest cause is a key
// rotated out of the list, and the reader should be told which key to look
// for.
func (s *Signer) Knows(key string) bool {
	if key == "" {
		return false
	}
	for _, accepted := range s.Accepted {
		if base64.StdEncoding.EncodeToString(accepted) == key {
			return true
		}
	}
	return false
}

// verify says whether these bytes carry this bot's signature.
//
// A missing signature is a failure whenever the bot signs: an attacker who can
// edit the comment would otherwise simply delete the signature rather than try
// to forge one, and be believed. Only a deployment with no key of its own
// accepts an unsigned record, and says so everywhere it shows it.
func (s *Signer) verify(payload []byte, signature string) error {
	if signature == "" {
		if s.Signs() {
			return fmt.Errorf("%w: it carries no signature of mine", ErrTampered)
		}
		return nil
	}
	if s == nil || len(s.Accepted) == 0 {
		// A signed record and nothing to check it with. Saying "fine" would
		// make the signature worthless; the caller reports it as unverified.
		return fmt.Errorf("%w: I have no key to check it with", ErrTampered)
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: the signature is not base64", ErrTampered)
	}
	for _, key := range s.Accepted {
		if ed25519.Verify(key, payload, raw) {
			return nil
		}
	}
	return ErrTampered
}
