package bot

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// The codechecker lists, the bot's second repository (#49). The handles were
// checked to belong to nobody; the ORCID is ORCID's own test record.
const (
	testLists       = "codecheckers/testing-dev-codecheckers"
	madeUpRegistrar = "a-made-up-registrant"
	registrationRow = `Josiah Carberry,@a-made-up-registrant,0000-0002-1825-0097,see ORCID page,"psychoceramics","R, Python",expired,NA,`
)

// listsServer is a test bot that works on the lists, with ORCID and the lists
// served from a stub; lists is what it posted there.
func listsServer(t *testing.T) (server *Server, register, lists *recorder, stub *testserver.Server) {
	t.Helper()
	server, register = testServer(t)
	lists = &recorder{}
	server.Lists = lists

	stub = testserver.New(t)
	stub.JSON("/orcid/0000-0002-1825-0097/person",
		`{"name": {"given-names": {"value": "Josiah"}, "family-name": {"value": "Carberry"}},
		  "emails": {"email": [{"email": "made-up@example.invalid"}]}}`)
	stub.Handle("/lists/codecheckers.csv", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n" +
			"Fake Codechecker,@fake-codechecker,0000-0000-0000-0002,see ORCID page,testing,Go,NA,NA,\n"))
	})
	server.Settings.Chekhov.Lists.Files = []string{stub.At("/lists/codecheckers.csv")}
	server.Services = &check.Services{Access: check.Access{HTTP: stub.Client(), ORCID: stub.URL + "/orcid"}}
	return server, register, lists, stub
}

// deliverEvent sends an event built here rather than read from a fixture.
func deliverEvent(t *testing.T, server *Server, eventType string, payload map[string]any) int {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return deliverBody(t, server, eventType, body).Code
}

// registrationEvent is a registration issue being opened, or commented on
// when comment is not empty.
func registrationEvent(repository, row, comment, commenter string, labels ...string) (string, map[string]any) {
	var named []map[string]string
	for _, label := range labels {
		named = append(named, map[string]string{"name": label})
	}
	issue := map[string]any{
		"number": 7,
		"title":  "Register as codechecker",
		"body":   "Your data:\n\n```csv\n" + row + "\n```\n\nHow did you hear about CODECHECK?\n",
		"user":   map[string]string{"login": madeUpRegistrar},
		"labels": named,
	}
	payload := map[string]any{
		"action":     "opened",
		"issue":      issue,
		"repository": map[string]string{"full_name": repository},
	}
	if comment == "" {
		return "issues", payload
	}
	payload["action"] = "created"
	payload["comment"] = map[string]any{"id": 99, "body": comment, "user": map[string]string{"login": commenter}}
	return "issue_comment", payload
}

