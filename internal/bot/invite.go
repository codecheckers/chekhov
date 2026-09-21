package bot

import (
	"context"
	"fmt"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/suggest"
)

// Inviting a new codechecker into the organisation's team.
//
// The bot's only write outside the register, and the only thing it does that
// changes what a person may do. What it is allowed to be is fenced in
// internal/github/invite.go, the one place that changes membership; what is
// here is the reading of the world around it - who is already in the team,
// who already has a row on a list - and the refusals for what this deployment
// cannot reach. See codecheckers/chekhov#42.

// Inviter is the part of the reply path that adds somebody to a team, and
// reads whether they are in it. A reply path that cannot - the command line
// preview, the test recorder - simply does not, and the reply says inviting is
// not configured rather than pretending an invitation was sent.
//
// It is deliberately this narrow. There is no method here that removes
// anybody, and none that sets a role.
type Inviter interface {
	TeamMembership(ctx context.Context, organisation, team, handle string) (string, bool, error)
	AddToTeam(ctx context.Context, organisation, team, handle string) (login, state string, err error)
}

// The reply path a deployment uses can invite, which a type assertion would
// otherwise only discover at runtime - as a command refusing with a message
// that sends an editor to audit a token that is perfectly fine.
var _ Inviter = (*github.Client)(nil)

// invite asks the organisation to add somebody to the codecheckers team.
//
// The row in the codechecker list stays a person's job: it carries a name, an
// ORCID and what they work with, none of which the bot can know. What it can
// do is the step that needs a permission nobody should have to ask for by
// hand.
func (s *Server) invite(ctx context.Context, parsed command.Command, services *check.Services) string {
	handle, err := command.ParseInvite(parsed.Args)
	if err != nil {
		return fmt.Sprintf("%s\n", err)
	}

	organisation, team := s.Settings.TeamOrganisation(), s.Settings.CodecheckersTeam()
	if organisation == "" || team == "" {
		return "I cannot invite anybody: this deployment names no organisation and " +
			"codecheckers team to invite into.\n"
	}
	inviter, ok := s.Replies.(Inviter)
	if !ok {
		return fmt.Sprintf("I cannot add anybody to `%s/%s`: this deployment is not set up to "+
			"manage team membership. See docs/github-token.md.\n", organisation, team)
	}

	onList, unread := s.onACodecheckerList(handle, services)
	invitation := command.Invitation{
		Handle: handle, Team: organisation + "/" + team,
		OnList: onList, Unread: listsUnread(unread), Lists: s.codecheckerListPages(),
	}

	// Asked before anything is written, so that "already a member" is an
	// answer rather than a write that happens to be idempotent - and so that
	// somebody in the team but not on a list is told which of the two is
	// missing.
	//
	// The membership cache is a hint here, not the verdict. It is a day old at
	// worst, and it is trusted elsewhere to decide what somebody may do, where
	// being out of date only ever withholds a privilege. Telling an editor
	// there is nothing to do is the opposite: somebody removed from the team
	// this morning would get no invitation and no reason.
	if s.Teams != nil && s.Teams.Has(ctx, team, handle) {
		state, inTeam, err := inviter.TeamMembership(ctx, organisation, team, handle)
		switch {
		case err != nil:
			return fmt.Sprintf("I could not check whether `@%s` is already in `%s/%s`: %s\n",
				handle, organisation, team, err)
		case inTeam && state == github.MembershipPending:
			// Invited already, and still not in the team: the invitation is
			// what is outstanding, and saying "nothing to do" would have an
			// editor waiting for somebody who has not accepted.
			invitation.Outcome = command.Invited
			return command.InvitedReply(invitation)
		case inTeam:
			invitation.Outcome = command.AlreadyAMember
			return command.InvitedReply(invitation)
		}
	}

	login, state, err := inviter.AddToTeam(ctx, organisation, team, handle)
	if err != nil {
		// Said plainly, and never as though the invitation had been sent: a
		// missing permission is the deployment's problem to fix, and a handle
		// that is not a person is the editor's.
		return fmt.Sprintf("I could not invite `@%s`: %s\n", handle, err)
	}
	if login != "" {
		// GitHub's own capitalisation of the account, now that it is known.
		invitation.Handle = login
	}

	switch state {
	case github.MembershipActive:
		invitation.Outcome = command.Added
		// The cache is a day old at worst, and somebody who is in the team now
		// should not have to wait it out to be treated as a codechecker.
		if s.Teams != nil {
			s.Teams.Refresh(ctx, team)
		}
	case github.MembershipPending:
		invitation.Outcome = command.Invited
	default:
		// Neither state GitHub documents. The write went through, so say that
		// and no more: claiming an immediate membership would assert what the
		// answer did not.
		invitation.Outcome = command.Unclear
	}
	return command.InvitedReply(invitation)
}

// onACodecheckerList reports whether somebody already has a row on one of the
// lists, and which lists could not be read.
//
// An unreadable list answers "not on one", which is the safe direction: the
// reply then says the row is still to be added, and mentioning a row that
// exists is a smaller mistake than never mentioning one that does not. What it
// could not read is returned rather than swallowed, because "not on a list" and
// "I could not see the list" are different things to tell an editor.
func (s *Server) onACodecheckerList(handle string, services *check.Services) (bool, []string) {
	if !services.Enabled() {
		return false, s.Settings.CodecheckerLists()
	}
	codecheckers, unread := suggest.Load(services, s.Settings)
	for _, codechecker := range codecheckers {
		// Both sides come through command.Handle, so both are lowercased.
		if codechecker.Handle == handle {
			return true, unread
		}
	}
	return false, unread
}
