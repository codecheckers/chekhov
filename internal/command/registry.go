package command

import (
	"sort"
	"strings"
)

// Groups the listing is arranged in, in the order they are shown.
const (
	GroupBasics     = "Basics"
	GroupValidation = "Validation"
	GroupRegister   = "Register"
	GroupPeople     = "People"
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
		Name:    Assign,
		Summary: "Give somebody a role on this check",
		Usage:   "assign @user as codechecker|author|handling editor",
		Group:   GroupPeople,
		Role:    RoleEditor,
	},
	{
		Name:    Remove,
		Summary: "Take a role away again",
		Usage:   "remove @user as codechecker|author|handling editor",
		Group:   GroupPeople,
		Role:    RoleEditor,
	},
	{
		Name:    Accept,
		Summary: "Adopt a roles record I did not write, after somebody edited it",
		Usage:   "accept roles",
		Group:   GroupPeople,
		Role:    RoleEditor,
	},
	{
		Name:    ListRoles,
		Summary: "Say who holds which role on this check",
		Usage:   "roles",
		Group:   GroupPeople,
		Role:    RoleAnyone,
	},
	{
		Name:    ListCodecheckers,
		Summary: "Say where the lists of codecheckers are",
		Usage:   "codecheckers",
		Group:   GroupPeople,
		Role:    RoleAnyone,
	},
	{
		Name:    SuggestCodecheckers,
		Summary: "Propose codecheckers for this check, from a repository, a DOI, or a pasted abstract",
		Usage:   "suggest codecheckers [<repository>|<DOI>|<certificate>]",
		Group:   GroupPeople,
		Role:    RoleEditor,
	},
	{
		Name:    Check,
		Summary: "Validate a `codecheck.yml` against the CODECHECK rules, or describe the repository under check",
		Usage:   "check [config|metadata|bundle|references|report|register|repository] <repository, or certificate>",
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
	known := []string{GroupBasics, GroupPeople, GroupValidation, GroupRegister}
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
