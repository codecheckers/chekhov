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

// inviting is a client set up to add to one team, watching everything it sends.
//
// Every test here reads the record: what this code must not do matters more
// than what it does, and the only proof of that is the requests that left.
func inviting(t *testing.T, handler http.HandlerFunc) (*Client, *[]sent) {
	t.Helper()
	var requests []sent
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		requests = append(requests, sent{Method: r.Method, Path: r.URL.Path, Body: string(raw)})
		handler(w, r)
	})
	client.Organisation, client.InviteTeam = "codecheckers", "codecheckers"
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
	client, requests := inviting(t, answers("User", MembershipActive, TeamRoleMember))

	if _, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err != nil {
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
	client, requests := inviting(t, answers("User", MembershipPending, TeamRoleMember))

	if _, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err != nil {
		t.Fatalf("invite: %v", err)
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
	client, requests := inviting(t, answers("User", MembershipActive, TeamRoleMember))
	if _, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err != nil {
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
	client, requests := inviting(t, answers("User", MembershipActive, TeamRoleMember))

	_, _, err := client.AddToTeam(context.Background(), "someone-else", "codecheckers", "a-codechecker")
	if err == nil {
		t.Fatal("membership of another organisation was changed")
	}
	if !strings.Contains(err.Error(), "works on codecheckers only") {
		t.Errorf("error %q, want it to name the one organisation", err)
	}
	nothingLeft(t, requests)
}

// The editors team is what grants the editor-only commands. A bot that could
// add to any team could hand out its own permissions.
func TestAnotherTeamIsRefused(t *testing.T) {
	client, requests := inviting(t, answers("User", MembershipActive, TeamRoleMember))

	_, _, err := client.AddToTeam(context.Background(), "codecheckers", "editors", "a-codechecker")
	if err == nil {
		t.Fatal("somebody was added to the editors team")
	}
	if !strings.Contains(err.Error(), "may only add to codecheckers/codecheckers") {
		t.Errorf("error %q, want it to name the one team", err)
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
			client, requests := inviting(t, answers("User", MembershipActive, TeamRoleMember))
			if _, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", handle); err == nil {
				t.Errorf("%q was accepted as a handle", handle)
			}
			nothingLeft(t, requests)
		})
	}
}

// An answer describing more than was asked for is reported as the surprise it
// is, rather than as a successful invitation.
func TestARoleTheBotDidNotAskForIsReported(t *testing.T) {
	client, _ := inviting(t, answers("User", MembershipActive, "maintainer"))

	_, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
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

	if _, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker"); err == nil {
		t.Fatal("an unconfigured client invited somebody")
	}
	nothingLeft(t, &requests)
}

// --- what it does ----------------------------------------------------------

// Somebody outside the organisation is sent an invitation, and is not in the
// team until they accept it. Saying otherwise would have an editor waiting for
// a codechecker who never arrives.
func TestAnInvitationIsPendingUntilAccepted(t *testing.T) {
	client, requests := inviting(t, answers("User", MembershipPending, TeamRoleMember))

	_, state, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if state != MembershipPending {
		t.Errorf("state %q, want it pending", state)
	}
	// GitHub's own capitalisation of the handle, not the one that was typed.
	for _, request := range *requests {
		if request.Method == http.MethodPut && !strings.HasSuffix(request.Path, "/A-Codechecker") {
			t.Errorf("the write used %q rather than the account's own login", request.Path)
		}
	}
}

// Somebody already in the organisation is in the team at once.
func TestAMemberOfTheOrganisationIsAddedAtOnce(t *testing.T) {
	client, _ := inviting(t, answers("User", MembershipActive, TeamRoleMember))

	_, state, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if state != MembershipActive {
		t.Errorf("state %q, want it immediate", state)
	}
}

// The handle is checked against GitHub before the team is touched, as the
// registration runbook does by hand.
func TestAHandleThatDoesNotResolveIsNotInvited(t *testing.T) {
	client, requests := inviting(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Error("the team was touched for a handle that does not exist")
	})

	_, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
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
	client, requests := inviting(t, answers("Organization", MembershipActive, TeamRoleMember))

	_, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
	if !errors.Is(err, ErrNotAUser) {
		t.Fatalf("error %v, want it to wrap ErrNotAUser", err)
	}
	if !strings.Contains(err.Error(), "not a person") {
		t.Errorf("error %q, want it to say why", err)
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
	client, _ := inviting(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/users/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login": "A-Codechecker", "type": "User"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by personal access token"}`))
	})

	_, _, err := client.AddToTeam(context.Background(), "codecheckers", "codecheckers", "a-codechecker")
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
