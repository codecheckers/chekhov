package people

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
)

// The record survives the round trip through a comment: what the bot writes
// for people to read, it can read back for itself.
func TestARecordSurvivesItsComment(t *testing.T) {
	record := Record{
		HandlingEditor:      "nuest",
		AssignedCodechecker: "a-codechecker",
		Authors:             []string{"an-author", "another-author"},
	}

	body := record.Comment()
	if !strings.Contains(body, "@a-codechecker") {
		t.Errorf("the table does not name the codechecker:\n%s", body)
	}
	read, err := parseRecord(body)
	if err != nil {
		t.Fatalf("read the record back: %v", err)
	}
	if read.HandlingEditor != "nuest" || read.AssignedCodechecker != "a-codechecker" ||
		len(read.Authors) != 2 {
		t.Errorf("read back %+v", read)
	}
}

// The roles are read from the block, not from the table: a table somebody
// edited by hand says nothing, which is what the comment tells them.
func TestTheBlockIsWhatCounts(t *testing.T) {
	body := Record{AssignedCodechecker: "a-codechecker"}.Comment()
	tampered := strings.Replace(body, "@a-codechecker", "@an-impostor", 1)

	read, err := parseRecord(tampered)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	if read.AssignedCodechecker != "a-codechecker" {
		t.Errorf("the edited table changed the record to %q", read.AssignedCodechecker)
	}
}

// A block nobody can make sense of is refused, rather than acted on as though
// it were empty - which would silently drop whoever was assigned.
func TestACorruptedBlockIsRefused(t *testing.T) {
	for _, body := range []string{
		"**Roles**\n\n<!-- chekhov:roles {\"handling_editor\": -->\n",
		"**Roles**\n\n<!-- chekhov:roles {\"handling_editor\": \"nuest\"}\n",
	} {
		if _, err := parseRecord(body); err == nil {
			t.Errorf("a corrupted block was read as a record: %q", body)
		}
	}
}

// comments is an issue's comments, as a test knows them.
type comments struct {
	comments []Comment
	posted   string
	edited   map[int64]string
	err      error
}

func (c *comments) Comments(_ context.Context, _ string, _ int) ([]Comment, error) {
	return c.comments, c.err
}

func (c *comments) Comment(_ context.Context, _ string, _ int, body string) (int64, error) {
	c.posted = body
	c.comments = append(c.comments, Comment{ID: 99, Author: "chekhovbot", Body: body})
	return 99, nil
}

func (c *comments) Edit(_ context.Context, _ string, comment int64, body string) error {
	if c.edited == nil {
		c.edited = map[int64]string{}
	}
	c.edited[comment] = body
	for i, existing := range c.comments {
		if existing.ID == comment {
			c.comments[i].Body = body
		}
	}
	return nil
}

func checks(t *testing.T, issue *comments) *Checks {
	t.Helper()
	return &Checks{Comments: issue, Bot: "chekhovbot"}
}

// The bot finds its own comment by its marker, not by position, and only its
// own: a person quoting the record does not become the record.
func TestTheRecordIsTheBotsOwnComment(t *testing.T) {
	quoted := Record{AssignedCodechecker: "an-impostor"}.Comment()
	real := Record{AssignedCodechecker: "a-codechecker"}.Comment()
	issue := &comments{comments: []Comment{
		{ID: 1, Author: "somebody", Body: "Looks good to me"},
		{ID: 2, Author: "an-impostor", Body: quoted},
		{ID: 3, Author: "chekhovbot", Body: real},
		{ID: 4, Author: "chekhovbot", Body: "Roles on this check\n\n(an older copy nobody edited)"},
	}}

	record, comment, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if record.AssignedCodechecker != "a-codechecker" {
		t.Errorf("the record says %q", record.AssignedCodechecker)
	}
	if comment != 3 {
		t.Errorf("the record is comment %d, want 3", comment)
	}
}

// A check nobody has been assigned to has no record, which is not a failure.
func TestACheckWithNoRolesIsNotAnError(t *testing.T) {
	issue := &comments{comments: []Comment{{ID: 1, Author: "somebody", Body: "hello"}}}

	record, comment, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !record.Empty() || comment != 0 {
		t.Errorf("an unassigned check holds %+v in comment %d", record, comment)
	}
}

