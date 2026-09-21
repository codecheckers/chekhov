package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// An inviter stands in for the one write outside the register, so a test can
// read what the bot asked for without an organisation.
type inviter struct {
	recorder
	asked              []string
	state              string
	err                error
	organisation, team string
	// inTeam is what the organisation says when asked, which the handler
	// trusts over its own cache.
	inTeam map[string]bool
}

func (i *inviter) TeamMembership(_ context.Context, _, _, handle string) (string, bool, error) {
	if i.inTeam[handle] {
		return github.MembershipActive, true, nil
	}
	return "", false, nil
}

func (i *inviter) AddToTeam(_ context.Context, organisation, team, handle string) (string, string, error) {
	i.asked = append(i.asked, handle)
	i.organisation, i.team = organisation, team
	if i.err != nil {
		return "", "", i.err
	}
	state := i.state
	if state == "" {
		state = github.MembershipPending
	}
	// GitHub answers with its own capitalisation of the account.
	return strings.ToUpper(handle[:1]) + handle[1:], state, nil
}

// invitingServer is a bot that can invite, reading the same stubbed lists the
// suggesting tests use: @a-codechecker has a row, @nuest is in the team
// without one, and @a-newcomer is neither.
func invitingServer(t *testing.T) (*Server, *inviter) {
	t.Helper()
	server, _ := testServer(t)
	replies := &inviter{}
	server.Replies = replies
	listing(t, server)
	// The organisation agrees with the team cache unless a test says
	// otherwise: @nuest and @a-codechecker are in the team.
	replies.inTeam = map[string]bool{"nuest": true, "a-codechecker": true}
	return server, replies
}

// The invitation is sent, and the row in the list is said to be a person's
// job - it carries a name, an ORCID and what they work with, none of which
// the bot can know.
func TestInviteSendsTheInvitationAndSaysWhatIsLeft(t *testing.T) {
	server, replies := invitingServer(t)

	reply := answer(t, server, command.Invite, []string{"@a-newcomer"}, "")

	if len(replies.asked) != 1 || replies.asked[0] != "a-newcomer" {
		t.Fatalf("the bot asked to add %v", replies.asked)
	}
	if replies.team != server.Settings.CodecheckersTeam() {
		t.Errorf("added to the %q team, want the codecheckers team", replies.team)
	}
	for _, want := range []string{"invited", "accept it", "still a person's job"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

// Somebody already in the team and on the list needed nothing, and the reply
// says so rather than sending a write.
func TestInviteSaysWhenThereIsNothingToDo(t *testing.T) {
	server, replies := invitingServer(t)

	reply := answer(t, server, command.Invite, []string{"@a-codechecker"}, "")

	if len(replies.asked) != 0 {
		t.Errorf("a write went out for somebody already in the team: %v", replies.asked)
	}
	if !strings.Contains(reply, "nothing to do") {
		t.Errorf("reply:\n%s", reply)
	}
}

// In the team but not on a list is a different problem, and the one worth
// fixing: without a row nobody can be suggested for a check.
func TestInviteTellsAMemberWithNoRowApart(t *testing.T) {
	server, replies := invitingServer(t)

	reply := answer(t, server, command.Invite, []string{"@nuest"}, "")

	if len(replies.asked) != 0 {
		t.Errorf("a write went out for somebody already in the team: %v", replies.asked)
	}
	if !strings.Contains(reply, "a different problem") {
		t.Errorf("reply:\n%s", reply)
	}
}

// A reply path that cannot invite says so, rather than pretending an
// invitation was sent. The command line preview is one.
func TestInviteWithoutTheAbilitySaysSo(t *testing.T) {
	server, _ := testServer(t)

	reply := answer(t, server, command.Invite, []string{"@a-newcomer"}, "")

	if !strings.Contains(reply, "not set up to manage team membership") {
		t.Errorf("reply:\n%s", reply)
	}
}

// Whatever went wrong, the reply never reads as an invitation that was sent.
func TestInviteThatFailedNeverReadsAsSent(t *testing.T) {
	server, replies := invitingServer(t)
	replies.err = github.ErrNotPermitted

	reply := answer(t, server, command.Invite, []string{"@a-newcomer"}, "")

	if !strings.Contains(reply, "could not invite") {
		t.Errorf("reply:\n%s", reply)
	}
	if strings.Contains(reply, "I have invited") || strings.Contains(reply, "I have added") {
		t.Errorf("a failure read as a success:\n%s", reply)
	}
}

// Inviting is an editor's command: a stranger is not offered it and cannot
// run it.
func TestInviteIsForEditors(t *testing.T) {
	server, replies := invitingServer(t)

	reply := server.answer(context.Background(), mention{
		Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-passer-by",
	}, command.Command{Name: command.Invite, Args: []string{"@a-newcomer"}})

	if len(replies.asked) != 0 {
		t.Errorf("a stranger invited somebody: %v", replies.asked)
	}
	if !strings.Contains(reply, "is for") {
		t.Errorf("reply:\n%s", reply)
	}
	if strings.Contains(command.Listing(nil), "invite") {
		t.Error("a command a stranger may not run should be absent from their listing")
	}
}
