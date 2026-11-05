package command

import (
	"strings"
	"testing"
	"time"
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

// A write not done is one shape wherever it is said: marked, worded "I could
// not", and its reason a sentence - the reason GitHub gave or none.
func TestAWriteNotDoneIsOneShape(t *testing.T) {
	for _, tc := range []struct{ what, why, want string }{
		{"record the identifier", "github answered 502", "✖ I could not record the identifier. Github answered 502."},
		{"open the pull request", FailureRefused.Why(), "✖ I could not open the pull request. GitHub refused me permission"},
		{"put them in `o/t`", "", "✖ I could not put them in `o/t`."},
	} {
		if got := NotDone(tc.what, tc.why); !strings.HasPrefix(got, tc.want) || strings.HasSuffix(got, " ") {
			t.Errorf("NotDone(%q, %q) = %q, want it to start %q", tc.what, tc.why, got, tc.want)
		}
	}
	if got := TeamFailed("o", "t", "it failed"); got != "✖ I could not put them in `o/t`. It failed." {
		t.Errorf("TeamFailed = %q", got)
	}
}

// The record changed is marked done on its own line, as the writes reported
// beneath it are: a bare headline above "✔ Assignee of this issue set." read
// as though only the assignee had been confirmed.
func TestARecordChangedIsMarkedDone(t *testing.T) {
	when := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	for name, reply := range map[string]string{
		"assigned": AssignedReply(RoleAssignedCodechecker, "a-codechecker", "", "", "nuest", when),
		"removed":  RemovedReply(RoleAssignedCodechecker, "a-codechecker", true, "", "nuest", when),
		"adopted":  AcceptedReply(Holders{}, "nuest", true, when),
		"row":      RowSet(Holders{}, "nuest"),
		"identifier": CertificateReservedReply(CertificateReserved{ID: "1970-022", Label: "id assigned"},
			"nuest", when),
	} {
		if !strings.HasPrefix(reply, "✔ ") {
			t.Errorf("%s: the change is not marked done:\n%s", name, reply)
		}
	}
	// Nothing changed is not a change done.
	for name, reply := range map[string]string{
		"not held": RemovedReply(RoleAssignedCodechecker, "a-codechecker", false, "", "nuest", when),
		"already": CertificateReservedReply(CertificateReserved{ID: "1970-022", Unchanged: true},
			"nuest", when),
	} {
		if strings.HasPrefix(reply, "✔ ") {
			t.Errorf("%s: an unchanged record is marked done:\n%s", name, reply)
		}
	}
}
