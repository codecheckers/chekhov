package people

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// The roles one check gave out, kept in the issue that check lives in.
//
// The bot has no database. The checks issue is the only durable store it has,
// and it is the right one: the roles are about that check, and they should
// still be readable when the bot is long gone. Within the issue they go in a
// comment the bot posted, rather than in the issue body - which every
// participant writes in - or in labels, which cannot hold a handle.
//
// How much that protects, exactly: a comment by the bot cannot be edited by
// somebody without write access to the repository, which is most people. It
// **can** be edited by anyone who has write access - on the register that is
// the editors and the codecheckers - because GitHub lets a repository
// collaborator edit anybody's comment, keeping the original author. So this is
// a store that a passer-by cannot touch and a colleague can. Detecting a
// colleague's edit needs the bot to sign what it writes, which is
// codecheckers/chekhov#41.
//
// The comment carries a table for people and a machine-readable block for the
// bot, inside an HTML comment so a reader sees only the table.

// marker opens the record, and a record comment begins with it.
//
// Being a comment by the bot is not enough to be the record. The bot quotes
// people - an unknown command is echoed back, a check report carries strings
// out of somebody's codecheck.yml - so a marker can appear in a bot comment
// without the bot having meant it. Anyone who could put one there could name
// themselves the handling editor of any check. So the record is recognised by
// what the bot writes deliberately: the marker on the *first line* of a
// comment the bot wrote, and nothing else in the issue is a record, however
// much it looks like one. Replies also have HTML comments stripped out of any
// text they quote, see command.Defuse - two defences, because this one decides
// who holds a role.
//
// "record" rather than "roles", because it holds more than the roles: the
// certificate identifier lives here too (#20), and whatever else a check
// needs the bot to keep will.
const marker = "<!-- chekhov:record "

// legacyMarker is what the record was marked with while it held only roles.
// A record written then is still read, and verifies as written; the next
// change writes it with marker.
const legacyMarker = "<!-- chekhov:roles "

// recordBlock is the record as the comment carries it: the marker above, and
// the signed-block format every kind of record the bot writes shares. See
// block.go.
var recordBlock = Block{Marker: marker, Legacy: []string{legacyMarker}, Name: "record of this check"}

// A Record is who holds which per-check role on one issue, and the
// certificate identifier the check was given.
//
// Handles are stored lowercased and without the @, as internal/people compares
// them everywhere.
type Record struct {
	HandlingEditor      string   `json:"handling_editor,omitempty"`
	AssignedCodechecker string   `json:"assigned_codechecker,omitempty"`
	Authors             []string `json:"authors,omitempty"`
	// Certificate is the identifier `set certificate` reserved, YYYY-NNN. It
	// lives here rather than in a record of its own so that it shares the
	// lock, the signature and the tamper check the roles have; the title
	// and the `id assigned` label repeat it where people and the launch-pad
	// look, and anybody can edit those.
	Certificate string `json:"certificate,omitempty"`
}

// RolesOf is what one person is on this check.
func (r Record) RolesOf(handle string) command.Roles {
	handle = normalize(handle)
	if handle == "" {
		return nil
	}
	var roles command.Roles
	if r.HandlingEditor == handle {
		roles = roles.With(command.RoleHandlingEditor)
	}
	if r.AssignedCodechecker == handle {
		roles = roles.With(command.RoleAssignedCodechecker)
	}
	for _, author := range r.Authors {
		if author == handle {
			roles = roles.With(command.RoleAuthor)
			break
		}
	}
	return roles
}

// Holders is who holds which role on this check, in the terms a reply speaks.
//
// The one accessor: the record's fields are its storage shape, and everything
// that renders or reports goes through this.
func (r Record) Holders() command.Holders {
	return command.Holders{
		HandlingEditor:      r.HandlingEditor,
		AssignedCodechecker: r.AssignedCodechecker,
		Authors:             append([]string(nil), r.Authors...),
		Certificate:         r.Certificate,
	}
}

