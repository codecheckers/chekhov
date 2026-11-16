package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// `chekhov include check` reads a registration from a line, a list file or an
// issue (codecheckers/chekhov#49). The handles were checked to belong to
// nobody; the ORCID is ORCID's own test record.
const (
	madeUp    = "a-made-up-registrant"
	madeUpRow = `Josiah Carberry,@a-made-up-registrant,0000-0002-1825-0097,see ORCID page,psychoceramics,"R, Python",expired,NA,`
	otherRow  = `Made Up Too,@a-made-up-other-person,0000-0002-1825-0097,https://example.invalid/contact,psychoceramics,R,NA,NA,`
)

// Offline nothing that needs ORCID passes: the line is reported as not
// checked, and the command fails, as `check` does for a rule it could not run.
func TestIncludeCheckOfALineOfflineSaysWhatItCouldNotAsk(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"include", "check", madeUpRow}, &out)
	if err == nil {
		t.Errorf("a registration nobody could check passed:\n%s", out.String())
	}
	for _, want := range []string{"the line, @" + madeUp + ": needs something", "could not ask ORCID"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the answer does not say %q:\n%s", want, out.String())
		}
	}
}

func TestIncludeCheckTakesCheckAndOneInput(t *testing.T) {
	for _, args := range [][]string{{"include"}, {"include", "open"}, {"include", "check"},
		{"include", "check", madeUpRow, otherRow}, {"include", "check", "--as"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%q was accepted", args)
		}
	}
}

// stubServices answer ORCID with a public email for the test record, and
// GitHub with what the test routes.
func stubServices(t *testing.T) (*check.Services, *testserver.Server) {
	server := testserver.New(t)
	server.JSON("/orcid/0000-0002-1825-0097/person",
		`{"emails": {"email": [{"email": "made-up@example.invalid", "verified": true}]}}`)
	return &check.Services{Access: check.Access{HTTP: server.Client(), ORCID: server.URL + "/orcid",
		GitHub: server.URL + "/github", RawContent: server.URL + "/raw"}}, server
}

