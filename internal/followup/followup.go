// Package followup is what a reply left outstanding.
//
// A reply often ends with something still to happen: an owner has been asked
// to invite somebody, a registration is waiting on details, a check has gone
// quiet. The bot has no database and wants none, so the outstanding thing
// lives where everything else about a check lives - in the issue, as a record
// in the comment that raised it.
//
// The record is marked and **signed**, for the reason the roles record is: a
// bot comment is safe from a passer-by and not from a repository collaborator,
// and an edited record would send a later sweep after a different person.
//
// One thing to know before writing one. `command.Defuse` escapes "<!--" in
// every reply the bot posts, so that nothing it quotes can look like a record;
// a marker written into a reply body is therefore neutralised on the way out,
// by design. Only code that knows it is emitting the bot's own record may
// write one, after that. See Render.
package followup

import (
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/people"
)

// Version of the record format. A record from a newer version is left alone
// rather than guessed at.
const Version = 1

// Kinds of follow-up. A kind is the whole of what a sweep needs to choose a
// handler, so it is a value rather than a shape: adding one is a constant and
// a handler, not a change to the record.
const (
	// KindOrganisation is somebody an editor tried to give a role to who is
	// not in the organisation yet, and the owners have been asked to invite
	// them. See codecheckers/chekhov#47.
	KindOrganisation = "organisation-invitation"
)

// block is how a follow-up is written into a comment: the marker that opens
// it, and the signed-block format every record the bot writes shares
// (internal/people/block.go). The marker has to be the first line, so that a
// comment merely quoting a record - a check report of somebody's
// codecheck.yml, say - cannot be read as one.
var block = people.Block{Marker: "<!-- chekhov:followup ", Name: "follow-up record"}

// ErrNoRecord is a comment that carries no follow-up, which is most of them.
var ErrNoRecord = people.ErrNoBlock

// A Record is one outstanding thing, and enough to resume it.
type Record struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	// Check is the issue the follow-up belongs to, as owner/repo#number. In
	// the record as well as around it, so that a record lifted into another
	// issue does not verify.
	Check string `json:"check"`
	// Subject is who it is about, lowercased as every handle the bot keeps is.
	Subject string `json:"subject"`
	// Role and Team are what was being asked for, empty when the kind needs
	// neither.
	Role string `json:"role,omitempty"`
	Team string `json:"team,omitempty"`
	// Reminders is how many times this has been asked again. In the record
	// because the thread is the only memory the bot has, and a reminder that
	// could not count itself would go on for ever.
	Reminders int `json:"reminders,omitempty"`
	// Asked is when this was raised, RFC 3339. What "three days old" is
	// measured from, and the reason a sweep needs no clock of its own.
	Asked string `json:"asked"`
	// Key is the public half of the key that signed it, so that a reader can
	// tell which key to check against - and a rotated key is visible rather
	// than silently unverifiable.
	Key string `json:"key"`
}

// Raised is when the record was written, zero when it does not say.
func (r Record) Raised() time.Time {
	when, err := time.Parse(time.RFC3339, r.Asked)
	if err != nil {
		return time.Time{}
	}
	return when
}

// Older reports whether the record was raised more than a given time ago. The
// question every handler asks before it repeats itself.
func (r Record) Older(than time.Duration, now time.Time) bool {
	raised := r.Raised()
	return !raised.IsZero() && now.Sub(raised) >= than
}

// Render writes the record as the block that opens a comment, signed as
// written.
//
// The caller puts the reply under it. This is the one thing in the bot that
// may write a marker, and it is deliberately not reachable from a reply body:
// see the package comment.
func Render(what Record, signer *people.Signer) (string, error) {
	what.Version = Version
	what.Key = signer.PublicKey()
	whole, signature, err := block.Render(what, signer)
	if err != nil {
		return "", err
	}
	return string(whole) + "\n" + block.Line(signature) + "\n", nil
}

// Parse reads a record out of a comment the bot wrote, and checks the
// signature against the key it names.
//
// A record the bot did not write, or one somebody edited, is refused: a sweep
// acts on these, and an edited one would send it after a different person.
func Parse(body, check string, signer *people.Signer) (Record, error) {
	var read Record
	whole, signature, err := block.Parse(body, &read)
	if err != nil {
		return Record{}, err
	}
	if read.Version > Version {
		return Record{}, fmt.Errorf("this follow-up was written by a newer version of me (%d), "+
			"and I will not guess at what it means", read.Version)
	}
	if err := block.Verify(whole, signature, read.Key, signer); err != nil {
		return Record{}, fmt.Errorf("the follow-up record is not one I wrote: %w", err)
	}
	// The check is inside the signature, so a record lifted from one issue
	// into another verifies and then fails here, which is the honest place.
	if check != "" && !strings.EqualFold(read.Check, check) {
		return Record{}, fmt.Errorf("this follow-up belongs to %s, not to %s", read.Check, check)
	}
	return read, nil
}
