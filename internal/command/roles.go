package command

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Who somebody is on one check, and what that lets them do.
//
// Two kinds of role, stored differently on purpose. A standing role - editor,
// codechecker - is a property of a person, and the organisation maintains it
// as a GitHub team, which the bot reads with the token's Members: read
// permission and holds in memory for a day, see internal/people. A per-check
// role - the handling editor, the assigned codechecker, an author - is a
// property of a person *on one issue*, and has nowhere to live but that issue.
//
// A team that cannot be read means nobody holds that role, never that everyone
// does.

// Role is something the person writing the comment is, on this check.
type Role string

const (
	// RoleAnyone is everybody, including people not in the organisation.
	RoleAnyone Role = "anyone"
	// RoleEditor is a CODECHECK editor, who may run the commands that change
	// the register.
	RoleEditor Role = "editor"
	// RoleCodechecker is somebody who performs CODECHECKs, whether or not they
	// are doing this one.
	RoleCodechecker Role = "codechecker"
	// RoleHandlingEditor is the editor looking after *this* check.
	RoleHandlingEditor Role = "handling editor"
	// RoleAssignedCodechecker is the codechecker doing *this* check.
	RoleAssignedCodechecker Role = "assigned codechecker"
	// RoleAuthor wrote the paper under check.
	RoleAuthor Role = "author"
)

// grantable are the roles a person can be given, in the order they are worth
// reading - the standing ones first, then the ones a check grants - with the
// words a refusal uses for each. One table, so that adding a role is one entry
// rather than several lists to keep in step.
var grantable = []struct {
	Role        Role
	Description string
	// Standing says the role is a property of the person, maintained by the
	// organisation as a team, rather than of one check.
	Standing bool
	// Requires is the standing role somebody has to hold already before a
	// check can give them this one, empty when anybody may hold it.
	Requires Role
}{
	{Role: RoleEditor, Description: "editors", Standing: true},
	{Role: RoleCodechecker, Description: "codecheckers", Standing: true},
	{Role: RoleHandlingEditor, Description: "the handling editor", Requires: RoleEditor},
	{Role: RoleAssignedCodechecker, Description: "the assigned codechecker"},
	{Role: RoleAuthor, Description: "the paper's authors"},
}

// conflicts are the roles one person cannot hold at once on the same check.
//
// These are conflicts of interest, not bookkeeping. CODECHECK exists so that
// somebody other than the author runs the code, and so that the editor
// deciding whether a check is finished is not the person whose paper it is.
// Being an editor and a codechecker at the same time is no conflict at all -
// most editors check papers - so the standing roles never conflict.
//
// Handling editor and assigned codechecker is deliberately allowed: an editor
// looking after a check may also be the one performing it, which is ordinary
// in a small community. It is the pairing a reader will look for here, so it
// is written down rather than left to the absence of a line.
var conflicts = []struct {
	A, B    Role
	Because string
}{
	{RoleAssignedCodechecker, RoleAuthor, "a codechecker cannot check a paper they wrote"},
	{RoleHandlingEditor, RoleAuthor, "an editor cannot handle the check of a paper they wrote"},
}

// Description is the role in words, for a refusal that has to say what the
// asker would have to be.
func (r Role) Description() string {
	for _, known := range grantable {
		if known.Role == r {
			return known.Description
		}
	}
	return string(r)
}

// Standing reports whether the role belongs to the person rather than to one
// check: an editor is an editor everywhere, an author is an author of one
// paper.
func (r Role) Standing() bool {
	for _, known := range grantable {
		if known.Role == r {
			return known.Standing
		}
	}
	return false
}

// Checks reports whether a role means the person performs CODECHECKs, and so
// belongs in a codechecker team. An author does not - CODECHECK exists so that
// somebody other than the author runs the code - and the handling editor is an
// editor already.
func (r Role) Checks() bool {
	return r == RoleCodechecker || r == RoleAssignedCodechecker
}

// Requires is the standing role somebody must already hold before a check can
// give them this one: only an editor can be the handling editor.
func (r Role) Requires() (Role, bool) {
	for _, known := range grantable {
		if known.Role == r && known.Requires != "" {
			return known.Requires, true
		}
	}
	return "", false
}

// Roles are everything one person is on one check.
//
// A set rather than a single role: the same person can be an editor and the
// codechecker assigned to this check, and a command either of them may run has
// to be open to them once rather than twice. A slice because there are a
// handful of roles and never more: the order is the order they were granted
// in, and the zero value is somebody nothing is known about.
type Roles []Role

// With returns the roles plus one more, leaving the original alone.
//
// Copied rather than appended in place: a resolver that works out the standing
// roles once and adds a per-check role to them must not widen what every other
// caller sees.
func (r Roles) With(role Role) Roles {
	if role == "" || role == RoleAnyone || r.Has(role) {
		return r
	}
	return append(append(make(Roles, 0, len(r)+1), r...), role)
}

// Has reports whether the person holds a role. Everybody is RoleAnyone, and
// somebody who holds nothing holds nothing else.
func (r Roles) Has(role Role) bool {
	return role == RoleAnyone || slices.Contains(r, role)
}

