package bot

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/suggest"
)

// Noticing a label that no longer describes the check, in the commands that
// already run. See codecheckers/chekhov#44, and command/labels.go for the
// table that decides; what is here is the reading of the world it judges.

// labelNote is what a command whose subject is the check's state says about
// the check's labels, empty when there is nothing to say.
//
// Gathered after the command has answered, so that what it did is part of
// what is looked at: the codechecker `assign` has just recorded is the one
// the labels are compared against. Appended centrally, so it appears at most
// once however many commands could have produced it.
//
// The record of the check is therefore read again here rather than carried out of
// the command. For `assign` and `remove` there is no choice - a note built on
// what the record said before the write would be a note about the wrong
// check - and for `roles` and `suggest codecheckers` it costs a second
// listing of the issue's comments, which is the price of not keeping a memo
// that every future writing command would have to remember to invalidate.
//
// The issue is memoised, and the memo is kept honest: the bot does now change
// labels - `check start` takes `needs codechecker` off - so moveLabels tells
// the memo what it moved. Without that the note is built on the labels as they
// were before the write, and the same comment reports a label removed and
// then warns that the check still carries it.
//
// Everything here fails quiet, and in the cheap order: no label named means
// no rule can fire, so nothing is read at all; an issue that will not answer
// leaves every rule with nothing to compare against; and the codechecker
// lists, the one read that leaves the register, are asked for only when the
// rule that needs them could still say something.
func (s *Server) labelNote(ctx context.Context, event mention, services *check.Services) string {
	state := command.CheckState{Names: command.LabelNames{
		Institution:      s.Settings.InstitutionLabel(),
		NeedsCodechecker: s.Settings.NeedsCodecheckerLabel(),
	}}
	if event.Issue <= 0 || !state.Names.Watching() {
		return ""
	}

	issue, err := s.issue(ctx, event)
	if err != nil {
		s.Logger.Warn("could not read a check's labels, so nothing was said about them",
			"issue", event.Issue, "error", err)
		return ""
	}
	state.Labels, state.LabelsRead, state.Closed = issue.Labels, true, issue.Closed

	if s.Checks != nil {
		reading, err := s.Checks.Read(ctx, event.Repository, event.Issue)
		switch {
		case err != nil || reading.Tampered != nil:
			// A record somebody edited grants nothing here either: the roles
			// it names may be theirs rather than an editor's, and a warning
			// built on them would be a warning about whatever they typed.
			s.Logger.Warn("the roles of this check could not be trusted, so nothing was said about the labels",
				"issue", event.Issue, "error", errors.Join(err, reading.Tampered))
		default:
			state.AssignedCodechecker = reading.Record.Holders().AssignedCodechecker
			state.RolesRead = true
		}
	}

	if state.AssignedCodechecker != "" && state.WantsInstitution() && services.Enabled() {
		state.Institution, state.AlsoVolunteers =
			suggest.InstitutionOf(services, s.Settings, state.AssignedCodechecker)
	}
	return state.Note()
}

// errNoIssueReader is a reply path that cannot read the register's issues -
// the command line preview, a test recorder. Not a failure: the parts that
// wanted the issue each do without it, and say so.
var errNoIssueReader = errors.New("this reply path cannot read the register's issues")

// An issueMemo is one read of the check, however many parts of a command ask
// for it - and whatever the command has since done to its labels.
//
// A pointer, and carried as one on the mention, because a mention is copied
// freely: a command that moved a label has to be able to tell the copy the
// dispatch will judge the labels from. The alternative is reading the issue
// again after every write, which costs a request to learn what the bot itself
// just did.
type issueMemo struct {
	read func() (github.Issue, error)
	mu   sync.Mutex
	// moved is what the bot has changed in this command: label to whether it
	// is now on. Applied over what was read, so the memo answers with the
	// check as it is rather than as it was found.
	moved map[string]bool
}

// get is the check, with the bot's own changes applied.
func (m *issueMemo) get() (github.Issue, error) {
	issue, err := m.read()
	if err != nil {
		return issue, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.moved) == 0 {
		return issue, nil
	}
	labels := make([]string, 0, len(issue.Labels)+len(m.moved))
	for _, carried := range issue.Labels {
		if _, changed := m.moved[strings.ToLower(strings.TrimSpace(carried))]; !changed {
			labels = append(labels, carried)
		}
	}
	for label, on := range m.moved {
		if on {
			labels = append(labels, label)
		}
	}
	issue.Labels = labels
	return issue, nil
}

// moveLabel records what the bot did, so that everything reading the memo
// afterwards sees it.
func (m *issueMemo) moveLabel(label string, add bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.moved == nil {
		m.moved = map[string]bool{}
	}
	m.moved[strings.ToLower(strings.TrimSpace(label))] = add
}

// issueOnce is the memo answer puts in the mention. The error is remembered
// too, so a register having a bad moment costs one request rather than three.
func (s *Server) issueOnce(ctx context.Context, event mention) *issueMemo {
	return &issueMemo{read: sync.OnceValues(func() (github.Issue, error) {
		reader, ok := s.Replies.(Issues)
		if !ok {
			return github.Issue{}, errNoIssueReader
		}
		return reader.Issue(ctx, event.Repository, event.Issue)
	})}
}

// issue is the check a command is on. A mention the dispatch set up answers
// from the memo; one made anywhere else reads for itself.
func (s *Server) issue(ctx context.Context, event mention) (github.Issue, error) {
	if event.read == nil {
		event.read = s.issueOnce(ctx, event)
	}
	return event.read.get()
}
