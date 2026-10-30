package bot

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// `check register`, `repository set` and `venue set` (#21).

// registering wires a server to check #1, with the body and labels given, a
// register that does not have the check's row yet - or does, when listed says
// so - and testdata/valid-2.0, whose certificate is 1970-001, as the
// configuration of github::codecheckers/demo.
func registering(t *testing.T, body string, listed bool, labels ...string) (*Server, *workflow) {
	t.Helper()
	server, path := checking(t, "", labels...)
	path.issues[0].Body = body

	stub := testserver.New(t)
	configuration, err := os.ReadFile("../../testdata/valid-2.0/codecheck.yml")
	if err != nil {
		t.Fatal(err)
	}
	stub.Bytes("/raw/codecheckers/demo/HEAD/codecheck.yml", configuration)
	register := "Certificate,Repository,Type,Venue,Issue\n" +
		"1970-000,github::codecheckers/older,journal,FAKE Journal,7\n"
	if listed {
		register += "1970-001,github::codecheckers/demo,journal,FAKE Journal,1\n"
	}
	raw := "/raw/" + server.Settings.TargetRepository() + "/HEAD/"
	stub.Text(raw+"register.csv", register)
	// Two journals, so a journal's issue needs a label naming it; one
	// community venue, which `community` names alone.
	stub.Text(raw+"venues.csv", "name,longname,label\n"+
		"FAKE Journal,FAKE Journal,journal\n"+
		"FAKE Other Journal,FAKE Other Journal,journal\n"+
		"fake-codecheck,fake-codecheck,community\n")
	server.Services = &check.Services{Access: check.Access{
		HTTP:       stub.Client(),
		Register:   server.Settings.TargetRepository(),
		RawContent: stub.URL + "/raw",
	}}
	return server, path
}

const demoIssueBody = "**Repository**: https://github.com/codecheckers/demo\n\n**Certificate**: 1970-001\n"

// reserve records a certificate identifier, the way `certificate set` does.
func reserve(t *testing.T, server *Server, id string) {
	t.Helper()
	_, err := server.Checks.Update(context.Background(), server.Settings.TargetRepository(), 1,
		func(record people.Record) (people.Record, error) {
			record.Certificate = id
			return record, nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// The row is filled in from the issue - its Repository line and its labels -
// and every column says where it came from, so a value read off a label is
// seen to be one before it is in a pull request. Nothing is written.
func TestCheckRegisterBuildsTheRowFromTheIssue(t *testing.T) {
	server, path := registering(t, demoIssueBody, false, "work in progress", "journal", "FAKE Journal")
	reserve(t, server, "1970-001")

	reply := answer(t, server, command.CheckRegister, nil, "")
	for _, want := range []string{
		"| Certificate | `1970-001` | the record |",
		"| Repository | `github::codecheckers/demo` | the issue's Repository line |",
		"| Type | `journal` | the label `journal` |",
		"| Venue | `FAKE Journal` | the labels |",
		"| Issue | `1` | this issue |",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not show %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "I have run no rules") || strings.Contains(reply, "not run the register rules") {
		t.Errorf("a complete row was not judged:\n%s", reply)
	}
	if len(path.moved) != 0 {
		t.Errorf("check register wrote labels: %q", path.moved)
	}
}

// The register rules judge the row before it is in register.csv: a
// certificate already there is taken, which they could not say of a row they
// cannot see.
func TestCheckRegisterJudgesTheRowBeforeItExists(t *testing.T) {
	server, _ := registering(t, demoIssueBody, true, "journal", "FAKE Journal")
	reserve(t, server, "1970-001")

	reply := answer(t, server, command.CheckRegister, nil, "")
	if !strings.Contains(reply, "1970-001 is already in register.csv") {
		t.Errorf("the register rules did not judge the proposed row:\n%s", reply)
	}
}

// Given beats recorded beats read off the issue, and the reply says which.
func TestCheckRegisterTakesWhatItIsToldFirst(t *testing.T) {
	server, _ := registering(t, demoIssueBody, false, "journal", "FAKE Journal")
	reserve(t, server, "1970-001")
	answer(t, server, command.VenueSet, []string{"journal", "fake", "other", "journal"}, "")

	reply := answer(t, server, command.CheckRegister, []string{"codecheckers/demo", "community", "fake-codecheck"}, "")
	for _, want := range []string{
		"| Repository | `github::codecheckers/demo` | given |",
		"| Type | `community` | given |",
		"| Venue | `fake-codecheck` | given |",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not show %q:\n%s", want, reply)
		}
	}

	reply = answer(t, server, command.CheckRegister, nil, "")
	if !strings.Contains(reply, "| Venue | `FAKE Other Journal` | the record |") {
		t.Errorf("the recorded venue does not win over the labels:\n%s", reply)
	}
}

// A column that cannot be filled stops the command before any rule runs, and
// says how to fill it.
func TestCheckRegisterRefusesHalfARow(t *testing.T) {
	// `journal` names no venue alone: two venues carry it.
	server, _ := registering(t, "no repository here", false, "journal")

	reply := answer(t, server, command.CheckRegister, nil, "")
	for _, want := range []string{
		"I have run no rules",
		"this check has reserved none",
		"repository set github::owner/repo",
		"no label names a venue",
		"venue set <type> <venue>",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, reply)
		}
	}
}

