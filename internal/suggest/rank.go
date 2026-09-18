package suggest

import (
	"sort"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
)

// Ranking the candidates.
//
// Two weights, and they are not arbitrary. A shared language decides whether
// the code can be run at all, a shared field only whether the paper can be read
// with interest, so a language counts double. Everything else is a tie-break,
// and every tie-break is deterministic: an editor who asks twice gets the same
// answer, and a test can say what the answer is.

// Most is how many candidates a reply names. Five is enough to choose from and
// few enough to read; a longer list is the CSV again.
const Most = 5

const (
	languageWeight = 2
	fieldWeight    = 1
)

// A Suggestion is one candidate, with the reason they are suggested.
type Suggestion struct {
	Codechecker
	// Languages and Fields are what they share with the check.
	Languages []string
	Fields    []string
	// OpenChecks is how many checks they are already assigned to.
	OpenChecks int
	Score      int
}

// An Exclusion is somebody deliberately left out, and why.
type Exclusion struct {
	Handle string
	Why    string
}

// Excluded is who may not be suggested, whatever they match.
//
// Each set is filled by whoever can answer it, and left empty by whoever
// cannot: the command line has no issue, so it knows nothing about this
// check's roles, and says so rather than suggesting the paper's author.
type Excluded struct {
	// Authors of the paper under check, by handle.
	Authors []string
	// OnThisCheck is anybody already holding a role here.
	OnThisCheck []string
	// OpenChecks is how many open checks each handle is assigned to.
	OpenChecks map[string]int
}

// Rank orders the candidates by what they share with the check.
//
// Pure: everything it needs has been fetched already, so the interesting cases
// - an author who is also a codechecker, a tie, nothing to go on - are tested
// without a server.
func Rank(codecheckers []Codechecker, evidence Evidence, excluded Excluded) ([]Suggestion, []Exclusion) {
	authors := handleSet(excluded.Authors)
	for _, author := range evidence.Authors {
		// An author who registered as a codechecker is found by ORCID, which
		// is the only identifier the two lists share.
		if orcid := check.ORCIDDigits(author.ORCID); orcid != "" {
			for _, candidate := range codecheckers {
				if candidate.ORCID != "" && candidate.ORCID == orcid {
					authors[candidate.Handle] = true
				}
			}
		}
	}
	onThisCheck := handleSet(excluded.OnThisCheck)

	var ranked []Suggestion
	var left []Exclusion
	for _, candidate := range codecheckers {
		switch {
		case authors[candidate.Handle]:
			left = append(left, Exclusion{candidate.Handle, "author of the paper"})
			continue
		case onThisCheck[candidate.Handle]:
			left = append(left, Exclusion{candidate.Handle, "already has a role on this check"})
			continue
		}

		suggestion := Suggestion{
			Codechecker: candidate,
			Languages:   shared(candidate.Languages, evidence.Languages),
			Fields:      shared(candidate.Fields, evidence.Fields),
			OpenChecks:  excluded.OpenChecks[candidate.Handle],
		}
		suggestion.Score = languageWeight*len(suggestion.Languages) + fieldWeight*len(suggestion.Fields)
		if suggestion.Score == 0 {
			// Not an exclusion: somebody who matches nothing is not ruled out,
			// they simply have no reason to be at the top of the list.
			continue
		}
		if suggestion.OpenChecks > 0 {
			left = append(left, Exclusion{candidate.Handle, "has an open check"})
			continue
		}
		ranked = append(ranked, suggestion)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		switch {
		case a.Score != b.Score:
			return a.Score > b.Score
		case len(a.Languages) != len(b.Languages):
			return len(a.Languages) > len(b.Languages)
		default:
			return a.Handle < b.Handle
		}
	})
	if len(ranked) > Most {
		ranked = ranked[:Most]
	}
	sort.SliceStable(left, func(i, j int) bool { return left[i].Handle < left[j].Handle })
	return ranked, left
}

// Why is the reason a candidate is suggested, in words.
func (s Suggestion) Why() string {
	var reasons []string
	if len(s.Languages) > 0 {
		reasons = append(reasons, strings.Join(s.Languages, ", "))
	}
	if len(s.Fields) > 0 {
		reasons = append(reasons, strings.Join(s.Fields, ", "))
	}
	return strings.Join(reasons, " · ")
}

// shared is the terms two lists have in common, in the order the candidate
// declared them: most proficient first is how the lists are written.
func shared(declared, wanted []string) []string {
	var common []string
	for _, term := range declared {
		if contains(wanted, term) {
			common = append(common, term)
		}
	}
	return common
}

func handleSet(handles []string) map[string]bool {
	set := map[string]bool{}
	for _, handle := range handles {
		if handle := command.Handle(handle); handle != "" {
			set[handle] = true
		}
	}
	return set
}
