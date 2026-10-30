package command

import (
	"slices"
	"sort"
	"strings"
)

// Groups the listing is arranged in, in the order they are shown.
const (
	GroupBasics     = "Basics"
	GroupValidation = "Validation"
	GroupRegister   = "Register"
	GroupPeople     = "People"
	GroupWorkflow   = "Workflow"
)

// A Definition is one command the bot knows: what it is called, what it does,
// and who may run it.
//
// The registry is the single source for all of that. The help listing is
// generated from it, so a command cannot be added and then forgotten in the
// documentation - which is the whole point of having a registry rather than a
// switch.
//
// A Definition's Name may be a **phrase** - `check start` - and then it is a
// subcommand: the parser matches the two words before it tries the first one
// alone. A subcommand is its own entry, with its own role, summary and line in
// the listing, which is the point: `check` is for anyone and `check finish` is
// for editors, and a command someone may not run is absent from the listing
// rather than shown and refused.
//
// A phrase is the subject first and then what to do with it - `certificate
// set`, `codecheckers suggest` - so that the commands about one thing sit
// together in the listing (#56). The forms written verb first before that,
// `set certificate` and the rest, are Aliases: an alias may be a phrase too,
// and is looked up whole, so nobody who learned the old form is turned away.
type Definition struct {
	Name    Name
	Aliases []string
	// Summary is one line, shown in the listing.
	Summary string
	// Usage is how the command is written, without the mention.
	Usage string
	Group string
	Role  Role
	// Or are the other roles that may run the command, for the few where one
	// role is not the whole answer: `check start` is the codechecker's own
	// command and an editor's alike. Empty for everything else, and Role is
	// then the whole of it.
	Or []Role
	// Hidden keeps a working command out of the listing. hello is hidden
	// because it is a smoke test rather than something a codechecker needs,
	// and thanks and goodbye because a listing is for what to do next, not for
	// manners.
	Hidden bool
	// AboutTheCheck says this command's subject is the state of the check
	// rather than something the check happens to be on - so a label that no
	// longer describes the check belongs under its answer. Here rather than
	// in the command, so that "at most one such note per reply" is a property
	// of the dispatch and not something each command remembers; see
	// labels.go, and codecheckers/chekhov#44. A command whose subject is not
	// the check - check, rules, version, hello - leaves it off.
	AboutTheCheck bool
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
		Name:    Goodbye,
		Aliases: []string{"bye", "farewell"},
		Summary: "Sign off when a check ends",
		Usage:   "goodbye",
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
		Name:    TeamsRefresh,
		Aliases: []string{"refresh teams", "refresh"},
		Summary: "Read the organisation's teams again, after a membership change",
		Usage:   "teams refresh",
		Group:   GroupBasics,
		Role:    RoleEditor,
	},
	{
		Name:          Assign,
		Summary:       "Give somebody a role on this check",
		Usage:         "assign @user as codechecker|author|handling editor [in <team>]",
		Group:         GroupPeople,
		Role:          RoleEditor,
		AboutTheCheck: true,
	},
	{
		Name:          Remove,
		Summary:       "Take a role away again",
		Usage:         "remove @user as codechecker|author|handling editor",
		Group:         GroupPeople,
		Role:          RoleEditor,
		AboutTheCheck: true,
	},
	{
		Name:          ListRoles,
		Summary:       "Say who holds which role on this check",
		Usage:         "roles",
		Group:         GroupPeople,
		Role:          RoleAnyone,
		AboutTheCheck: true,
	},
	{
		Name:    RecordAccept,
		Aliases: []string{"accept record", "accept roles", "accept"},
		Summary: "Adopt the record of this check - roles, certificate - after somebody edited it",
		Usage:   "record accept",
		Group:   GroupPeople,
		Role:    RoleEditor,
	},
	{
		Name:    ListCodecheckers,
		Summary: "Say where the lists of codecheckers are",
		Usage:   "codecheckers",
		Group:   GroupPeople,
		Role:    RoleAnyone,
	},
	{
		Name:          CodecheckersSuggest,
		Aliases:       []string{"suggest codecheckers", "suggest codechecker"},
		Summary:       "Propose codecheckers for this check, from a repository, a DOI, or a pasted abstract",
		Usage:         "codecheckers suggest [<repository>|<DOI>|<certificate>]",
		Group:         GroupPeople,
		Role:          RoleEditor,
		AboutTheCheck: true,
	},
	{
		Name:    Nudge,
		Summary: "Pick up what earlier replies left outstanding, across the register",
		Usage:   "nudge",
		Group:   GroupPeople,
		Role:    RoleEditor,
	},
	{
		Name:    Check,
		Summary: "Validate a `codecheck.yml` against the CODECHECK rules, describe the repository under check, or ask whether its links answer",
		Usage:   "check [config|metadata [references]|bundle|report|certificate|repository|links] <repository, or certificate>",
		Group:   GroupValidation,
		Role:    RoleAnyone,
	},
	{
		Name:    CheckReady,
		Summary: "Say whether a check can start: a README, a `codecheck.yml` with a manifest, a licence, and a paper",
		Usage:   "check ready [<repository>|<certificate>]",
		Group:   GroupValidation,
		Role:    RoleAnyone,
	},
	{
		Name:    CheckZenodo,
		Summary: "Audit the certificate's Zenodo record, or a draft by its share link, against the community's curation policy",
		Usage:   "check zenodo [<DOI>|<record link>|<share link>]",
		Group:   GroupValidation,
		Role:    RoleAnyone,
	},
	{
		Name:          CheckStart,
		Summary:       "Begin this check: the labels, what the repository holds, and the checklist",
		Usage:         "check start [<repository>|<certificate>]",
		Group:         GroupWorkflow,
		Role:          RoleEditor,
		Or:            []Role{RoleAssignedCodechecker},
		AboutTheCheck: true,
	},
	{
		Name:          Status,
		Summary:       "Say where this check stands: the record, the labels, and what is still open",
		Usage:         "status [<repository>|<certificate>]",
		Group:         GroupWorkflow,
		Role:          RoleAnyone,
		AboutTheCheck: true,
	},
	{
		Name:          CheckFinish,
		Summary:       "Finish this check: run the rules, move the labels, and point at `register`",
		Usage:         "check finish [<repository>|<certificate>]",
		Group:         GroupWorkflow,
		Role:          RoleEditor,
		AboutTheCheck: true,
	},
	{
		Name:    Rules,
		Summary: "Say which rules I judge by, and whether they are the register's current ones",
		Usage:   "rules",
		Group:   GroupValidation,
		Role:    RoleAnyone,
	},
	{
		Name:    Certificate,
		Summary: "Say which certificate identifier this check holds",
		Usage:   "certificate",
		Group:   GroupRegister,
		Role:    RoleAnyone,
	},
	{
		Name:    CertificateNext,
		Aliases: []string{"next certificate"},
		Summary: "Say which certificate identifier comes next, from `register.csv` and the issues",
		Usage:   "certificate next",
		Group:   GroupRegister,
		Role:    RoleEditor,
	},
	{
		Name:    CertificateSet,
		Aliases: []string{"set certificate"},
		Summary: "Reserve a certificate identifier for this check: the next one, or the one given",
		Usage:   "certificate set [YYYY-NNN] [anyway]",
		Group:   GroupRegister,
		Role:    RoleEditor,
	},
	{
		Name:    RepositorySet,
		Aliases: []string{"set repository"},
		Summary: "Record the repository this check's register row names, where the issue's Repository line does not",
		Usage:   "repository set <repository>",
		Group:   GroupRegister,
		Role:    RoleEditor,
	},
	{
		Name:    VenueSet,
		Aliases: []string{"set venue"},
		Summary: "Record the Type and Venue of this check's register row, where its labels do not say",
		Usage:   "venue set [<type>] <venue>",
		Group:   GroupRegister,
		Role:    RoleEditor,
	},
	{
		Name:          CheckRegister,
		Summary:       "Build this check's register row and run the rules on it, before anything is written",
		Usage:         "check register [<repository>] [<type>] [<venue>]",
		Group:         GroupRegister,
		Role:          RoleAnyone,
		AboutTheCheck: true,
	},
	{
		Name:          Register,
		Summary:       "Open the pull request adding this check's row to `register.csv`, once the rules allow it",
		Usage:         "register [<repository>] [<type>] [<venue>]",
		Group:         GroupRegister,
		Role:          RoleEditor,
		AboutTheCheck: true,
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

// index maps every name and alias to its definition. A phrase name is keyed
// whole, so that looking one up is the same operation as looking up a word.
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

// Permits reports whether somebody's roles let them run the command: the
// command's own role, or any of the others it names.
func (d Definition) Permits(roles Roles) bool {
	for _, may := range d.roles() {
		if roles.Has(may) {
			return true
		}
	}
	return false
}

// roles is every role that may run the command: its own, and the others it
// names. One union, because three questions are asked of it - may they, who is
// it for, and does answering cost a read of the record - and a fourth that
// forgot `Or` would refuse the person whose command it is.
func (d Definition) roles() []Role {
	return append([]Role{d.Role}, d.Or...)
}

// Who is who the command is for, in words, for the refusal that has to say
// what the asker would have to be. One function, so the refusal and anything
// else describing the command cannot word it differently.
func (d Definition) Who() string {
	who := make([]string, 0, len(d.Or)+1)
	for _, may := range d.roles() {
		who = append(who, may.Description())
	}
	return list(who, "or")
}

// NeedsCheckRoles reports whether answering "may they run this?" costs a read
// of the check's own record, which is a listing of the issue's comments.
//
// Asked of every role the command names, not only the first: a command an
// editor **or** the assigned codechecker may run is gated on a per-check role,
// and resolving only the standing ones would refuse the codechecker whose
// command it is.
func (d Definition) NeedsCheckRoles() bool {
	for _, role := range d.roles() {
		if role != RoleAnyone && !role.Standing() {
			return true
		}
	}
	return false
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

// SuggestSub finds the subcommand a misspelling was probably meant to be:
// `check fnish` for `check finish`.
//
// The parser learned about phrases; without this the hint did not, and a
// mistyped subcommand falls through to its parent command, which answers with
// whatever the typo looked like as an argument.
//
// Measured on the second word alone, against the second words of that parent's
// subcommands and against the one-word commands - `check status` is what
// somebody types once `check start` and `check finish` exist, and the answer
// is the plain `status` command. The parent is only ever a phrase's first
// word here, so it can never be suggested as its own subcommand.
func SuggestSub(parent Name, word string) (Name, bool) {
	word = normalize(word)
	if word == "" {
		return "", false
	}
	prefix := string(parent) + " "
	best, distance, bestIsSub := Name(""), 0, false
	for _, definition := range registry {
		// The parent's own subcommands, and the commands whose whole name is
		// one word: `check status` is the mistake somebody makes once
		// `check start` and `check finish` exist, and the answer to it is the
		// plain `status` command rather than a near miss among the two. A
		// subcommand is found by its aliases too, so that `set certifcate`
		// is answered with `certificate set`, the form it was meant to be.
		for _, spelling := range definition.spellings() {
			candidate, isSub := strings.CutPrefix(spelling, prefix)
			if !isSub {
				if spelling != string(definition.Name) || strings.Contains(spelling, " ") {
					continue
				}
				candidate = spelling
			}
			// A subcommand wins a tie with a one-word command: the word was
			// written after its parent, and `set certifcate` is as close to
			// the `certificate` command as to `certificate set`.
			d := editDistance(word, candidate)
			if best == "" || d < distance || (d == distance && isSub && !bestIsSub) {
				best, distance, bestIsSub = definition.Name, d, isSub
			}
		}
	}
	// The same budget Suggest allows: a third of the word, at most two
	// characters. Enough for a slip of the finger, not enough to turn a
	// repository name into a command.
	//
	// Unlike Suggest, a distance of nought counts. There it means the word
	// was a command and needs no hint; here it means the word is spelled
	// perfectly and is attached to the wrong parent - `check status` for
	// `status` - which is the mistake most worth answering. A subcommand
	// cannot reach this with a distance of nought, because Parse would have
	// matched the phrase and never asked.
	if best == "" || distance > min(len(word)/3, 2) {
		return "", false
	}
	return best, true
}

// Subcommands are the phrases a word begins that somebody may run, when the
// word is no command on its own: `repository` is `repository set`, for an
// editor. Empty for a word that is a command, whose own reply says what can
// follow it - see Children - and for somebody who may run none of them,
// because a command someone may not run is absent, not offered.
func Subcommands(word string, roles Roles) []Name {
	if _, isCommand := index[normalize(word)]; isCommand {
		return nil
	}
	return Children(word, roles)
}

// families maps a word to the commands it begins - by their name or by an
// alias - in registry order. Built once, as index is: what a word begins
// never changes after startup, and three questions are asked of it for
// every command.
var families = func() map[string][]Definition {
	byParent := make(map[string][]Definition)
	for _, definition := range registry {
		begun := make(map[string]bool)
		for _, spelling := range definition.spellings() {
			if first, _, phrase := strings.Cut(spelling, " "); phrase && !begun[first] {
				begun[first] = true
				byParent[first] = append(byParent[first], definition)
			}
		}
	}
	return byParent
}()

// Children are the commands a word begins that somebody may run, each once and
// by its name: `certificate` is `certificate next` and `certificate set`. The
// aliases count, so `set`, which began them before #56, still finds
// `certificate set`, `repository set` and `venue set`.
func Children(word string, roles Roles) []Name {
	var children []Name
	for _, definition := range families[normalize(word)] {
		if !definition.Hidden && definition.Permits(roles) {
			children = append(children, definition.Name)
		}
	}
	return children
}

// IsParent reports whether a command begins any other, whoever may run them:
// what decides that a second word after it may be a mistyped subcommand.
func IsParent(name Name) bool { return len(families[string(name)]) > 0 }

// ChildrenNeedCheckRoles reports whether any command a name begins is gated on
// a per-check role, so that offering it - in a hint, or under the bare name -
// asks about the roles it is actually gated on.
func ChildrenNeedCheckRoles(name Name) bool {
	return slices.ContainsFunc(families[string(name)], Definition.NeedsCheckRoles)
}

// spellings are every way a command is written: its name, then its aliases.
func (d Definition) spellings() []string {
	return append([]string{string(d.Name)}, d.Aliases...)
}

// candidates are the words Suggest compares against, in a fixed order.
func candidates() []string {
	words := make([]string, 0, len(index))
	for _, definition := range registry {
		// A phrase can never be a near miss for one typed word, and
		// SuggestSub is what answers for those.
		if !strings.Contains(string(definition.Name), " ") {
			words = append(words, string(definition.Name))
		}
	}
	for _, definition := range registry {
		for _, alias := range definition.Aliases {
			if !strings.Contains(alias, " ") {
				words = append(words, alias)
			}
		}
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
	known := []string{GroupBasics, GroupPeople, GroupValidation, GroupWorkflow, GroupRegister}
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
