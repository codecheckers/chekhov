package suggest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// The lists, as the register serves them: the volunteers' list with its nine
// columns, and the institutional one, which has no fields and no languages.
const (
	listPath        = "/lists/codecheckers.csv"
	institutionPath = "/lists/institutional-codecheckers.csv"

	volunteers = "name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n" +
		"Ada,@ada,0000-0000-0000-0001,see ORCID page,\"geospatial data analysis,containers\"," +
		"\"R, Python\",NA,NA,\n" +
		"Bo,@bo,0000-0000-0000-0002,bo@example.invalid,bioinformatics,\"Python(expert), Perl\",NA,NA,\n"
	institutional = "name,handle,ORCID,institution,fediverse\n" +
		"Cy,@cy,0000-0000-0000-0003,FAKE University,\n" +
		"Ada,@ada,0000-0000-0000-0001,FAKE University,\n"
)

type world struct {
	*testserver.Server
	services *check.Services
	settings *config.Settings
}

func newWorld(t *testing.T) *world {
	t.Helper()
	server := testserver.New(t)
	settings := &config.Settings{}
	settings.Chekhov.Env.CodecheckerLists = []string{server.At(listPath), server.At(institutionPath)}

	server.Text(listPath, volunteers)
	server.Text(institutionPath, institutional)

	return &world{
		Server:   server,
		settings: settings,
		services: &check.Services{
			HTTP:     server.Client(),
			Register: "codecheckers/testing",
			GitHub:   server.URL + "/github",
			GitLab:   server.URL + "/gitlab",
			Crossref: server.URL + "/crossref",
		},
	}
}

func TestLoadReadsEveryList(t *testing.T) {
	world := newWorld(t)
	codecheckers, unread := Load(world.services, world.settings)
	if len(unread) != 0 {
		t.Fatalf("could not read %v", unread)
	}
	if got := handlesOf(asSuggestions(codecheckers)); !equal(got, []string{"ada", "bo", "cy"}) {
		t.Errorf("read %v, want ada, bo and cy once each", got)
	}
	if got := codecheckers[1].Languages; !equal(got, []string{"Python", "Perl"}) {
		// "Python(expert)" is Python; the note in brackets is not a language.
		t.Errorf("languages %v", got)
	}
}

func TestLoadNamesAListItCouldNotRead(t *testing.T) {
	world := newWorld(t)
	world.Status(institutionPath, http.StatusNotFound)

	codecheckers, unread := Load(world.services, world.settings)
	if len(unread) != 1 || !strings.HasSuffix(unread[0], institutionPath) {
		t.Errorf("unread %v, want the institutional list", unread)
	}
	if len(codecheckers) != 2 {
		t.Errorf("read %d codecheckers, want the two of the list that answered", len(codecheckers))
	}
}

func TestVocabularyComesFromTheLists(t *testing.T) {
	world := newWorld(t)
	codecheckers, _ := Load(world.services, world.settings)
	vocabulary := VocabularyOf(codecheckers)
	if !contains(vocabulary.Languages, "perl") || !contains(vocabulary.Fields, "containers") {
		t.Errorf("vocabulary %+v", vocabulary)
	}
}

// The forms a target may be written in are check's, and tested there; what is
// this package's is which of the three kinds a word falls into.
func TestRead(t *testing.T) {
	cases := []struct{ written, kind, value string }{
		{"https://github.com/codecheckers/demo", "repository", "github::codecheckers/demo"},
		{"`github::codecheckers/demo`", "repository", "github::codecheckers/demo"},
		{"https://doi.org/10.5281/zenodo.3674056", "doi", "10.5281/zenodo.3674056"},
		{"spatial", "text", "spatial"},
		{"", "text", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.written, func(t *testing.T) {
			got := Read(testCase.written)
			if got.Kind != testCase.kind || got.Value != testCase.value {
				t.Errorf("read as %s %q, want %s %q", got.Kind, got.Value, testCase.kind, testCase.value)
			}
		})
	}
}

func TestGatherFollowsARepository(t *testing.T) {
	world := newWorld(t)
	world.JSON("/github/repos/codecheckers/demo/languages", `{"R": 4000, "Python": 9000}`)

	codecheckers, _ := Load(world.services, world.settings)
	evidence := Gather(world.services, VocabularyOf(codecheckers),
		Hints([]string{"codecheckers/demo"}, ""))

	if !equal(evidence.Languages, []string{"Python", "R"}) {
		t.Errorf("languages %v, want the most-used first", evidence.Languages)
	}
	if len(evidence.Sources) == 0 || !strings.Contains(evidence.Sources[0], "codecheckers/demo") {
		t.Errorf("sources %v, want the repository named", evidence.Sources)
	}
}

