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
				Authors: []check.Person{{Name: "A. Da", ORCID: "https://orcid.org/0000-0000-0000-0001"}}},
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
			ranked, left := Rank(candidates(), testCase.evidence, testCase.excluded)
			if got := handlesOf(ranked); !equal(got, testCase.want) {
				t.Errorf("suggested %v, want %v", got, testCase.want)
			}
			var out []string
			for _, exclusion := range left {
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
	ranked, _ := Rank(many, Evidence{Languages: []string{"r"}}, Excluded{})
	if len(ranked) != Most {
		t.Fatalf("suggested %d, want at most %d", len(ranked), Most)
	}
}

func TestWhySaysWhatIsShared(t *testing.T) {
	ranked, _ := Rank(candidates(),
		Evidence{Languages: []string{"r", "python"}, Fields: []string{"containers"}}, Excluded{})
	if len(ranked) == 0 {
		t.Fatal("nobody was suggested")
	}
	if got := ranked[0].Why(); got != "R, Python · containers" {
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
