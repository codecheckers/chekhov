package bot

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/registration"
)

// `include`: an editor adds a registered codechecker (#49).
//
// Two halves, as with `assign`: the row, which the bot proposes in a pull
// request on the lists for an editor to merge, and the organisation, where
// the owners invite and the bot does the team. The second half is checked
// rather than assumed - the owners' part is a thing a person does by hand,
// and the ways it goes wrong are an invitation never sent, sent to a mistyped
// handle, or in the wrong role - so the reply says where it stands each time
// it is asked, and asks the owners only when there is no invitation to see.

// include is the reply to `include` on a registration issue, and the record
// the nightly sweep follows it up from: until they are in the organisation,
// in the team and on the list, and then the welcome.
func (s *Server) include(ctx context.Context, event mention) answered {
	report := registration.Check(ctx, event.IssueBody, s.registrationWorld(event))
	if !report.Includable() {
		return said(command.IncludeIncomplete(report))
	}
	login := report.Row.Login()

	var out strings.Builder
	out.WriteString(command.IncludeHeading(login))
	if report.Listed() {
		// The pull request was merged, or the row was there before: the
		// organisation's half is what is left to look at.
		out.WriteString(command.AlreadyListed(login, registration.RegistrationList))
	} else {
		out.WriteString(s.proposeListing(ctx, event, report.Row))
	}
	out.WriteString(s.organisationStep(ctx, login))
	return answered{body: out.String(), follow: s.followUp(event, followup.Record{
		Kind: followup.KindInclusion, Subject: strings.ToLower(login), Team: s.Settings.CodecheckersTeam()})}
}

// proposeListing opens the pull request adding the row, or says why it did
// not.
func (s *Server) proposeListing(ctx context.Context, event mention, row registration.Row) string {
	file := registration.RegistrationList
	writer, ok := s.Lists.(Registering)
	if !ok {
		return command.NotFromHere("open a pull request")
	}
	// A pull request already adding the row is the one to merge, whoever
	// opened it: an editor's, opened by hand from a branch of their own, is
	// not opened a second time beside. The bot's own is brought up to date
	// below, as asking again always did. Could not tell is the old path: the
	// bot's branch is still fenced by name.
	open, err := s.openRowPullRequest(ctx, event, row.Login())
	switch {
	case err != nil:
		// Not knowing whether an editor's pull request is there is not a
		// reason to open a second one beside it.
		s.Logger.Warn("could not read the open pull requests of the lists", "issue", event.Issue, "error", err)
		return command.Item(command.NotDone("tell whether a pull request already adds the row, so I have opened none",
			whyNot(err)))
	case open.Number != 0 && open.By != "":
		return command.ListPullRequestBySomebody(file, open.Number, open.By)
	}
	pull, err := writer.ProposeRow(ctx, event.Repository, github.RowProposal{
		Row: row.Cells(),
		// Defused as the body is: the name is the registrant's own text.
		Title:   command.Defuse(command.ListPullRequestTitle(row.Name, file)),
		Message: command.ListCommitMessage(row.Login(), file, event.Issue),
		Body:    command.Defuse(command.ListPullRequestBody(row.Login(), file, event.Issue, event.Author)),
	})
	switch {
	case errors.Is(err, github.ErrRowExists):
		return command.AlreadyListed(row.Login(), file)
	case errors.Is(err, github.ErrBranchNotOurs) && open.Number != 0:
		// Somebody's commits on the bot's branch, and its pull request open:
		// theirs to finish, which may have changed the row.
		return command.ListBranchWorkedOn(pull.Branch, open.Number)
	case errors.Is(err, github.ErrBranchNotOurs):
		return command.ListBranchNotOurs(pull.Branch)
	case err != nil:
		s.Logger.Warn("the list pull request could not be opened", "issue", event.Issue, "error", err)
		return command.ListPullRequestFailed(failure(err))
	}
	return command.ListPullRequestOpened(file, pull.Number, pull.Updated, pull.Unchanged)
}

