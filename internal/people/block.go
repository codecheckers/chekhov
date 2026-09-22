package people

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The bot's own record inside a comment, which is the only store it has.
//
// There are two kinds now - the roles of a check, and what a reply left
// outstanding (internal/followup) - and there will be more. They are the same
// thing on the wire, and this is that thing in one place: the format a reader
// of docs/record-key.md is told about, and the parse that decides whether the
// bot will act on what it finds. A second copy of it would be a second thing
// to harden.

// blockEnd closes a marker and the signature line under it.
const blockEnd = " -->"

// signatureMarker holds the signature over exactly the bytes of the block
// above it.
//
// Outside the payload rather than a field within it, so that what is signed is
// exactly the bytes between the marker and its end - as they stand in the
// comment, not as Go would marshal them again. A signature over a re-marshal
// covers an equivalence class of blocks rather than the one a reader sees.
const signatureMarker = "<!-- chekhov:sig "

// ErrNoBlock is a comment that carries no record, which is most of them.
var ErrNoBlock = errors.New("no record in this comment")

// A Block is one kind of record: how it is marked, and what to call it in an
// error somebody will read.
type Block struct {
	// Marker opens the block, and a comment carrying one begins with it.
	// Being a comment by the bot is not enough: the bot quotes people, so a
	// marker can appear in a bot comment without the bot having meant it.
	Marker string
	// Name is the kind in words, for the errors: "roles record".
	Name string
}

// Render writes a payload as the block, signed as written.
//
// The bytes returned are exactly what the signature covers, so the caller has
// to write them out unchanged - which is what Line and the callers' own
// composition do.
func (b Block) Render(payload any, signer *Signer) (whole []byte, signature string, err error) {
	written, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	whole = []byte(b.Marker + string(written) + blockEnd)
	return whole, signer.sign(whole), nil
}

// Line is the signature line that goes under the block, empty when there is
// no signature to write.
func (b Block) Line(signature string) string {
	if signature == "" {
		return ""
	}
	return signatureMarker + signature + blockEnd + "\n"
}

// Parse reads a block out of a comment the bot wrote, into whatever holds that
// kind of payload.
//
// The marker has to open the comment - see Marker for why - and appear once.
// Anything else is a comment that merely mentions a record, which is not the
// same thing at all. The bytes the signature covers come back with it, because
// what is verified is what was written and not a re-marshal of it.
func (b Block) Parse(body string, into any) (whole []byte, signature string, err error) {
	body = strings.TrimLeft(body, " \t\r\n")
	if !strings.HasPrefix(body, b.Marker) {
		return nil, "", ErrNoBlock
	}
	if strings.Count(body, b.Marker) > 1 {
		return nil, "", fmt.Errorf("the comment carries more than one %s", b.Name)
	}
	end := strings.Index(body, blockEnd)
	if end < 0 {
		return nil, "", fmt.Errorf("the %s is not closed", b.Name)
	}

	whole = []byte(body[:end+len(blockEnd)])
	if err := json.Unmarshal([]byte(strings.TrimSpace(body[len(b.Marker):end])), into); err != nil {
		return nil, "", fmt.Errorf("the %s could not be read: %w", b.Name, err)
	}
	return whole, signatureIn(body[end:]), nil
}

// Verify is everything the signature has to say before the bot acts on a
// record: it was signed with a key this bot accepts, and it covers these
// bytes.
func (b Block) Verify(whole []byte, signature, key string, signer *Signer) error {
	if signature != "" && !signer.Knows(key) {
		return fmt.Errorf("%w: it names a key I do not accept (%s)", ErrTampered, key)
	}
	return signer.verify(whole, signature)
}

// signatureIn finds the signature line under a block.
func signatureIn(body string) string {
	start := strings.Index(body, signatureMarker)
	if start < 0 {
		return ""
	}
	rest := body[start+len(signatureMarker):]
	end := strings.Index(rest, blockEnd)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
