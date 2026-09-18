package bot

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// The lists a suggesting bot reads, and the issues it reads them against.

const (
	listPath        = "/lists/codecheckers.csv"
	institutionPath = "/lists/institutional-codecheckers.csv"
)

// listing wires a server to a stub register: two codechecker lists, and the
// languages of one repository.
func listing(t *testing.T, server *Server) *testserver.Server {
	t.Helper()
	stub := testserver.New(t)
	stub.Text(listPath, "name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n"+
		"A Codechecker,@a-codechecker,0000-0000-0000-0001,see ORCID page,geospatial data analysis,"+
		"\"R, Python\",NA,NA,\n"+
		"Busy Codechecker,@a-busy-codechecker,0000-0000-0000-0002,b@example.invalid,bioinformatics,"+
		"R,NA,NA,\n")
	stub.Text(institutionPath, "name,handle,ORCID,institution,fediverse\n"+
		"An Institutional Codechecker,@an-institutional-codechecker,NA,FAKE University,\n")
	stub.JSON("/github/repos/codecheckers/demo/languages", `{"R": 900}`)

	server.Settings.Chekhov.Env.CodecheckerLists = []string{stub.At(listPath), stub.At(institutionPath)}
	server.Services = &check.Services{
		HTTP:     stub.Client(),
		Register: server.Settings.TargetRepository(),
		GitHub:   stub.URL + "/github",
		Crossref: stub.URL + "/crossref",
		OpenAlex: stub.URL + "/openalex",
	}
	return stub
}

// answer runs one command as an editor on issue 1 of the target register.
func answer(t *testing.T, server *Server, name command.Name, args []string, body string) string {
	t.Helper()
	return server.answer(context.Background(), mention{
		Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest", Body: body,
	}, command.Command{Name: name, Args: args})
}

func TestCodecheckersPointsAtTheLists(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	reply := answer(t, server, command.ListCodecheckers, nil, "")
	for _, want := range []string{"codecheckers.csv", "2 entries", "institutional-codecheckers.csv", "1 entry"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "A Codechecker") {
		t.Errorf("the reply copies the list instead of linking it:\n%s", reply)
	}
}

func TestCodecheckersSaysWhichListItCouldNotRead(t *testing.T) {
	server, _ := testServer(t)
	stub := listing(t, server)
	stub.Remove(institutionPath)

	reply := answer(t, server, command.ListCodecheckers, nil, "")
	if !strings.Contains(reply, "could not read it just now") {
		t.Errorf("a list that answered 404 should be named as such:\n%s", reply)
	}
}

func TestSuggestRanksFromARepository(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "codecheckers/demo"}, "")
	if !strings.Contains(reply, "`@a-codechecker`") {
		t.Errorf("the codechecker who declares R is not suggested:\n%s", reply)
	}
	if !strings.Contains(reply, "github::codecheckers/demo") {
		t.Errorf("the reply does not say what it read:\n%s", reply)
	}
	assertNobodyIsMentioned(t, reply)
}

func TestSuggestReadsAPastedAbstract(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	reply := answer(t, server, command.SuggestCodecheckers, []string{"codecheckers"},
		"@chekhovbot suggest codecheckers\n\nWe analysed the data in R; the work is "+
			"geospatial data analysis.\n")
	if !strings.Contains(reply, "`@a-codechecker`") {
		t.Errorf("the abstract was not read:\n%s", reply)
	}
	if !strings.Contains(reply, "the text of the comment") {
		t.Errorf("the reply does not say the comment was what it read:\n%s", reply)
	}
}

func TestSuggestLeavesOutAnAuthorOfTheCheck(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)
	if reply := answer(t, server, command.Assign,
		[]string{"a-codechecker", "as", "author"}, ""); !strings.Contains(reply, "author") {
		t.Fatalf("the author could not be recorded: %s", reply)
	}

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "codecheckers/demo"}, "")
	if !strings.Contains(reply, "Left out: `@a-codechecker` (author of the paper)") {
		t.Errorf("an author of the paper is still suggested:\n%s", reply)
	}
	assertNobodyIsMentioned(t, reply)
}

