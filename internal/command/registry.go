package command

import (
	"sort"
	"strings"
)

// Role is what the person writing the comment is allowed to do.
//
// Roles are decided by the handles in the settings file rather than by GitHub
// team membership, because reading a team needs an organisation-wide
// permission on the bot's token and the token is scoped to one repository. See
// docs/github-token.md.
type Role string

const (
	// RoleAnyone is everybody, including people not in the organisation.
	RoleAnyone Role = "anyone"
	// RoleEditor is a CODECHECK editor, who may run the commands that change
	// the register.
	RoleEditor Role = "editor"
)

// Groups the listing is arranged in, in the order they are shown.
const (
	GroupBasics     = "Basics"
	GroupValidation = "Validation"
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
	// because it is a smoke test rather than something a codechecker needs.
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
		Name:    Check,
		Summary: "Validate a `codecheck.yml` against the CODECHECK rules",
		Usage:   "check [config|metadata|bundle|references|report|register] [target]",
		Group:   GroupValidation,
		Role:    RoleAnyone,
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
	definition, found := index[strings.ToLower(strings.TrimSpace(word))]
	return definition, found
}

// Permits reports whether a role may run the command.
func (d Definition) Permits(role Role) bool {
	return d.Role != RoleEditor || role == RoleEditor
}

// Visible returns the commands a role should be told about: the ones they may
// run, minus the hidden ones. A command someone may not run is absent rather
// than shown and refused, so the listing is a list of what to do next.
func Visible(role Role) []Definition {
	var visible []Definition
	for _, definition := range registry {
		if !definition.Hidden && definition.Permits(role) {
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
	word = strings.ToLower(strings.TrimSpace(word))
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
	known := []string{GroupBasics, GroupValidation}
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