// Grant gives a role to somebody, and returns the record with it.
//
// One handling editor and one assigned codechecker per check: granting either
// replaces whoever held it, and the caller is told who that was so the reply
// can say. A paper has as many authors as it has.
func (r Record) Grant(role command.Role, handle string) (Record, string, error) {
	handle = normalize(handle)
	if handle == "" {
		return r, "", fmt.Errorf("no handle to give the role to")
	}
	if conflicting, because, conflicts := r.RolesOf(handle).Conflict(role); conflicts {
		return r, "", fmt.Errorf("%s is already the %s of this check, and %s",
			handle, conflicting, because)
	}

	replaced := ""
	switch role {
	case command.RoleHandlingEditor:
		if r.HandlingEditor == handle {
			return r, "", ErrUnchanged
		}
		replaced, r.HandlingEditor = r.HandlingEditor, handle
	case command.RoleAssignedCodechecker:
		if r.AssignedCodechecker == handle {
			return r, "", ErrUnchanged
		}
		replaced, r.AssignedCodechecker = r.AssignedCodechecker, handle
	case command.RoleAuthor:
		for _, author := range r.Authors {
			if author == handle {
				return r, "", ErrUnchanged
			}
		}
		r.Authors = append(append([]string(nil), r.Authors...), handle)
		sort.Strings(r.Authors)
	default:
		return r, "", fmt.Errorf("%s is not a role a check gives out", role)
	}
	return r, replaced, nil
}

// Holder is who holds a role on this check now, empty when nobody does.
//
// Only the roles one person holds at a time answer: a paper has as many
// authors as it has, and "the author" is not a thing to ask about.
func (r Record) Holder(role command.Role) string {
	switch role {
	case command.RoleHandlingEditor:
		return r.HandlingEditor
	case command.RoleAssignedCodechecker:
		return r.AssignedCodechecker
	default:
		return ""
	}
}

// Revoke takes a role away, and says whether anything changed.
func (r Record) Revoke(role command.Role, handle string) (Record, bool) {
	handle = normalize(handle)
	switch role {
	case command.RoleHandlingEditor:
		if r.HandlingEditor != handle || handle == "" {
			return r, false
		}
		r.HandlingEditor = ""
	case command.RoleAssignedCodechecker:
		if r.AssignedCodechecker != handle || handle == "" {
			return r, false
		}
		r.AssignedCodechecker = ""
	case command.RoleAuthor:
		kept := make([]string, 0, len(r.Authors))
		for _, author := range r.Authors {
			if author != handle {
				kept = append(kept, author)
			}
		}
		if len(kept) == len(r.Authors) {
			return r, false
		}
		r.Authors = kept
	default:
		return r, false
	}
	return r, true
}

// normalized is the record with every handle written the one way.
func (r Record) normalized() Record {
	r.HandlingEditor = normalize(r.HandlingEditor)
	r.AssignedCodechecker = normalize(r.AssignedCodechecker)
	for i, author := range r.Authors {
		r.Authors[i] = normalize(author)
	}
	return r
}

// Empty reports whether the check has given out no roles at all.
func (r Record) Empty() bool {
	return r.HandlingEditor == "" && r.AssignedCodechecker == "" && len(r.Authors) == 0
}

// Comment is the comment body that holds the record: the block the bot reads
// back, and then a table for people.
//
// The block comes first because that is how a record is recognised - see
// marker. The table is written from the same record, by the same renderer the
// `roles` reply uses, so the two cannot drift.
func (c content) Comment() string {
	var body strings.Builder
	body.Write(c.payload)
	body.WriteString("\n")
	body.WriteString(recordBlock.Line(c.Signature))
	body.WriteString("\n")
	body.WriteString(command.RolesTable(c.Roles.Holders()))
	body.WriteString(command.RecordNote(c.AcceptedBy, c.AcceptedAt, c.Signature != ""))
	return body.String()
}

