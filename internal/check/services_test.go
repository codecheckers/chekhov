package check

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
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

// CSVs reads the files at once and hands them back in the order asked for:
// the first file answers only after the second has been served, which reading
// one after the other would never do. A file that fails keeps its place.
func TestCSVsKeepTheOrderAskedFor(t *testing.T) {
	stub := newStub(t)
	secondServed := make(chan struct{})
	var once sync.Once
	stub.Handle("/first.csv", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-secondServed:
		case <-time.After(2 * time.Second):
			t.Error("the first file was answered before the second was asked for: the reads are not concurrent")
		}
		fmt.Fprint(w, "name\nfirst\n")
	})
	stub.Handle("/second.csv", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "name\nsecond\n")
		once.Do(func() { close(secondServed) })
	})

	urls := []string{stub.At("/first.csv"), stub.At("/missing.csv"), stub.At("/second.csv")}
	tables := stub.services.CSVs(urls...)
	if len(tables) != len(urls) {
		t.Fatalf("%d tables, want %d", len(tables), len(urls))
	}
	for i, want := range []string{"first", "", "second"} {
		table := tables[i]
		if want == "" {
			if !IsNotFound(table.Err) {
				t.Errorf("table %d: error %v, want not found", i, table.Err)
			}
			continue
		}
		if table.Err != nil || len(table.Rows) != 1 || table.Rows[0]["name"] != want {
			t.Errorf("table %d: %v %v, want one row named %q", i, table.Rows, table.Err, want)
		}
	}
}