// organisationStep checks the owners' half and does the bot's: where the
// registrant stands with the organisation, and the team once they are in it.
//
// Decided by looking, every time: an invitation the owners sent since the
// last `include` is seen, and asking again changes nothing that is done.
func (s *Server) organisationStep(ctx context.Context, login string) string {
	organisation, team := s.Settings.TeamOrganisation(), s.Settings.CodecheckersTeam()
	reader, ok := s.Replies.(Organisation)
	if !ok || organisation == "" {
		return command.NotFromHere("read the organisation")
	}
	// A team the bot may not add to is said here, in its own words, rather
	// than as the fence's refusal - and none at all is not "in it already".
	if team == "" || !s.managedTeam(team) {
		return command.TeamNotManaged(organisation, team)
	}
	invitation, err := reader.OrganisationMembership(ctx, organisation, login)
	switch {
	case err != nil:
		s.Logger.Warn("could not read where somebody stands with the organisation",
			"handle", login, "organisation", organisation, "error", err)
		return command.IncludeUnknown(login, organisation, whyNot(err))
	case invitation.Invited():
		return command.IncludeInvited(login, organisation, team)
	case !invitation.Joined():
		// Able to read the organisation is able to read its owners: the one
		// interface covers both.
		request, _ := s.ownersRequest(ctx, command.OutsideRequest{
			Handle: login, Organisation: organisation, Team: team})
		return command.IncludeOutsideReply(request)
	}

	var out strings.Builder
	if note, _ := s.intoTheTeam(ctx, login, team); note != "" {
		out.WriteString(command.Item(note))
	} else {
		out.WriteString(command.IncludeInTeam(login, organisation, team))
	}
	if invitation.Owner() {
		out.WriteString(command.IncludeOwner(login, organisation))
	}
	return out.String()
}

// Closer is the part of the lists' reply path that closes a registration
// once it is done.
type Closer interface {
	CloseIssue(ctx context.Context, repository string, issue int) error
}

var _ Closer = (*github.Client)(nil)

// invitationWaitingAfter is how long an invitation waits before the
// registrant is told it is there. GitHub sends them one email, and the
// invitation expires after seven days: two leave it long enough for the
// email to have been read, and most of the week to accept in.
const invitationWaitingAfter = 2 * 24 * time.Hour

// followInclusion resumes an `include`, by looking: where the registrant
// stands with the organisation, the team and the list. The owners' half is
// checked as the editor's command checked it - no invitation is reminded,
// as `assign`'s ask is; a pending one is the registrant's to accept, and they
// are told once - and the bot's half is done as soon as it can be. When all
// three hold, the welcome, settled so that it is said once, and the issue is
// closed.
func (s *Server) followInclusion(ctx context.Context, event mention, record followup.Record,
	services *check.Services) (answered, bool, error) {
	organisation := s.Settings.TeamOrganisation()
	invitation, known := s.whereStands(ctx, record.Subject)
	if !known {
		return answered{}, false, fmt.Errorf("could not read whether `@%s` is in the organisation yet",
			record.Subject)
	}
	switch {
	case invitation.Invited():
		if record.Told || !record.Older(invitationWaitingAfter, time.Now()) {
			return answered{}, true, nil
		}
		told := s.followUp(event, record)
		if told != nil {
			told.Told = true
		}
		return answered{body: command.InvitationWaiting(organisation), follow: told}, true, nil
	case !invitation.Joined():
		// The owners are read only when there is a reminder left to post.
		if !record.Older(remindAfter, time.Now()) || record.Reminders > mostReminders {
			return answered{}, true, nil
		}
		request, _ := s.ownersRequest(ctx, command.OutsideRequest{
			Handle: record.Subject, Organisation: organisation, Team: record.Team})
		return s.reminder(event, record, command.IncludeOutsideReply(request),
			fmt.Sprintf("%s %s", command.Bot, command.Include)), true, nil
	}

	note, inTeam := s.intoTheTeam(ctx, record.Subject, record.Team)
	listed, err := s.onTheList(record.Subject, services)
	switch {
	case !inTeam && note != "":
		// The team could not be done - a refusal, an invitation to the team
		// not accepted. Said every three days at most, with a fresh record to
		// measure from, rather than every night.
		if !record.Older(remindAfter, time.Now()) {
			return answered{}, true, nil
		}
		return answered{body: command.IncludeTeamNote(note), follow: s.followUp(event, record)}, true, nil
	case err != nil && note != "":
		// Put in the team just now, which is said once: tomorrow's read finds
		// them there. The list is asked again then.
		return answered{body: note + "\n"}, true, nil
	case err != nil:
		return answered{}, true, err
	case inTeam && !listed && note == "":
		return s.noRowPullRequest(ctx, event, record)
	case !inTeam || !listed:
		if note == "" {
			// Nothing done, nothing to say: the team for whatever stopped it
			// last time.
			return answered{}, true, nil
		}
		return answered{body: command.IncludeTeamNote(note)}, true, nil
	}

	why := ""
	if closer, ok := s.Lists.(Closer); !ok {
		why = "this deployment cannot"
	} else if err := closer.CloseIssue(ctx, event.Repository, event.Issue); err != nil {
		s.Logger.Warn("could not close a registration", "issue", event.Issue, "error", err)
		why = whyNot(err)
	}
	closing := ""
	if why != "" {
		closing = "\n" + command.NotDone("close this issue", why) + "\n"
	}
	done := s.followUp(event, record)
	if done != nil {
		done.Settled = true
	}
	body := command.Welcome(organisation, record.Team, s.Settings.ListsRepository(),
		registration.RegistrationList, closing)
	if note != "" {
		body = note + "\n\n" + body
	}
	return answered{body: body, follow: done}, false, nil
}