// content is a record as a comment carries it: the payload exactly as written,
// what it decodes to, and the signature over those bytes.
type content struct {
	payload []byte
	record
	Signature string
}

// record is what the payload says. Its JSON is what the signature covers, so
// its field names and shape are part of the record format rather than an
// implementation detail: see docs/record-key.md.
type record struct {
	Version int    `json:"v"`
	Check   string `json:"check"`
	// Key is the public half of the key this record was signed with, so that
	// a reader knows which published key to check it against. Naming the key
	// proves nothing on its own - a forger would name their own - but without
	// it a verifier cannot tell a rotated key from a wrong one.
	Key   string `json:"key,omitempty"`
	Roles Record `json:"roles"`
	// AcceptedBy and AcceptedAt record an editor knowingly adopting a record
	// the bot did not write. Inside the signed payload, so that adopting an
	// edit is part of the record rather than an invisible reset.
	AcceptedBy string `json:"accepted_by,omitempty"`
	AcceptedAt string `json:"accepted_at,omitempty"`
}

// render writes a record and signs it as written.
func render(what record, signer *Signer) (content, error) {
	what.Key = signer.PublicKey()
	whole, signature, err := recordBlock.Render(what, signer)
	if err != nil {
		return content{}, err
	}
	return content{payload: whole, record: what, Signature: signature}, nil
}

// parseContent reads a record out of a comment the bot wrote.
//
// The marker has to open the comment - see marker for why - and appear once.
// Anything else is a comment that merely mentions the record, which is not the
// same thing at all.
func parseContent(body string) (content, error) {
	var read content
	whole, signature, err := recordBlock.Parse(body, &read.record)
	switch {
	case errors.Is(err, ErrNoBlock):
		return content{}, errNoRecord
	case err != nil:
		return content{}, err
	}
	if read.Version > recordVersion {
		return content{}, fmt.Errorf("this record was written by a newer version of me (%d), "+
			"and I will not guess at what it means", read.Version)
	}
	read.payload, read.Signature = whole, signature
	read.Roles = read.Roles.normalized()
	return read, nil
}

// Comments is what the store reads and writes the record through,
// internal/github in production.
type Comments interface {
	Comments(ctx context.Context, repository string, issue int) ([]Comment, error)
	// Post writes a comment exactly: the record is read back byte for byte,
	// so nothing may be appended to it.
	Post(ctx context.Context, repository string, issue int, body string) (int64, error)
	Edit(ctx context.Context, repository string, comment int64, body string) error
}

// A Comment is one comment of an issue. The same type the client returns:
// mirroring it here bought nothing but an adapter to convert between the two.
type Comment = github.Comment

// Checks reads and writes the per-check roles.
//
// Nothing is cached: the issue is the record, a role granted while the bot was
// restarting has to be seen, and a check has a handful of comments rather than
// a stream of them.
type Checks struct {
	Comments Comments
	// Signer signs what is written and checks what is read. Nil writes
	// unsigned records and accepts them, which is a deployment without a key.
	Signer *Signer

	// Bot is the account whose comment is the record. A comment by anybody
	// else carrying the marker is somebody quoting the bot, and is ignored.
	Bot string

	// writing serialises the read-modify-write of one issue's record, so that
	// two commands arriving together cannot each read the same record and
	// write over one another. One deployment answers one register, so an
	// in-process lock is the whole of the problem; the re-read in Update
	// catches what a second deployment would do.
	writing sync.Map // repository#issue -> *sync.Mutex
}

// errNoRecord is a comment that is not the record: most of them.
var errNoRecord = errors.New("not a record of this check")

// errNoComments is a store with no way to reach the issue.
var errNoComments = errors.New("no GitHub access to read the record of the check with")

// ErrUnchanged is what a change returns when the record already says what was
// asked for: nothing is written, and the caller reports it as such.
var ErrUnchanged = errors.New("the record already says that")

