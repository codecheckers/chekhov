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