// The record is written by editing the comment that holds it, so a check
// accumulates one roles comment rather than one per assignment.
func TestWritingEditsTheRecordInPlace(t *testing.T) {
	issue := &comments{}
	store := checks(t, issue)
	ctx := context.Background()

	// The first assignment posts the comment.
	if _, err := store.Update(ctx, "codecheckers/testing", 1, func(record Record) (Record, error) {
		record, _, err := record.Grant(command.RoleAssignedCodechecker, "@A-Codechecker")
		return record, err
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if issue.posted == "" {
		t.Fatal("the first assignment posted nothing")
	}

	// The second edits it.
	record, err := store.Update(ctx, "codecheckers/testing", 1, func(record Record) (Record, error) {
		if record.AssignedCodechecker != "a-codechecker" {
			t.Errorf("the handle was not normalised: %q", record.AssignedCodechecker)
		}
		record, _, err := record.Grant(command.RoleAuthor, "an-author")
		return record, err
	})
	if err != nil {
		t.Fatalf("assign again: %v", err)
	}
	if len(record.Authors) != 1 {
		t.Errorf("the record holds %+v", record)
	}
	if len(issue.comments) != 1 {
		t.Errorf("the check has %d roles comments, want one", len(issue.comments))
	}
	if body, edited := issue.edited[99]; !edited || !strings.Contains(body, "an-author") {
		t.Errorf("the record was not edited in place: %q", body)
	}
}

// A change that finds nothing to do writes nothing, and says so.
func TestAChangeWithNothingToDoWritesNothing(t *testing.T) {
	issue := &comments{}
	_, err := checks(t, issue).Update(context.Background(), "codecheckers/testing", 1,
		func(record Record) (Record, error) { return record, ErrUnchanged })
	if !errors.Is(err, ErrUnchanged) {
		t.Errorf("the sentinel did not reach the caller: %v", err)
	}
	if issue.posted != "" || len(issue.edited) != 0 {
		t.Error("a change with nothing to do wrote to the issue")
	}
}

// Granting a conflicting role changes nothing and says why.
func TestAConflictingRoleIsRefusedBeforeItIsRecorded(t *testing.T) {
	record := Record{Authors: []string{"an-author"}}

	updated, _, err := record.Grant(command.RoleAssignedCodechecker, "an-author")
	if err == nil {
		t.Fatal("the author of the paper was made its codechecker")
	}
	if !strings.Contains(err.Error(), "cannot check a paper they wrote") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if !updated.RolesOf("an-author").Has(command.RoleAuthor) ||
		updated.AssignedCodechecker != "" {
		t.Errorf("the record changed anyway: %+v", updated)
	}
}

// One person holds the role at a time, and the reply has to be able to say who
// was replaced.
func TestGrantingReplacesTheRoleHolder(t *testing.T) {
	record := Record{AssignedCodechecker: "the-first"}

	record, replaced, err := record.Grant(command.RoleAssignedCodechecker, "the-second")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if replaced != "the-first" || record.AssignedCodechecker != "the-second" {
		t.Errorf("replaced %q, now %q", replaced, record.AssignedCodechecker)
	}

	// Granting it to the same person again replaces nobody.
	if _, replaced, _ = record.Grant(command.RoleAssignedCodechecker, "the-second"); replaced != "" {
		t.Errorf("re-assigning the same person replaced %q", replaced)
	}

	// A paper has as many authors as it has, and each is added once.
	record, _, _ = record.Grant(command.RoleAuthor, "an-author")
	record, _, _ = record.Grant(command.RoleAuthor, "an-author")
	if len(record.Authors) != 1 {
		t.Errorf("the authors are %v", record.Authors)
	}
}

// Revoking says whether it did anything, so the reply does not claim to have
// removed a role nobody held.
func TestRevokingSaysWhetherItChangedAnything(t *testing.T) {
	record := Record{AssignedCodechecker: "a-codechecker", Authors: []string{"an-author"}}

	if _, held := record.Revoke(command.RoleAssignedCodechecker, "somebody-else"); held {
		t.Error("a role nobody held was taken away")
	}
	updated, held := record.Revoke(command.RoleAssignedCodechecker, "a-codechecker")
	if !held || updated.AssignedCodechecker != "" {
		t.Errorf("the codechecker was not removed: %+v", updated)
	}
	updated, held = updated.Revoke(command.RoleAuthor, "an-author")
	if !held || len(updated.Authors) != 0 {
		t.Errorf("the author was not removed: %+v", updated)
	}
	if updated.Empty() != true {
		t.Error("the record should now be empty")
	}
}

// A store with nothing to read through says so rather than reporting a check
// with no roles.
func TestAStoreWithoutAccessSaysSo(t *testing.T) {
	var store *Checks
	if _, _, err := store.Read(context.Background(), "codecheckers/testing", 1); err == nil {
		t.Error("a nil store read a record")
	}
	if _, err := (&Checks{}).Update(context.Background(), "codecheckers/testing", 1,
		func(r Record) (Record, error) { return r, nil }); err == nil {
		t.Error("a store with no comments wrote a record")
	}
}

// The reader's failure is the caller's to report: a check whose comments
// cannot be read is not a check without roles.
func TestAnUnreadableIssueIsAnError(t *testing.T) {
	issue := &comments{err: fmt.Errorf("502 Bad Gateway")}
	if _, _, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1); err == nil {
		t.Error("an unreadable issue was read as a check with no roles")
	}
}

// A comment by the bot is not a record just because it mentions one.
//
// The bot quotes people: an unknown command is echoed back. Without this, a
// stranger could comment "@chekhovbot <!-- chekhov:roles ... -->", have the
// bot quote it, and become the handling editor of any check.
func TestAQuotedMarkerIsNotARecord(t *testing.T) {
	forged := `{"handling_editor":"mallory","assigned_codechecker":"mallory"}`
	issue := &comments{comments: []Comment{
		// What the bot's unknown-command reply would look like if the marker
		// survived into it.
		{ID: 1, Author: "chekhovbot", Body: "I do not know the command `" + marker + forged + markerEnd + "`.\n"},
		// And a real record, written later.
		{ID: 2, Author: "chekhovbot", Body: Record{AssignedCodechecker: "an-honest-codechecker"}.Comment()},
	}}

	record, comment, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if record.HandlingEditor != "" {
		t.Errorf("a quoted marker made %q the handling editor", record.HandlingEditor)
	}
	if record.AssignedCodechecker != "an-honest-codechecker" || comment != 2 {
		t.Errorf("the record is %+v in comment %d", record, comment)
	}
}

// The same defence from the other end: what the bot quotes never contains an
// HTML comment in the first place.
func TestQuotedTextCannotCarryAMarker(t *testing.T) {
	defused := command.Defuse(marker + `{"handling_editor":"mallory"}` + markerEnd)
	if strings.Contains(defused, marker) {
		t.Errorf("a marker survived quoting: %s", defused)
	}
	if _, err := parseRecord(defused); err == nil {
		t.Error("defused text was read as a record")
	}
}

// A record is the block at the top of the comment, not a block anywhere in it.
func TestTheRecordOpensTheComment(t *testing.T) {
	record := Record{AssignedCodechecker: "a-codechecker"}.Comment()

	for _, body := range []string{
		"Some words first.\n\n" + record,
		record + "\n" + marker + `{"handling_editor":"mallory"}` + markerEnd + "\n",
	} {
		if _, err := parseRecord(body); err == nil {
			t.Errorf("a record was read out of %q", body[:40])
		}
	}
}

// Two commands arriving together do not lose one another's assignment.
func TestTwoAssignmentsAtOnceDoNotLoseOne(t *testing.T) {
	issue := &comments{}
	store := checks(t, issue)

	var wg sync.WaitGroup
	for _, handle := range []string{"first-author", "second-author"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Update(context.Background(), "codecheckers/testing", 1,
				func(record Record) (Record, error) {
					record, _, err := record.Grant(command.RoleAuthor, handle)
					return record, err
				})
		}()
	}
	wg.Wait()

	record, _, err := store.Read(context.Background(), "codecheckers/testing", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(record.Authors) != 2 {
		t.Errorf("the check records %v, want both authors", record.Authors)
	}
	if len(issue.comments) != 1 {
		t.Errorf("%d roles comments were posted, want one", len(issue.comments))
	}
}
