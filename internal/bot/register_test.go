package bot

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// `check register`, `set repository` and `set venue` (#21).

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

// reserve records a certificate identifier, the way `set certificate` does.
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
	answer(t, server, command.SetVenue, []string{"journal", "fake", "other", "journal"}, "")

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
		"set repository github::owner/repo",
		"no label names a venue",
		"set venue <type> <venue>",
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

// `set venue` stores the venue as venues.csv spells it, and says when that
// file does not have it rather than refusing - CC-REG-005 warns, it does not
// stop a row. The type goes with the venue: set alone, the venue leaves the
// type to the labels.
func TestSetVenueRecordsTheVenue(t *testing.T) {
	server, _ := registering(t, "", false)

	reply := answer(t, server, command.SetVenue, []string{"journal", "fake", "JOURNAL"}, "")
	if !strings.Contains(reply, "**Register row**: type `journal`, venue `FAKE Journal`") {
		t.Errorf("the reply does not show what was recorded:\n%s", reply)
	}
	if reply := answer(t, server, command.SetVenue, []string{"journal", "FAKE", "Journal"}, ""); !strings.Contains(reply, "already says so") {
		t.Errorf("setting the same venue again is not a no-op:\n%s", reply)
	}

	reply = answer(t, server, command.SetVenue, []string{"Somewhere", "Else"}, "")
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

// `set repository` stores the repository the way register.csv writes one,
// whichever way it was given.
func TestSetRepositoryRecordsASpec(t *testing.T) {
	server, _ := registering(t, "", false)

	reply := answer(t, server, command.SetRepository, []string{"https://github.com/codecheckers/demo"}, "")
	if !strings.Contains(reply, "repository `github::codecheckers/demo`") {
		t.Errorf("the repository is not recorded as a spec:\n%s", reply)
	}
	if reply := answer(t, server, command.SetRepository, []string{"2020-001"}, ""); !strings.Contains(reply, "Usage") {
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