// Conflict reports the role somebody already holds that stops this one being
// given to them, and why.
//
// It is asked before a role is granted, not after: a check whose author is
// also its codechecker is not a CODECHECK, and the assignment is refused with
// the reason rather than recorded and regretted.
func (r Roles) Conflict(role Role) (Role, string, bool) {
	for _, pair := range conflicts {
		switch {
		case pair.A == role && r.Has(pair.B):
			return pair.B, pair.Because, true
		case pair.B == role && r.Has(pair.A):
			return pair.A, pair.Because, true
		}
	}
	return "", "", false
}

// AssignableRole reads the role an editor named in an assign or remove
// command.
//
// Only the per-check roles can be assigned. Editor and codechecker come from
// the organisation's teams, and a command that appeared to hand them out would
// be lying about what it had done.
func AssignableRole(words string) (Role, error) {
	switch strings.Join(strings.Fields(strings.ToLower(words)), " ") {
	case "":
		return "", fmt.Errorf("no role named; I can assign %s", assignableRoles())
	case "codechecker", "assigned codechecker", "checker":
		return RoleAssignedCodechecker, nil
	case "author", "authors":
		return RoleAuthor, nil
	case "handling editor", "handling-editor", "handling":
		return RoleHandlingEditor, nil
	case "editor", "editors":
		return "", fmt.Errorf("editors come from a team on GitHub, not from a command; " +
			"did you mean `handling editor`, the editor looking after this check?")
	default:
		return "", fmt.Errorf("%q is not a role I can assign; I can assign %s", words, assignableRoles())
	}
}

// Typed is the shortest word AssignableRole accepts for a role, which is what
// a reply should put in front of somebody as a command to run: "assigned
// codechecker" parses, but nobody types it.
func (r Role) Typed() string {
	switch r {
	case RoleAssignedCodechecker:
		return "codechecker"
	case RoleHandlingEditor:
		return "handling editor"
	default:
		return string(r)
	}
}

func assignableRoles() string {
	var names []string
	for _, known := range grantable {
		if !known.Standing {
			names = append(names, "`"+string(known.Role)+"`")
		}
	}
	return strings.Join(names, ", ")
}

// Handle is how a GitHub handle is written wherever this bot keeps one:
// lowercased, and without the @ somebody typed in front of it. GitHub handles
// are case-insensitive, and the @ belongs to the sentence rather than to the
// name. One normaliser, because a handle written two ways is two people to
// every comparison that matters - a role, a team, an exclusion.
func Handle(written string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(written), "@"))
}

// parseHandle is how every command reads the person it was given: normalised,
// and refused in one wording when it is not a handle at all.
func parseHandle(written string) (string, error) {
	handle := Handle(written)
	if !gitHubHandle.MatchString(handle) {
		return "", fmt.Errorf("%q is not a GitHub handle", written)
	}
	return handle, nil
}

// gitHubHandle is what GitHub allows: letters, digits and single hyphens, up
// to 39 characters, and not starting or ending with one.
//
// Checked before a handle is stored, because the record is a markdown table in
// a comment: a handle with a pipe in it would break the row for every later
// reader, and one with an HTML comment in it would be worse.
//
// internal/github has the same pattern, deliberately, and neither should be
// made to depend on the other. That one guards a request path, where a slash
// or a dot segment moves the request to a different endpoint; this one guards
// a comment. Each has to fail closed on its own, and this package has no
// business importing the thing that writes to GitHub.
var gitHubHandle = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}$`)

// ParseAssignment reads `@user as <role>` and `@user as <role> in <team>`,
// the arguments of assign and remove.
//
// "as" is optional, because half the people who write the command will leave
// it out, and the words after it may be several: `handling editor` is a role
// with a space in it.
//
// `in <team>` overrides the team a codechecker is put in. The bot works the
// team out from the check - an institutional check sends them to the
// institutional team - and an editor knows things the labels do not, in both
// directions: an institutional codechecker taking an ordinary check, or an
// ordinary one standing in on an institutional one. The team is returned as
// written; which teams may be named at all is the settings' business, not the
// parser's.
func ParseAssignment(args []string) (handle string, role Role, team string, err error) {
	if len(args) == 0 {
		return "", "", "", fmt.Errorf(
			"who, and as what? For example `%s assign @a-codechecker as codechecker`", Bot)
	}
	handle, err = parseHandle(args[0])
	if err != nil {
		return "", "", "", err
	}

	rest := args[1:]
	if len(rest) > 0 && strings.EqualFold(rest[0], "as") {
		rest = rest[1:]
	}
	// "in" separates the role from the team, so that a role with a space in it
	// still reads as one thing.
	for i, word := range rest {
		if !strings.EqualFold(word, "in") {
			continue
		}
		team = strings.Join(rest[i+1:], " ")
		rest = rest[:i]
		if strings.TrimSpace(team) == "" {
			return "", "", "", fmt.Errorf("in which team? For example "+
				"`%s assign @a-codechecker as codechecker in institutional-codecheckers`", Bot)
		}
		break
	}

	role, err = AssignableRole(strings.Join(rest, " "))
	if err != nil {
		return "", "", "", err
	}
	if team != "" && !role.Checks() {
		return "", "", "", fmt.Errorf("a %s is not put in a codechecker team, so `in %s` "+
			"means nothing here", role, team)
	}
	return handle, role, strings.TrimSpace(team), nil
}
