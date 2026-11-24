package command

import (
	"strings"
	"testing"
)

// A mention is what GitHub notifies: an @handle outside code, not in a word
// or an address, in any case (#68).
func TestMentionsIsWhatGitHubNotifies(t *testing.T) {
	for body, want := range map[string]bool{
		"@chekhovbot assign @a-made-up-newcomer as codechecker": true,
		"thanks, @A-Made-Up-Newcomer!":                          true,
		"(@a-made-up-newcomer)":                                 true,
		"is `@a-made-up-newcomer` right?":                       false,
		"```\n@a-made-up-newcomer\n```":                         false,
		"mail a@a-made-up-newcomer.org":                         false,
		"@a-made-up-newcomer-two":                               false,
		"github.com/@a-made-up-newcomer":                        false,
		"cc @a-made-up-editor @a-made-up-newcomer":              true,
		"@a-made-up-editor\n@a-made-up-newcomer":                true,
		"ping @a-made-up-newcomer/editors":                      false,
		"<!-- @a-made-up-newcomer -->":                          false,
	} {
		if got := Mentions(body, "@a-made-up-newcomer"); got != want {
			t.Errorf("Mentions(%q) = %v, want %v", body, got, want)
		}
	}
}

// `invite` is listed on the register for an editor, and not for anybody else.
func TestInviteIsListedForEditors(t *testing.T) {
	listed := func(roles Roles) bool {
		for _, definition := range Visible(roles) {
			if definition.Name == Invite {
				return true
			}
		}
		return false
	}
	if !listed(Roles{RoleEditor}) || listed(Roles{RoleAnyone}) {
		t.Errorf("listed for an editor %v, for anybody %v", listed(Roles{RoleEditor}), listed(Roles{RoleAnyone}))
	}
}

// The registration says, for somebody outside, what is to come, and either way
// mentions the registrant and nobody else: the template's example handles
// are real accounts.
func TestARegistrationFromOutsideNotifiesOnlyTheRegistrant(t *testing.T) {
	body := RegistrationInvite("a-made-up-newcomer", "codecheckers.csv", "codecheckers")
	if !strings.Contains(body, "https://github.com/orgs/codecheckers/invitation") {
		t.Errorf("no link to the invitation:\n%s", body)
	}
	for _, body := range []string{body, RegistrationInvite("a-made-up-newcomer", "codecheckers.csv", "")} {
		found := mentioned(withoutCode(body))
		if len(found) != 1 || found[0] != "a-made-up-newcomer" {
			t.Errorf("mentions %q, want only the registrant:\n%s", found, body)
		}
	}
}

// The ask to the owners says where the invitation is sent from.
func TestTheAskToTheOwnersLinksWhereToInvite(t *testing.T) {
	for _, role := range []Role{"", RoleAssignedCodechecker} {
		reply := OutsideReply(OutsideRequest{Handle: "a-made-up-newcomer", Role: role, Organisation: "codecheckers",
			Team: "codecheckers", Owners: []string{"nuest"}})
		if !strings.Contains(reply, "*Invite member* at https://github.com/orgs/codecheckers/people") {
			t.Errorf("no link to the people page for role %q:\n%s", role, reply)
		}
	}
	unread := OutsideReply(OutsideRequest{Handle: "a-made-up-newcomer", Organisation: "codecheckers",
		Unreadable: "GitHub answered 403"})
	if strings.Contains(unread, "/people") {
		t.Errorf("a link with nobody asked:\n%s", unread)
	}
}
