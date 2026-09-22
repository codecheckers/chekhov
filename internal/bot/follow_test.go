package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/announce/announcetest"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/mastodon"
)

// followServer is a test server whose services read the published
// certificate 1970-001, with following switched on and a fake Mastodon.
func followServer(t *testing.T) (*Server, *fakeToots) {
	t.Helper()
	server, _ := testServer(t)

	published := announcetest.New(t)
	server.Services = published.Services
	server.Settings.Chekhov.Env.Certificates = published.Settings.Chekhov.Env.Certificates
	server.Settings.Chekhov.Env.CodecheckerLists = published.Settings.CodecheckerLists()
	server.Settings.Chekhov.Mastodon.Follow = true

	toots := &fakeToots{}
	server.Toots = toots
	return server, toots
}

func followAs(server *Server, author string, args ...string) string {
	return server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: author},
		command.Command{Name: command.Follow, Args: args}).body
}

func TestFollowPreviewActsOnNothingWhenDisabled(t *testing.T) {
	server, toots := followServer(t)
	server.Settings.Chekhov.Mastodon.Follow = false

	reply := followAs(server, "nuest", "1970-001")
	if !strings.Contains(reply, "Following is switched off for this deployment") {
		t.Errorf("the reply does not say following is off: %s", reply)
	}
	if len(toots.followed) != 0 || len(toots.collections) != 0 {
		t.Error("a disabled preview followed or touched a collection")
	}
}

func TestFollowPreviewShowsWhoIsAlreadyFollowed(t *testing.T) {
	server, toots := followServer(t)
	toots.followed = map[string]bool{"id-@carberry@example.social": true}

	reply := followAs(server, "nuest", "1970-001")
	for _, want := range []string{
		"Preview of following certificate 1970-001's accounts",
		"codecheckers: `@else@example.social`, `@inst@example.social`",
		"authors: `@carberry@example.social`",
		"venue: `@venue@example.social`",
		"already followed: `@carberry@example.social`",
		"not yet followed:",
		"@chekhovbot follow 1970-001 confirm",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the preview does not say %q:\n%s", want, reply)
		}
	}
	if len(toots.followed) != 1 {
		t.Error("a preview followed someone")
	}
	if len(toots.collections) != 0 {
		t.Error("a preview created a collection")
	}
}

func TestFollowConfirmFollowsAndCuratesTheCollections(t *testing.T) {
	server, toots := followServer(t)

	reply := followAs(server, "nuest", "1970-001", "confirm")
	for _, want := range []string{
		"Followed for certificate 1970-001",
		"followed:",
		"@carberry@example.social",
		"@else@example.social",
		"@inst@example.social",
		"@venue@example.social",
		"requested for the Codecheckers collection:",
		"requested for the Authors collection: `@carberry@example.social`",
		"requested for the Venues collection: `@venue@example.social`",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}

	if len(toots.followed) != 4 {
		t.Errorf("%d accounts followed, want 4", len(toots.followed))
	}
	if len(toots.collections) != 3 {
		t.Fatalf("%d collections exist, want 3", len(toots.collections))
	}
	codecheckers := toots.collections["Codecheckers"]
	if !codecheckers.Discoverable {
		t.Error("the Codecheckers collection was not created discoverable")
	}
	if members := toots.collectionItems[codecheckers.ID]; len(members) != 2 {
		t.Errorf("Codecheckers has %d members, want 2", len(members))
	}
}

// A second confirm must not re-request anyone: follow, like announce, reads
// the world afresh and so has to notice what is already true.
func TestFollowConfirmTwiceRequestsNobodyTwice(t *testing.T) {
	server, toots := followServer(t)

	followAs(server, "nuest", "1970-001", "confirm")
	codecheckers := toots.collections["Codecheckers"]
	firstCount := len(toots.collectionItems[codecheckers.ID])

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "followed: nobody new") {
		t.Errorf("a second confirm followed someone again: %s", reply)
	}
	if !strings.Contains(reply, "collections: already up to date") {
		t.Errorf("a second confirm reported collection changes: %s", reply)
	}
	if got := len(toots.collectionItems[codecheckers.ID]); got != firstCount {
		t.Errorf("Codecheckers gained members on a second confirm: %d, was %d", got, firstCount)
	}
}

