package suggest

import (
	"hash/fnv"
	"math"
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
//
// A share is worth what it distinguishes. Fifty-one of the seventy-five people
// on the list declare R and fifty-three Python, while thirty-eight of the
// fifty-four languages are declared by exactly one person - so counting every
// share alike gives every R paper the same score for half the community, and
// the tie-break, whatever it is, then picks the same few people for every
// check. Weighting a share by how rare it is puts the person who declares what
// this paper actually needs above the fifty who declare what everything needs.

// Most is how many candidates a reply names. Five is enough to choose from and
// few enough to read; a longer list is the CSV again.
const Most = 5

const (
	languageWeight = 2.0
	fieldWeight    = 1.0
)

// counts is how many of the candidates declare each term, lowercased, which is
// what makes a share worth more or less.
type counts struct{ languages, fields map[string]int }

func frequencies(codecheckers []Codechecker) counts {
	declared := counts{languages: map[string]int{}, fields: map[string]int{}}
	for _, codechecker := range codecheckers {
		countInto(declared.languages, codechecker.Languages)
		countInto(declared.fields, codechecker.Fields)
	}
	return declared
}

func countInto(into map[string]int, terms []string) {
	seen := map[string]bool{}
	for _, term := range terms {
		if key := strings.ToLower(term); !seen[key] {
			seen[key] = true
			into[key]++
		}
	}
}

// weight is what a set of shared terms is worth: rarer is worth more, and a
// term everybody declares is still worth something, because being able to run
// the code is the point.
func weight(shared []string, declaring map[string]int, total int) float64 {
	sum := 0.0
	for _, term := range shared {
		sum += rarity(declaring[strings.ToLower(term)], total)
	}
	return sum
}

// rarity is 1 for a term the whole list declares, and grows as fewer do.
func rarity(declaring, total int) float64 {
	if declaring <= 0 || total <= 0 {
		return 1
	}
	return 1 + math.Log(float64(total)/float64(declaring))
}

// A Suggestion is one candidate, with the reason they are suggested.
type Suggestion struct {
	Codechecker
	// Languages and Fields are what they share with the check.
	Languages []string
	Fields    []string
	// OpenChecks is how many checks they are already assigned to.
	OpenChecks int
	// Score is the weight of everything shared, rarest first. Comparable
	// within one ranking and meaningless outside it, so no reply prints it.
	Score float64
}

// A Ranking is the answer: who to suggest, how many could have been, and who
// was deliberately left out.
//
// Matched is there because "five codecheckers" and "five of forty-two" are
// different answers to an editor: the first reads as a shortlist, the second
// says the shortlist is a slice and that the criteria were broad.
type Ranking struct {
	Suggested []Suggestion
	Matched   int
	LeftOut   []Exclusion
	// SharedLanguages and SharedFields are what the suggestions actually have
	// in common with the check - the handful that decided the answer, out of
	// the thirty subjects a metadata source can name for one paper.
	SharedLanguages []string
	SharedFields    []string
	// Undistinguished says every suggestion shares exactly the same thing
	// with the check, so their order is not a recommendation. A paper written
	// in plain R matches half the community equally, and pretending the first
	// five are the best five would be a lie an editor acts on.
	Undistinguished bool
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
// seed makes the order of an otherwise tied shortlist this check's own: the
// same check ranks the same way every time it is asked, and two checks that
// match the same fifty people do not both get the first five in the alphabet.
// Empty falls back to the handle, which is what a test wants.
func Rank(codecheckers []Codechecker, evidence Evidence, excluded Excluded, seed string) Ranking {
	declared := frequencies(codecheckers)
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
		suggestion.Score = languageWeight*weight(suggestion.Languages, declared.languages, len(codecheckers)) +
			fieldWeight*weight(suggestion.Fields, declared.fields, len(codecheckers))
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
		case a.Handle == b.Handle:
			return false
		default:
			// Nothing about the check separates them, so nothing about the
			// alphabet should either.
			return order(seed, a.Handle) < order(seed, b.Handle)
		}
	})
	ranking := Ranking{Matched: len(ranked), LeftOut: left, Undistinguished: allAlike(ranked)}
	if len(ranked) > Most {
		ranked = ranked[:Most]
	}
	ranking.Suggested = ranked
	// What the people actually shown share, not what every match did: the
	// line is there to explain the five names under it, and terms only the
	// forty who did not make the list share would explain nothing.
	//
	// Languages and fields counted apart: a word can be both - "R" is a
	// language, and somebody's field is "R packages" - and one set across the
	// two would drop the second silently.
	for _, suggestion := range ranking.Suggested {
		ranking.SharedLanguages = append(ranking.SharedLanguages, suggestion.Languages...)
		ranking.SharedFields = append(ranking.SharedFields, suggestion.Fields...)
	}
	ranking.SharedLanguages = Distinct(ranking.SharedLanguages)
	ranking.SharedFields = Distinct(ranking.SharedFields)
	sort.SliceStable(left, func(i, j int) bool { return left[i].Handle < left[j].Handle })
	return ranking
}

// order is where a handle falls for one check. A hash rather than the
// alphabet, so that being called Aaron is not a career.
func order(seed, handle string) uint64 {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(seed + "\x00" + handle))
	return digest.Sum64()
}

// allAlike reports that every suggestion shares the same thing with the check.
func allAlike(ranked []Suggestion) bool {
	if len(ranked) < 2 {
		return false
	}
	first := ranked[0].Why()
	for _, suggestion := range ranked[1:] {
		if suggestion.Why() != first {
			return false
		}
	}
	return true
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