// A list file is every row, its header skipped, each its own registrant, and
// none compared with the lists - a list's rows are on it.
func TestIncludeCheckOfAFileChecksEveryRow(t *testing.T) {
	services, _ := stubServices(t)
	file := filepath.Join(t.TempDir(), "codecheckers.csv")
	header := "name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse"
	if err := os.WriteFile(file, []byte(header+"\n"+madeUpRow+"\n"+otherRow+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registrations, err := readRegistrations(ctx, services, "chekhovbot", []string{"https://example.invalid/codecheckers.csv"},
		file, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(registrations) != 2 || registrations[0].lists != nil || !strings.HasSuffix(registrations[1].where, "row 2") {
		t.Fatalf("registrations = %+v", registrations)
	}
	var out bytes.Buffer
	if err := checkRegistrations(ctx, services, registrations, &out); err != nil {
		t.Errorf("%v:\n%s", err, out.String())
	}
	for _, want := range []string{"row 1, @" + madeUp + ": has everything",
		"row 2, @a-made-up-other-person: has everything", "2 of 2 registrations have everything"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the answer does not say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "example.invalid") {
		t.Errorf("a contact was quoted:\n%s", out.String())
	}
}

// An issue is its author's first post, compared with the lists of the
// issue's own repository.
func TestIncludeCheckOfAnIssueReadsItsAuthorsRow(t *testing.T) {
	services, server := stubServices(t)
	body := "Your data:\n\n```csv\n" + madeUpRow + "\n```\n"
	server.JSON("/github/repos/codecheckers/testing-lists/issues/7", `{"user": {"login": "`+madeUp+`"}, "body": `+quote(body)+`}`)
	server.JSON("/github/repos/codecheckers/testing-lists/issues/7/comments", `[]`)
	ctx := context.Background()

	opened, err := readRegistrations(ctx, services, "chekhovbot", []string{"https://example.invalid/x/codecheckers.csv"},
		"https://github.com/codecheckers/testing-lists/issues/7", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0].author != madeUp || opened[0].where != "codecheckers/testing-lists#7" ||
		opened[0].lists[0] != server.URL+"/raw/codecheckers/testing-lists/HEAD/codecheckers.csv" {
		t.Errorf("an issue the registrant opened: %+v", opened)
	}
	if _, err := readRegistrations(ctx, services, "chekhovbot", nil,
		"https://github.com/codecheckers/testing-lists/issues/9", ""); err == nil {
		t.Error("an issue GitHub does not have was read")
	}
}

// On a registration the bot opened, the registrant is the one its signed
// record names, as the bot reads it, and the row is their newest - not a row
// somebody else posted after it. Without a record that verifies, there is
// nothing to check, as the bot says.
func TestIncludeCheckOfAnIssueTheBotOpenedReadsItsRecord(t *testing.T) {
	signer, err := people.GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	published := recordKeys
	recordKeys = []string{signer.PublicKey()}
	t.Cleanup(func() { recordKeys = published })

	services, server := stubServices(t)
	record, err := followup.Render(followup.Record{Kind: followup.KindRegistration, Check: "codecheckers/testing-lists#8",
		Subject: madeUp, Asked: time.Now().UTC().Format(time.RFC3339)}, signer)
	if err != nil {
		t.Fatal(err)
	}
	ownRow := "@chekhovbot include check\n\n```csv\n" + madeUpRow + "\n```\n"
	othersRow := "@chekhovbot include check\n\n```csv\n" + otherRow + "\n```\n"
	server.JSON("/github/repos/codecheckers/testing-lists/issues/8", `{"user": {"login": "chekhovbot"}, "body": "welcome"}`)
	server.JSON("/github/repos/codecheckers/testing-lists/issues/8/comments", `[
	  {"user": {"login": "chekhovbot"}, "body": `+quote(record+"This registration is for you")+`},
	  {"user": {"login": "`+madeUp+`"}, "body": `+quote(ownRow)+`},
	  {"user": {"login": "a-made-up-other-person"}, "body": `+quote(othersRow)+`}]`)
	server.JSON("/github/repos/codecheckers/testing-lists/issues/10", `{"user": {"login": "chekhovbot"}, "body": "welcome"}`)
	server.JSON("/github/repos/codecheckers/testing-lists/issues/10/comments", `[
	  {"user": {"login": "`+madeUp+`"}, "body": `+quote(ownRow)+`}]`)
	ctx := context.Background()

	byBot, err := readRegistrations(ctx, services, "chekhovbot", nil, "github.com/codecheckers/testing-lists/issues/8", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(byBot) != 1 || byBot[0].author != madeUp || !strings.Contains(byBot[0].body, "@"+madeUp) ||
		strings.Contains(byBot[0].body, "a-made-up-other-person") {
		t.Errorf("an issue the bot opened: %+v", byBot)
	}

	unrecorded, err := readRegistrations(ctx, services, "chekhovbot", nil, "github.com/codecheckers/testing-lists/issues/10", "")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := checkRegistrations(ctx, services, unrecorded, &out); err == nil ||
		!strings.Contains(out.String(), "carries no record of whom it is for that I can trust") {
		t.Errorf("an issue with no record: %v\n%s", err, out.String())
	}
}

// The keys a record is verified against here are the ones published.
func TestRecordKeysAreThePublishedOnes(t *testing.T) {
	published, err := os.ReadFile(filepath.Join("..", "..", "docs", "record-keys.pub"))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, line := range strings.Split(string(published), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && !strings.HasPrefix(line, "#") {
			keys = append(keys, fields[1])
		}
	}
	if strings.Join(keys, " ") != strings.Join(recordKeys, " ") {
		t.Errorf("recordKeys = %q, docs/record-keys.pub publishes %q", recordKeys, keys)
	}
}

// A BOM before the header, a name that is no file, and rows typed together.
func TestIncludeCheckReadsWhatItIsGiven(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "codecheckers.csv")
	if err := os.WriteFile(file, []byte("\ufeffname,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n"+
		madeUpRow+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := readRegistrations(ctx, nil, "chekhovbot", nil, file, ""); err != nil || len(found) != 1 {
		t.Errorf("a file with a byte-order mark: %+v, %v", found, err)
	}
	if _, err := readRegistrations(ctx, nil, "chekhovbot", nil, "codecheckers.cvs", ""); err == nil ||
		!strings.Contains(err.Error(), "is not a file") {
		t.Errorf("a mistyped file name: %v", err)
	}
	found, err := readRegistrations(ctx, nil, "chekhovbot", []string{"x"}, madeUpRow+"\n"+otherRow, "")
	if err != nil || len(found) != 2 || found[0].lists != nil {
		t.Errorf("two rows typed together are a list, and are not compared with the lists: %+v, %v", found, err)
	}
	if found, _ := readRegistrations(ctx, nil, "chekhovbot", []string{"x"}, madeUpRow, ""); len(found) != 1 || found[0].lists == nil {
		t.Errorf("one row is compared with the lists: %+v", found)
	}
}

func quote(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(text) + `"`
}
