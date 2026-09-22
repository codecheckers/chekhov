package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A request the bot made, for a test to hold it to what it is allowed to do.
type sent struct {
	Method string
	Path   string
	Body   string
}

// adding is a client set up to add to the managed teams, watching everything
// it sends.
//
// Every test here reads the record: what this code must not do matters more
// than what it does, and the only proof of that is the requests that left.
func adding(t *testing.T, handler http.HandlerFunc) (*Client, *[]sent) {
	t.Helper()
	var requests []sent
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		requests = append(requests, sent{Method: r.Method, Path: r.URL.Path, Body: string(raw)})
		handler(w, r)
	})
	client.Organisation = "codecheckers"
	client.ManagedTeams = []string{"codecheckers", "institutional-codecheckers"}
	return client, &requests
}

// answers serves the two requests an invitation makes: who the handle is, and
// the membership that comes back.
func answers(accountType, state, role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/users/") {
			fmt.Fprintf(w, `{"login": "A-Codechecker", "type": %q}`, accountType)
			return
		}
		fmt.Fprintf(w, `{"state": %q, "role": %q}`, state, role)
	}
}

// nothingLeft asserts that no request was made at all, which is the only
// acceptable outcome for a refusal.
func nothingLeft(t *testing.T, requests *[]sent) {
	t.Helper()
	if len(*requests) != 0 {
		t.Errorf("a refusal still sent %+v", *requests)
	}
}

// --- what it must never do -------------------------------------------------

// The organisation membership endpoint is one path segment away from the team
// one, and with role:admin it makes somebody an owner of the organisation.
// Nothing here may ever reach it, whatever it is asked.
func TestTheBotNeverTouchesOrganisationMembership(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))

	if _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err != nil {
		t.Fatalf("invite: %v", err)
	}

	if len(*requests) == 0 {
		t.Fatal("nothing was sent")
	}
	for _, request := range *requests {
		// The team path contains /teams/; the account-type path does not.
		if strings.Contains(request.Path, "/orgs/") && !strings.Contains(request.Path, "/teams/") {
			t.Errorf("this request can change an account's organisation role: %+v", request)
		}
		if strings.Contains(request.Body, "admin") || strings.Contains(request.Body, "owner") {
			t.Errorf("this request body asks for more than membership: %+v", request)
		}
	}
}

// The role sent is always a plain member. A maintainer may add and remove
// others, which is the bot's own permission handed on.
func TestTheBotOnlyEverAsksForAPlainMember(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))

	if _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err != nil {
		t.Fatalf("add: %v", err)
	}

	writes := 0
	for _, request := range *requests {
		if request.Method != http.MethodPut {
			continue
		}
		writes++
		if request.Body != `{"role":"member"}` {
			t.Errorf("the body is %q, want exactly the member role", request.Body)
		}
		if strings.Contains(request.Body, "maintainer") {
			t.Errorf("the bot asked for a maintainer: %+v", request)
		}
	}
	if writes != 1 {
		t.Errorf("%d writes, want exactly one", writes)
	}
}

// Every method here adds; none removes. A role is taken away by a person, in
// the organisation's settings, where it is logged against their name.
func TestThereIsNoWayToRemoveAnybody(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))
	if _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err != nil {
		t.Fatalf("invite: %v", err)
	}
	for _, request := range *requests {
		if request.Method == http.MethodDelete {
			t.Errorf("this package sent a DELETE: %+v", request)
		}
	}
}

// Another organisation is refused before a request is made: a misconfigured
// deployment must not be able to reach into somebody else's organisation.
func TestAnotherOrganisationIsRefused(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))

	_, err := client.AddToTeam(context.Background(), "someone-else", "codecheckers", "a-codechecker")
	if err == nil {
		t.Fatal("membership of another organisation was changed")
	}
	if !strings.Contains(err.Error(), "works on codecheckers only") {
		t.Errorf("error %q, want it to name the one organisation", err)
	}
	nothingLeft(t, requests)
}

