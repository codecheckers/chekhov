package command

import (
	"strings"
	"testing"
)

// Roles are a set: a person is whatever they are, and a command open to one of
// those is open to them.
func TestRolesAreASet(t *testing.T) {
	roles := (Roles{}).With(RoleEditor).With(RoleCodechecker)

	for _, role := range []Role{RoleEditor, RoleCodechecker, RoleAnyone} {
		if !roles.Has(role) {
			t.Errorf("%q is not held, but should be", role)
		}
	}
	for _, role := range []Role{RoleAssignedCodechecker, RoleAuthor, ""} {
		if roles.Has(role) {
			t.Errorf("%q is held, but nothing granted it", role)
		}
	}
	if len(roles) != 2 || roles[0] != RoleEditor {
		t.Errorf("the roles are held in the order they were granted, got %v", roles)
	}

	// Somebody nothing is known about is still anyone, and nothing else.
	var nobody Roles
	if !nobody.Has(RoleAnyone) || nobody.Has(RoleEditor) {
		t.Error("a person with no roles is anyone, and no more")
	}

	// Adding to a set leaves the set alone: a resolver that works out the
	// standing roles once must not widen them for everybody who comes after.
	standing := Roles{RoleEditor}
	if onThisCheck := standing.With(RoleAssignedCodechecker); standing.Has(RoleAssignedCodechecker) ||
		!onThisCheck.Has(RoleAssignedCodechecker) {
		t.Errorf("With widened the original: %v became %v", standing, onThisCheck)
	}
	// Neither sentinel can be granted, or a set would satisfy a real role by
	// accident.
	if granted := (Roles{}).With("").With(RoleAnyone); len(granted) != 0 {
		t.Errorf("a sentinel was granted: %v", granted)
	}
}

// Every role a refusal can name has words for it, or the reply reads
// "is for assigned codechecker".
func TestEveryRoleCanBeNamed(t *testing.T) {
	for _, known := range grantable {
		if known.Description == "" || known.Description == string(known.Role) {
			t.Errorf("%q is described as %q", known.Role, known.Description)
		}
	}
}

// The conflicts of interest CODECHECK exists to prevent, in both directions:
// it makes no difference which role somebody was given first.
func TestConflictingRolesAreRefused(t *testing.T) {
	cases := []struct {
		name     string
		held     Roles
		adding   Role
		conflict Role
	}{
		{
			name:     "the author of the paper cannot check it",
			held:     Roles{RoleAuthor},
			adding:   RoleAssignedCodechecker,
			conflict: RoleAuthor,
		},
		{
			name:     "nor can the codechecker turn out to be an author",
			held:     Roles{RoleAssignedCodechecker},
			adding:   RoleAuthor,
			conflict: RoleAssignedCodechecker,
		},
		{
			name:     "an author cannot handle the check of their own paper",
			held:     Roles{RoleAuthor},
			adding:   RoleHandlingEditor,
			conflict: RoleAuthor,
		},
		{
			name:     "nor can the handling editor turn out to be an author",
			held:     Roles{RoleHandlingEditor},
			adding:   RoleAuthor,
			conflict: RoleHandlingEditor,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			with, because, conflicting := test.held.Conflict(test.adding)
			if !conflicting {
				t.Fatalf("%v may not also be %q", test.held, test.adding)
			}
			if with != test.conflict {
				t.Errorf("the conflict is with %q, want %q", with, test.conflict)
			}
			if because == "" {
				t.Error("a refusal has to say why")
			}
		})
	}
}

// Most editors check papers, and somebody checking this one may well be
// looking after it too. Only being the author conflicts.
func TestTheRolesThatDoNotConflict(t *testing.T) {
	cases := []struct {
		held   Roles
		adding Role
	}{
		{held: Roles{RoleEditor}, adding: RoleCodechecker},
		{held: Roles{RoleCodechecker}, adding: RoleEditor},
		{held: Roles{RoleEditor, RoleCodechecker}, adding: RoleAssignedCodechecker},
		{held: Roles{RoleEditor, RoleHandlingEditor}, adding: RoleAssignedCodechecker},
		{held: Roles{RoleAssignedCodechecker}, adding: RoleHandlingEditor},
		{held: Roles{RoleAuthor}, adding: RoleCodechecker},
		{held: nil, adding: RoleAuthor},
	}

	for _, test := range cases {
		if with, _, conflicting := test.held.Conflict(test.adding); conflicting {
			t.Errorf("%v was refused %q because of %q", test.held, test.adding, with)
		}
	}
}

