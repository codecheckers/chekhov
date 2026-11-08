package bot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/github"
)

// `include`, an editor adding a registered codechecker (#49).

// listWriter is the lists' reply path with the pull request, recording what
// it was asked to propose.
type listWriter struct {
	recorder
	proposed []github.RowProposal
	refuse   error
}

func (l *listWriter) ProposeRow(_ context.Context, repository string, proposal github.RowProposal) (github.RowPullRequest, error) {
	if repository != testLists {
		return github.RowPullRequest{}, errors.New("proposed on " + repository)
	}
	if l.refuse != nil {
		return github.RowPullRequest{Branch: "codechecker-a-made-up-registrant"}, l.refuse
	}
	l.proposed = append(l.proposed, proposal)
	return github.RowPullRequest{Number: 12, Updated: len(l.proposed) > 1}, nil
}

// including is a bot on the lists whose register client reads and changes
// the organisation, and whose lists client opens pull requests.
func including(t *testing.T) (*Server, *organisation, *listWriter) {
	t.Helper()
	server, _, _, _ := listsServer(t)
	org := &organisation{
		issueReader: &issueReader{recorder: &recorder{}},
		members:     map[string]bool{"nuest": true, "a-codechecker": true},
		inTeam:      map[string]bool{"a-codechecker": true},
		pending:     map[string]bool{},
		maintains:   map[string]bool{},
		owners:      []string{"an-owner", "another-owner"},
		invitations: map[string]github.Invitation{},
	}
	server.Replies = org
	// As in organised: the real list of owners is read, not the override.
	server.Settings.Chekhov.Teams.Owners = nil
	lists := &listWriter{}
	server.Lists = lists
	return server, org, lists
}

// includeBy is `include` on the registration, asked by somebody.
func includeBy(t *testing.T, server *Server, lists *listWriter, by string) string {
	t.Helper()
	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include", by, "registration")
	if code := deliverEvent(t, server, eventType, payload); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	return lists.last()
}

func says(t *testing.T, reply string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(reply, w) {
			t.Errorf("the reply does not say %q:\n%s", w, reply)
		}
	}
}

func saysNot(t *testing.T, reply string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(reply, u) {
			t.Errorf("the reply says %q:\n%s", u, reply)
		}
	}
}

// Somebody with no invitation: the row is proposed and the owners are asked,
// mentioned because only they can do it. Nobody is put in a team, because the
// write that would do it invites them.
func TestIncludeProposesTheRowAndAsksTheOwners(t *testing.T) {
	server, org, lists := including(t)

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "Including `@a-made-up-registrant` as a codechecker",
		"Opened the pull request adding the row to `codecheckers.csv`: #12",
		"I see no invitation for them", "@an-owner", "@another-owner", "as a member")
	if len(org.added) != 0 {
		t.Errorf("put somebody outside the organisation in a team: %q", org.added)
	}
	if len(lists.proposed) != 1 {
		t.Fatalf("proposed %d rows", len(lists.proposed))
	}
	proposed := lists.proposed[0]
	if proposed.Row["handle"] != "@a-made-up-registrant" || proposed.Row["ORCID"] != "0000-0002-1825-0097" ||
		proposed.Row["contact"] != "see ORCID page" || proposed.Row["languages"] != "R, Python" {
		t.Errorf("the row is %+v", proposed.Row)
	}
	if !strings.Contains(proposed.Body, "#7") || !strings.Contains(proposed.Body, "`@nuest`") ||
		strings.Contains(proposed.Title, "@") {
		t.Errorf("the pull request is %q / %q", proposed.Title, proposed.Body)
	}
	if strings.Contains(proposed.Body, "example.invalid") || strings.Contains(reply, "example.invalid") {
		t.Error("a contact was quoted")
	}
}

// The owners' half is checked: an invitation out is not asked for again.
func TestIncludeSeesAnInvitationWaiting(t *testing.T) {
	server, org, lists := including(t)
	org.invitations[madeUpRegistrar] = github.Invitation{State: "pending", Role: "member"}

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "has been invited to `codecheckers` and has not accepted yet")
	saysNot(t, reply, "@an-owner")
	if len(org.added) != 0 {
		t.Errorf("put somebody who has not accepted in a team: %q", org.added)
	}
}

// Once they have joined, the bot does the team; an owner is noted, and the
// role left alone.
func TestIncludePutsAMemberInTheTeam(t *testing.T) {
	server, org, lists := including(t)
	org.members[madeUpRegistrar] = true

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "I have put `@a-made-up-registrant` in `codecheckers/codecheckers`")
	saysNot(t, reply, "@an-owner", "is an owner")
	if strings.Join(org.added, ",") != madeUpRegistrar {
		t.Errorf("added %q", org.added)
	}

	server, org, lists = including(t)
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	org.invitations[madeUpRegistrar] = github.Invitation{State: "active", Role: "admin"}
	reply = includeBy(t, server, lists, "nuest")
	says(t, reply, "is in `codecheckers/codecheckers` already", "is an owner of `codecheckers`")
	if len(org.added) != 0 {
		t.Errorf("added somebody in the team already: %q", org.added)
	}
}