// A Reading is what one look at a check's roles found.
//
// Signed says the block carried a signature the bot accepts. Unsigned says it
// carried none - a record written before signing existed, or by a deployment
// with no key - which is not a failure and is put right the next time the
// record is written. Tampered is a signature that did not verify: somebody
// with write access edited the record, and the bot will not act on it until an
// editor adopts it.
type Reading struct {
	Record   Record
	Comment  int64
	Unsigned bool
	Tampered error
	// AcceptedBy and AcceptedAt are set when an editor has adopted an edited
	// record before.
	AcceptedBy, AcceptedAt string
}

// Signed reports that the bot wrote this record and nobody has touched it
// since. Derived rather than stored: it is exactly "carries a signature, and
// nothing is wrong with it".
func (r Reading) Signed() bool { return r.Tampered == nil && !r.Unsigned }

// Read returns the roles of one check, and what is known about where they came
// from.
//
// A check with no record yet is not an error: it is a check nobody has been
// assigned to. A record that cannot be parsed is an error, and the caller
// refuses rather than acting on half-understood state.
func (c *Checks) Read(ctx context.Context, repository string, issue int) (Reading, error) {
	if c == nil || c.Comments == nil {
		return Reading{}, errNoComments
	}
	if strings.TrimSpace(c.Bot) == "" {
		// Without the bot's own name there is no way to tell its comments from
		// anybody else's, and every check would look unassigned while every
		// assignment posted a fresh record.
		return Reading{}, fmt.Errorf("I do not know my own account name, so I cannot find my record")
	}
	comments, err := c.Comments.Comments(ctx, repository, issue)
	if err != nil {
		return Reading{}, err
	}

	// The oldest comment by the bot that carries the marker wins. Somebody may
	// have quoted the bot, and the bot may - through some accident - have
	// posted two; the first one it wrote is the record, and the rest are text.
	for _, comment := range comments {
		if !strings.EqualFold(comment.Author, c.Bot) {
			continue
		}
		read, err := parseContent(comment.Body)
		if errors.Is(err, errNoRecord) {
			continue
		}
		if err != nil {
			return Reading{Comment: comment.ID}, fmt.Errorf(
				"the record of %s#%d could not be read: %w (comment %d is the one to fix)",
				repository, issue, err, comment.ID)
		}

		reading := Reading{
			Record: read.Roles, Comment: comment.ID,
			Unsigned:   read.Signature == "",
			AcceptedBy: read.AcceptedBy, AcceptedAt: read.AcceptedAt,
		}
		reading.Tampered = c.check(read, comment.Body, repository, issue)
		return reading, nil
	}
	return Reading{}, nil
}

// check is everything that has to be true of a record before the bot will act
// on it: it belongs to this check, it carries this bot's signature over the
// bytes it is written in, and the comment around it is the one the bot would
// write from it.
func (c *Checks) check(read content, body, repository string, issue int) error {
	// The signature covers which check the record belongs to, so a record
	// lifted from another issue is refused rather than read as somebody
	// else's roles. Compared case-insensitively: GitHub's names are.
	if !strings.EqualFold(read.Check, CheckOf(repository, issue)) {
		return fmt.Errorf("%w: it says it belongs to %s", ErrTampered, read.Check)
	}
	if err := recordBlock.Verify(read.payload, read.Signature, read.Key, c.Signer); err != nil {
		return err
	}
	// The table under the record is what people read, and it is not covered by
	// the signature. Rather than sign it too - which would make the payload
	// depend on how a table is rendered - the bot checks that the comment is
	// the one it would write from this record. Anything else is an edit, even
	// when the record itself is untouched.
	if rewritten := read.Comment(); strings.TrimSpace(rewritten) != strings.TrimSpace(body) {
		return fmt.Errorf("%w: the comment around it has been changed", ErrTampered)
	}
	return nil
}