func TestFollowConfirmSkipsAlreadyMemberAccounts(t *testing.T) {
	server, toots := followServer(t)
	toots.collections = map[string]mastodon.Collection{"Codecheckers": {ID: "1", Name: "Codecheckers"}}
	toots.collectionItems = map[string][]mastodon.CollectionItem{
		"1": {{ID: "existing", State: mastodon.CollectionItemAccepted, AccountID: "id-@else@example.social", CreatedAt: time.Unix(0, 0)}},
	}

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if strings.Contains(reply, "requested for the Codecheckers collection: `@else@example.social`") {
		t.Errorf("an already-member account was reported as requested: %s", reply)
	}
	if !strings.Contains(reply, "requested for the Codecheckers collection: `@inst@example.social`") {
		t.Errorf("the missing account was not requested: %s", reply)
	}
	if len(toots.collectionItems["1"]) != 2 {
		t.Errorf("Codecheckers has %d items, want 2", len(toots.collectionItems["1"]))
	}
}

// Mastodon caps a collection at mastodon.MaxCollectionItems; past that, the
// oldest member is evicted to make room for a new one.
func TestFollowConfirmEvictsTheOldestMemberWhenFull(t *testing.T) {
	server, toots := followServer(t)
	toots.collections = map[string]mastodon.Collection{"Codecheckers": {ID: "1", Name: "Codecheckers"}}
	items := make([]mastodon.CollectionItem, mastodon.MaxCollectionItems)
	for i := range items {
		items[i] = mastodon.CollectionItem{
			ID: fmt.Sprintf("old-%d", i), State: mastodon.CollectionItemPending,
			AccountID: fmt.Sprintf("old-account-%d", i), CreatedAt: time.Unix(int64(i), 0),
		}
	}
	toots.collectionItems = map[string][]mastodon.CollectionItem{"1": items}

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "made room in the Codecheckers collection by evicting 2 older members") {
		t.Errorf("the reply does not report the eviction: %s", reply)
	}
	if !strings.Contains(reply, "requested for the Codecheckers collection:") {
		t.Errorf("the new members were not requested: %s", reply)
	}
	final := toots.collectionItems["1"]
	if len(final) != mastodon.MaxCollectionItems {
		t.Errorf("Codecheckers has %d items, want the cap of %d", len(final), mastodon.MaxCollectionItems)
	}
	for _, item := range final {
		if item.ID == "old-0" || item.ID == "old-1" {
			t.Errorf("the oldest items were not evicted: %+v", final)
		}
	}
}

// An account Mastodon refuses to add (not following @codecheck back, most
// likely) is not a hard failure: the run continues, and the account is sent
// a private message asking them to follow back, since the GitHub reply never
// reaches them.
func TestFollowConfirmAsksAnIneligibleAccountToFollowBack(t *testing.T) {
	server, toots := followServer(t)
	toots.addItemDenied = map[string]bool{"id-@else@example.social": true}

	reply := followAs(server, "nuest", "1970-001", "confirm")
	for _, want := range []string{
		"not yet eligible for the Codecheckers collection (they do not follow @codecheck back): `@else@example.social`",
		"requested for the Codecheckers collection: `@inst@example.social`",
		"privately asked to follow back: `@else@example.social`",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if len(toots.directMessages) != 1 {
		t.Fatalf("%d direct messages sent, want 1", len(toots.directMessages))
	}
	for _, want := range []string{"@else@example.social", "1970-001", "Codecheckers: https://example.social/collections/"} {
		if !strings.Contains(toots.directMessages[0], want) {
			t.Errorf("the direct message does not mention %q: %s", want, toots.directMessages[0])
		}
	}
	// Everyone else was followed and the venue still followed normally.
	if len(toots.followed) != 4 {
		t.Errorf("%d accounts followed, want 4", len(toots.followed))
	}
}

// PostDirect failing must not undo or block what the run already did.
func TestFollowConfirmContinuesWhenTheDirectMessageFails(t *testing.T) {
	server, toots := followServer(t)
	toots.addItemDenied = map[string]bool{"id-@else@example.social": true}
	toots.postDirectErr = errors.New("Mastodon answered 500: internal server error")

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "not yet eligible for the Codecheckers collection") {
		t.Errorf("the reply does not report the ineligible account: %s", reply)
	}
	if strings.Contains(reply, "privately asked to follow back") {
		t.Errorf("the reply claims a direct message was sent when it failed: %s", reply)
	}
	if len(toots.directMessages) != 0 {
		t.Errorf("a direct message was recorded despite the simulated failure: %v", toots.directMessages)
	}
}