func TestAnOpenedRegistrationIsAnsweredOnTheLists(t *testing.T) {
	server, register, lists, _ := listsServer(t)

	eventType, payload := registrationEvent(testLists, registrationRow, "", "", "registration")
	if code := deliverEvent(t, server, eventType, payload); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if register.count() != 0 {
		t.Errorf("the register was written to: %q", register.last())
	}
	reply := lists.last()
	for _, want := range []string{"Thanks for your interest in becoming a codechecker", "has everything the list needs"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if lists.comments[0].Repository != testLists || lists.comments[0].Issue != 7 {
		t.Errorf("posted on %s#%d", lists.comments[0].Repository, lists.comments[0].Issue)
	}
	if strings.Contains(reply, "example.invalid") {
		t.Errorf("the reply quotes a contact:\n%s", reply)
	}
}

func TestIncludeCheckLooksAgain(t *testing.T) {
	server, _, lists, stub := listsServer(t)
	stub.JSON("/orcid/0000-0002-1825-0097/person", `{"emails": {"email": []}}`)

	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include check", madeUpRegistrar, "registration")
	if code := deliverEvent(t, server, eventType, payload); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	reply := lists.last()
	for _, want := range []string{"Before you can be listed", "didn't find an email", "`@chekhovbot include check`"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "Thanks for your interest") {
		t.Errorf("asking again is thanked as though the issue were new:\n%s", reply)
	}
}

// The survey left as the template has it is asked about in the answer to the
// issue being opened, and never again: a later check does not repeat it, and
// a survey answered is not asked about.
func TestTheSurveyIsAskedAboutOnlyInTheFirstAnswer(t *testing.T) {
	for _, c := range []struct {
		name, answer, comment string
		want                  bool
	}{
		{"opened, unanswered", "_I ..._", "", true},
		{"opened, answered", "I heard of it from a colleague.", "", false},
		{"checked again, unanswered", "_I ..._", "@chekhovbot include check", false},
	} {
		server, _, lists, _ := listsServer(t)
		eventType, payload := registrationEvent(testLists, registrationRow, c.comment, madeUpRegistrar, "registration")
		issue := payload["issue"].(map[string]any)
		issue["body"] = issue["body"].(string) + "\n" + c.answer + "\n"
		if code := deliverEvent(t, server, eventType, payload); code != http.StatusAccepted {
			t.Fatalf("%s: status = %d, want 202", c.name, code)
		}
		reply := lists.last()
		if !strings.Contains(reply, "Before you can be listed") && !strings.Contains(reply, "has everything the list needs") {
			t.Errorf("%s: not an answer to the registration:\n%s", c.name, reply)
		}
		if got := strings.Contains(reply, "how you heard about CODECHECK - is still unanswered"); got != c.want {
			t.Errorf("%s: reminded = %v, want %v:\n%s", c.name, got, c.want, reply)
		}
	}
}

// Offline nothing can be checked, and a registration that could not be
// checked is not complete.
func TestARegistrationThatCannotBeCheckedIsNotComplete(t *testing.T) {
	server, _, lists, _ := listsServer(t)
	server.Services = nil

	eventType, payload := registrationEvent(testLists, registrationRow, "", "", "registration")
	deliverEvent(t, server, eventType, payload)
	if reply := lists.last(); !strings.Contains(reply, "Before you can be listed") || !strings.Contains(reply, "could not") {
		t.Errorf("an unchecked registration:\n%s", reply)
	}
}

func TestOnlyARegistrationIsAnsweredUnasked(t *testing.T) {
	server, register, lists, _ := listsServer(t)

	for _, c := range []struct{ name, comment, commenter, label string }{
		{"an issue that is not a registration", "", "", "question"},
		{"a comment not addressed to the bot", "Thanks, done!", madeUpRegistrar, "registration"},
		{"the bot's own comment", "@chekhovbot include check", "chekhovbot", "registration"},
	} {
		eventType, payload := registrationEvent(testLists, registrationRow, c.comment, c.commenter, c.label)
		if code := deliverEvent(t, server, eventType, payload); code != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", c.name, code)
		}
	}
	if lists.count()+register.count() != 0 {
		t.Errorf("something was posted: %q %q", lists.last(), register.last())
	}
}

// Each repository has its own commands, and one asked in the wrong place is
// told where it belongs.
func TestEachRepositoryAnswersItsOwnCommands(t *testing.T) {
	server, register, lists, _ := listsServer(t)

	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot check", madeUpRegistrar, "registration")
	deliverEvent(t, server, eventType, payload)
	if reply := lists.last(); !strings.Contains(reply, "I answer `@chekhovbot include check`") {
		t.Errorf("a register command on the lists:\n%s", reply)
	}

	eventType, payload = registrationEvent(testLists, registrationRow, "@chekhovbot include check", madeUpRegistrar, "question")
	deliverEvent(t, server, eventType, payload)
	if reply := lists.last(); !strings.Contains(reply, "not a registration") {
		t.Errorf("include check on an issue that is not a registration:\n%s", reply)
	}

	eventType, payload = registrationEvent(server.Settings.TargetRepository(), registrationRow, "@chekhovbot include check", madeUpRegistrar)
	deliverEvent(t, server, eventType, payload)
	if reply := register.last(); !strings.Contains(reply, "is for a registration issue on `"+testLists+"`") {
		t.Errorf("include check on the register:\n%s", reply)
	}
	if lists.count() != 2 {
		t.Errorf("the lists got %d replies, want 2", lists.count())
	}
}

// A deployment without a client for the lists does not answer there, as it
// does not answer on any other repository it was not given.
func TestListsWithoutAClientAreNotAnswered(t *testing.T) {
	server, register, _, _ := listsServer(t)
	server.Lists = nil

	eventType, payload := registrationEvent(testLists, registrationRow, "", "", "registration")
	if code := deliverEvent(t, server, eventType, payload); code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", code)
	}
	if register.count() != 0 {
		t.Errorf("posted: %q", register.last())
	}
}