// A configuration that carries another identifier than the one reserved is
// not judged against the register: the row would name one and point at a file
// that says the other.
func TestCheckRegisterRefusesAnotherCertificate(t *testing.T) {
	server, _ := registering(t, demoIssueBody, false, "journal", "FAKE Journal")
	reserve(t, server, "1970-005")

	reply := answer(t, server, command.CheckRegister, nil, "")
	if !strings.Contains(reply, "says certificate `1970-001`, and this check reserved `1970-005`") {
		t.Errorf("the mismatch is not refused:\n%s", reply)
	}
}

// `venue set` stores the venue as venues.csv spells it, and says when that
// file does not have it rather than refusing - CC-REG-005 warns, it does not
// stop a row. The type goes with the venue: set alone, the venue leaves the
// type to the labels.
func TestSetVenueRecordsTheVenue(t *testing.T) {
	server, _ := registering(t, "", false)

	reply := answer(t, server, command.VenueSet, []string{"journal", "fake", "JOURNAL"}, "")
	if !strings.Contains(reply, "**Register row**: type `journal`, venue `FAKE Journal`") {
		t.Errorf("the reply does not show what was recorded:\n%s", reply)
	}
	if reply := answer(t, server, command.VenueSet, []string{"journal", "FAKE", "Journal"}, ""); !strings.Contains(reply, "already says so") {
		t.Errorf("setting the same venue again is not a no-op:\n%s", reply)
	}

	reply = answer(t, server, command.VenueSet, []string{"Somewhere", "Else"}, "")
	if !strings.Contains(reply, "`Somewhere Else` is not in `venues.csv`") {
		t.Errorf("an unlisted venue is not pointed out:\n%s", reply)
	}
	if !strings.Contains(reply, "**Register row**: venue `Somewhere Else`\n") {
		t.Errorf("a venue alone kept the type of the one before:\n%s", reply)
	}
}

// A Type word is a type only as the Type column writes it, so a venue named
// with one keeps it.
func TestAVenueMayBeginWithATypeWord(t *testing.T) {
	given := rowArguments(strings.Fields("Journal of Open Source Software"))
	if given.venueType != "" || given.venue != "Journal of Open Source Software" {
		t.Errorf("read %+v", given)
	}
}

// Prose after `check register` is not a venue: a venue given there is one
// venues.csv lists.
func TestCheckRegisterReadsNoProseAsAVenue(t *testing.T) {
	server, _ := registering(t, demoIssueBody, false, "journal", "FAKE Journal")
	reserve(t, server, "1970-001")

	reply := answer(t, server, command.CheckRegister, []string{"please"}, "")
	if !strings.Contains(reply, "`please` is no venue `venues.csv` lists") || strings.Contains(reply, "| Venue |") {
		t.Errorf("a word of prose was read as the venue:\n%s", reply)
	}
	reply = answer(t, server, command.CheckRegister, []string{"1970-004"}, "")
	if !strings.Contains(reply, "check certificate 1970-004") {
		t.Errorf("the old form is not pointed at the new one:\n%s", reply)
	}
}

// The Repository line is read however the link is written.
func TestTheRepositoryLineIsReadHoweverTheLinkIsWritten(t *testing.T) {
	for _, body := range []string{
		"**Repository**: https://github.com/codecheckers/demo",
		"**Repository**: <https://github.com/codecheckers/demo>",
		"**Repository**: [codecheckers/demo](https://github.com/codecheckers/demo)",
	} {
		about := whatTheBotKnows{labelsRead: true}
		about.issue.Body = body
		if got := repositoryLine(about, nil); got.Value != "github::codecheckers/demo" {
			t.Errorf("%s: read %+v", body, got)
		}
	}
	if got := repositoryLine(whatTheBotKnows{}, nil); !strings.Contains(got.Missing, "could not read this issue") {
		t.Errorf("an unread issue reads as one without a Repository line: %+v", got)
	}
}

// `repository set` stores the repository the way register.csv writes one,
// whichever way it was given.
func TestSetRepositoryRecordsASpec(t *testing.T) {
	server, _ := registering(t, "", false)

	reply := answer(t, server, command.RepositorySet, []string{"https://github.com/codecheckers/demo"}, "")
	if !strings.Contains(reply, "repository `github::codecheckers/demo`") {
		t.Errorf("the repository is not recorded as a spec:\n%s", reply)
	}
	if reply := answer(t, server, command.RepositorySet, []string{"2020-001"}, ""); !strings.Contains(reply, "Usage") {
		t.Errorf("a certificate identifier was taken for a repository:\n%s", reply)
	}
}

