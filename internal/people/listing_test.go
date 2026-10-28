package people

import (
	"context"
	"errors"
	"testing"
)

// One command's look at an issue's comments (codecheckers/chekhov#54).

// counted is an issue's comments, counting every listing that leaves.
type counted struct {
	*comments
	listings int
}

func (c *counted) Comments(ctx context.Context, repository string, issue int) ([]Comment, error) {
	c.listings++
	return c.comments.Comments(ctx, repository, issue)
}

const listed = "codecheckers/testing"

func TestAListingListsOnce(t *testing.T) {
	issue := &counted{comments: &comments{comments: []Comment{{ID: 1, Author: "a-codechecker", Body: "hello"}}}}
	listing := &Listing{Source: issue, Repository: listed, Issue: 1}

	for range 3 {
		got, err := listing.Comments(context.Background(), listed, 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("listing = %v, %v", got, err)
		}
		// What a reader does with its copy is its own business.
		got[0].Body = "changed by a reader"
	}
	if issue.listings != 1 {
		t.Errorf("three readers listed the comments %d times, want once", issue.listings)
	}
	if got, _ := listing.Comments(context.Background(), listed, 1); got[0].Body != "hello" {
		t.Errorf("a reader changed what the next one is given: %q", got[0].Body)
	}
}

// An issue that will not answer costs one request, not one per reader.
func TestAListingKeepsItsError(t *testing.T) {
	issue := &counted{comments: &comments{err: errors.New("GitHub is having a moment")}}
	listing := &Listing{Source: issue, Repository: listed, Issue: 1}

	for range 2 {
		if _, err := listing.Comments(context.Background(), listed, 1); err == nil {
			t.Fatal("the error was lost")
		}
	}
	if issue.listings != 1 {
		t.Errorf("listed %d times, want once", issue.listings)
	}
}

// A listing answers for its own issue and passes every other one on.
func TestAListingAnswersOnlyForItsIssue(t *testing.T) {
	issue := &counted{comments: &comments{}}
	listing := &Listing{Source: issue, Repository: listed, Issue: 1}

	for range 2 {
		if _, err := listing.Comments(context.Background(), listed, 2); err != nil {
			t.Fatal(err)
		}
	}
	if issue.listings != 2 {
		t.Errorf("another issue was answered from the listing: %d listings, want 2", issue.listings)
	}
}

// A change lists the issue again under the lock, so that it is made from what
// the issue says now; and what it writes is put into the listing, so a reader
// afterwards sees the record as it is after the write without listing again.
func TestAWriteListsAfreshAndKeepsTheListingHonest(t *testing.T) {
	issue := &counted{comments: &comments{}}
	store := &Checks{Comments: issue, Bot: "chekhovbot", Signer: signer(t)}
	listing := &Listing{Source: issue, Repository: listed, Issue: 1}
	view := store.On(listing)
	ctx := context.Background()

	if _, err := view.Read(ctx, listed, 1); err != nil {
		t.Fatal(err)
	}
	// Somebody else records an author between the read and the write.
	issue.comments.comments = []Comment{{ID: 7, Author: "chekhovbot",
		Body: comment(t, signer(t), listed, 1, Record{Authors: []string{"a-handling-editor"}})}}

	if _, err := view.Update(ctx, listed, 1, func(record Record) (Record, error) {
		record.AssignedCodechecker = "a-codechecker"
		return record, nil
	}); err != nil {
		t.Fatal(err)
	}
	reading, err := view.Read(ctx, listed, 1)
	if err != nil {
		t.Fatal(err)
	}
	if reading.Record.AssignedCodechecker != "a-codechecker" {
		t.Errorf("the listing does not carry the write: %+v", reading.Record)
	}
	if len(reading.Record.Authors) != 1 {
		t.Errorf("the write was made from the stale listing and lost the author: %+v", reading.Record)
	}
	if !reading.Signed() {
		t.Errorf("the record in the listing does not verify: %v", reading.Tampered)
	}
	if issue.listings != 2 {
		t.Errorf("listed %d times, want twice: once to read, once under the lock", issue.listings)
	}
}

// A record posted rather than edited goes at the end of the listing, where
// GitHub lists it.
func TestAPostedRecordJoinsTheListing(t *testing.T) {
	issue := &counted{comments: &comments{}}
	store := &Checks{Comments: issue, Bot: "chekhovbot", Signer: signer(t)}
	view := store.On(&Listing{Source: issue, Repository: listed, Issue: 1})
	ctx := context.Background()

	if _, err := view.Update(ctx, listed, 1, func(record Record) (Record, error) {
		record.AssignedCodechecker = "a-codechecker"
		return record, nil
	}); err != nil {
		t.Fatal(err)
	}
	reading, err := view.Read(ctx, listed, 1)
	if err != nil || reading.Record.AssignedCodechecker != "a-codechecker" || reading.Comment != 99 {
		t.Errorf("reading after the post = %+v, %v", reading, err)
	}
	if issue.listings != 1 {
		t.Errorf("listed %d times, want once", issue.listings)
	}
}

// A change whose fresh look fails reports it, and leaves the listing the
// command already had: the label note after a failed `assign` is still built
// from what was read, not from the error.
func TestAFailedFreshLookLeavesTheListing(t *testing.T) {
	issue := &counted{comments: &comments{comments: []Comment{{ID: 7, Author: "chekhovbot",
		Body: comment(t, signer(t), listed, 1, Record{AssignedCodechecker: "a-codechecker"})}}}}
	store := &Checks{Comments: issue, Bot: "chekhovbot", Signer: signer(t)}
	view := store.On(&Listing{Source: issue, Repository: listed, Issue: 1})
	ctx := context.Background()

	if _, err := view.Read(ctx, listed, 1); err != nil {
		t.Fatal(err)
	}
	issue.comments.err = errors.New("GitHub is having a moment")
	if _, err := view.Update(ctx, listed, 1, func(record Record) (Record, error) {
		return record, nil
	}); err == nil {
		t.Fatal("the failed listing under the lock was not reported")
	}
	reading, err := view.Read(ctx, listed, 1)
	if err != nil || reading.Record.AssignedCodechecker != "a-codechecker" {
		t.Errorf("the listing was lost to the failure: %+v, %v", reading, err)
	}
}
