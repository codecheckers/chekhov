package bot

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// `invite` on a check (#68).

// inviting is a bot on the register that can open registrations on the lists,
// with a check whose thread mentions the newcomer in an editor's comment, as
// `assign @… as codechecker` does.
func inviting(t *testing.T) (*Server, *organisation, *listsThread) {
	t.Helper()
	server, replies := organised(t)
	lists := &listsThread{}
	server.Lists = lists
	stubbed, _, _, _ := listsServer(t)
	server.Services, server.Settings.Chekhov.Lists.Files = stubbed.Services, stubbed.Settings.Chekhov.Lists.Files
	replies.thread = append(replies.thread, github.Comment{ID: 50, Author: "nuest",
		Body: "@chekhovbot assign @a-made-up-newcomer as codechecker"})
	return server, replies, lists
}

// The record the reply left, of the kind asked for, if any.
func recordOn(t *testing.T, server *Server, replies *organisation, kind string) (followup.Record, bool) {
	t.Helper()
	for i := len(replies.thread) - 1; i >= 0; i-- {
		record, err := followup.Parse(replies.thread[i].Body, "codecheckers/testing-dev-register#1", server.signer())
		if err == nil && record.Kind == kind {
			return record, true
		}
	}
	return followup.Record{}, false
}

// Somebody outside the organisation whom the check has mentioned: their
// registration opens, telling them an invitation is to come, and the owners
// are asked for it - mentioned, as `assign` mentions them - with a record that
// asks for no role.
func TestInviteOpensARegistrationAndAsksTheOwners(t *testing.T) {
	server, replies, lists := inviting(t)

	reply := acted(t, server, "@chekhovbot invite @a-made-up-newcomer")

	says(t, reply, "I have opened "+testLists+"#21 for `@a-made-up-newcomer`", "@an-owner",
		"please invite `@a-made-up-newcomer` to the organisation", "codecheckers/codecheckers")
	saysNot(t, reply, "@a-made-up-newcomer,", "the assigned codechecker of this check")
	if len(lists.opened) != 1 || !strings.Contains(lists.opened[0], "@a-made-up-newcomer, welcome") ||
		!strings.Contains(lists.opened[0], "https://github.com/orgs/codecheckers/invitation") {
		t.Errorf("opened %q", lists.opened)
	}
	record, ok := recordOn(t, server, replies, followup.KindOrganisation)
	if !ok || record.Subject != "a-made-up-newcomer" || record.Role != "" || record.Team != "codecheckers" {
		t.Errorf("the record of the ask: %+v, %v", record, ok)
	}
}

// Nobody the check has brought in: a mistyped handle would notify a stranger,
// so nothing opens and nobody is asked. The command's own mention of them
// does not count.
func TestInviteRefusesSomebodyNotInTheThread(t *testing.T) {
	server, replies, lists := inviting(t)
	replies.thread = nil

	reply := acted(t, server, "@chekhovbot invite @a-made-up-newcomer")

	says(t, reply, "has not taken part in this issue, so I have opened no registration")
	saysNot(t, reply, "@an-owner")
	if len(lists.opened) != 0 {
		t.Errorf("opened %q", lists.opened)
	}
	// Backticks notify nobody, so a handle written in them is no part in it.
	replies.thread = []github.Comment{{ID: 50, Author: "nuest", Body: "is `@a-made-up-newcomer` right?"}}
	says(t, acted(t, server, "@chekhovbot invite @a-made-up-newcomer"), "has not taken part in this issue")
	// Nor is the same command, refused before: posting it again changes
	// nothing about who the issue has brought in.
	replies.thread = []github.Comment{{ID: 51, Author: "nuest", Body: "@chekhovbot invite @a-made-up-newcomer"}}
	says(t, acted(t, server, "@chekhovbot invite @a-made-up-newcomer"), "has not taken part in this issue")
	if len(lists.opened) != 0 {
		t.Errorf("opened %q", lists.opened)
	}
}

// The owners are asked once for one person: an ask on the check already,
// `assign`'s, is reminded by the sweep; an invitation out is the person's to
// accept. A member needs neither.
func TestInviteAsksTheOwnersOnlyWhenNobodyHas(t *testing.T) {
	server, replies, _ := inviting(t)
	asked(t, server, replies, "a-made-up-newcomer", time.Hour)
	reply := acted(t, server, "@chekhovbot invite @a-made-up-newcomer")
	says(t, reply, "The owners were asked on this check already")
	saysNot(t, reply, "@an-owner")

	server, replies, _ = inviting(t)
	replies.invitations = map[string]github.Invitation{"a-made-up-newcomer": {State: "pending"}}
	reply = acted(t, server, "@chekhovbot invite @a-made-up-newcomer")
	says(t, reply, "has been invited to `codecheckers` already")
	saysNot(t, reply, "@an-owner")

	server, replies, lists := inviting(t)
	replies.members["a-made-up-newcomer"] = true
	reply = acted(t, server, "@chekhovbot invite @a-made-up-newcomer")
	says(t, reply, "I have opened "+testLists+"#21")
	saysNot(t, reply, "@an-owner", "not in the `codecheckers` organisation")
	if len(lists.opened) != 1 || strings.Contains(lists.opened[0], "/invitation") {
		t.Errorf("opened %q", lists.opened)
	}
	// In the organisation already, so in the team now, as somebody outside
	// is put there the night they join.
	if !slices.Equal(replies.added, []string{"a-made-up-newcomer"}) ||
		!slices.Equal(replies.addedTo, []string{"codecheckers"}) {
		t.Errorf("added %v to %v, want the newcomer to codecheckers", replies.added, replies.addedTo)
	}
}

