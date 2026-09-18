package people

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

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

// marker opens the block the record is read back from, and a record comment
// begins with it.
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
const marker = "<!-- chekhov:roles "

const markerEnd = " -->"

// A Record is who holds which per-check role on one issue.
//
// Handles are stored lowercased and without the @, as internal/people compares
// them everywhere.
type Record struct {
	HandlingEditor      string   `json:"handling_editor,omitempty"`
	AssignedCodechecker string   `json:"assigned_codechecker,omitempty"`
	Authors             []string `json:"authors,omitempty"`
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
func (r Record) Comment() string {
	encoded, err := json.Marshal(r)
	if err != nil {
		// The record is three strings and a list of strings; this cannot fail
		// without the type changing, and an empty block reads as "no roles"
		// rather than as corruption.
		encoded = []byte("{}")
	}

	var body strings.Builder
	body.WriteString(marker + string(encoded) + markerEnd + "\n\n")
	body.WriteString(command.RolesTable(r.Holders()))
	body.WriteString("\nI keep this comment up to date. Editing it by hand changes nothing: " +
		"I read the block at the top, not the table.\n")
	return body.String()
}

// parseRecord reads the block out of a comment the bot wrote.
//
// The marker has to open the comment - see marker for why - and appear once.
// Anything else is a comment that merely mentions the record, which is not the
// same thing at all.
func parseRecord(body string) (Record, error) {
	body = strings.TrimLeft(body, " \t\r\n")
	if !strings.HasPrefix(body, marker) {
		return Record{}, errNoRecord
	}
	if strings.Count(body, marker) > 1 {
		return Record{}, fmt.Errorf("the comment carries more than one roles block")
	}

	rest := body[len(marker):]
	end := strings.Index(rest, markerEnd)
	if end < 0 {
		return Record{}, fmt.Errorf("the roles block is not closed")
	}

	var record Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &record); err != nil {
		return Record{}, fmt.Errorf("the roles block could not be read: %w", err)
	}
	record.HandlingEditor = normalize(record.HandlingEditor)
	record.AssignedCodechecker = normalize(record.AssignedCodechecker)
	for i, author := range record.Authors {
		record.Authors[i] = normalize(author)
	}
	return record, nil
}

// Comments is what the store reads and writes the record through,
// internal/github in production.
type Comments interface {
	Comments(ctx context.Context, repository string, issue int) ([]Comment, error)
	Comment(ctx context.Context, repository string, issue int, body string) (int64, error)
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

// Read returns the roles of one check, and the comment they are kept in.
//
// A check with no record yet is not an error: it is a check nobody has been
// assigned to. A record that cannot be read is an error, and the caller
// refuses rather than acting on half-understood state.
func (c *Checks) Read(ctx context.Context, repository string, issue int) (Record, int64, error) {
	if c == nil || c.Comments == nil {
		return Record{}, 0, errNoComments
	}
	if strings.TrimSpace(c.Bot) == "" {
		// Without the bot's own name there is no way to tell its comments from
		// anybody else's, and every check would look unassigned while every
		// assignment posted a fresh record.
		return Record{}, 0, fmt.Errorf("I do not know my own account name, so I cannot find my record")
	}
	comments, err := c.Comments.Comments(ctx, repository, issue)
	if err != nil {
		return Record{}, 0, err
	}

	// The oldest comment by the bot that carries the marker wins. Somebody may
	// have quoted the bot, and the bot may - through some accident - have
	// posted two; the first one it wrote is the record, and the rest are text.
	for _, comment := range comments {
		if !strings.EqualFold(comment.Author, c.Bot) {
			continue
		}
		record, err := parseRecord(comment.Body)
		if errors.Is(err, errNoRecord) {
			continue
		}
		if err != nil {
			return Record{}, comment.ID, fmt.Errorf(
				"the roles of %s#%d could not be read: %w (comment %d is the one to fix)",
				repository, issue, err, comment.ID)
		}
		return record, comment.ID, nil
	}
	return Record{}, 0, nil
}

// errNoRecord is a comment that is not the record: most of them.
var errNoRecord = errors.New("not a roles record")

// errNoComments is a store with no way to reach the issue.
var errNoComments = errors.New("no GitHub access to read the check's roles with")

// ErrUnchanged is what a change returns when the record already says what was
// asked for: nothing is written, and the caller reports it as such.
var ErrUnchanged = errors.New("the record already says that")

// Update reads the roles of a check, gives them to change, and records the
// result - one operation, because every caller wants all three and because
// doing them separately is how two assignments at once lose one of them.
//
// change may refuse, and then nothing is written. The record it returns is
// what was stored, and the comment it was stored in.
func (c *Checks) Update(ctx context.Context, repository string, issue int,
	change func(Record) (Record, error)) (Record, error) {
	if c == nil || c.Comments == nil {
		return Record{}, errNoComments
	}

	unlock := c.lock(repository, issue)
	defer unlock()

	record, comment, err := c.Read(ctx, repository, issue)
	if err != nil {
		return Record{}, err
	}
	updated, err := change(record)
	switch {
	case errors.Is(err, ErrUnchanged):
		// The record already says what the caller wanted. Nothing is written -
		// a comment edited to itself is noise in the issue - and the sentinel
		// is passed on, because the reply has to say "there was nothing to do"
		// rather than "done".
		return record, err
	case err != nil:
		return record, err
	}

	if comment > 0 {
		if err := c.Comments.Edit(ctx, repository, comment, updated.Comment()); err != nil {
			return record, err
		}
		return updated, nil
	}
	if _, err := c.Comments.Comment(ctx, repository, issue, updated.Comment()); err != nil {
		return record, err
	}
	return updated, nil
}

// lock serialises writes to one issue's record.
func (c *Checks) lock(repository string, issue int) func() {
	key := fmt.Sprintf("%s#%d", repository, issue)
	held, _ := c.writing.LoadOrStore(key, &sync.Mutex{})
	mutex := held.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
