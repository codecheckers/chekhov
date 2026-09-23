package command

import (
	"strings"
	"testing"
	"time"
)

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
