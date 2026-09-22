package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// An organisation stands in for the membership GitHub holds, so a test can
// watch what the bot asked for without an organisation.
type organisation struct {
	// issueReader is the existing double for a reply path that also reads the
	// register's issues, which is how teamFor learns a check's labels.
	*issueReader
	members               map[string]bool
	inTeam                map[string]bool
	owners                []string
	ownersErr, membersErr error
	teamErr               error
	// pending is somebody an owner invited through the team who has not
	// accepted: GitHub answers 200 for them, and they are not in it.
	pending map[string]bool
	added   []string
	addedTo []string
}

// labelled is the check's own labels, which decide which team a codechecker
// belongs in.
func (o *organisation) labelled(labels ...string) {
	o.issues = []github.Issue{{Number: 1, Labels: labels}}
}

func (o *organisation) InOrganisation(_ context.Context, _, handle string) (bool, error) {
	if o.membersErr != nil {
		return false, o.membersErr
	}
	return o.members[handle], nil
}

func (o *organisation) Owners(_ context.Context, _ string) ([]string, error) {
	return o.owners, o.ownersErr
}

func (o *organisation) TeamMembership(_ context.Context, _, _, handle string) (bool, bool, error) {
	return o.inTeam[handle], o.pending[handle], o.teamErr
}

func (o *organisation) AddToTeam(_ context.Context, _, team, handle string) (string, error) {
	o.added = append(o.added, handle)
	o.addedTo = append(o.addedTo, team)
	return handle, nil
}

// organised is a bot whose reply path can read and change membership.
func organised(t *testing.T) (*Server, *organisation) {
	t.Helper()
	server, _ := testServer(t)
	replies := &organisation{
		issueReader: &issueReader{recorder: &recorder{}},
		members:     map[string]bool{"nuest": true, "a-codechecker": true},
		inTeam:      map[string]bool{"a-codechecker": true},
		pending:     map[string]bool{},
		owners:      []string{"an-owner", "another-owner"},
	}
	server.Replies = replies
	// The shipped development settings name who to ask instead of the
	// organisation's owners, so that a test never notifies them. Cleared here
	// so that most tests exercise the reading of the real list; the override
	// has its own test.
	server.Settings.Chekhov.Teams.Owners = nil
	return server, replies
}

