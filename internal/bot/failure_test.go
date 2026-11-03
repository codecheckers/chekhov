package bot

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/httpretry"
)

// What GitHub answered on codecheckers/register#238, and what it answers when
// the bot writes too fast: both a 403, and only one of them passes.
var (
	refusedPermission = fmt.Errorf("wrapped: %w", &httpretry.StatusError{Service: "GitHub",
		Status: http.StatusForbidden, Body: `{"message":"Resource not accessible by personal access token"}`})
	secondaryLimit = fmt.Errorf("wrapped: %w", &httpretry.StatusError{Service: "GitHub",
		Status: http.StatusForbidden, Body: `{"message":"You have exceeded a secondary rate limit"}`})
)

// failureCases is each 403, and what the reply has to say about it - never
// GitHub's own body.
var failureCases = []struct {
	name string
	err  error
	says string
}{
	{"permission", refusedPermission, "Asking again will not help"},
	{"rate limit", secondaryLimit, "Ask me again in a few minutes"},
}

func saysWhy(t *testing.T, name, reply, want string) {
	t.Helper()
	if !strings.Contains(reply, want) {
		t.Errorf("%s: the reply does not say %q:\n%s", name, want, reply)
	}
	if strings.Contains(reply, "Resource not accessible") || strings.Contains(reply, "secondary rate limit") {
		t.Errorf("%s: GitHub's own answer reached the reply:\n%s", name, reply)
	}
}

// The assignee that could not be set is said, and why: on
// codecheckers/register#238 the reply read as though it had been.
func TestAnAssigneeThatCouldNotBeSetIsSaid(t *testing.T) {
	for _, tc := range failureCases {
		server, replies := organised(t)
		replies.assignErr = tc.err

		reply := answer(t, server, command.Assign, []string{"@a-codechecker", "as", "codechecker"}, "")
		if !strings.Contains(reply, "✖ I could not make `@a-codechecker` the assignee of this issue") {
			t.Errorf("%s: the reply does not say the assignee was not set:\n%s", tc.name, reply)
		}
		if !strings.Contains(reply, "is now the assigned codechecker") {
			t.Errorf("%s: the role was not recorded over the assignee:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)
	}
}

// The title, likewise: it said only that it could not be changed.
func TestATitleThatCouldNotBeChangedSaysWhy(t *testing.T) {
	for _, tc := range failureCases {
		server, register, _ := registered(t)
		register.titleErr = tc.err

		reply := answer(t, server, command.CertificateSet, nil, "")
		if !strings.Contains(reply, "✖ I could not change the title") || !strings.Contains(reply, "after a `|`") {
			t.Errorf("%s: the reply does not say the title stayed:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)
	}
}

// A label, which used to pass on GitHub's answer whole.
func TestALabelThatCouldNotBeMovedSaysWhy(t *testing.T) {
	for _, tc := range failureCases {
		server, path := checking(t, "", "needs codechecker")
		path.refuse = tc.err
		assignCodechecker(t, server, "a-codechecker")

		reply := answer(t, server, command.CheckStart, nil, "")
		if !strings.Contains(reply, "✖ could not remove `needs codechecker`") {
			t.Errorf("%s: the reply does not report the label:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)
	}
}

// GitHub takes an assignee it cannot assign without an error, and leaves them
// out of the answer; the reply says so rather than nothing.
func TestAnAssigneeGitHubLeftOutIsSaid(t *testing.T) {
	server, replies := organised(t)
	replies.ignore = true

	reply := answer(t, server, command.Assign, []string{"@a-codechecker", "as", "codechecker"}, "")
	if !strings.Contains(reply, "✖ I could not make `@a-codechecker` the assignee") ||
		!strings.Contains(reply, "left them out") {
		t.Errorf("the reply does not say the assignee was left out:\n%s", reply)
	}
}

// The codechecker replaced, or removed, who could not be taken off as
// assignee is still on the issue, and the reply says so.
func TestAnAssigneeThatCouldNotBeRemovedIsSaid(t *testing.T) {
	for _, tc := range failureCases {
		server, replies := organised(t)
		replies.members["a-made-up-codechecker"] = true
		replies.inTeam["a-made-up-codechecker"] = true
		answer(t, server, command.Assign, []string{"@a-codechecker", "as", "codechecker"}, "")
		replies.unassignErr = tc.err

		reply := answer(t, server, command.Assign, []string{"@a-made-up-codechecker", "as", "codechecker"}, "")
		if !strings.Contains(reply, "✖ I could not take `@a-codechecker` off as assignee") {
			t.Errorf("%s: reassigning does not say the old assignee stayed:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)

		reply = answer(t, server, command.Remove, []string{"@a-made-up-codechecker", "as", "codechecker"}, "")
		if !strings.Contains(reply, "✖ I could not take `@a-made-up-codechecker` off as assignee") {
			t.Errorf("%s: removing does not say the assignee stayed:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)
	}
}

// A record GitHub would not write says what was not recorded, not only why.
func TestARecordThatCouldNotBeWrittenSaysWhat(t *testing.T) {
	for _, tc := range failureCases {
		server, _ := testServer(t)
		server.Checks.Comments.(*issueComments).err = tc.err

		reply := answer(t, server, command.Assign, []string{"@a-codechecker", "as", "codechecker"}, "")
		if !strings.Contains(reply, "✖ I could not record the role.") {
			t.Errorf("%s: the reply does not say the role was not recorded:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)
	}
}

// `certificate set`'s label, worded as a sentence after the request to add it.
func TestACertificateLabelThatCouldNotBeAddedSaysWhy(t *testing.T) {
	for _, tc := range failureCases {
		server, register, _ := registered(t)
		register.labelErr = tc.err

		reply := answer(t, server, command.CertificateSet, nil, "")
		if !strings.Contains(reply, "I could not add the label `id assigned`, so please add it yourself.") {
			t.Errorf("%s: the reply does not ask for the label:\n%s", tc.name, reply)
		}
		saysWhy(t, tc.name, reply, tc.says)
	}
}
