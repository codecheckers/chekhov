package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
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
	// maintains is somebody GitHub calls a maintainer of the team - an
	// organisation owner, whose membership this bot cannot read at all.
	maintains map[string]bool
	// thread is what the issue's comments are, as the sweep reads them, and
	// issuesErr a register that will not answer at all.
	thread    []github.Comment
	issuesErr error
	// issuesPanic is a register read that panics rather than failing, which
	// is what the sweep's recover is for.
	issuesPanic any
	added       []string
	addedTo     []string
	// assigned and unassigned are what the issue's assignee was changed to
	// and from, which is the other half of finishing an assignment.
	assigned   []string
	unassigned []string
}

func (o *organisation) Assign(_ context.Context, _ string, _ int, handle string) ([]string, error) {
	o.assigned = append(o.assigned, handle)
	return o.assigned, nil
}

func (o *organisation) Unassign(_ context.Context, _ string, _ int, handle string) error {
	o.unassigned = append(o.unassigned, handle)
	return nil
}

// Comments and OpenIssues make this a Reader too, which is what the sweep
// walks the register with.
func (o *organisation) Comments(_ context.Context, _ string, _ int) ([]github.Comment, error) {
	return o.thread, o.issuesErr
}

func (o *organisation) OpenIssues(_ context.Context, _ string) ([]github.Issue, error) {
	if o.issuesPanic != nil {
		panic(o.issuesPanic)
	}
	if o.issuesErr != nil {
		return nil, o.issuesErr
	}
	return o.issues, nil
}

// labelled is the check's own labels, which decide which team a codechecker
// belongs in.
func (o *organisation) labelled(labels ...string) {
	o.issues = []github.Issue{{Number: 1, Labels: labels}}
}

// Comment posts as the recorder does, and leaves the comment in the thread:
// what the bot says on an issue is what a later sweep reads back, records and
// all.
func (o *organisation) Comment(ctx context.Context, repository string, issue int, body string) (int64, error) {
	id, err := o.recorder.Comment(ctx, repository, issue, body)
	if err == nil {
		o.thread = append(o.thread, github.Comment{ID: id, Author: "chekhovbot", Body: body})
	}
	return id, err
}