// The author of a paper is often a codechecker in general - many people are
// both - and that says nothing about who may check *this* paper. Only the
// per-check roles conflict.
func TestTheStandingRolesNeverConflict(t *testing.T) {
	for _, pair := range conflicts {
		for _, role := range []Role{pair.A, pair.B} {
			if role.Standing() {
				t.Errorf("%q is a standing role, so it cannot conflict on one check", role)
			}
		}
	}
}

// Only an editor can be the handling editor: a role a check gives out may
// still need somebody to be something already.
func TestAHandlingEditorMustBeAnEditor(t *testing.T) {
	required, needs := RoleHandlingEditor.Requires()
	if !needs || required != RoleEditor {
		t.Errorf("the handling editor requires %q (%v)", required, needs)
	}
	for _, role := range []Role{RoleAssignedCodechecker, RoleAuthor, RoleEditor} {
		if _, needs := role.Requires(); needs {
			t.Errorf("%q should be open to anybody a check names", role)
		}
	}
}

// A conflict names roles that exist, or it can never fire.
func TestConflictsNameKnownRoles(t *testing.T) {
	known := func(role Role) bool {
		for _, entry := range grantable {
			if entry.Role == role {
				return true
			}
		}
		return false
	}
	for _, pair := range conflicts {
		if !known(pair.A) || !known(pair.B) {
			t.Errorf("a conflict names a role nothing can grant: %q and %q", pair.A, pair.B)
		}
		if pair.Because == "" {
			t.Errorf("the conflict between %q and %q says no reason", pair.A, pair.B)
		}
	}
}

// The parser's handle shape and internal/github's are separate on purpose,
// and have to agree on what a handle is. Written as the same table on both
// sides rather than as an import, so that neither guard depends on the other.
func TestTheParserAgreesOnWhatAHandleIs(t *testing.T) {
	for _, written := range []string{"@a-codechecker", "nuest", "@a1", "a"} {
		if _, err := parseHandle(written); err != nil {
			t.Errorf("%q should be a handle: %v", written, err)
		}
	}
	for _, written := range []string{"", "@-a", "a-", "a--b", "a/b", "..", strings.Repeat("a", 40)} {
		if handle, err := parseHandle(written); err == nil {
			t.Errorf("%q was accepted as %q", written, handle)
		}
	}
}

// `in <team>` overrides the team, and only means something for a role that
// puts somebody in one.
func TestParseAssignmentReadsATeamOverride(t *testing.T) {
	handle, role, team, err := ParseAssignment([]string{"@a-codechecker", "as", "codechecker",
		"in", "institutional-codecheckers"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// "codechecker" on a check is the assigned codechecker; the standing role
	// of the same name comes from the team.
	if handle != "a-codechecker" || role != RoleAssignedCodechecker || team != "institutional-codecheckers" {
		t.Errorf("read %q / %q / %q", handle, role, team)
	}

	// A role with a space in it still reads as one thing.
	if _, role, team, err = ParseAssignment([]string{"@nuest", "as", "handling", "editor"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if role != RoleHandlingEditor || team != "" {
		t.Errorf("read %q / %q", role, team)
	}

	// A team means nothing for somebody who is not put in one.
	if _, _, _, err = ParseAssignment([]string{"@an-author", "as", "author", "in", "codecheckers"}); err == nil {
		t.Error("a team was accepted for an author")
	}
	// And "in" with nothing after it is a question, not a team.
	if _, _, _, err = ParseAssignment([]string{"@a-codechecker", "as", "codechecker", "in"}); err == nil {
		t.Error("an empty team was accepted")
	}
}

// A reply that tells somebody to run a command has to use a word the parser
// takes, and the plainest one: "assigned codechecker" parses but nobody types
// it.
func TestTheTypedWordForARoleParses(t *testing.T) {
	for _, role := range []Role{RoleAssignedCodechecker, RoleAuthor, RoleHandlingEditor} {
		typed := role.Typed()
		got, err := AssignableRole(typed)
		if err != nil {
			t.Errorf("%q is not a word I accept: %v", typed, err)
			continue
		}
		if got != role {
			t.Errorf("%q parses as %q, want %q", typed, got, role)
		}
	}
	if got := RoleAssignedCodechecker.Typed(); got != "codechecker" {
		t.Errorf("the assigned codechecker is typed %q", got)
	}
}