func TestSuggestSaysWhenItHasNothingToGoOn(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	reply := answer(t, server, command.SuggestCodecheckers, []string{"codecheckers"}, "")
	if !strings.Contains(reply, "nothing to go on") {
		t.Errorf("a suggestion out of nothing is an arbitrary list of people:\n%s", reply)
	}
}

func TestSuggestSaysWhatItCouldNotCheck(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	// No issue: the command line preview, where this check's roles and the
	// register's open issues are both out of reach.
	reply := server.answer(context.Background(), mention{Author: "nuest"},
		command.Command{Name: command.SuggestCodecheckers, Args: []string{"codecheckers", "codecheckers/demo"}})
	if !strings.Contains(reply, "no check to read here") {
		t.Errorf("the reply does not say which exclusions it could not apply:\n%s", reply)
	}
	if !strings.Contains(reply, "who is already busy") {
		t.Errorf("the reply does not say it could not read the open issues:\n%s", reply)
	}
}

func TestSuggestLeavesOutSomebodyWithAnOpenCheck(t *testing.T) {
	server, replies := testServer(t)
	listing(t, server)
	server.Replies = &issueReader{recorder: replies, issues: []github.Issue{
		{Number: 1, Title: "This check"},
		{Number: 2, Title: "Another check", Assignees: []string{"a-busy-codechecker"}},
	}}

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "codecheckers/demo"}, "")
	if !strings.Contains(reply, "Left out: `@a-busy-codechecker` (has an open check)") {
		t.Errorf("somebody with an open check is still suggested:\n%s", reply)
	}
	if !strings.Contains(reply, "`@a-codechecker`") {
		t.Errorf("the codechecker who is free is not suggested:\n%s", reply)
	}
}

func TestSuggestIsForEditors(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	reply := server.answer(context.Background(), mention{
		Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-stranger",
	}, command.Command{Name: command.SuggestCodecheckers, Args: []string{"codecheckers"}})
	if !strings.Contains(reply, "is for editors") {
		t.Errorf("a stranger was answered:\n%s", reply)
	}
}

func TestSuggestRefusesWhatItCannotSuggest(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)

	reply := answer(t, server, command.SuggestCodecheckers, []string{"venues"}, "")
	if !strings.Contains(reply, "I can suggest `codecheckers`") {
		t.Errorf("reply %q", reply)
	}
}

// A handle in a code span renders as text; anywhere else it notifies whoever
// owns it. Asking who could check a paper must notify nobody, so the check is
// for a handle outside the backticks.
var handleInText = regexp.MustCompile(`@[A-Za-z0-9-]+`)

func assertNobodyIsMentioned(t *testing.T, reply string) {
	t.Helper()
	for _, line := range strings.Split(reply, "\n") {
		// Odd pieces are inside a code span, even pieces outside one.
		for i, piece := range strings.Split(line, "`") {
			if i%2 == 0 {
				if found := handleInText.FindString(piece); found != "" {
					t.Errorf("the reply mentions %q, which notifies its owner: %s", found, line)
				}
			}
		}
	}
}

// issueReader is a reply path that can also read the register's issues.
type issueReader struct {
	*recorder
	issues []github.Issue
}

func (r *issueReader) Issue(_ context.Context, _ string, number int) (github.Issue, error) {
	for _, issue := range r.issues {
		if issue.Number == number {
			return issue, nil
		}
	}
	return github.Issue{}, nil
}

func (r *issueReader) OpenIssues(context.Context, string) ([]github.Issue, error) {
	return r.issues, nil
}

