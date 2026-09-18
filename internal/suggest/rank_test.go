package suggest

import (
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
)

// The candidates every ranking case starts from, written the way the lists
// write them: an @ on the handle, a comma-separated most-proficient-first list
// of languages, and one row with nothing declared at all.
func candidates() []Codechecker {
	return []Codechecker{
		{Name: "Ada", Handle: "ada", ORCID: "0000-0000-0000-0001",
			Fields: terms("geospatial data analysis,containers"), Languages: terms("R, Python")},
		{Name: "Bo", Handle: "bo", ORCID: "0000-0000-0000-0002",
			Fields: terms("bioinformatics"), Languages: terms("Python, Perl")},
		{Name: "Cy", Handle: "cy", ORCID: "0000-0000-0000-0003",
			Fields: terms("neuroscience"), Languages: terms("R")},
		{Name: "Di", Handle: "di", ORCID: "0000-0000-0000-0004"},
	}
}

func TestRank(t *testing.T) {
	cases := []struct {
		name     string
		evidence Evidence
		excluded Excluded
		want     []string
		left     []string
	}{
		{
			name:     "a language everybody shares ranks on how much else they share",
			evidence: Evidence{Languages: []string{"r"}, Fields: []string{"geospatial data analysis"}},
			want:     []string{"ada", "cy"},
		},
		{
			name:     "a field alone is enough to be suggested",
			evidence: Evidence{Fields: []string{"bioinformatics"}},
			want:     []string{"bo"},
		},
		{
			name:     "somebody who declared nothing is not suggested, and not an exclusion",
			evidence: Evidence{Languages: []string{"fortran"}},
			want:     nil,
		},
		{
			name:     "an equal score is broken by the handle, so the answer is the same twice",
			evidence: Evidence{Languages: []string{"r"}},
			want:     []string{"ada", "cy"},
		},
		{
			name:     "an author of the paper is left out by handle",
			evidence: Evidence{Languages: []string{"r"}},
			excluded: Excluded{Authors: []string{"@Ada"}},
			want:     []string{"cy"},
			left:     []string{"ada"},
		},
		{
			name: "an author is found by ORCID, which is all the two lists share",
			evidence: Evidence{Languages: []string{"r"},
				Authors: []check.WorkAuthor{{Name: "A. Da", ORCID: "0000-0000-0000-0001"}}},
			want: []string{"cy"},
			left: []string{"ada"},
		},
		{
			name:     "somebody already on this check is left out",
			evidence: Evidence{Languages: []string{"r"}},
			excluded: Excluded{OnThisCheck: []string{"cy"}},
			want:     []string{"ada"},
			left:     []string{"cy"},
		},
		{
			name:     "somebody with an open check is left out",
			evidence: Evidence{Languages: []string{"r"}},
			excluded: Excluded{OpenChecks: map[string]int{"ada": 1}},
			want:     []string{"cy"},
			left:     []string{"ada"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ranking := Rank(candidates(), testCase.evidence, testCase.excluded, "")
			if got := handlesOf(ranking.Suggested); !equal(got, testCase.want) {
				t.Errorf("suggested %v, want %v", got, testCase.want)
			}
			var out []string
			for _, exclusion := range ranking.LeftOut {
				out = append(out, exclusion.Handle)
				if exclusion.Why == "" {
					t.Errorf("%s was left out with no reason", exclusion.Handle)
				}
			}
			if !equal(out, testCase.left) {
				t.Errorf("left out %v, want %v", out, testCase.left)
			}
		})
	}
}

func TestRankNamesAtMostFive(t *testing.T) {
	var many []Codechecker
	for _, handle := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		many = append(many, Codechecker{Handle: handle, Languages: terms("R")})
	}
	ranking := Rank(many, Evidence{Languages: []string{"r"}}, Excluded{}, "")
	if len(ranking.Suggested) != Most {
		t.Fatalf("suggested %d, want at most %d", len(ranking.Suggested), Most)
	}
	if ranking.Matched != len(many) {
		t.Errorf("matched %d, want all %d, so the reply can say it is showing a slice",
			ranking.Matched, len(many))
	}
}

func TestWhySaysWhatIsShared(t *testing.T) {
	ranking := Rank(candidates(),
		Evidence{Languages: []string{"r", "python"}, Fields: []string{"containers"}}, Excluded{}, "")
	if len(ranking.Suggested) == 0 {
		t.Fatal("nobody was suggested")
	}
	if got := ranking.Suggested[0].Why(); got != "R, Python · containers" {
		t.Errorf("why %q", got)
	}
}

func TestEvidenceEmpty(t *testing.T) {
	if !(Evidence{}).Empty() {
		t.Error("evidence with nothing in it should be empty")
	}
	if (Evidence{Fields: []string{"neuroscience"}}).Empty() {
		t.Error("evidence with a term is not empty")
	}
}

func handlesOf(suggestions []Suggestion) []string {
	var handles []string
	for _, suggestion := range suggestions {
		handles = append(handles, suggestion.Handle)
	}
	return handles
}

func equal(got, want []string) bool {
	return strings.Join(got, ",") == strings.Join(want, ",")
}

