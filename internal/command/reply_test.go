package command

import (
	"strings"
	"testing"
	"time"
)

// Somebody saying goodbye may think the check is now closed; the reply says
// what does that.
func TestGoodbyeReplyPointsAtCheckFinish(t *testing.T) {
	reply := GoodbyeReply()
	for _, want := range []string{"Goodbye", "changes nothing", "`" + Bot + " " + string(CheckFinish) + "`", "for editors"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q: %s", want, reply)
		}
	}
}

// A register-wide failure is one problem per open issue. The reply says how
// many it left out rather than growing past what GitHub will accept.
func TestASweepReplyBoundsTheProblemsItLists(t *testing.T) {
	swept := Swept{Issues: 200}
	for range mostProblemsListed + 7 {
		swept.Problems = append(swept.Problems, "could not read the comments: 401")
	}

	reply := SweptReply(swept)

	if listed := strings.Count(reply, "Could not check:"); listed != mostProblemsListed {
		t.Errorf("listed %d problems, want %d:\n%s", listed, mostProblemsListed, reply)
	}
	if !strings.Contains(reply, "And 7 more like that") {
		t.Errorf("the reply does not say what it left out:\n%s", reply)
	}
}

// The certificate is part of the record, so every reading of the record shows
// it: the comment the bot keeps in the issue, the `roles` reply, and an
// adoption. It used to be appended to the record comment alone, so an editor
// who ran `roles` after `set certificate` was not shown the identifier they
// had just reserved - the drift RolesTable exists to prevent.
func TestEveryReadingOfTheRecordShowsTheCertificate(t *testing.T) {
	holders := Holders{AssignedCodechecker: "a-codechecker", Certificate: "2026-072"}
	teams := Teams{Editors: "editors", Codecheckers: "codecheckers"}

	for name, body := range map[string]string{
		"the roles table": RolesTable(holders),
		"the roles reply": RolesReply(holders, teams, nil, Provenance{}),
		"an adoption":     AcceptedReply(holders, "nuest", true, time.Now()),
	} {
		if !strings.Contains(body, "`2026-072`") {
			t.Errorf("%s does not show the certificate:\n%s", name, body)
		}
	}

	// A check that has reserved none says nothing, rather than showing a blank.
	if body := RolesTable(Holders{}); strings.Contains(body, "Certificate") {
		t.Errorf("a check with no certificate mentions one:\n%s", body)
	}
}

// What an editor set for the register row is part of the record as well, and
// shown wherever the certificate is (#21); a field left to the issue is not.
func TestEveryReadingOfTheRecordShowsTheRegisterRow(t *testing.T) {
	holders := Holders{Certificate: "2026-072", Type: "community", Venue: "codecheck NL"}
	teams := Teams{Editors: "editors", Codecheckers: "codecheckers"}

	for name, body := range map[string]string{
		"the roles table": RolesTable(holders),
		"the roles reply": RolesReply(holders, teams, nil, Provenance{}),
		"an adoption":     AcceptedReply(holders, "nuest", true, time.Now()),
	} {
		if !strings.Contains(body, "**Register row**: type `community`, venue `codecheck NL`") {
			t.Errorf("%s does not show the register row:\n%s", name, body)
		}
	}

	if body := RolesTable(Holders{Certificate: "2026-072"}); strings.Contains(body, "Register row") {
		t.Errorf("a check with nothing set for its row mentions one:\n%s", body)
	}
}