// A rejected or revoked item is a declined request, not an absent one: it
// must not be asked again every run, even though it doesn't count towards
// the cap (activeItems leaves it out on purpose).
func TestFollowConfirmDoesNotReaskARejectedAccount(t *testing.T) {
	server, toots := followServer(t)
	toots.collections = map[string]mastodon.Collection{"Codecheckers": {ID: "1", Name: "Codecheckers"}}
	toots.collectionItems = map[string][]mastodon.CollectionItem{
		"1": {{ID: "declined", State: "rejected", AccountID: "id-@inst@example.social", CreatedAt: time.Unix(0, 0)}},
	}

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if strings.Contains(reply, "requested for the Codecheckers collection: `@inst@example.social`") {
		t.Errorf("a rejected account was requested again: %s", reply)
	}
	if !strings.Contains(reply, "requested for the Codecheckers collection: `@else@example.social`") {
		t.Errorf("the other codechecker was not requested: %s", reply)
	}
	// The rejected item is not "active" (doesn't count towards the cap), so
	// it stays in place rather than being evicted or replaced.
	if len(toots.collectionItems["1"]) != 2 {
		t.Errorf("Codecheckers has %d items, want 2 (the rejection kept, plus the new request)", len(toots.collectionItems["1"]))
	}
}

// An account the directory matched can still fail to resolve on the instance
// - deleted, suspended or moved. That must not abandon the whole command:
// everyone else is still previewed, followed and curated.
func TestFollowContinuesWhenAnAccountCannotBeResolved(t *testing.T) {
	server, toots := followServer(t)
	toots.resolveErr = map[string]error{"@carberry@example.social": errors.New("Mastodon answered 410: Gone")}

	preview := followAs(server, "nuest", "1970-001")
	if !strings.Contains(preview, "on record, but the instance could not resolve: `@carberry@example.social`") {
		t.Errorf("the preview does not report the unresolved account: %s", preview)
	}

	reply := followAs(server, "nuest", "1970-001", "confirm")
	for _, want := range []string{
		"on record, but the instance could not resolve: `@carberry@example.social`",
		"@else@example.social",
		"@inst@example.social",
		"@venue@example.social",
		"requested for the Codecheckers collection:",
		"requested for the Venues collection: `@venue@example.social`",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "requested for the Authors collection") {
		t.Errorf("the unresolved author was requested for a collection: %s", reply)
	}
	if len(toots.followed) != 3 {
		t.Errorf("%d accounts followed, want 3 (not the unresolved one)", len(toots.followed))
	}
}

// A single confirm must read the account's collections once and reuse it for
// all three, not ask again per collection.
func TestFollowConfirmReadsCollectionsOnce(t *testing.T) {
	server, toots := followServer(t)

	followAs(server, "nuest", "1970-001", "confirm")
	if toots.collectionsCalls != 1 {
		t.Errorf("Collections was called %d times, want 1", toots.collectionsCalls)
	}
}

// A token of the wrong account must not follow or touch a collection,
// mirroring the guard announce gets for free through RecentStatuses.
func TestFollowConfirmRefusesATokenOfTheWrongAccount(t *testing.T) {
	server, toots := followServer(t)
	toots.verifyErr = errors.New("the token belongs to @someone, but this deployment posts as @codecheck")

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "the token belongs to @someone") {
		t.Errorf("the reply does not surface the wrong-account error: %s", reply)
	}
	if len(toots.followed) != 0 || len(toots.collections) != 0 {
		t.Error("a wrong-account confirm followed or touched a collection")
	}
}

func TestFollowIsRefusedForANonEditor(t *testing.T) {
	server, toots := followServer(t)

	reply := followAs(server, "someone-else", "1970-001", "confirm")
	if !strings.Contains(reply, "is for editors") {
		t.Errorf("a non-editor was not refused: %s", reply)
	}
	if len(toots.followed) != 0 {
		t.Error("a non-editor's confirm followed someone")
	}
}

func TestFollowNeedsACertificate(t *testing.T) {
	server, _ := followServer(t)
	if reply := followAs(server, "nuest", "please"); !strings.Contains(reply, "follow 2020-001") {
		t.Errorf("the reply does not say how to name a certificate: %s", reply)
	}
}

func TestFollowWithoutTootsIsTreatedAsDisabled(t *testing.T) {
	server, _ := followServer(t)
	server.Toots = nil

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "Following is switched off for this deployment") {
		t.Errorf("the reply does not say following is off: %s", reply)
	}
}