// onTheIssue is a comment already in the thread, for the sweep to find.
func (o *organisation) onTheIssue(body string) {
	o.thread = append(o.thread, github.Comment{ID: int64(len(o.thread) + 1),
		Author: "chekhovbot", Body: body})
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

func (o *organisation) TeamMembership(_ context.Context, _, _, handle string) (github.Place, error) {
	role := ""
	if o.inTeam[handle] {
		role = github.TeamRoleMember
	}
	if o.maintains[handle] {
		// An organisation owner, whose membership GitHub does not show this
		// bot at all: the read answers as though they were not in the team.
		return github.Place{}, o.teamErr
	}
	return github.Place{Active: o.inTeam[handle], Pending: o.pending[handle], Role: role}, o.teamErr
}

func (o *organisation) AddToTeam(_ context.Context, _, team, handle string) (github.Place, error) {
	o.added = append(o.added, handle)
	o.addedTo = append(o.addedTo, team)
	if o.maintains[handle] {
		// What GitHub answers for an owner: in the team, as a maintainer the
		// PUT did not make them.
		return github.Place{Login: handle, Active: true, Role: github.TeamRoleMaintainer}, nil
	}
	return github.Place{Login: handle, Active: true, Role: github.TeamRoleMember}, nil
}

// acted runs one command the way a delivery does - through act, which defuses
// the reply and then writes any record - and returns what was posted.
func acted(t *testing.T, server *Server, body string) string {
	t.Helper()
	parsed, addressed := command.Parse(body)
	if !addressed {
		t.Fatalf("%q is not addressed to the bot", body)
	}
	server.act(mention{Repository: server.Settings.TargetRepository(), Issue: 1,
		Author: "nuest", Body: body}, parsed)
	if server.done != nil {
		<-server.done
	}
	return server.Replies.(*organisation).last()
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
		maintains:   map[string]bool{},
		owners:      []string{"an-owner", "another-owner"},
	}
	// One open issue, which is the check every test here is about. labelled
	// replaces it when the labels matter.
	replies.issues = []github.Issue{{Number: 1}}
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

// The ask to the owners leaves a record behind, so that the nightly sweep can
// finish the job once the person has joined. It has to survive `act`, which
// defuses every reply - a marker written into a body would be escaped on the
// way out, which is exactly the property this relies on.
func TestTheAskToTheOwnersLeavesASignedRecord(t *testing.T) {
	server, _ := organised(t)

	posted := acted(t, server, "@chekhovbot assign @a-newcomer as codechecker")
	record, err := followup.Parse(posted, "codecheckers/testing-dev-register#1", server.signer())
	if err != nil {
		t.Fatalf("the posted comment carries no usable record: %v\n%s", err, posted)
	}
	if record.Kind != followup.KindOrganisation || record.Subject != "a-newcomer" {
		t.Errorf("record %+v", record)
	}
	if record.Team != "codecheckers" {
		t.Errorf("the record does not say which team: %+v", record)
	}
	if record.Raised().IsZero() {
		t.Error("the record does not say when it was raised, so nothing can be three days old")
	}
	// The reply a person reads is still under it.
	if !strings.Contains(posted, "please invite `@a-newcomer`") {
		t.Errorf("the reply is missing from the comment:\n%s", posted)
	}
}

// An ordinary reply carries no record: there is nothing to chase, and a
// marker on every comment would make the sweep's work meaningless.
func TestAnOrdinaryReplyCarriesNoRecord(t *testing.T) {
	server, _ := organised(t)

	posted := acted(t, server, "@chekhovbot hello")
	if _, err := followup.Parse(posted, "", server.signer()); err != followup.ErrNoRecord {
		t.Errorf("an ordinary reply carries a record: %v\n%s", err, posted)
	}
}

// The reply promises a team and the record carries one; they are worked out
// once so that they cannot disagree - a sweep putting somebody somewhere the
// reply did not promise would be worse than not sweeping at all.
func TestTheRecordAndTheReplyAgreeOnTheTeam(t *testing.T) {
	server, replies := organised(t)
	replies.labelled("institution")

	posted := acted(t, server, "@chekhovbot assign @a-newcomer as codechecker")

	record, err := followup.Parse(posted, "codecheckers/testing-dev-register#1", server.signer())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if record.Team != "institutional-codecheckers" {
		t.Errorf("the record says %q", record.Team)
	}
	if !strings.Contains(posted, "codecheckers/institutional-codecheckers") {
		t.Errorf("the reply promises a different team:\n%s", posted)
	}
}

// A role that puts nobody in a team leaves a record with no team in it, and a
// reply that promises none.
func TestARecordForARoleWithNoTeamNamesNone(t *testing.T) {
	server, _ := organised(t)

	posted := acted(t, server, "@chekhovbot assign @an-author as author")

	record, err := followup.Parse(posted, "codecheckers/testing-dev-register#1", server.signer())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if record.Team != "" {
		t.Errorf("an author's record names a team: %q", record.Team)
	}
	if strings.Contains(posted, "I put them in") {
		t.Errorf("the reply promises a team for an author:\n%s", posted)
	}
}

// An organisation owner is a maintainer of every team they are in, and GitHub
// shows this bot no membership for them at all - so the add is how it finds
// out they were there already. That is not a failure, and the reply says what
// is true rather than that something went wrong (codecheckers/chekhov#47).
func TestAnOwnerAlreadyInTheTeamIsNotReportedAsAFailure(t *testing.T) {
	server, replies := organised(t)
	replies.members["an-editor"] = true
	replies.maintains["an-editor"] = true

	reply := answer(t, server, command.Assign, []string{"@an-editor", "as", "codechecker"}, "")

	for _, want := range []string{"is now the assigned codechecker", "already in `codecheckers/codecheckers`",
		"as a maintainer", "did not set and will not change"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "could not put them") {
		t.Errorf("an owner already in the team was reported as a failure:\n%s", reply)
	}
}

// Somebody already in the team is left alone entirely: no write goes out,
// because a PUT asking for `member` would take a maintainer's role away.
func TestSomebodyAlreadyInTheTeamIsNotWrittenTo(t *testing.T) {
	server, replies := organised(t)
	replies.members["a-codechecker"] = true
	replies.inTeam["a-codechecker"] = true

	answer(t, server, command.Assign, []string{"@a-codechecker", "as", "codechecker"}, "")

	if len(replies.added) != 0 {
		t.Errorf("the team was written to for somebody already in it: %v", replies.added)
	}
}