// An edited roles record names nobody: whoever edited it could have deleted
// the author line of the paper they wrote, and being suggested to check their
// own paper would be the prize for doing so.
func TestSuggestDoesNotTrustAnEditedRecord(t *testing.T) {
	server, _ := testServer(t)
	listing(t, server)
	if reply := answer(t, server, command.Assign,
		[]string{"a-codechecker", "as", "author"}, ""); !strings.Contains(reply, "author") {
		t.Fatalf("the author could not be recorded: %s", reply)
	}

	// Somebody with write access edits the bot's own comment.
	store := server.Checks.Comments.(*issueComments)
	store.comments[0].Body = strings.Replace(store.comments[0].Body, "a-codechecker", "a-stranger", 1)

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "codecheckers/demo"}, "")
	if strings.Contains(reply, "Left out: `@a-codechecker` (author of the paper)") {
		t.Errorf("an edited record was trusted:\n%s", reply)
	}
	if !strings.Contains(reply, "could not trust the roles of this check") {
		t.Errorf("the reply does not say the record could not be trusted:\n%s", reply)
	}
}

// A metadata source names thirty things a paper is about; the reply names the
// handful that decided the answer, not the gathering.
func TestSuggestReportsWhatDecidedTheAnswer(t *testing.T) {
	server, _ := testServer(t)
	stub := listing(t, server)
	stub.JSON("/openalex/works", `{"results": [{
	  "display_name": "A paper", "primary_topic": {"field": {"display_name": "Neuroscience"}},
	  "keywords": [{"display_name": "Melanopsin", "score": 0.9}],
	  "concepts": [{"display_name": "Luminance", "score": 0.8},
	    {"display_name": "Radiance", "score": 0.8}],
	  "abstract_inverted_index": {"Written": [0], "in": [1], "R": [2]}}]}`)

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "10.1000/fake"}, "")
	if !strings.Contains(reply, "Matched on languages `R`") {
		t.Errorf("the reply does not name what was shared:\n%s", reply)
	}
	for _, unwanted := range []string{"Melanopsin", "Luminance", "Radiance"} {
		if strings.Contains(reply, unwanted) {
			t.Errorf("the reply names %q, which nobody declares and which decided nothing:\n%s",
				unwanted, reply)
		}
	}
}

// A source that would not answer is the reply, not "nothing to go on": that
// sends the editor looking for a better command rather than for the outage.
func TestSuggestSaysWhenTheOnlySourceFailed(t *testing.T) {
	server, _ := testServer(t)
	stub := listing(t, server)
	stub.Status("/openalex/works", http.StatusTooManyRequests)

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "10.1000/fake"}, "")
	if !strings.Contains(reply, "could not read") || !strings.Contains(reply, "OpenAlex") {
		t.Errorf("the reply hides why it has nothing:\n%s", reply)
	}
	if strings.Contains(reply, "nothing to go on") {
		t.Errorf("a failed source is not the editor writing a bad command:\n%s", reply)
	}
}

// When one source failed and another simply had nothing, the reply says both:
// "I could not read the DOI" without "I read the repository" leaves the editor
// unable to tell which of the two came back empty.
func TestSuggestSaysWhatItReadEvenWhenItFoundNothing(t *testing.T) {
	server, _ := testServer(t)
	stub := listing(t, server)
	// The repository is read and uses a language nobody on the lists declares;
	// the DOI will not answer at all.
	stub.JSON("/github/repos/codecheckers/demo/languages", `{"Fortran": 900}`)
	stub.Status("/openalex/works", http.StatusTooManyRequests)

	reply := answer(t, server, command.SuggestCodecheckers,
		[]string{"codecheckers", "codecheckers/demo", "10.1000/fake"}, "")
	if !strings.Contains(reply, "github::codecheckers/demo") {
		t.Errorf("the reply does not say the repository was read:\n%s", reply)
	}
	if !strings.Contains(reply, "could not read") {
		t.Errorf("the reply does not say the DOI failed:\n%s", reply)
	}
}
