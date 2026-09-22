package followup

import (
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
)

func signer(t *testing.T) *people.Signer {
	t.Helper()
	signer, err := people.GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func raised(t *testing.T, s *people.Signer, when time.Time) (Record, string) {
	t.Helper()
	record := Record{Kind: KindOrganisation, Check: "codecheckers/testing-dev-register#202",
		Subject: "a-newcomer", Role: "assigned codechecker", Team: "codecheckers",
		Asked: when.Format(time.RFC3339)}
	block, err := Render(record, s)
	if err != nil {
		t.Fatal(err)
	}
	return record, block
}

// A record survives being written and read, and carries what a sweep needs.
func TestARecordSurvivesTheRoundTrip(t *testing.T) {
	s := signer(t)
	when := time.Now().UTC().Truncate(time.Second)
	_, block := raised(t, s, when)

	read, err := Parse(block+"\nThe reply a person reads.\n", "codecheckers/testing-dev-register#202", s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if read.Kind != KindOrganisation || read.Subject != "a-newcomer" || read.Team != "codecheckers" {
		t.Errorf("read %+v", read)
	}
	if !read.Raised().Equal(when) {
		t.Errorf("raised %v, want %v", read.Raised(), when)
	}
	if read.Version != Version {
		t.Errorf("version %d", read.Version)
	}
}

// The record drives an action, and a repository collaborator can edit a
// comment the bot wrote. An edited one is refused, as the roles record is.
func TestAnEditedRecordIsRefused(t *testing.T) {
	s := signer(t)
	_, block := raised(t, s, time.Now())

	edited := strings.Replace(block, "a-newcomer", "an-intruder", 1)
	if _, err := Parse(edited, "codecheckers/testing-dev-register#202", s); err == nil {
		t.Fatal("an edited record was accepted")
	} else if !strings.Contains(err.Error(), "not one I wrote") {
		t.Errorf("error %q, want it to say the record is not the bot's", err)
	}
}

// Signed by somebody else's key is not signed by mine.
func TestARecordFromAnotherKeyIsRefused(t *testing.T) {
	_, block := raised(t, signer(t), time.Now())
	if _, err := Parse(block, "codecheckers/testing-dev-register#202", signer(t)); err == nil {
		t.Error("a record signed by another key was accepted")
	}
}

// The check is inside the signature, so a record lifted whole from one issue
// into another verifies - and is then refused for being somebody else's.
func TestARecordCannotBeLiftedBetweenIssues(t *testing.T) {
	s := signer(t)
	_, block := raised(t, s, time.Now())

	if _, err := Parse(block, "codecheckers/testing-dev-register#999", s); err == nil {
		t.Fatal("a record was accepted on another issue")
	} else if !strings.Contains(err.Error(), "belongs to") {
		t.Errorf("error %q", err)
	}
}

// Most comments carry no record, and that is not an error worth a log line.
func TestACommentWithoutARecordSaysSoQuietly(t *testing.T) {
	if _, err := Parse("Hello! I am awake.\n", "", signer(t)); err != ErrNoRecord {
		t.Errorf("error %v, want ErrNoRecord", err)
	}
}

// A marker that is not at the top is a comment quoting a record, not a record
// - which is how a check report of somebody's codecheck.yml must read.
func TestAQuotedRecordIsNotARecord(t *testing.T) {
	s := signer(t)
	_, block := raised(t, s, time.Now())

	if _, err := Parse("Somebody wrote this at me:\n\n"+block, "", s); err != ErrNoRecord {
		t.Errorf("error %v, want a quoted record to be no record at all", err)
	}
}

// Defuse escapes the marker, so a record written into a reply body never
// survives the way out. That is the property this package depends on: only
// Render may emit one.
func TestAReplyBodyCannotCarryARecord(t *testing.T) {
	s := signer(t)
	_, block := raised(t, s, time.Now())

	if _, err := Parse(command.Defuse(block), "", s); err != ErrNoRecord {
		t.Errorf("a defused record still parsed: %v", err)
	}
}

// Older is what "three days" is measured with, and a record with no date is
// never old enough to act on twice.
func TestOlderMeasuresFromWhenItWasRaised(t *testing.T) {
	now := time.Now()
	recent := Record{Asked: now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)}
	stale := Record{Asked: now.Add(-4 * 24 * time.Hour).Format(time.RFC3339)}
	undated := Record{}

	if recent.Older(3*24*time.Hour, now) {
		t.Error("two days is not three")
	}
	if !stale.Older(3*24*time.Hour, now) {
		t.Error("four days is more than three")
	}
	if undated.Older(0, now) {
		t.Error("a record with no date should never be old enough to act on")
	}
}