// Somebody outside the organisation cannot hold a role, so the role is not
// recorded and the owners are asked - mentioned, because the reply exists to
// reach them, which is the one exception to writing handles in backticks.
func TestAssigningSomebodyOutsideTheOrganisationAsksTheOwners(t *testing.T) {
	server, replies := organised(t)

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	for _, want := range []string{"@an-owner", "@another-owner", "not in the `codecheckers` organisation",
		"I will handle the rest", "codecheckers/codecheckers"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if len(replies.added) != 0 {
		t.Errorf("somebody outside the organisation was added to a team: %v", replies.added)
	}
	// The role is not recorded: they cannot act on the check yet.
	if strings.Contains(reply, "is now the") {
		t.Errorf("a role was recorded for somebody outside the organisation:\n%s", reply)
	}
}

// The owners are mentioned so that GitHub notifies them. Everywhere else a
// handle is in backticks; this reply is the exception, and the distinction is
// the point.
func TestTheOwnersAreMentionedAndTheSubjectIsNot(t *testing.T) {
	server, _ := organised(t)

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	if !strings.Contains(reply, "@an-owner") || strings.Contains(reply, "`@an-owner`") {
		t.Errorf("an owner is not mentioned in a way that notifies:\n%s", reply)
	}
	if !strings.Contains(reply, "`@a-newcomer`") {
		t.Errorf("the person being discussed should be in backticks:\n%s", reply)
	}
}

// Owners that could not be read are said so, rather than the reply asking
// nobody and reading as though it had asked somebody.
func TestOwnersThatCouldNotBeReadAreReported(t *testing.T) {
	server, replies := organised(t)
	replies.ownersErr = errors.New("GitHub answered 403")

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")
	if !strings.Contains(reply, "could not read who the owners are") {
		t.Errorf("reply:\n%s", reply)
	}
}

// Could not look is not the same as not a member, and must not cost somebody
// their role - but it must not license a team write either. A PUT on a team
// membership is how GitHub invites somebody who is not in the organisation,
// so acting on an unanswered question would be the one thing this bot must
// not do.
func TestAMembershipThatCouldNotBeReadRecordsTheRoleAndTouchesNoTeam(t *testing.T) {
	server, replies := organised(t)
	replies.membersErr = errors.New("GitHub answered 500")

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")
	if strings.Contains(reply, "not in the") {
		t.Errorf("an outage refused a role:\n%s", reply)
	}
	if len(replies.added) != 0 {
		t.Errorf("a team membership was written on a guess: %v", replies.added)
	}
	if strings.Contains(reply, "I have put") {
		t.Errorf("the reply claims a team change that did not happen:\n%s", reply)
	}
}

// A reply path that cannot ask at all - the command line preview - records the
// role and touches no team, for the same reason.
func TestAPreviewRecordsTheRoleAndTouchesNoTeam(t *testing.T) {
	server, _ := testServer(t)

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")
	if strings.Contains(reply, "not in the") || strings.Contains(reply, "I have put") {
		t.Errorf("a preview refused a role or claimed a team change:\n%s", reply)
	}
}

// A codechecker in the organisation but not in the team is put in it: the
// owners' half was the invitation, the team is the bot's.
func TestAssigningACodecheckerPutsThemInTheTeam(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	if len(replies.added) != 1 || replies.added[0] != "a-newcomer" {
		t.Fatalf("the bot added %v", replies.added)
	}
	if replies.addedTo[0] != "codecheckers" {
		t.Errorf("added to %q, want the ordinary team", replies.addedTo[0])
	}
	if !strings.Contains(reply, "I have put `@a-newcomer` in `codecheckers/codecheckers`") {
		t.Errorf("the reply does not say what was done:\n%s", reply)
	}
}

// An author is not a codechecker and does not belong in a codechecker team.
func TestAssigningAnAuthorTouchesNoTeam(t *testing.T) {
	server, replies := organised(t)
	replies.members["an-author"] = true

	answer(t, server, command.Assign, []string{"@an-author", "as", "author"}, "")
	if len(replies.added) != 0 {
		t.Errorf("an author was put in a codechecker team: %v", replies.added)
	}
}

// Somebody already in the team is left alone rather than added again.
func TestACodecheckerAlreadyInTheTeamIsNotAddedAgain(t *testing.T) {
	server, replies := organised(t)

	answer(t, server, command.Assign, []string{"@a-codechecker", "as", "codechecker"}, "")
	if len(replies.added) != 0 {
		t.Errorf("somebody already in the team was added again: %v", replies.added)
	}
}

// An institutional check is covered by an arrangement an institution made, and
// the people who check under it are kept in their own team - so the team
// follows from the check rather than from the person. This is what the managed
// allow-list exists for.
func TestAnInstitutionalCheckUsesTheInstitutionalTeam(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true
	replies.labelled("needs codechecker", "institution")

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	if len(replies.addedTo) != 1 || replies.addedTo[0] != "institutional-codecheckers" {
		t.Fatalf("added to %v, want the institutional team", replies.addedTo)
	}
	if !strings.Contains(reply, "institutional-codecheckers") {
		t.Errorf("the reply does not name the team:\n%s", reply)
	}

	// And the ask to the owners names it too, so they know where the person is
	// headed before they invite them.
	delete(replies.members, "a-newcomer")
	asked := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")
	if !strings.Contains(asked, "codecheckers/institutional-codecheckers") {
		t.Errorf("the request to the owners does not name the institutional team:\n%s", asked)
	}
}

// A check with no institution label gets the ordinary team, and so does one
// whose labels could not be read: the wrong team is a thing a person can fix,
// and refusing to name one would stop the assignment over a question nobody
// asked.
func TestAnOrdinaryCheckUsesTheOrdinaryTeam(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true
	replies.labelled("needs codechecker")

	answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")
	if len(replies.addedTo) != 1 || replies.addedTo[0] != "codecheckers" {
		t.Errorf("added to %v, want the ordinary team", replies.addedTo)
	}
}

// The bot works the team out from the check, and an editor may say otherwise
// in either direction: an institutional codechecker taking an ordinary check,
// or an ordinary one standing in on an institutional one. They know things the
// labels do not.
func TestAnEditorCanNameTheTeamEitherWay(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true

	// An institutional check, overridden to the ordinary team.
	replies.labelled("institution")
	answer(t, server, command.Assign,
		[]string{"@a-newcomer", "as", "codechecker", "in", "codecheckers"}, "")
	if len(replies.addedTo) != 1 || replies.addedTo[0] != "codecheckers" {
		t.Fatalf("added to %v, want the ordinary team the editor named", replies.addedTo)
	}

	// An ordinary check, overridden to the institutional team.
	server, replies = organised(t)
	replies.members["a-newcomer"] = true
	replies.labelled("needs codechecker")
	answer(t, server, command.Assign,
		[]string{"@a-newcomer", "as", "codechecker", "in", "institutional-codecheckers"}, "")
	if len(replies.addedTo) != 1 || replies.addedTo[0] != "institutional-codecheckers" {
		t.Errorf("added to %v, want the institutional team the editor named", replies.addedTo)
	}
}

// A team the settings do not manage is refused with the list, rather than
// leaving the request to be refused by the fence.
func TestATeamOutsideTheAllowListIsRefusedWithTheList(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true

	reply := answer(t, server, command.Assign,
		[]string{"@a-newcomer", "as", "codechecker", "in", "editors"}, "")
	if len(replies.added) != 0 {
		t.Errorf("a write went out for an unmanaged team: %v", replies.addedTo)
	}
	for _, want := range []string{"`codecheckers`", "`institutional-codecheckers`", "not in `editors`"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

// The override reaches the request to the owners too, so they know where the
// person is headed before they invite them.
func TestTheNamedTeamIsInTheRequestToTheOwners(t *testing.T) {
	server, _ := organised(t)

	reply := answer(t, server, command.Assign,
		[]string{"@a-newcomer", "as", "codechecker", "in", "institutional-codecheckers"}, "")
	if !strings.Contains(reply, "codecheckers/institutional-codecheckers") {
		t.Errorf("the request does not name the team the editor chose:\n%s", reply)
	}
}

// The membership read is only an optimisation - the write is idempotent - so a
// read that failed falls through to the write rather than returning nothing,
// which an editor would read as "the team was handled".
func TestAMembershipReadThatFailedStillAddsTheCodechecker(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true
	replies.teamErr = errors.New("GitHub answered 500")

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	if len(replies.added) != 1 {
		t.Fatalf("the add was skipped after a failed read: %v", replies.added)
	}
	if !strings.Contains(reply, "I have put") {
		t.Errorf("the reply says nothing about the team:\n%s", reply)
	}
}

// A deployment may name who to ask instead of reading the organisation's
// owners. That reply is the one place the bot writes a plain @mention, so a
// development deployment asks whoever is testing it - and says that is what it
// did, or the reply would read as though the owners had been notified.
func TestAConfiguredOwnersListIsAskedInsteadOfTheOrganisation(t *testing.T) {
	server, replies := organised(t)
	server.Settings.Chekhov.Teams.Owners = []string{"@a-tester"}

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	if !strings.Contains(reply, "@a-tester") {
		t.Errorf("the configured tester was not asked:\n%s", reply)
	}
	for _, owner := range replies.owners {
		if strings.Contains(reply, "@"+owner) {
			t.Errorf("an organisation owner was notified in a test: %s\n%s", owner, reply)
		}
	}
	if !strings.Contains(reply, "have not been notified") {
		t.Errorf("the reply does not say the owners were not asked:\n%s", reply)
	}
}

// The shipped development settings must carry that override, because the
// deployment that answers the testing register runs them.
func TestTheShippedDevelopmentSettingsDoNotNotifyTheOwners(t *testing.T) {
	server, _ := testServer(t)
	if len(server.Settings.OwnersOverride()) == 0 {
		t.Error("development settings would notify the organisation's real owners")
	}
}

// A membership GitHub calls "pending" is not in effect: somebody an owner
// invited through the team who has not accepted. Reading it as "in the team"
// would leave the editor believing the team was handled.
func TestAPendingTeamMembershipIsSaidRatherThanIgnored(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true
	replies.pending["a-newcomer"] = true

	reply := answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")

	if len(replies.added) != 0 {
		t.Errorf("somebody with a pending membership was added again: %v", replies.added)
	}
	for _, want := range []string{"has not accepted yet", "they are not in it"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

// Re-running assign to move somebody into another team means the team. The
// role being unchanged is not the same as there being nothing to do, and
// saying "already" and stopping would silently ignore what was asked for.
func TestReassigningToAnotherTeamStillChangesTheTeam(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-newcomer"] = true

	// First time: the role is new, and the ordinary team follows.
	answer(t, server, command.Assign, []string{"@a-newcomer", "as", "codechecker"}, "")
	if len(replies.addedTo) != 1 || replies.addedTo[0] != "codecheckers" {
		t.Fatalf("added to %v", replies.addedTo)
	}
	replies.inTeam["a-newcomer"] = true

	// Second time, naming the other team: the role is unchanged and the team
	// is not.
	replies.inTeam["a-newcomer"] = false
	reply := answer(t, server, command.Assign,
		[]string{"@a-newcomer", "as", "codechecker", "in", "institutional-codecheckers"}, "")

	if len(replies.addedTo) != 2 || replies.addedTo[1] != "institutional-codecheckers" {
		t.Fatalf("added to %v, want the team the editor named the second time", replies.addedTo)
	}
	if !strings.Contains(reply, "already the assigned codechecker") {
		t.Errorf("the reply does not say the role was unchanged:\n%s", reply)
	}
	if !strings.Contains(reply, "institutional-codecheckers") {
		t.Errorf("the reply says nothing about the team that changed:\n%s", reply)
	}
}
