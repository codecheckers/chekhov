package bot

import (
	"context"
	"errors"
	"strings"

	"github.com/codecheckers/chekhov/internal/command"
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

// include is the reply to `include` on a registration issue.
func (s *Server) include(ctx context.Context, event mention) string {
	report := registration.Check(ctx, event.IssueBody, s.registrationWorld(event))
	if !report.Includable() {
		return command.IncludeIncomplete(report)
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
	return out.String()
}

// proposeListing opens the pull request adding the row, or says why it did
// not.
func (s *Server) proposeListing(ctx context.Context, event mention, row registration.Row) string {
	file := registration.RegistrationList
	writer, ok := s.Lists.(Registering)
	if !ok {
		return command.NotFromHere("open a pull request")
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
	if note := s.intoTheTeam(ctx, login, team); note != "" {
		out.WriteString(command.Item(note))
	} else {
		out.WriteString(command.IncludeInTeam(login, organisation, team))
	}
	if invitation.Owner() {
		out.WriteString(command.IncludeOwner(login, organisation))
	}
	return out.String()
}
