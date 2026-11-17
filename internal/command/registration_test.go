package command

import (
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/registration"
)

// The survey reminder names the questions left and no others, and is nothing
// when none is (codecheckers/chekhov#49).
func TestTheSurveyReminderNamesOnlyWhatIsLeft(t *testing.T) {
	asked := func(topics ...string) []registration.SurveyQuestion {
		var questions []registration.SurveyQuestion
		for _, topic := range topics {
			questions = append(questions, registration.SurveyQuestion{Topic: topic})
		}
		return questions
	}
	for _, c := range []struct {
		left []registration.SurveyQuestion
		want []string
	}{
		{nil, nil},
		{asked("a"), []string{"the short question at the end of your first post - a - is still", "your answer! It helps",
			"You can add it to"}},
		{asked("a", "b"), []string{"the two short questions at the end of your first post - a and b - are still",
			"your answers! They help", "You can add them to"}},
		{asked("a", "b", "c"), []string{"the three short questions", "- a, b, and c - are still"}},
	} {
		reminder := SurveyReminder(c.left)
		if c.want == nil && reminder != "" {
			t.Errorf("nothing left, and reminded:\n%s", reminder)
		}
		for _, want := range c.want {
			if !strings.Contains(reminder, want) {
				t.Errorf("%d left: the reminder does not say %q:\n%s", len(c.left), want, reminder)
			}
		}
		if strings.Contains(reminder, "@") {
			t.Errorf("the reminder names somebody:\n%s", reminder)
		}
	}
}

// A complete registration promises the team only to somebody not in it
// already (codecheckers/chekhov#49).
func TestACompleteRegistrationPromisesOnlyWhatIsLeft(t *testing.T) {
	row := registration.Row{Name: "Josiah Carberry", Handle: "@a-made-up-registrant"}

	outside := RegistrationReply(registration.Report{Row: row}, false, false, "")
	if !strings.Contains(outside, "add you to the list and to the team.") {
		t.Errorf("somebody outside the team is not told about the team:\n%s", outside)
	}

	inside := RegistrationReply(registration.Report{Row: row, InTeam: true}, false, false, "")
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

// The notes below the request to edit the row say they hold nothing up, since
// a list there reads as more to fix; a complete row's notes need no such
// lead-in (codecheckers/codecheckers#98).
func TestNotesBelowTheRequestSayTheyHoldNothingUp(t *testing.T) {
	row := registration.Row{Name: "Josiah Carberry", Handle: "@a-made-up-registrant"}
	note := registration.Finding{Outcome: registration.Note, Text: "a note"}
	missing := registration.Finding{Outcome: registration.Failed, Text: "something missing"}
	const lead = "Nothing below holds you up:"

	incomplete := RegistrationReply(registration.Report{Row: row,
		Findings: []registration.Finding{missing, note}}, false, false, "")
	ask, said, noted := strings.Index(incomplete, "for me to look again."),
		strings.Index(incomplete, lead), strings.Index(incomplete, "a note")
	if ask < 0 || said < ask || noted < said {
		t.Errorf("the note is not introduced after the request to look again:\n%s", incomplete)
	}

	complete := RegistrationReply(registration.Report{Row: row,
		Findings: []registration.Finding{note}}, false, false, "")
	if strings.Contains(complete, lead) || !strings.Contains(complete, "a note") {
		t.Errorf("a complete row's note:\n%s", complete)
	}
	if only := RegistrationReply(registration.Report{Row: row,
		Findings: []registration.Finding{missing}}, false, false, ""); strings.Contains(only, lead) {
		t.Errorf("a lead-in with nothing below it:\n%s", only)
	}
}
