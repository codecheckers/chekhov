package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/registration"
)

// The pull request adding a registration's row is kept in step with the
// registration (#49). `include` opens it when the row passes, but a row can
// stop passing afterwards - a later release checks more, an ORCID record
// changes, the registrant edits the row - and an open pull request would then
// be merged as it is, and the sweep would welcome somebody whose row the list
// does not want yet. So while the registration needs something, the bot's own
// pull request is a draft, which GitHub will not merge; once it does not, the
// row on its branch is brought up to date from the registration and the
// pull request is ready for review again. The draft is the state: nothing is
// remembered, and the next look decides from what it sees.
//
// The bot's own is the pull request it opened, from a codechecker-<login>
// branch of the lists repository: github.SetRowPullRequestDraft reads all
// three before it changes anything. Anybody else's - an editor's opened by
// hand, or one opened from the bot's branch - is only said to wait.

// RowDrafts is the part of the lists' reply path that finds the bot's own
// pull request adding a row, and turns it into a draft and back.
type RowDrafts interface {
	OwnRowPullRequest(ctx context.Context, repository, key string) (github.RowPullRequest, error)
	SetRowPullRequestDraft(ctx context.Context, repository string, number int, draft bool) error
}

var _ RowDrafts = (*github.Client)(nil)

// rowStep is what keeping the row's pull request in step did: a line for the
// registration's reply, empty for nothing to say, and whether the pull
// request was turned - into a draft, or ready again - which is all the sweep
// says anything about.
type rowStep struct {
	line   string
	turned bool
}

// keepRowInStep finds the pull request open from the bot's own branch for the
// registration's row, in two reads, and keeps it in step with the report.
// One an editor opened from a branch of their own is not looked for: finding
// it means reading every open pull request, on every check of every
// registration, and it is theirs to keep in step. Nothing when none is open,
// or when it cannot be told: the reply is about the registration, and the
// sweep looks again.
func (s *Server) keepRowInStep(ctx context.Context, event mention, report registration.Report) rowStep {
	login := report.Row.Login()
	drafts, ok := s.Lists.(RowDrafts)
	if login == "" || !ok {
		return rowStep{}
	}
	pull, err := drafts.OwnRowPullRequest(ctx, event.Repository, login)
	if err != nil {
		s.Logger.Warn("could not read the open pull requests of the lists", "issue", event.Issue, "error", err)
		return rowStep{}
	}
	if pull.Number == 0 {
		return rowStep{}
	}
	return s.stepRow(ctx, event, report, pull, true)
}

// stepRow keeps one open pull request adding the row in step with the
// registration as the report has it now. update says to bring the row on
// the branch up to date when the registration passes; the sweep does that
// only for a draft it is about to make ready, as an edit is otherwise
// followed by the check that asks for it.
func (s *Server) stepRow(ctx context.Context, event mention, report registration.Report,
	pull github.RowPullRequest, update bool) rowStep {
	draft := !report.Includable()
	if pull.By != "" {
		if !draft {
			return rowStep{}
		}
		return rowStep{line: command.RowPullRequestBySomebodyWaits(pull.Number, pull.By)}
	}
	updated := false
	if !draft && update {
		var stop bool
		var line string
		if updated, line, stop = s.updateRow(ctx, event, report.Row, pull); stop {
			return rowStep{line: line}
		}
	}
	switch {
	case pull.Draft != draft:
		line, done := s.turnRowPullRequest(ctx, event, pull.Number, draft)
		return rowStep{line: line, turned: done}
	case draft:
		return rowStep{line: command.RowPullRequestWaits(pull.Number)}
	case updated:
		return rowStep{line: command.RowPullRequestUpdated(pull.Number)}
	}
	return rowStep{}
}

// rowProposal is the row a pull request adds, with what is said about it:
// the same for the pull request `include` opens and for the one it or a
// check brings up to date, so that an update does not rewrite the title.
func rowProposal(row registration.Row, issue int, body string) github.RowProposal {
	file := registration.RegistrationList
	return github.RowProposal{
		Row: row.Cells(),
		// Defused as the body is: the name is the registrant's own text.
		Title:   command.Defuse(command.ListPullRequestTitle(row.Name, file)),
		Message: command.ListCommitMessage(row.Login(), file, issue),
		Body:    body,
	}
}

