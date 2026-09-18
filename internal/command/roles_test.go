package command

import "testing"

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