// The shape of the real lists: nearly everybody declares R and Python, and
// most other languages are declared by one person. Counting every share alike
// gives half the community the same score for every R paper, and the
// tie-break - whatever it is - then sends every such check to the same few
// people. A rare share has to outweigh a common one.
func TestACommonLanguageDoesNotDecideTheRanking(t *testing.T) {
	var many []Codechecker
	for _, handle := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		many = append(many, Codechecker{Handle: handle, Languages: terms("R")})
	}
	// One person late in the alphabet declares what this paper actually needs.
	many = append(many, Codechecker{Handle: "zoe", Languages: terms("R, Fortran")})

	ranking := Rank(many, Evidence{Languages: []string{"r", "fortran"}}, Excluded{}, "")
	if len(ranking.Suggested) == 0 {
		t.Fatal("nobody was suggested")
	}
	if got := ranking.Suggested[0].Handle; got != "zoe" {
		t.Errorf("first suggestion is %q, want the one who declares the rare language", got)
	}
	if ranking.Matched != len(many) {
		t.Errorf("matched %d, want all %d", ranking.Matched, len(many))
	}
}

func TestRarity(t *testing.T) {
	everybody, one := rarity(75, 75), rarity(1, 75)
	if everybody != 1 {
		t.Errorf("a term the whole list declares is worth %v, want 1", everybody)
	}
	if one <= everybody {
		t.Errorf("a term one person declares is worth %v, not more than %v", one, everybody)
	}
	if rarity(0, 75) != 1 || rarity(1, 0) != 1 {
		t.Error("a term nobody declares, or a list of nobody, should not be worth more than anything")
	}
}

// Fifty people who all declare R are not a shortlist, and the same five must
// not be the answer to every R paper. The order is the check's own, and stable
// for that check.
func TestATiedShortlistIsThisChecksOwnAndSaysSo(t *testing.T) {
	var many []Codechecker
	for _, handle := range []string{"ada", "bo", "cy", "di", "eve", "fay", "gus", "hal"} {
		many = append(many, Codechecker{Handle: handle, Languages: terms("R")})
	}
	evidence := Evidence{Languages: []string{"r"}}

	first := Rank(many, evidence, Excluded{}, "codecheckers/register#1")
	again := Rank(many, evidence, Excluded{}, "codecheckers/register#1")
	other := Rank(many, evidence, Excluded{}, "codecheckers/register#2")

	if !equal(handlesOf(first.Suggested), handlesOf(again.Suggested)) {
		t.Error("the same check ranked differently twice")
	}
	if equal(handlesOf(first.Suggested), handlesOf(other.Suggested)) {
		t.Errorf("two checks got the same five of eight tied candidates: %v", handlesOf(first.Suggested))
	}
	if !first.Undistinguished {
		t.Error("a shortlist where everybody shares the same thing should say so")
	}
}

func TestAShortlistThatIsDistinguishedDoesNotApologise(t *testing.T) {
	ranking := Rank(candidates(), Evidence{Languages: []string{"r", "python"},
		Fields: []string{"bioinformatics"}}, Excluded{}, "x#1")
	if ranking.Undistinguished {
		t.Errorf("candidates sharing different things were called indistinguishable: %v",
			handlesOf(ranking.Suggested))
	}
}

// The ranking says what the suggestions actually share with the check, which
// is the handful that decided the answer rather than the thirty subjects a
// metadata source names for one paper.
func TestRankingReportsWhatWasShared(t *testing.T) {
	ranking := Rank(candidates(), Evidence{
		Languages: []string{"r", "python"},
		Fields:    []string{"containers", "bioinformatics", "Melanopsin", "Luminance"},
	}, Excluded{}, "x#1")

	if !equal(ranking.SharedLanguages, []string{"R", "Python"}) {
		t.Errorf("shared languages %v", ranking.SharedLanguages)
	}
	for _, want := range []string{"containers", "bioinformatics"} {
		if !contains(ranking.SharedFields, want) {
			t.Errorf("shared fields %v, want %q", ranking.SharedFields, want)
		}
	}
	for _, unwanted := range []string{"Melanopsin", "Luminance"} {
		if contains(ranking.SharedFields, unwanted) {
			t.Errorf("shared fields %v name %q, which nobody declares", ranking.SharedFields, unwanted)
		}
	}
}

// A word can be a language and somebody else's field; counting them in one set
// would drop the second silently.
func TestRankingCountsLanguagesAndFieldsApart(t *testing.T) {
	people := []Codechecker{
		{Handle: "ada", Languages: terms("R")},
		{Handle: "bo", Fields: terms("R")},
	}
	ranking := Rank(people, Evidence{Languages: []string{"R"}, Fields: []string{"R"}}, Excluded{}, "x#1")
	if !contains(ranking.SharedLanguages, "R") || !contains(ranking.SharedFields, "R") {
		t.Errorf("languages %v, fields %v - both should name R",
			ranking.SharedLanguages, ranking.SharedFields)
	}
}

// The "matched on" line explains the five names under it, so it is what they
// share - not what the forty who did not make the list shared.
func TestSharedTermsDescribeTheShortlist(t *testing.T) {
	var many []Codechecker
	for _, handle := range []string{"a", "b", "c", "d", "e"} {
		many = append(many, Codechecker{Handle: handle, Languages: terms("Fortran")})
	}
	// Matches on a common language, so ranks below all five of the above.
	many = append(many, Codechecker{Handle: "zoe", Languages: terms("Fortran, R")})

	ranking := Rank(many, Evidence{Languages: []string{"Fortran", "R"}}, Excluded{}, "x#1")
	if len(ranking.Suggested) != Most {
		t.Fatalf("suggested %d", len(ranking.Suggested))
	}
	if contains(ranking.SharedLanguages, "R") && !suggests(ranking, "zoe") {
		t.Errorf("shared languages %v name R, which nobody shown shares",
			ranking.SharedLanguages)
	}
}

func suggests(ranking Ranking, handle string) bool {
	for _, suggestion := range ranking.Suggested {
		if suggestion.Handle == handle {
			return true
		}
	}
	return false
}
