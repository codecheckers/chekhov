package check

import (
	"net/http"
	"strings"
	"testing"
)

func TestOpenAlexWork(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/openalex/works", `{"results": [{
	  "display_name": "A paper", "primary_location": {"source": {"display_name": "A Journal"}},
	  "primary_topic": {"display_name": "Circadian rhythm and melatonin",
	    "subfield": {"display_name": "Endocrine and Autonomic Systems"},
	    "field": {"display_name": "Neuroscience"},
	    "domain": {"display_name": "Life Sciences"}},
	  "topics": [{"display_name": "Retinal Development and Disorders"}],
	  "keywords": [{"display_name": "Melanopsin", "score": 0.9}],
	  "concepts": [{"display_name": "Luminance", "score": 0.5},
	    {"display_name": "Computer science", "score": 0.1}],
	  "abstract_inverted_index": {"Melanopsin": [0], "matters": [1], "here": [2]},
	  "authorships": [{"author": {"display_name": "Josiah Carberry",
	    "orcid": "https://orcid.org/0000-0002-1825-0097"}},
	    {"author": {"display_name": "Ada Lovelace"}}]}]}`)

	work, err := stub.services.Work("https://doi.org/10.1000/fake")
	if err != nil {
		t.Fatalf("work: %v", err)
	}
	if work.Title != "A paper" || work.Journal != "A Journal" {
		t.Errorf("read %+v", work)
	}
	if work.Abstract != "Melanopsin matters here" {
		t.Errorf("abstract %q, want it rebuilt from the inverted index", work.Abstract)
	}
	// Broad and narrow together: a field somebody declares, and a keyword that
	// would find the one person who knows this subject.
	for _, want := range []string{"Life Sciences", "Neuroscience", "Melanopsin", "Luminance"} {
		if !ContainsFold(work.Subjects, want) {
			t.Errorf("subjects %v, want %q", work.Subjects, want)
		}
	}
	if ContainsFold(work.Subjects, "Computer science") {
		t.Errorf("subjects %v, want the concept it is unsure of left out", work.Subjects)
	}
	if len(work.Authors) != 2 {
		t.Fatalf("authors %+v", work.Authors)
	}
	if work.Authors[0].ORCID != "0000-0002-1825-0097" || work.Authors[0].Family != "Carberry" {
		t.Errorf("first author %+v", work.Authors[0])
	}
	if work.Authors[1].ORCID != "" {
		t.Errorf("an author with no ORCID should have none: %+v", work.Authors[1])
	}
}

// OpenAlex holds some DOIs twice; asking by filter answers a list, and the
// first record is the answer rather than an error.
func TestOpenAlexTakesTheFirstOfSeveral(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/openalex/works", `{"results": [
	  {"display_name": "The one"}, {"display_name": "The duplicate"}]}`)

	work, err := stub.services.Work("10.1000/fake")
	if err != nil {
		t.Fatalf("work: %v", err)
	}
	if work.Title != "The one" {
		t.Errorf("title %q", work.Title)
	}
}

func TestOpenAlexNotFoundAndUnreachable(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/openalex/works", `{"results": []}`)
	if _, err := stub.services.Work("10.1000/fake"); err == nil ||
		!strings.Contains(err.Error(), "does not know") {
		t.Errorf("a DOI OpenAlex has never seen should say so, got %v", err)
	}

	stub.Status("/openalex/works", http.StatusTooManyRequests)
	stub.services.reset()
	if _, err := stub.services.Work("10.1000/fake"); err == nil ||
		!strings.Contains(err.Error(), "could not ask OpenAlex") {
		t.Errorf("a rate limit should read as could-not-ask, got %v", err)
	}
}

// The polite pool: OpenAlex asks callers to say who they are.
func TestOpenAlexSendsTheMailto(t *testing.T) {
	stub := newStub(t)
	var asked string
	stub.Handle("/openalex/works", func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": [{"display_name": "A paper"}]}`))
	})

	stub.services.Mailto = "chekhov@cdchck.science"
	if _, err := stub.services.Work("10.1000/fake"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "mailto=chekhov%40cdchck.science") {
		t.Errorf("query %q, want the polite-pool address", asked)
	}

	stub.services.reset()
	stub.services.Mailto = ""
	if _, err := stub.services.Work("10.1000/fake"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(asked, "mailto") {
		t.Errorf("query %q, want no address when none is configured", asked)
	}
}