// The words after the command are read by what they are, in any order, and
// a venue of several words needs no quoting.
func TestRowArgumentsAreReadByWhatTheyAre(t *testing.T) {
	given := rowArguments(strings.Fields("community codecheck NL github::codecheckers/demo"))
	if given.repository != "github::codecheckers/demo" || given.venueType != "community" || given.venue != "codecheck NL" {
		t.Errorf("read %+v", given)
	}
}

// `check start`, `status` and `check finish` read the repository an editor
// set for the row, before the certificate's row in register.csv: it is the
// one thing an editor said about where the configuration is.
func TestTheWorkflowReadsTheRecordedRepository(t *testing.T) {
	server, _ := registering(t, "", true)
	record := people.Record{Repository: "github::codecheckers/other", Certificate: "1970-001"}
	if target := server.targetFor(nil, record, server.Services); target != "github::codecheckers/other" {
		t.Errorf("read %q, want the recorded repository", target)
	}
}

// proposer is the reply path with the pull request, recording what it was
// asked to propose.
type proposer struct {
	*workflow
	proposed []github.RowProposal
	refuse   error
}

func (p *proposer) ProposeRow(_ context.Context, _ string, proposal github.RowProposal) (github.RowPullRequest, error) {
	if p.refuse != nil {
		return github.RowPullRequest{}, p.refuse
	}
	p.proposed = append(p.proposed, proposal)
	return github.RowPullRequest{Number: 300, URL: "https://github.example/pull/300",
		Updated: len(p.proposed) > 1}, nil
}

func proposingServer(t *testing.T, listed bool) (*Server, *proposer) {
	t.Helper()
	server, path := registering(t, demoIssueBody, listed, "journal", "FAKE Journal")
	reserve(t, server, "1970-001")
	writer := &proposer{workflow: path}
	server.Replies = writer
	return server, writer
}

// A row the rules pass is proposed as it was shown: the row in the file's
// column names, the commit closing the checks issue, and who asked.
func TestRegisterOpensThePullRequest(t *testing.T) {
	server, writer := proposingServer(t, false)

	reply := answer(t, server, command.Register, nil, "")
	if len(writer.proposed) != 1 {
		t.Fatalf("proposed %d times:\n%s", len(writer.proposed), reply)
	}
	proposal := writer.proposed[0]
	want := map[string]string{"Certificate": "1970-001", "Repository": "github::codecheckers/demo",
		"Type": "journal", "Venue": "FAKE Journal", "Issue": "1"}
	for column, value := range want {
		if proposal.Row[column] != value {
			t.Errorf("%s = %q, want %q", column, proposal.Row[column], value)
		}
	}
	if proposal.Title != "add 1970-001" || proposal.Message != "add 1970-001, closes #1" {
		t.Errorf("title %q, message %q", proposal.Title, proposal.Message)
	}
	if !strings.Contains(proposal.Body, "as `@nuest` asked") || strings.Contains(proposal.Body, " @nuest") {
		t.Errorf("the body does not name who asked without notifying them:\n%s", proposal.Body)
	}
	if !strings.Contains(reply, "Opened the pull request adding this row: #300") {
		t.Errorf("the reply does not link the pull request:\n%s", reply)
	}

	reply = answer(t, server, command.Register, nil, "")
	if !strings.Contains(reply, "Brought up to date the pull request") {
		t.Errorf("a second register does not say it updated:\n%s", reply)
	}
}

// A rule failing as an error stops the pull request, which is never asked
// for; the reply says what stopped it.
func TestRegisterOpensNothingWhenARuleFails(t *testing.T) {
	server, writer := proposingServer(t, true) // 1970-001 is in register.csv already

	reply := answer(t, server, command.Register, nil, "")
	if len(writer.proposed) != 0 {
		t.Errorf("a failing row was proposed: %+v", writer.proposed)
	}
	if !strings.Contains(reply, "1970-001 is already in register.csv") || !strings.Contains(reply, "opened no pull request") {
		t.Errorf("the refusal does not say why:\n%s", reply)
	}
}

// What the writer refuses is said in the reply's words.
func TestRegisterSaysWhyThePullRequestWasRefused(t *testing.T) {
	for err, want := range map[error]string{
		github.ErrBranchNotOurs:     "has commits that are not mine",
		github.ErrAlreadyRegistered: "already has a row for `1970-001`",
	} {
		server, writer := proposingServer(t, false)
		writer.refuse = err
		if reply := answer(t, server, command.Register, nil, ""); !strings.Contains(reply, want) {
			t.Errorf("%v: the reply does not say %q:\n%s", err, want, reply)
		}
	}
}

// Without the pull request on the reply path - the preview - the row is
// judged and nothing is opened.
func TestRegisterOpensNothingFromThePreview(t *testing.T) {
	server, _ := registering(t, demoIssueBody, false, "journal", "FAKE Journal")
	reserve(t, server, "1970-001")

	reply := answer(t, server, command.Register, nil, "")
	if !strings.Contains(reply, "cannot open a pull request from here") {
		t.Errorf("the preview did not say it opens nothing:\n%s", reply)
	}
}