// The editors team is what grants the editor-only commands. A bot that could
// add to any team could hand out its own permissions - and config refuses a
// managed list that contains it, so this is the second line of that defence.
func TestAnotherTeamIsRefused(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))

	_, err := client.AddToTeam(context.Background(), "codecheckers", "editors", "a-codechecker")
	if err == nil {
		t.Fatal("somebody was added to the editors team")
	}
	if !strings.Contains(err.Error(), "may only add to codecheckers/codecheckers") {
		t.Errorf("error %q, want it to name the teams it may add to", err)
	}
	nothingLeft(t, requests)
}

// A handle goes into a request path. One carrying a slash or a dot segment
// would not merely fail to resolve - it would move the request somewhere else,
// and next door is the endpoint that makes owners.
func TestAHandleCannotRedirectTheRequest(t *testing.T) {
	escapes := []string{
		"../../orgs/codecheckers/memberships/an-intruder",
		"a-codechecker/../../memberships/an-intruder",
		"a-codechecker?role=admin",
		"a-codechecker#x",
		"..",
		".",
		"a codechecker",
		"a-codechecker/extra",
		"",
		strings.Repeat("a", 40),
		"-leading-hyphen",
		"trailing-hyphen-",
		"double--hyphen",
	}
	for _, handle := range escapes {
		t.Run(handle, func(t *testing.T) {
			client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))
			if _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", handle); err == nil {
				t.Errorf("%q was accepted as a handle", handle)
			}
			nothingLeft(t, requests)
		})
	}
}

// An answer describing more than was asked for is reported as the surprise it
// is, rather than as a successful invitation.
func TestARoleTheBotDidNotAskForIsReported(t *testing.T) {
	client, _ := adding(t, answers("User", membershipActive, "maintainer"))

	_, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if err == nil {
		t.Fatal("a maintainer came back and was reported as a member")
	}
	if !strings.Contains(err.Error(), "maintainer") || !strings.Contains(err.Error(), "settings") {
		t.Errorf("error %q, want it to say what came back and what to look at", err)
	}
}

// A client no deployment configured for inviting refuses, rather than aiming
// the request at an empty organisation.
func TestAnUnconfiguredClientCannotInvite(t *testing.T) {
	var requests []sent
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		requests = append(requests, sent{Method: r.Method, Path: r.URL.Path, Body: string(raw)})
	})

	if _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err == nil {
		t.Fatal("an unconfigured client invited somebody")
	}
	nothingLeft(t, &requests)
}

// --- what it does ----------------------------------------------------------

// A pending membership cannot arise: the bot only adds people who are already
// in the organisation, and bringing somebody in from outside is an owner's to
// do. If GitHub ever answers "pending" here, this file no longer does what its
// comment says, and saying so is better than reporting a membership that is
// not in effect.
func TestAPendingMembershipIsReportedAsImpossible(t *testing.T) {
	client, _ := adding(t, answers("User", "pending", TeamRoleMember))

	_, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if err == nil {
		t.Fatal("a pending membership was reported as done")
	}
	if !strings.Contains(err.Error(), "pending") || !strings.Contains(err.Error(), "should not") {
		t.Errorf("error %q, want it to say what came back and that it should not have", err)
	}
}

// Somebody already in the organisation is in the team at once, and the account
// comes back as GitHub capitalises it, which is how a reply should write it.
func TestAMemberOfTheOrganisationIsAddedAtOnce(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))

	login, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if login != "A-Codechecker" {
		t.Errorf("login = %q, want GitHub's own capitalisation", login)
	}
	for _, request := range *requests {
		if request.Method == http.MethodPut && !strings.HasSuffix(request.Path, "/A-Codechecker") {
			t.Errorf("the write used %q rather than the account's own login", request.Path)
		}
	}
}

// The handle is checked against GitHub before the team is touched, as the
// registration runbook does by hand.
func TestAHandleThatDoesNotResolveIsNotInvited(t *testing.T) {
	client, requests := adding(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Error("the team was touched for a handle that does not exist")
	})

	_, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if !errors.Is(err, ErrNotAUser) {
		t.Fatalf("error %v, want it to wrap ErrNotAUser", err)
	}
	for _, request := range *requests {
		if request.Method == http.MethodPut {
			t.Errorf("a write went out for a handle that does not exist: %+v", request)
		}
	}
}

