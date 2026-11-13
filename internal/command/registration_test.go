package command

import (
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/registration"
)

// A complete registration promises the team only to somebody not in it
// already (codecheckers/chekhov#49).
func TestACompleteRegistrationPromisesOnlyWhatIsLeft(t *testing.T) {
	row := registration.Row{Name: "Josiah Carberry", Handle: "@a-made-up-registrant"}

	outside := RegistrationReply(registration.Report{Row: row}, false, false)
	if !strings.Contains(outside, "add you to the list and to the team.") {
		t.Errorf("somebody outside the team is not told about the team:\n%s", outside)
	}

	inside := RegistrationReply(registration.Report{Row: row, InTeam: true}, false, false)
	if strings.Contains(inside, "to the team") || !strings.Contains(inside, "add you to the list.") {
		t.Errorf("somebody in the team is promised the team:\n%s", inside)
	}
}

// The editor who ran `include` is asked alone, and the owners are not named:
// the editor is one of them. A record with no editor names them instead, and
// the development note is said only under that list.
func TestIncludeOutsideReplyAsksTheEditorAlone(t *testing.T) {
	request := OutsideRequest{Handle: "a-made-up-registrant", Organisation: "codecheckers",
		Team: "codecheckers", Owners: []string{"nuest", "an-owner"}, Configured: true}
	reply := IncludeOutsideReply(request, "nuest")
	if !strings.Contains(reply, "\n@nuest — please invite `@a-made-up-registrant`") ||
		strings.Contains(reply, "an-owner") || strings.Contains(reply, "configured") {
		t.Errorf("the editor's reply:\n%s", reply)
	}
	reply = IncludeOutsideReply(request, "")
	for _, want := range []string{"An owner has to invite", ": `@nuest`, `@an-owner`.", "configured"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}