// A handle GitHub has no person for is refused before anything is opened.
func TestInviteRefusesAHandleWithNoAccount(t *testing.T) {
	server, replies, lists := inviting(t)
	server.Replies = &withAccounts{organisation: replies}
	reply := server.answer(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1,
		Author: "nuest"}, command.Command{Name: command.Invite, Args: []string{"@a-made-up-newcomer"}}).body
	says(t, reply, "There is no GitHub account")
	if len(lists.opened) != 0 {
		t.Errorf("opened %q", lists.opened)
	}
}

// withAccounts is a reply path that knows nobody's account.
type withAccounts struct{ *organisation }

func (withAccounts) PersonAccount(_ context.Context, handle string) (string, error) {
	return "", errors.New("there is no GitHub account `@" + handle + "`")
}

// Once somebody an `invite` asked for joins, the sweep puts them in the team
// and records no role: the ask wanted none.
func TestTheSweepFinishesAnInviteWithTheTeamOnly(t *testing.T) {
	server, replies, _ := inviting(t)
	acted(t, server, "@chekhovbot invite @a-made-up-newcomer")
	replies.members["a-made-up-newcomer"] = true

	server.nudge(context.Background())

	says(t, replies.last(), "has joined", "codecheckers/codecheckers", "already has a registration")
	saysNot(t, replies.last(), "They are now the")
	if len(replies.added) != 1 || len(replies.assigned) != 0 {
		t.Errorf("added %v, assigned %v", replies.added, replies.assigned)
	}
}

// An invitation out and nobody asking on the check: nobody is asked, and the
// record is left all the same, so that the sweep puts them in the team once
// they accept - which the reply promises.
func TestInviteForSomebodyInvitedLeavesTheSweepARecord(t *testing.T) {
	server, replies, _ := inviting(t)
	replies.invitations = map[string]github.Invitation{"a-made-up-newcomer": {State: "pending"}}
	reply := acted(t, server, "@chekhovbot invite @a-made-up-newcomer")
	saysNot(t, reply, "@an-owner")
	if record, ok := recordOn(t, server, replies, followup.KindOrganisation); !ok || record.Role != "" {
		t.Errorf("the record for the sweep: %+v, %v", record, ok)
	}
}

// An ask that has reminded the owners as often as it will is over: its last
// reminder offers `invite` to start it again, and `invite` does.
func TestInviteStartsAnExhaustedAskOver(t *testing.T) {
	server, replies, _ := inviting(t)
	block, err := followup.Render(followup.Record{Kind: followup.KindOrganisation,
		Check: "codecheckers/testing-dev-register#1", Subject: "a-made-up-newcomer", Team: "codecheckers",
		Reminders: mostReminders + 1, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(block + "I will not ask again")
	says(t, acted(t, server, "@chekhovbot invite @a-made-up-newcomer"), "@an-owner", "please invite")
}

// A thread that could not be read is said to be that, not a misspelling.
func TestInviteSaysWhenTheThreadCouldNotBeRead(t *testing.T) {
	server, replies, lists := inviting(t)
	replies.issuesErr = errors.New("GitHub answered 502")
	reply := acted(t, server, "@chekhovbot invite @a-made-up-newcomer")
	says(t, reply, "I could not read who has taken part in this issue")
	saysNot(t, reply, "check the spelling")
	if len(lists.opened) != 0 {
		t.Errorf("opened %q", lists.opened)
	}
}

// An `assign`'s ask that has run out promised a role: `invite` starting it
// over keeps it, or the sweep would add them to the team and forget the check
// they were assigned to.
func TestInviteStartingAnAssignsAskOverKeepsTheRole(t *testing.T) {
	server, replies, _ := inviting(t)
	block, err := followup.Render(followup.Record{Kind: followup.KindOrganisation,
		Check: "codecheckers/testing-dev-register#1", Subject: "a-made-up-newcomer", Team: "codecheckers",
		Role: string(command.RoleAssignedCodechecker), Reminders: mostReminders + 1,
		Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(block + "I will not ask again")

	says(t, acted(t, server, "@chekhovbot invite @a-made-up-newcomer"), "@an-owner", "please invite")
	if record, ok := recordOn(t, server, replies, followup.KindOrganisation); !ok ||
		record.Role != string(command.RoleAssignedCodechecker) || record.Reminders != 0 {
		t.Errorf("the new ask: %+v, %v", record, ok)
	}
}

// `invite` asked the owners already, so `assign` after it does not notify
// them a second time for one person; its record carries the role, which the
// sweep gives once they join.
func TestAssignAfterInviteDoesNotAskTheOwnersAgain(t *testing.T) {
	server, replies, _ := inviting(t)
	// The invite's ask has reminded the owners twice: the assign takes it
	// over, and the count with it.
	block, err := followup.Render(followup.Record{Kind: followup.KindOrganisation,
		Check: "codecheckers/testing-dev-register#1", Subject: "a-made-up-newcomer", Team: "codecheckers",
		Reminders: 2, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	replies.onTheIssue(block + "asked twice already")

	reply := acted(t, server, "@chekhovbot assign @a-made-up-newcomer as codechecker")
	says(t, reply, "The owners were asked on this check already", "Once they have joined, I make them the")
	saysNot(t, reply, "@an-owner", "@another-owner")
	if record, ok := recordOn(t, server, replies, followup.KindOrganisation); !ok ||
		record.Role != string(command.RoleAssignedCodechecker) || record.Reminders != 2 {
		t.Errorf("the record for the sweep: %+v, %v", record, ok)
	}
}