// updateRow brings the row on the bot's branch up to date from the
// registration, keeping the pull request's body, which says who asked for it:
// whether it changed - or stop, with the line saying why, when the pull
// request must be left as it is. Somebody's commits on the branch are theirs:
// the row is left as they made it, and a draft is not made ready on the
// strength of a row the bot did not write. A row merged meanwhile leaves
// nothing to say.
func (s *Server) updateRow(ctx context.Context, event mention, row registration.Row,
	pull github.RowPullRequest) (updated bool, line string, stop bool) {
	writer, ok := s.Lists.(Registering)
	if !ok {
		return false, "", false
	}
	proposed, err := writer.ProposeRow(ctx, event.Repository, rowProposal(row, event.Issue, pull.Body))
	switch {
	case errors.Is(err, github.ErrBranchNotOurs):
		return false, strings.TrimSpace(strings.TrimPrefix(command.ListBranchWorkedOn(pull.Branch, pull.Number), "- ")),
			true
	case errors.Is(err, github.ErrRowExists):
		return false, "", true
	case err != nil:
		s.Logger.Warn("could not bring the list pull request up to date", "issue", event.Issue, "error", err)
		return false, command.NotDone(fmt.Sprintf("bring #%d, the pull request adding your row, up to date",
			pull.Number), whyNot(err)), true
	}
	return !proposed.Unchanged, "", false
}

// turnRowPullRequest makes the bot's own pull request a draft or ready for
// review, says why on it, and gives the line for the registration's reply -
// what it did, or what stopped it - and whether it was done. The pull
// request's comment is a courtesy to whoever is looking at it: one that fails
// is logged, and the turn stands.
func (s *Server) turnRowPullRequest(ctx context.Context, event mention, number int, draft bool) (string, bool) {
	turn, done, say := "ready for review again", command.RowPullRequestReadyAgain(number),
		command.RowPullRequestReady(event.Issue)
	if draft {
		turn, done, say = "a draft", command.RowPullRequestWaits(number), command.RowPullRequestDrafted(event.Issue)
	}
	drafts, ok := s.Lists.(RowDrafts)
	if !ok {
		return command.NotDone(fmt.Sprintf("make #%d, the pull request adding your row, %s", number, turn),
			"this deployment cannot change the pull requests of the lists"), false
	}
	if err := drafts.SetRowPullRequestDraft(ctx, event.Repository, number, draft); err != nil {
		s.Logger.Warn("could not turn the list pull request", "pull", number, "draft", draft, "error", err)
		return command.NotDone(fmt.Sprintf("make #%d, the pull request adding your row, %s", number, turn),
			whyNot(err)), false
	}
	if _, err := s.Lists.Comment(ctx, event.Repository, number, say); err != nil {
		s.Logger.Warn("could not say why on the list pull request", "pull", number, "error", err)
	}
	return done, true
}

// followRowPullRequest is the sweep's nightly look at the bot's own pull
// request adding an included registrant's row, whatever else the inclusion
// is waiting for: the registration is checked again, as an `include check`
// would, and a pull request turned - an ORCID record fixed, a row edited
// without asking - is said on the registration with the verdict that turned
// it. Nothing to say otherwise; nothing is looked at when there is no pull
// request of the bot's open.
func (s *Server) followRowPullRequest(ctx context.Context, event mention, record followup.Record) (answered, bool) {
	drafts, ok := s.Lists.(RowDrafts)
	if !ok {
		return answered{}, false
	}
	pull, err := drafts.OwnRowPullRequest(ctx, event.Repository, record.Subject)
	if err != nil || pull.Number == 0 || pull.By != "" {
		return answered{}, false
	}
	report, inComment, unread, err := s.recheckRegistration(ctx, event)
	if err != nil {
		s.Logger.Warn("could not read a registration", "issue", event.Issue, "error", err)
		return answered{}, false
	}
	if unread != "" || !strings.EqualFold(report.Row.Login(), record.Subject) {
		return answered{}, false
	}
	step := s.stepRow(ctx, event, report, pull, pull.Draft)
	if !step.turned {
		return answered{}, false
	}
	// No fresh record: the turn is the world's to remember, and the
	// inclusion's own reminders keep the pace they had.
	return answered{body: command.RegistrationReply(report, false, inComment, step.line)}, true
}

// recheckRegistration checks a registration again from the sweep, which has
// the issue's number and not its first post: read it first.
func (s *Server) recheckRegistration(ctx context.Context, event mention) (registration.Report, bool, string, error) {
	reader, err := s.listsReader()
	if err != nil {
		return registration.Report{}, false, "", err
	}
	issue, err := reader.Issue(ctx, event.Repository, event.Issue)
	if err != nil {
		return registration.Report{}, false, "", err
	}
	event.IssueAuthor, event.IssueBody = issue.Author, issue.Body
	report, inComment, unread := s.checkRegistration(ctx, event)
	return report, inComment, unread, nil
}