func TestGatherFollowsADOI(t *testing.T) {
	world := newWorld(t)
	world.JSON("/crossref/works/10.1000/fake", `{"message": {
		"title": ["Containers for reproducible geospatial data analysis"],
		"subject": ["Bioinformatics"],
		"abstract": "The analysis was written in R.",
		"author": [{"given": "A.", "family": "Da", "ORCID": "https://orcid.org/0000-0000-0000-0001"}]}}`)

	codecheckers, _ := Load(world.services, world.settings)
	evidence := Gather(world.services, VocabularyOf(codecheckers), Hints([]string{"10.1000/fake"}, ""))

	if !contains(evidence.Languages, "r") {
		t.Errorf("languages %v, want R from the abstract", evidence.Languages)
	}
	if !contains(evidence.Fields, "geospatial data analysis") || !contains(evidence.Fields, "bioinformatics") {
		t.Errorf("terms %v, want the title's field and Crossref's subject", evidence.Fields)
	}
	if len(evidence.Authors) != 1 || evidence.Authors[0].ORCID != "0000-0000-0000-0001" {
		t.Errorf("authors %+v", evidence.Authors)
	}
}

func TestGatherReadsPastedProse(t *testing.T) {
	world := newWorld(t)
	world.JSON("/github/repos/codecheckers/demo/languages", `{"Perl": 1}`)

	codecheckers, _ := Load(world.services, world.settings)
	evidence := Gather(world.services, VocabularyOf(codecheckers), Hints(nil,
		"The code is at https://github.com/codecheckers/demo and needs Python; "+
			"the research is bioinformatics."))

	if !contains(evidence.Languages, "python") || !contains(evidence.Languages, "perl") {
		t.Errorf("languages %v, want the one named and the ones the repository uses", evidence.Languages)
	}
	if !contains(evidence.Fields, "bioinformatics") {
		t.Errorf("terms %v", evidence.Fields)
	}
}

func TestGatherKeepsGoingWhenASourceFails(t *testing.T) {
	world := newWorld(t)
	world.Status("/github/repos/codecheckers/demo/languages", http.StatusForbidden)

	codecheckers, _ := Load(world.services, world.settings)
	evidence := Gather(world.services, VocabularyOf(codecheckers),
		Hints([]string{"codecheckers/demo", "bioinformatics"}, ""))

	if len(evidence.Unread) != 1 {
		t.Errorf("unread %v, want the repository that answered 403", evidence.Unread)
	}
	if !contains(evidence.Fields, "bioinformatics") {
		t.Errorf("terms %v, want the word that was readable", evidence.Fields)
	}
}

func TestPageOf(t *testing.T) {
	const raw = "https://raw.githubusercontent.com/codecheckers/testing-dev-register/HEAD/codecheckers.csv"
	const page = "https://github.com/codecheckers/testing-dev-register/blob/HEAD/codecheckers.csv"
	if got := PageOf(raw); got != page {
		t.Errorf("PageOf(%s) = %s", raw, got)
	}
	if got := PageOf("https://example.invalid/list.csv"); got != "https://example.invalid/list.csv" {
		t.Errorf("a URL that is not a raw one is left alone, got %s", got)
	}
}

func asSuggestions(codecheckers []Codechecker) []Suggestion {
	suggestions := make([]Suggestion, 0, len(codecheckers))
	for _, codechecker := range codecheckers {
		suggestions = append(suggestions, Suggestion{Codechecker: codechecker})
	}
	return suggestions
}

func TestGatherLeavesProsePunctuationAlone(t *testing.T) {
	world := newWorld(t)
	codecheckers, _ := Load(world.services, world.settings)

	// Nothing here is a repository, and following any of it would cost an API
	// call and a line of apology in the reply.
	evidence := Gather(world.services, VocabularyOf(codecheckers), Hints(nil,
		"The input/output ran at 30 km/h and/or faster, with R/Bioconductor."))
	if len(evidence.Unread) != 0 {
		t.Errorf("followed %v out of ordinary prose", evidence.Unread)
	}
}

func TestGatherSaysWhereItsTextCameFrom(t *testing.T) {
	world := newWorld(t)
	codecheckers, _ := Load(world.services, world.settings)

	evidence := Gather(world.services, VocabularyOf(codecheckers),
		[]Hint{{Kind: "text", Value: "bioinformatics", From: "this check's issue, #7"}})
	if len(evidence.Sources) != 1 || evidence.Sources[0] != "this check's issue, #7" {
		t.Errorf("sources %v, want the issue rather than a comment that said nothing", evidence.Sources)
	}
}

func TestMentionedFindsATermWithAnythingInIt(t *testing.T) {
	// A term written with a slash or a dash has to survive the same
	// normalisation the prose goes through, or it can never be found.
	for _, term := range []string{"MATLAB/Octave", "C/C++", "scikit-learn", "R"} {
		found := mentioned("We used "+term+" throughout.", []string{term})
		if len(found) != 1 {
			t.Errorf("%q was not found in a sentence that uses it", term)
		}
	}
	if found := mentioned("This is research, and going well.", []string{"R", "Go"}); len(found) != 0 {
		t.Errorf("found %v inside longer words", found)
	}
}

func TestGatherFilesLanguagesAsLanguages(t *testing.T) {
	world := newWorld(t)
	world.JSON("/github/repos/codecheckers/demo/languages", `{"Perl": 1}`)
	codecheckers, _ := Load(world.services, world.settings)

	evidence := Gather(world.services, VocabularyOf(codecheckers),
		Hints([]string{"codecheckers/demo"}, ""))
	if len(evidence.Fields) != 0 {
		t.Errorf("fields %v, want a repository's languages counted as languages only", evidence.Fields)
	}
}
