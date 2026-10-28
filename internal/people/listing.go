package people

import (
	"context"
	"slices"
	"sync"
)

// A Lister lists the comments of one issue: internal/github in production.
type Lister interface {
	Comments(ctx context.Context, repository string, issue int) ([]Comment, error)
}

// A Listing is one command's look at the comments of the issue it is on,
// however many parts of the command ask for them. See
// codecheckers/chekhov#54: the record of the check, the label note, the note
// already said, and the follow-ups `status` reports are all read from the
// same comments, and each used to list them for itself - several requests
// each, on a long checks issue.
//
// It belongs to one command and never to the deployment: a deployment lives
// for weeks, and a listing kept for it would answer for a check somebody has
// since edited. The error is kept too, so an issue that will not answer costs
// one request rather than one per reader.
//
// It is kept honest about what the bot itself writes. Checks.Update and
// Checks.Accept list the comments again under the lock, because a record is
// changed from what the issue says now and not from what it said when the
// command began, and the listing takes that fresher look; the record they
// then write is put into it, so that what reads it afterwards - the label
// note after `assign` - sees the check as it is after the write. A writing
// command therefore lists twice and every other command once.
type Listing struct {
	// Source is where the comments come from, and the issue is the one the
	// command is on. A listing asked about any other issue passes the
	// question on rather than answering with the wrong comments.
	Source     Lister
	Repository string
	Issue      int

	mu       sync.Mutex
	read     bool
	comments []Comment
	err      error
}

// Comments is the listing, read once. A copy, so that nothing a reader does
// with it can change what the next reader is given.
func (l *Listing) Comments(ctx context.Context, repository string, issue int) ([]Comment, error) {
	if !l.mine(repository, issue) {
		return l.Source.Comments(ctx, repository, issue)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.read {
		// Kept as a copy too: wrote changes it, and the slice is the source's.
		var listed []Comment
		listed, l.err = l.Source.Comments(ctx, repository, issue)
		l.comments, l.read = slices.Clone(listed), true
	}
	return slices.Clone(l.comments), l.err
}

// mine reports that the listing answers for this issue: a nil one answers
// for none, and any other issue is passed on by Comments.
func (l *Listing) mine(repository string, issue int) bool {
	return l != nil && repository == l.Repository && issue == l.Issue
}

// refresh lists the comments again and keeps what it finds. A listing that
// fails leaves the earlier one standing: the change that wanted the fresh look
// reports the error, and the readers after it still have what was read.
func (l *Listing) refresh(ctx context.Context, repository string, issue int) error {
	if !l.mine(repository, issue) {
		return nil
	}
	listed, err := l.Source.Comments(ctx, repository, issue)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.comments, l.err, l.read = slices.Clone(listed), nil, true
	return nil
}

// wrote puts a comment the bot has just written into the listing: an edit
// replaces the body of the comment it edited, a new comment goes at the end,
// where GitHub lists it. A listing that has not been read, or could not be,
// is left alone - the next reader lists the issue and finds the comment there.
func (l *Listing) wrote(repository string, issue int, comment Comment) {
	if !l.mine(repository, issue) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.read || l.err != nil {
		return
	}
	for i := range l.comments {
		if l.comments[i].ID == comment.ID {
			l.comments[i].Body = comment.Body
			return
		}
	}
	l.comments = append(l.comments, comment)
}