// An organisation cannot be in a team, and asking is a puzzle of a 422. It is
// refused in words, before the write.
func TestAnOrganisationCannotBeInvited(t *testing.T) {
	client, requests := adding(t, answers("Organization", membershipActive, TeamRoleMember))

	_, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if !errors.Is(err, ErrNotAUser) {
		t.Fatalf("error %v, want it to wrap ErrNotAUser", err)
	}
	if !strings.Contains(err.Error(), "organization") {
		t.Errorf("error %q, want it to name what the account is", err)
	}
	for _, request := range *requests {
		if request.Method == http.MethodPut {
			t.Errorf("a write went out for an organisation: %+v", request)
		}
	}
}

// A token that may not manage membership says so plainly. Read as a failed
// invitation it would have an editor trying again for ever.
func TestATokenThatMayNotManageMembershipSaysSo(t *testing.T) {
	client, _ := adding(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login": "A-Codechecker", "type": "User"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by personal access token"}`))
	})

	_, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("error %v, want it to wrap ErrNotPermitted", err)
	}
}

// The two handle patterns are deliberately separate - one guards a comment,
// one guards a request path - so nothing keeps them in step but this.
func TestTheHandleShapeAgreesWithTheParsers(t *testing.T) {
	for _, handle := range []string{"a-codechecker", "nuest", "a1", "a"} {
		if !IsHandle(handle) {
			t.Errorf("%q should be a handle", handle)
		}
	}
	for _, handle := range []string{"", "-a", "a-", "a--b", "a/b", "..", "a b", strings.Repeat("a", 40)} {
		if IsHandle(handle) {
			t.Errorf("%q should not be a handle", handle)
		}
	}
}

// A managed team the settings do name is allowed, so that an institutional
// codechecker can go into the institutional team.
func TestEveryManagedTeamIsAllowed(t *testing.T) {
	client, requests := adding(t, answers("User", membershipActive, TeamRoleMember))

	for _, team := range client.ManagedTeams {
		if _, err := client.AddToTeam(context.Background(), "codecheckers", team, "a-codechecker"); err != nil {
			t.Errorf("adding to the managed team %q was refused: %v", team, err)
		}
	}
	for _, request := range *requests {
		if request.Method == http.MethodPut && !strings.Contains(request.Path, "/teams/") {
			t.Errorf("a write went somewhere other than a team: %+v", request)
		}
	}
}

// The owners are read from the organisation, so a reply asking them to act
// names whoever is an owner now.
func TestOwnersComeFromTheOrganisation(t *testing.T) {
	client, _ := adding(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("role") != "admin" {
			t.Errorf("owners were read without role=admin: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`[{"login": "an-owner"}, {"login": "another-owner"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	owners, err := client.Owners(context.Background(), "codecheckers")
	if err != nil {
		t.Fatalf("owners: %v", err)
	}
	if len(owners) != 2 || owners[0] != "an-owner" {
		t.Errorf("owners = %v", owners)
	}
	if _, err := client.Owners(context.Background(), "someone-else"); err == nil {
		t.Error("the owners of another organisation were read")
	}
}

// Membership of the organisation is the question assign has to ask first, and
// GitHub answers "no" with a 404 rather than an error.
func TestOrganisationMembershipTellsNoFromBroken(t *testing.T) {
	client, _ := adding(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/members/a-member"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/members/a-stranger"):
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	for handle, want := range map[string]bool{"a-member": true, "a-stranger": false} {
		in, err := client.InOrganisation(context.Background(), "codecheckers", handle)
		if err != nil {
			t.Errorf("%s: %v", handle, err)
		}
		if in != want {
			t.Errorf("%s in the organisation = %v, want %v", handle, in, want)
		}
	}
	if _, err := client.InOrganisation(context.Background(), "codecheckers", "a-bad-day"); err == nil {
		t.Error("a 500 should be an error, not an answer of no")
	}
	if _, err := client.InOrganisation(context.Background(), "codecheckers", "../x"); err == nil {
		t.Error("a handle that is not one should be refused before the request")
	}
}
