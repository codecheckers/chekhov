package command

import (
	"slices"
	"sort"
	"strings"
)

// Role is something the person writing the comment is, on this check.
//
// Two kinds, stored differently on purpose. A standing role - editor,
// codechecker - is a property of a person, and the organisation maintains it
// as a GitHub team, which the bot reads with the token's Members: read
// permission and holds in memory for a day, see internal/people. A per-check
// role - the assigned codechecker, an author - is a property of a person *on
// one issue*, and has nowhere to live but that issue.
//
// A team that cannot be read means nobody holds that role, never that everyone
// does.
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
	// RoleAssignedCodechecker is the codechecker doing *this* check.
	RoleAssignedCodechecker Role = "assigned codechecker"
	// RoleAuthor wrote the paper under check.
	RoleAuthor Role = "author"
)

// grantable are the roles a person can be given, in the order they are worth
// reading - the standing ones first, then the ones a check grants - with the
// words a refusal uses for each. One table, so that adding a role is one entry
// rather than three lists to keep in step.
var grantable = []struct {
	Role        Role
	Description string
}{
	{RoleEditor, "editors"},
	{RoleCodechecker, "codecheckers"},
	{RoleAssignedCodechecker, "the assigned codechecker"},
	{RoleAuthor, "the paper's authors"},
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

// Roles are everything one person is on one check.
//
// A set rather than a single role: the same person can be an editor and the
// codechecker assigned to this check, and a command either of them may run has
// to be open to them once rather than twice. A slice because there are four
// roles and never more: the order is the order they were granted in, and the
// zero value is somebody nothing is known about.
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

// Groups the listing is arranged in, in the order they are shown.
const (
	GroupBasics     = "Basics"
	GroupValidation = "Validation"
	GroupRegister   = "Register"
)

// A Definition is one command the bot knows: what it is called, what it does,
// and who may run it.
//
// The registry is the single source for all of that. The help listing is
// generated from it, so a command cannot be added and then forgotten in the
// documentation - which is the whole point of having a registry rather than a
// switch.
type Definition struct {
	Name    Name
	Aliases []string
	// Summary is one line, shown in the listing.
	Summary string
	// Usage is how the command is written, without the mention.
	Usage string
	Group string
	Role  Role
	// Hidden keeps a working command out of the listing. hello is hidden
	// because it is a smoke test rather than something a codechecker needs,
	// and thanks because a listing is for what to do next, not for manners.
	Hidden bool
}

// registry is every command the bot answers to.
var registry = []Definition{
	{
		Name:    Commands,
		Aliases: []string{"help"},
		Summary: "List the commands you can use",
		Usage:   "commands",
		Group:   GroupBasics,
		Role:    RoleAnyone,
	},
	{
		Name:    Hello,
		Summary: "Check that the bot is awake",
		Usage:   "hello",
		Group:   GroupBasics,
		Role:    RoleAnyone,
		Hidden:  true,
	},
	{
		Name:    Thanks,
		Aliases: []string{"thank", "thx", "cheers"},
		Summary: "Acknowledge thanks, in the words of the bot's namesake",
		Usage:   "thanks",
		Group:   GroupBasics,
		Role:    RoleAnyone,
		Hidden:  true,
	},
	{
		Name:    Version,
		Summary: "Report which build is running, and which register it works on",
		Usage:   "version",
		Group:   GroupBasics,
		Role:    RoleAnyone,
	},
	{
		Name:    Refresh,
		Summary: "Read the organisation's teams again, after a membership change",
		Usage:   "refresh teams",
		Group:   GroupBasics,
		Role:    RoleEditor,
	},
	{
		Name:    Check,
		Summary: "Validate a `codecheck.yml` against the CODECHECK rules",
		Usage:   "check [config|metadata|bundle|references|report|register] <repository, or certificate>",
		Group:   GroupValidation,
		Role:    RoleAnyone,
	},
	{
		Name:    Announce,
		Summary: "Toot about a published certificate: a preview first, then `confirm` to post",
		Usage:   "announce <certificate> [confirm]",
		Group:   GroupRegister,
		Role:    RoleEditor,
	},
	{
		Name: Follow,
		Summary: "Follow a certificate's accounts and curate the Codecheckers/Authors/Venues collections: " +
			"a preview first, then `confirm` to act",
		Usage: "follow <certificate> [confirm]",
		Group: GroupRegister,
		Role:  RoleEditor,
	},
}

// index maps every name and alias to its definition.
var index = func() map[string]Definition {
	byWord := make(map[string]Definition, len(registry)*2)
	for _, definition := range registry {
		byWord[string(definition.Name)] = definition
		for _, alias := range definition.Aliases {
			byWord[alias] = definition
		}
	}
	return byWord
}()

// Definitions returns the registry, in listing order.
func Definitions() []Definition { return registry }

// Lookup finds the command a word names, aliases included.
func Lookup(word string) (Definition, bool) {
	definition, found := index[normalize(word)]
	return definition, found
}

// normalize is what a command word looks like once the sentence around it is
// taken off: "Thanks!" and "check." are the command with the punctuation of
// the comment they were written in. Lookup and Suggest both go through it, so
// that a typo is measured against the same word a match would have been.
func normalize(word string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(word)), "!.,;:?")
}

// Permits reports whether a role may run the command.
func (d Definition) Permits(roles Roles) bool {
	return roles.Has(d.Role)
}

// Visible returns the commands a role should be told about: the ones they may
// run, minus the hidden ones. A command someone may not run is absent rather
// than shown and refused, so the listing is a list of what to do next.
func Visible(roles Roles) []Definition {
	var visible []Definition
	for _, definition := range registry {
		if !definition.Hidden && definition.Permits(roles) {
			visible = append(visible, definition)
		}
	}
	return visible
}

// Suggest finds the command a misspelling was probably meant to be.
//
// Only a near miss counts: "registr" is a typo for "register", "polish the
// certificate" is not a typo for anything, and guessing at it would be worse
// than saying nothing.
func Suggest(word string) (Name, bool) {
	word = normalize(word)
	if word == "" {
		return "", false
	}

	// Names before aliases, both in registry order, so that a word equally
	// close to two commands always gets the same answer - and the answer is
	// the command's own name rather than one of its aliases.
	best, distance := "", 0
	for _, candidate := range candidates() {
		d := editDistance(word, candidate)
		if best == "" || d < distance {
			best, distance = candidate, d
		}
	}
	// A third of the word may be wrong, at most two characters: enough for a
	// slip of the finger, not enough to turn one command into another.
	budget := min(len(word)/3, 2)
	if best == "" || distance > budget || distance == 0 {
		return "", false
	}
	definition, _ := Lookup(best)
	return definition.Name, true
}

// candidates are the words Suggest compares against, in a fixed order.
func candidates() []string {
	words := make([]string, 0, len(index))
	for _, definition := range registry {
		words = append(words, string(definition.Name))
	}
	for _, definition := range registry {
		words = append(words, definition.Aliases...)
	}
	return words
}

// editDistance is the Levenshtein distance between two words.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, min(current[j-1]+1, previous[j-1]+cost))
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

// groups returns the group names in the order the listing shows them: the
// declared order first, anything new after it, alphabetically, so a group
// added without a thought here is still listed.
func groups() []string {
	known := []string{GroupBasics, GroupValidation, GroupRegister}
	var extra []string
	for _, definition := range registry {
		if !contains(known, definition.Group) && !contains(extra, definition.Group) {
			extra = append(extra, definition.Group)
		}
	}
	sort.Strings(extra)
	return append(known, extra...)
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
