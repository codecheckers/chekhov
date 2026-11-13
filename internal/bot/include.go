package bot

import (
	"context"
	"errors"
	"fmt"
	"github.com/codecheckers/chekhov/config"
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
// it is asked, and only when there is no invitation to see asks the editor
// who ran it to send one - which only an owner can, so then `include` is
// refused to any other editor before anything is done.

// include is the reply to `include` on a registration issue, and the record
// the nightly sweep follows it up from: until they are in the organisation,
// in the team and on the list, and then the welcome.
func (s *Server) include(ctx context.Context, event mention) answered {
	report, inComment, unread := s.checkRegistration(ctx, event)
	if unread != "" {
		return said(unread)
	}
	if !report.Includable() {
		return said(command.IncludeIncomplete(report, inComment))
	}
	login, editor := report.Row.Login(), command.Handle(event.Author)
	if refusal := s.mayInclude(ctx, login, editor); refusal != "" {
		return said(refusal)
	}

	var out strings.Builder
	out.WriteString(command.IncludeHeading(login))
	// What holds nothing up and is still for an editor to see: another list,
	// the institutional team.
	out.WriteString(command.IncludeWarnings(report))
	if report.Listed() {
		// The pull request was merged, or the row was there before: the
		// organisation's half is what is left to look at.
		out.WriteString(command.AlreadyListed(login, registration.RegistrationList))
	} else {
		out.WriteString(s.proposeListing(ctx, event, report.Row))
	}
	step, inTeam := s.organisationStep(ctx, login, editor)
	out.WriteString(step)
	// By is the editor, whom the sweep's reminder asks again rather than the
	// owners.
	record := followup.Record{Kind: followup.KindInclusion, Subject: strings.ToLower(login),
		By: editor, Team: s.Settings.CodecheckersTeam()}
	if report.Listed() && inTeam {
		// On the list and in the team: nothing is left to follow up, so the
		// welcome comes now, as the sweep would post it - asking `include`
		// again after the merge finishes the job rather than waiting a night.
		welcome, done := s.welcome(ctx, event, record)
		return answered{body: out.String() + "\n" + welcome, follow: done}
	}
	return answered{body: out.String(), follow: s.followUp(event, record)}
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
	return command.ListPullRequestOpened(file, pull.Number, pull.Updated, pull.Unchanged) +
		s.requestReview(ctx, event.Repository, pull.Number, row.Login(), event.Author)
}

// Reviewing is the part of the lists' reply path that asks for a review of
// the pull request adding a row.
type Reviewing interface {
	RequestReview(ctx context.Context, repository string, number int, reviewers []string) error
	Reviewed(ctx context.Context, repository string, number int) (bool, error)
}

var _ Reviewing = (*github.Client)(nil)

// requestReview asks for a review of the pull request: a person confirms an
// ORCID and a contact before they are listed, and the bot never merges (#49).
// Whom is the settings' lists.reviewers - the editor who ran `include` by
// default - and never the registrant, who cannot confirm their own row.
func (s *Server) requestReview(ctx context.Context, repository string, number int, registrant, asker string) string {
	reviewing, ok := s.Lists.(Reviewing)
	if !ok {
		return command.NotFromHere("ask for a review")
	}
	// Asked once: a pull request with a reviewer requested or a review given
	// is not asked again - which also asks again for one whose first request
	// failed, on the next `include`.
	switch reviewed, err := reviewing.Reviewed(ctx, repository, number); {
	case err != nil:
		s.Logger.Warn("could not read the reviews of a pull request", "pull", number, "error", err)
		return command.Item(command.NotDone("tell whether a review is asked for already, so I have asked nobody",
			whyNot(err)))
	case reviewed:
		return ""
	}
	candidates, configured, unread := s.reviewers(ctx, asker)
	if unread != "" {
		return unread
	}
	var reviewers []string
	for _, candidate := range candidates {
		if !strings.EqualFold(candidate, registrant) {
			reviewers = append(reviewers, candidate)
		}
	}
	if len(reviewers) == 0 {
		return command.NoReviewer(number)
	}
	if err := reviewing.RequestReview(ctx, repository, number, reviewers); err != nil {
		s.Logger.Warn("could not request a review", "pull", number, "error", err)
		return command.Item(command.NotDone("ask for a review", whyNot(err)))
	}
	return command.ReviewRequested(number, reviewers, configured)
}

// reviewers are whom the settings say to ask for a review, whether they are
// the development stand-ins rather than the people the mode names, and why
// they could not be read when they could not. A development deployment asks
// `teams.owners` for the owners and for the editors alike, as every reply
// that notifies a group does there.
func (s *Server) reviewers(ctx context.Context, asker string) (handles []string, configured bool, unread string) {
	mode, named := s.Settings.ListReviewers()
	switch mode {
	case config.ReviewByAsker:
		return []string{asker}, false, ""
	case config.ReviewByOwners:
		request, readable := s.ownersRequest(ctx, command.OutsideRequest{Organisation: s.Settings.TeamOrganisation()})
		switch {
		case !readable:
			return nil, false, command.NotFromHere("read who the owners are, to ask for a review")
		case request.Unreadable != "":
			return nil, false, command.Item(command.NotDone("read who the owners are, so I have asked nobody "+
				"to review it", request.Unreadable))
		}
		return request.Owners, request.Configured, ""
	case config.ReviewByEditors:
		if stand := s.Settings.OwnersOverride(); len(stand) > 0 {
			return stand, true, ""
		}
		editors := s.Teams.Members(ctx, s.Settings.EditorsTeam())
		if len(editors) == 0 {
			return nil, false, command.Item(command.NotDone("read the editors team, so I have asked nobody "+
				"to review it", "a team that cannot be read has no members"))
		}
		return editors, false, ""
	}
	return named, false, ""
}

// organisationStep checks the owners' half and does the bot's: where the
// registrant stands with the organisation, and the team once they are in it.
//
// Decided by looking, every time: an invitation the owners sent since the
// last `include` is seen, and asking again changes nothing that is done.
//
// inTeam says they are in the organisation and in the team now, which with
// the row on the list is the whole of an inclusion. editor ran `include`, and
// is the one asked to send an invitation, an owner as mayInclude saw to.
func (s *Server) organisationStep(ctx context.Context, login, editor string) (step string, inTeam bool) {
	organisation, team := s.Settings.TeamOrganisation(), s.Settings.CodecheckersTeam()
	reader, ok := s.Replies.(Organisation)
	if !ok || organisation == "" {
		return command.NotFromHere("read the organisation"), false
	}
	// A team the bot may not add to is said here, in its own words, rather
	// than as the fence's refusal - and none at all is not "in it already".
	if team == "" || !s.managedTeam(team) {
		return command.TeamNotManaged(organisation, team), false
	}
	invitation, err := reader.OrganisationMembership(ctx, organisation, login)
	switch {
	case err != nil:
		s.Logger.Warn("could not read where somebody stands with the organisation",
			"handle", login, "organisation", organisation, "error", err)
		return command.IncludeUnknown(login, organisation, whyNot(err)), false
	case invitation.Invited():
		return command.IncludeInvited(login, organisation, team), false
	case !invitation.Joined():
		return command.IncludeOutsideReply(command.OutsideRequest{
			Handle: login, Organisation: organisation, Team: team}, editor), false
	}

	var out strings.Builder
	note, in := s.intoTheTeam(ctx, login, team)
	if note != "" {
		out.WriteString(command.Item(note))
	} else {
		out.WriteString(command.IncludeInTeam(login, organisation, team))
	}
	if invitation.Owner() {
		out.WriteString(command.IncludeOwner(login, organisation))
	}
	return out.String(), in
}

// mayInclude refuses `include` from an editor who is not an owner of the
// organisation, when the registrant is neither in it nor invited: the
// command would end in asking the editor for an invitation, which only an
// owner can send. Decided before anything is done, so that a refusal leaves
// no pull request and no record behind.
//
// The editor's own role in the organisation, read from GitHub - not
// `teams.owners`, which only says whom a development deployment notifies.
// Anything that cannot be read lets the command go on, where
// organisationStep says so in its own words; only the editor's role that
// cannot be read refuses, because whether they may cannot be told.
func (s *Server) mayInclude(ctx context.Context, login, editor string) string {
	organisation := s.Settings.TeamOrganisation()
	reader, ok := s.Replies.(Organisation)
	if !ok || organisation == "" {
		return ""
	}
	if registrant, known := s.whereStands(ctx, login); !known || registrant.Invited() || registrant.Joined() {
		return ""
	}
	role, err := reader.OrganisationMembership(ctx, organisation, editor)
	switch {
	case err != nil:
		s.Logger.Warn("could not read whether an editor is an owner",
			"handle", editor, "organisation", organisation, "error", err)
		return command.IncludeNotAnOwner(login, organisation, whyNot(err))
	case !role.Joined() || !role.Owner():
		return command.IncludeNotAnOwner(login, organisation, "")
	}
	return ""
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
// checked as the editor's command checked it - no invitation is reminded to
// the editor in the record's By, as `assign`'s ask is to the owners; a
// pending one is the registrant's to accept, and they
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
		return s.tellOnce(event, record, invitationWaitingAfter, command.InvitationWaiting(organisation)), true, nil
	case !invitation.Joined():
		// The owners are read only when there is a reminder left to post.
		if !record.Older(remindAfter, time.Now()) || record.Reminders > mostReminders {
			return answered{}, true, nil
		}
		request := command.OutsideRequest{Handle: record.Subject, Organisation: organisation, Team: record.Team}
		if record.By == "" {
			// A record from before it named the editor: the owners are named
			// instead, and only then read.
			request, _ = s.ownersRequest(ctx, request)
		}
		return s.reminder(event, record, command.IncludeOutsideReply(request, record.By),
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

	body, done := s.welcome(ctx, event, record)
	if note != "" {
		body = note + "\n\n" + body
	}
	return answered{body: body, follow: done}, false, nil
}

// welcome is the end of an inclusion, from the sweep or from `include` alike:
// the issue closed, and the welcome with a settled record, so that it is said
// once whichever said it.
func (s *Server) welcome(ctx context.Context, event mention, record followup.Record) (string, *followup.Record) {
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
	return command.Welcome(s.Settings.TeamOrganisation(), record.Team, s.Settings.ListsRepository(),
		registration.RegistrationList, closing), done
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
	if !record.Older(remindAfter, time.Now()) {
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
		return s.pullRequestWaiting(ctx, event, record, pull), true, nil
	}
	if record.Reminders > mostReminders {
		return answered{}, true, nil
	}
	next := s.followUp(event, record)
	if next != nil {
		next.Reminders = record.Reminders + 1
	}
	return answered{body: command.NoRowPullRequest(record.Subject, registration.RegistrationList),
		follow: next}, true, nil
}

// pullRequestWaiting reminds the owners of the row's pull request, open and
// waiting for an editor: once it has waited a week, then weekly, by its own
// age - GitHub's, not the record's, which each reminder renews - and for as
// many weeks as any ask is repeated. The record says only when it was last
// said, so that a sweep twice in a night says it once.
func (s *Server) pullRequestWaiting(ctx context.Context, event mention, record followup.Record,
	pull github.RowPullRequest) answered {
	age := time.Since(pull.Created)
	if pull.Created.IsZero() || age < pullRequestWaitingAfter ||
		age > pullRequestWaitingAfter*(mostReminders+1) || !record.Older(pullRequestWaitingAfter, time.Now()) {
		return answered{}
	}
	request, readable := s.ownersRequest(ctx, command.OutsideRequest{Organisation: s.Settings.TeamOrganisation()})
	var owners []string
	if readable && request.Unreadable == "" {
		// Owners that cannot be read leave the reminder unaddressed rather
		// than unsaid.
		owners = request.Owners
	}
	return answered{body: command.PullRequestWaiting(owners, request.Configured, pull.Number,
		command.Days(daysSince(pull.Created, time.Now()))), follow: s.followUp(event, record)}
}

// pullRequestWaitingAfter is how long the pull request adding a row waits for
// an editor before the owners are reminded of it.
const pullRequestWaitingAfter = 7 * 24 * time.Hour

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
