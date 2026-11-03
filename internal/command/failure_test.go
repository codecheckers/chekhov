package command

import (
	"strings"
	"testing"
)

// Only what can pass on its own tells somebody to ask again; what cannot says
// so. codecheckers/register#238 was told "ask me again" about a permission.
func TestOnlyAFailureThatPassesSaysAskAgain(t *testing.T) {
	for failure, askAgain := range map[Failure]bool{
		FailureOther: true, FailureLimited: true,
		FailureRefused: false, FailureUnauthorised: false,
	} {
		why := failure.Why()
		if got := strings.Contains(why, "Ask me again"); got != askAgain {
			t.Errorf("%d: says ask me again %v, want %v: %s", failure, got, askAgain, why)
		}
		if !askAgain && !strings.Contains(why, "Asking again will not help") {
			t.Errorf("%d: does not say asking again will not help: %s", failure, why)
		}
	}
}

// The sweep's reply when somebody joined says the assignee too, as `assign`
// does.
func TestJoinedSaysWhyTheAssigneeWasNotSet(t *testing.T) {
	reply := JoinedReply(Joined{Handle: "a-codechecker", Role: RoleAssignedCodechecker, Granted: true,
		Assignee: AssigneeNote("a-codechecker", false, FailureLimited.Why()), Organisation: "codecheckers"})
	if !strings.Contains(reply, "I could not make `@a-codechecker` the assignee of this issue") ||
		!strings.Contains(reply, "Ask me again in a few minutes") {
		t.Errorf("the reply does not say why the assignee was not set:\n%s", reply)
	}
}
