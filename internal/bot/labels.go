package bot

import (
	"context"
	"errors"
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
// The roles record is therefore read again here rather than carried out of
// the command. For `assign` and `remove` there is no choice - a note built on
// what the record said before the write would be a note about the wrong
// check - and for `roles` and `suggest codecheckers` it costs a second
// listing of the issue's comments, which is the price of not keeping a memo
// that every future writing command would have to remember to invalidate.
// The issue itself is memoised, because nothing the bot does changes a label.
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

// issueOnce is the memo answer puts in the mention: one read of the check,
// however many parts of the command ask for it. The error is remembered too,
// so a register having a bad moment costs one request rather than three.
func (s *Server) issueOnce(ctx context.Context, event mention) func() (github.Issue, error) {
	return sync.OnceValues(func() (github.Issue, error) {
		reader, ok := s.Replies.(Issues)
		if !ok {
			return github.Issue{}, errNoIssueReader
		}
		return reader.Issue(ctx, event.Repository, event.Issue)
	})
}

// issue is the check a command is on. A mention the dispatch set up answers
// from the memo; one made anywhere else reads for itself.
func (s *Server) issue(ctx context.Context, event mention) (github.Issue, error) {
	if event.read == nil {
		event.read = s.issueOnce(ctx, event)
	}
	return event.read()
}
