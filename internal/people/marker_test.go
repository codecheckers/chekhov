package people

import (
	"context"
	"strings"
	"testing"
)

// legacyComment is a record as the bot wrote it while it was marked
// `chekhov:roles`: signed over those bytes, marker and all.
func legacyComment(t *testing.T, repository string, issue int, roles Record) string {
	t.Helper()
	old := Block{Marker: legacyMarker, Name: "roles record"}
	what := record{Version: recordVersion, Check: CheckOf(repository, issue), Key: signer(t).PublicKey(), Roles: roles}
	whole, signature, err := old.Render(what, signer(t))
	if err != nil {
		t.Fatal(err)
	}
	return content{payload: whole, record: what, Signature: signature}.Comment()
}

// The marker changed from `roles` to `record` when the record began to hold a
// certificate identifier too. A record written before still counts - it is
// read, and verifies as written - and the next change writes it with the new
// marker, in the same comment.
func TestARecordWrittenUnderTheOldMarkerStillCounts(t *testing.T) {
	body := legacyComment(t, "codecheckers/testing", 1, Record{AssignedCodechecker: "a-codechecker"})
	if !strings.HasPrefix(body, legacyMarker) {
		t.Fatalf("the fixture is not an old record: %q", body)
	}
	issue := &comments{comments: []Comment{{ID: 3, Author: "chekhovbot", Body: body}}}
	store := checks(t, issue)

	reading, err := store.Read(context.Background(), "codecheckers/testing", 1)
	if err != nil || reading.Record.AssignedCodechecker != "a-codechecker" || !reading.Signed() {
		t.Fatalf("the old record was not read as signed: %+v (%v)", reading, err)
	}

	if _, err := store.Update(context.Background(), "codecheckers/testing", 1, func(r Record) (Record, error) {
		r.Certificate = "2026-001"
		return r, nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	rewritten := issue.edited[3]
	if !strings.HasPrefix(rewritten, marker) || strings.Contains(rewritten, legacyMarker) {
		t.Errorf("the record was not rewritten with the new marker: %q", rewritten)
	}
	if issue.posted != "" {
		t.Errorf("a second record was posted beside the old one: %q", issue.posted)
	}

	reading, err = store.Read(context.Background(), "codecheckers/testing", 1)
	if err != nil || reading.Record.Certificate != "2026-001" || !reading.Signed() {
		t.Errorf("the rewritten record does not read back: %+v (%v)", reading, err)
	}
}

// One comment, one record: an old and a new marker together are two, and
// neither is believed.
func TestAnOldAndANewMarkerTogetherAreRefused(t *testing.T) {
	body := comment(t, signer(t), "codecheckers/testing", 1, Record{AssignedCodechecker: "a-codechecker"}) +
		"\n" + legacyMarker + `{"roles":{"handling_editor":"an-impostor"}} -->` + "\n"
	issue := &comments{comments: []Comment{{ID: 3, Author: "chekhovbot", Body: body}}}

	if _, err := checks(t, issue).Read(context.Background(), "codecheckers/testing", 1); err == nil ||
		!strings.Contains(err.Error(), "more than one") {
		t.Errorf("a comment with two records was read: %v", err)
	}
}
