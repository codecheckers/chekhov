// Package followup is what a reply left outstanding.
//
// A reply often ends with something still to happen: an owner has been asked
// to invite somebody, a registration is waiting on details, a check has gone
// quiet. The bot has no database and wants none, so the outstanding thing
// lives where everything else about a check lives - in the issue, as a record
// in the comment that raised it.
//
// The record is marked and **signed**, for the reason the record of a check is: a
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
	// KindArchive is a copy of a checked repository in the CODECHECK
	// organisation that was not archived when the check finished; the
	// handling editor is reminded once the certificate is registered. The
	// subject is the repository, as register.csv writes it, not a person. See
	// codecheckers/chekhov#27.
	KindArchive = "repository-archive"
	// KindReminder is a reminder somebody asked for with `remind`: the
	// subject is who to remind, Due when, and Note the reason they gave.
	// See codecheckers/chekhov#25.
	KindReminder = "reminder"
	// KindQueueNudge is the sweep telling the editors that a check has waited
	// in the queue too long. Written settled: nothing resumes it, because
	// the next nudge is decided by looking at the label again, and the
	// record is there only to say when this one was posted, so that the next
	// waits an interval. The subject is the queue's label. See
	// codecheckers/chekhov#25.
	KindQueueNudge = "queue-nudge"
	// KindQuietNudge is the sweep asking the assigned codechecker about a
	// check nobody has commented on. Written settled, as the queue nudge is:
	// it says when the codechecker was last asked, and Reminders how many
	// times in a row without an answer, so that the asking stops. The
	// subject is the codechecker.
	KindQuietNudge = "quiet-nudge"
	// KindInclusion is a registration an editor ran `include` on: the subject
	// is the registrant, Team the team they belong in. The sweep looks at the
	// organisation, the team and the list until all three hold, and then
	// welcomes them, settled, and closes the issue; Told says the registrant
	// has been told that an invitation waits for them, so that they are told
	// once. See codecheckers/chekhov#49.
	KindInclusion = "codechecker-inclusion"
	// KindRegistration is a registration the bot opened for somebody who
	// joined: the subject is who it is for, whose comments the row is read
	// from, since an issue's first post is its author's to edit and the bot
	// is that author. The sweep reminds them once, Told, when no row has come
	// a week on. See codecheckers/chekhov#49.
	KindRegistration = "codechecker-registration"
	// KindReady is a registration found complete, waiting for an editor's
	// `include`: the subject is the registrant. The sweep tells the editors
	// once, Told, when no `include` has left its record two days on. See
	// codecheckers/chekhov#49.
	KindReady = "registration-ready"
)

// outlivesTheCheck are the kinds still due after the check's issue is closed:
// the register pull request closes it, and archiving comes after that.
// aboutAThing are the kinds whose subject is a thing - a repository, a label
// - not a person.
// Properties of a kind, kept beside the kinds so that the sweep and `status`
// never have to name one.
var (
	outlivesTheCheck = map[string]bool{KindArchive: true}
	aboutAThing      = map[string]bool{KindArchive: true, KindQueueNudge: true}
)

// OutlivesTheCheck reports whether a follow-up of this kind is still due once
// its check is closed.
func OutlivesTheCheck(kind string) bool { return outlivesTheCheck[kind] }

// AboutAThing reports whether the subject of this kind is a thing - written as
// code - rather than a person, written as a handle.
func AboutAThing(kind string) bool { return aboutAThing[kind] }

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
	// Subject is who it is about, lowercased as every handle the bot keeps is
	// - or, for a kind about a thing rather than a person, what.
	Subject string `json:"subject"`
	// By is who asked, for a kind that may have to fall back on them: an
	// archive reminder goes to the handling editor, and to whoever finished
	// the check when there is none.
	By string `json:"by,omitempty"`
	// Role and Team are what was being asked for, empty when the kind needs
	// neither.
	Role string `json:"role,omitempty"`
	Team string `json:"team,omitempty"`
	// Reminders is how many times this has been asked again. In the record
	// because the thread is the only memory the bot has, and a reminder that
	// could not count itself would go on for ever.
	Reminders int `json:"reminders,omitempty"`
	// Due is when a reminder is due, RFC 3339, and it is due on that day in
	// UTC: the sweep runs once a day, and "in 2 weeks" is a promise about the
	// day. Kept to the second all the same, because it is part of what a
	// record is about, as Kind and Subject are: two reminders for one person,
	// even on one day, are two records, and the one that says the first was
	// posted carries the same Due and settles only that one.
	Due string `json:"due,omitempty"`
	// Note is what the person who asked wrote under the command, quoted back
	// when the reminder comes. Inside the signature, so that the words
	// somebody is reminded of are the words that were written.
	Note string `json:"note,omitempty"`
	// Settled says the thing is done and has been said to be done. Most kinds
	// need no such mark, because a handler that finds the world in order
	// says nothing; a kind that confirms the end out loud needs one, or it
	// would confirm again every night. Signed like the rest, so it cannot be
	// forged to silence a reminder.
	Settled bool `json:"settled,omitempty"`
	// Told says the person the follow-up is about has been told what is
	// theirs to do - an inclusion's invitation, waiting to be accepted - so
	// that it is said once. State the bot keeps, as Settled is, never words
	// anybody wrote; signed like the rest.
	Told bool `json:"told,omitempty"`
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

// DueBy reports whether a reminder is due on the day of now, or was before:
// false for a record that names no day.
func (r Record) DueBy(now time.Time) bool {
	due, err := time.Parse(time.RFC3339, r.Due)
	return err == nil && due.UTC().Format(time.DateOnly) <= now.UTC().Format(time.DateOnly)
}

// About is what one record is about: the newest record with a key is the state,
// and the older ones are the history of what was asked.
func (r Record) About() string {
	return r.Kind + "\x00" + strings.ToLower(r.Subject) + "\x00" + r.Due
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