// What cannot be done is said, each on its own line, and nothing is guessed.
func TestIncludeSaysWhatItCouldNotDo(t *testing.T) {
	server, org, lists := including(t)
	lists.refuse = github.ErrRowExists
	org.membersErr = errors.New("GitHub is having a bad day")

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "`codecheckers.csv` has a row for `@a-made-up-registrant`, so there is no pull request",
		"✖ I could not read whether `@a-made-up-registrant` is in `codecheckers`")
	saysNot(t, reply, "@an-owner")
	if len(org.added) != 0 {
		t.Errorf("acted on an organisation it could not read: %q", org.added)
	}
}

// Only an editor includes anybody, and a registration that is not complete
// is not included by anybody.
func TestIncludeIsForEditorsAndCompleteRegistrations(t *testing.T) {
	server, _, lists := including(t)
	reply := includeBy(t, server, lists, "a-codechecker")
	says(t, reply, "`@chekhovbot include` is for")
	if len(lists.proposed) != 0 {
		t.Errorf("a codechecker had a row proposed: %+v", lists.proposed)
	}

	server, org, lists := including(t)
	stubbed, _, _, stub := listsServer(t)
	server.Services = stubbed.Services
	stub.JSON("/orcid/0000-0002-1825-0097/person", `{"emails": {"email": []}}`)
	server.Settings.Chekhov.Lists.Files = stubbed.Settings.Chekhov.Lists.Files
	reply = includeBy(t, server, lists, "nuest")
	says(t, reply, "I have not included anybody", "didn't find an email", "`@chekhovbot include check`")
	saysNot(t, reply, "@an-owner")
	if len(lists.proposed) != 0 || len(org.added) != 0 {
		t.Errorf("an incomplete registration was acted on: %+v %q", lists.proposed, org.added)
	}
}

// A branch with somebody else's commits is named as the writer named it.
func TestIncludeNamesABranchThatIsNotItsOwn(t *testing.T) {
	server, _, lists := including(t)
	lists.refuse = github.ErrBranchNotOurs
	says(t, includeBy(t, server, lists, "nuest"),
		"- ✖ The branch `codechecker-a-made-up-registrant` has commits that are not mine")
}

// Once the row is merged, `include` still does the organisation's half: the
// listing is done, not a reason to stop.
func TestIncludeAfterTheRowWasMergedStillDoesTheTeam(t *testing.T) {
	server, org, lists := including(t)
	stubbed, _, _, stub := listsServer(t)
	stub.Handle("/lists/codecheckers.csv", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n" +
			registrationRow + "\n"))
	})
	server.Services, server.Settings.Chekhov.Lists.Files = stubbed.Services, stubbed.Settings.Chekhov.Lists.Files
	org.members[madeUpRegistrar] = true

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "has a row for `@a-made-up-registrant`", "I have put `@a-made-up-registrant` in `codecheckers/codecheckers`")
	if len(lists.proposed) != 0 {
		t.Errorf("proposed a row that is merged: %+v", lists.proposed)
	}
}

// `include` with anything after it - a mistyped `include check` - does
// nothing, and notifies nobody.
func TestIncludeWithArgumentsDoesNothing(t *testing.T) {
	server, org, lists := including(t)
	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include chekc", "nuest", "registration")
	deliverEvent(t, server, eventType, payload)
	reply := lists.last()
	says(t, reply, "takes nothing after it", "`@chekhovbot include check`")
	saysNot(t, reply, "@an-owner")
	if len(lists.proposed)+len(org.added) != 0 {
		t.Errorf("acted on a mistyped command: %+v %q", lists.proposed, org.added)
	}
}

// A codecheckers team the bot may not add to is said, and not claimed done.
func TestIncludeDoesNotClaimATeamItCannotChange(t *testing.T) {
	server, org, lists := including(t)
	org.members[madeUpRegistrar] = true
	server.Settings.Chekhov.Teams.Managed = nil

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "I am not set up to put anybody in `codecheckers/codecheckers`")
	saysNot(t, reply, "already.")
	if len(org.added) != 0 {
		t.Errorf("added to an unmanaged team: %q", org.added)
	}
}

// The title carries the registrant's own text, and is defused like the body.
func TestIncludeDefusesTheTitle(t *testing.T) {
	server, _, lists := including(t)
	eventType, payload := registrationEvent(testLists,
		strings.Replace(registrationRow, "Josiah Carberry", "Josiah <!-- Carberry", 1),
		"@chekhovbot include", "nuest", "registration")
	deliverEvent(t, server, eventType, payload)
	if len(lists.proposed) != 1 {
		t.Fatalf("proposed %d rows:\n%s", len(lists.proposed), lists.last())
	}
	if strings.Contains(lists.proposed[0].Title, "<!--") {
		t.Errorf("the title looks like a record: %q", lists.proposed[0].Title)
	}
}