// RowPullRequests is the part of the lists' reply path that says whether a
// row's pull request is still open.
type RowPullRequests interface {
	OpenRowPullRequest(ctx context.Context, repository, key string) (github.RowPullRequest, error)
}

var _ RowPullRequests = (*github.Client)(nil)

// openRowPullRequest is the pull request open to add the row, anybody's, the
// zero one for none - or for a reply path that cannot tell, which only the
// bot's own branch fences then.
func (s *Server) openRowPullRequest(ctx context.Context, event mention, login string) (github.RowPullRequest, error) {
	reader, ok := s.Lists.(RowPullRequests)
	if !ok {
		return github.RowPullRequest{}, nil
	}
	return reader.OpenRowPullRequest(ctx, event.Repository, login)
}

// noRowPullRequest is the one thing left being the row: quiet while its pull
// request is open and waiting for an editor, and said when there is none to
// merge - `include` could not open it, or it was closed unmerged - every
// three days at most and as often as the owners are reminded, so that a
// registration does not wait in silence for a pull request that will never
// come.
func (s *Server) noRowPullRequest(ctx context.Context, event mention, record followup.Record) (answered, bool, error) {
	if !record.Older(remindAfter, time.Now()) || record.Reminders > mostReminders {
		return answered{}, true, nil
	}
	reader, ok := s.Lists.(RowPullRequests)
	if !ok {
		return answered{}, true, errors.New("this deployment cannot read the pull requests of the lists")
	}
	// Anybody's: an editor's pull request, opened by hand, is as good as the
	// bot's.
	pull, err := reader.OpenRowPullRequest(ctx, event.Repository, record.Subject)
	if err != nil {
		return answered{}, true, err
	}
	if pull.Number != 0 {
		return answered{}, true, nil
	}
	next := s.followUp(event, record)
	if next != nil {
		next.Reminders = record.Reminders + 1
	}
	return answered{body: command.NoRowPullRequest(record.Subject, registration.RegistrationList),
		follow: next}, true, nil
}

// onTheList reads the registration list as it is now, and says whether it has
// a row for the registrant: their pull request merged.
func (s *Server) onTheList(login string, services *check.Services) (bool, error) {
	for _, file := range s.Settings.ListFiles() {
		if path.Base(file) != registration.RegistrationList {
			continue
		}
		list := s.readLists(services, file)[0]
		if list.Err != nil {
			return false, fmt.Errorf("could not read %s: %w", registration.RegistrationList, list.Err)
		}
		return list.Holds(login), nil
	}
	return false, fmt.Errorf("no %s among the lists in the settings", registration.RegistrationList)
}