// Crossref is disabled, not deleted.
func TestCrossrefStillWorksWhenNamed(t *testing.T) {
	stub := newStub(t)
	stub.services.Metadata = "crossref"
	stub.JSON("/crossref/works/10.1000/fake", `{"message": {
	  "title": ["A paper"], "container-title": ["A journal"], "subject": ["Neuroscience"],
	  "author": [{"given": "A.", "family": "Da", "ORCID": "https://orcid.org/0000-0000-0000-0001"}]}}`)

	work, err := stub.services.Work("10.1000/fake")
	if err != nil {
		t.Fatalf("work: %v", err)
	}
	if work.Title != "A paper" || work.Journal != "A journal" {
		t.Errorf("read %+v", work)
	}
	if len(work.Authors) != 1 || work.Authors[0].Family != "Da" ||
		work.Authors[0].ORCID != "0000-0000-0000-0001" {
		t.Errorf("authors %+v", work.Authors)
	}
	if stub.services.MetadataSource() != "Crossref" {
		t.Errorf("source named %q", stub.services.MetadataSource())
	}
}

func TestFamilyNameOf(t *testing.T) {
	for name, want := range map[string]string{
		"Josiah Carberry": "Carberry", "Ada Lovelace": "Lovelace",
		"Daniel Nüst": "Nüst", "Prince": "Prince", "  ": "", "": "",
	} {
		if got := familyNameOf(name); got != want {
			t.Errorf("familyNameOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// OpenAlex gives one display name, so the surname is taken from the end of it.
// A file that correctly lists the author must not be reported as missing them
// because the guess landed on a suffix.
func TestAuthorNameMatchSurvivesAGuessedSurname(t *testing.T) {
	cases := []struct {
		name     string
		inFile   string
		fromWork WorkAuthor
		found    bool
	}{
		{"a plain surname", "Josiah Carberry",
			WorkAuthor{Name: "Josiah Carberry", Family: "Carberry"}, true},
		{"a suffix guessed as the surname", "Josiah Carberry",
			WorkAuthor{Name: "Josiah Carberry Jr.", Family: "Jr."}, true},
		{"a middle name the file leaves out", "Stephen Piccolo",
			WorkAuthor{Name: "Stephen R Piccolo", Family: "Piccolo"}, true},
		{"somebody the file really does not list", "Josiah Carberry",
			WorkAuthor{Name: "Ada Lovelace", Family: "Lovelace"}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := namedIn([]string{Normalise(testCase.inFile)}, testCase.fromWork); got != testCase.found {
				t.Errorf("namedIn = %v, want %v", got, testCase.found)
			}
		})
	}
}

// The reply is posted on a public issue, and the address a deployment
// identifies itself to OpenAlex with is not the register's to publish.
func TestOpenAlexFailureDoesNotPublishTheAddress(t *testing.T) {
	stub := newStub(t)
	stub.Status("/openalex/works", http.StatusTooManyRequests)
	stub.services.Mailto = "someone@example.invalid"

	_, err := stub.services.Work("10.1000/fake")
	if err == nil {
		t.Fatal("a 429 should be an error")
	}
	if strings.Contains(err.Error(), "someone@example.invalid") ||
		strings.Contains(err.Error(), "mailto") {
		t.Errorf("the error carries the address into a public reply: %s", err)
	}
	if !strings.Contains(err.Error(), "10.1000/fake") {
		t.Errorf("the error no longer says which DOI failed: %s", err)
	}
}

// config.Load validates what a settings file says; a Services built by hand
// goes around it, and "Crossref" must not silently mean OpenAlex.
func TestMetadataSourceIsMatchedHoweverItIsWritten(t *testing.T) {
	for _, written := range []string{"crossref", "Crossref", " CROSSREF "} {
		services := &Services{Metadata: written}
		if got := services.MetadataSource(); got != "Crossref" {
			t.Errorf("Metadata %q read as %q", written, got)
		}
	}
	for _, written := range []string{"", "openalex", "something else"} {
		services := &Services{Metadata: written}
		if got := services.MetadataSource(); got != "OpenAlex" {
			t.Errorf("Metadata %q read as %q", written, got)
		}
	}
}
