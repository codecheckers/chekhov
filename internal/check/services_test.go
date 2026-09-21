package check

import (
	"net/http"
	"strings"
	"testing"
)

// Fresh carries the whole of Access across, so a field added to it cannot be
// left behind - which is what happened when the list was written out by hand
// and the OpenAlex base URL went missing, leaving every command in a
// deployment talking to nowhere.
//
// One comparison rather than a field-by-field check, because Access is
// comparable and that is the point of the split: the test cannot fall behind
// the struct either.
func TestFreshCarriesTheWholeOfAccess(t *testing.T) {
	services := &Services{Access: Access{
		HTTP: http.DefaultClient, GitHubToken: "token", Metadata: "crossref",
		Mailto: "chekhov@cdchck.science", Register: "codecheckers/testing",
		Zenodo: "zenodo", ZenodoSandbox: "sandbox", ORCID: "orcid",
		Crossref: "crossref", OpenAlex: "openalex", GitHub: "github",
		RawContent: "raw", GitLab: "gitlab", OSF: "osf",
	}}

	if fresh := services.Fresh(); fresh.Access != services.Access {
		t.Errorf("Fresh() changed the access:\n got %+v\nwant %+v", fresh.Access, services.Access)
	}
}

// And it carries nothing else: the cache is one run's, and a command that
// inherited it would answer from what was published before it ran.
func TestFreshCarriesNothingOfTheRun(t *testing.T) {
	services := &Services{Access: Access{HTTP: http.DefaultClient}}
	services.cache.Store("GET https://example.invalid ", &cachedResponse{status: 200})

	if _, seen := services.Fresh().cache.Load("GET https://example.invalid "); seen {
		t.Error("Fresh() inherited the response cache")
	}
}

// A RawContent configured with a trailing slash must not double the separator.
// raw.githubusercontent answers 404 for a doubled slash, which the rules about
// the bundle would report as a file the codechecker forgot to commit.
func TestRawURLsDoNotDoubleTheSeparator(t *testing.T) {
	services := &Services{Access: Access{RawContent: "https://raw.example.invalid/", Register: "codecheckers/testing"}}
	spec := RepositorySpec{Type: "github", Path: "codecheckers/demo"}

	for what, url := range map[string]string{
		"a register file": services.RegisterFile("register.csv"),
		"a bundle file":   spec.within(spec.rawBase(services), "codecheck.yml"),
		"the bundle root": spec.rawBase(services),
	} {
		if strings.Contains(strings.TrimPrefix(url, "https://"), "//") {
			t.Errorf("%s is %q", what, url)
		}
	}
}