// CheckOf names the issue a record belongs to, as the signature covers it.
// Exported because a follow-up record carries the same name and it must be
// the same spelling: a record whose check is written differently verifies and
// then reads as somebody else's.
func CheckOf(repository string, issue int) string {
	return fmt.Sprintf("%s#%d", repository, issue)
}

// Update reads the roles of a check, gives them to change, and records the
// result - one operation, because every caller wants all three and because
// doing them separately is how two assignments at once lose one of them.
//
// A record the bot did not write is refused: acting on roles somebody edited
// would make the signature decorative. An editor adopts such a record with
// Accept, and then it can be changed like any other.
func (c *Checks) Update(ctx context.Context, repository string, issue int,
	change func(Record) (Record, error)) (Record, error) {
	if c == nil || c.Comments == nil {
		return Record{}, errNoComments
	}

	unlock := c.lock(repository, issue)
	defer unlock()

	reading, err := c.Read(ctx, repository, issue)
	if err != nil {
		return Record{}, err
	}
	if reading.Tampered != nil {
		return reading.Record, reading.Tampered
	}
	updated, err := change(reading.Record)
	switch {
	case errors.Is(err, ErrUnchanged):
		// The record already says what the caller wanted. Nothing is written -
		// a comment edited to itself is noise in the issue - and the sentinel
		// is passed on, because the reply has to say "there was nothing to do"
		// rather than "done".
		return reading.Record, err
	case err != nil:
		return reading.Record, err
	}
	return updated, c.write(ctx, repository, issue, record{
		Roles: updated, AcceptedBy: reading.AcceptedBy, AcceptedAt: reading.AcceptedAt,
	}, reading.Comment)
}

// Accept adopts a record the bot did not write, and signs it as it stands.
//
// People fix things by hand, and refusing a record forever because somebody
// tidied it is worse than adopting it knowingly. Who adopted it, and when, is
// written into the block and signed with the rest, so the adoption is part of
// the record rather than a reset nobody can see afterwards.
func (c *Checks) Accept(ctx context.Context, repository string, issue int,
	editor string, when time.Time) (Reading, error) {
	if c == nil || c.Comments == nil {
		return Reading{}, errNoComments
	}

	unlock := c.lock(repository, issue)
	defer unlock()

	reading, err := c.Read(ctx, repository, issue)
	if err != nil {
		return reading, err
	}
	if reading.Comment == 0 {
		return reading, fmt.Errorf("there is no record here to adopt")
	}
	if reading.Tampered == nil && !reading.Unsigned {
		return reading, ErrUnchanged
	}

	adopted := record{
		Roles:      reading.Record,
		AcceptedBy: normalize(editor), AcceptedAt: when.UTC().Format(time.RFC3339),
	}
	if err := c.write(ctx, repository, issue, adopted, reading.Comment); err != nil {
		return reading, err
	}
	reading.Unsigned, reading.Tampered = !c.Signer.Signs(), nil
	reading.AcceptedBy, reading.AcceptedAt = adopted.AcceptedBy, adopted.AcceptedAt
	return reading, nil
}

// write signs the block and puts it in the issue, editing the record comment
// or posting one when there is none yet.
func (c *Checks) write(ctx context.Context, repository string, issue int, what record, comment int64) error {
	// The identity of the record is the store's to fill in, not the caller's:
	// it is what the signature binds the roles to.
	what.Version, what.Check = recordVersion, CheckOf(repository, issue)
	written, err := render(what, c.Signer)
	if err != nil {
		return err
	}
	if comment > 0 {
		return c.Comments.Edit(ctx, repository, comment, written.Comment())
	}
	_, err = c.Comments.Post(ctx, repository, issue, written.Comment())
	return err
}

// lock serialises writes to one issue's record.
func (c *Checks) lock(repository string, issue int) func() {
	key := CheckOf(repository, issue)
	held, _ := c.writing.LoadOrStore(key, &sync.Mutex{})
	mutex := held.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
