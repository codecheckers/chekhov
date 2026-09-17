package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

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
		command.Command{Name: command.Follow, Args: args})
}

func TestFollowPreviewActsOnNothingWhenDisabled(t *testing.T) {
	server, toots := followServer(t)
	server.Settings.Chekhov.Mastodon.Follow = false

	reply := followAs(server, "nuest", "1970-001")
	if !strings.Contains(reply, "Following is switched off for this deployment") {
		t.Errorf("the reply does not say following is off: %s", reply)
	}
	if len(toots.followed) != 0 || len(toots.lists) != 0 {
		t.Error("a disabled preview followed or listed something")
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
	if len(toots.lists) != 0 {
		t.Error("a preview created a list")
	}
}

func TestFollowConfirmFollowsAndFillsTheLists(t *testing.T) {
	server, toots := followServer(t)

	reply := followAs(server, "nuest", "1970-001", "confirm")
	for _, want := range []string{
		"Followed for certificate 1970-001",
		"followed:",
		"@carberry@example.social",
		"@else@example.social",
		"@inst@example.social",
		"@venue@example.social",
		"added to the Codecheckers list:",
		"added to the Authors list: `@carberry@example.social`",
		"added to the Venues list: `@venue@example.social`",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}

	if len(toots.followed) != 4 {
		t.Errorf("%d accounts followed, want 4", len(toots.followed))
	}
	if len(toots.lists) != 3 {
		t.Fatalf("%d lists exist, want 3", len(toots.lists))
	}
	codecheckers := toots.lists["Codecheckers"]
	if members := toots.listAccounts[codecheckers.ID]; len(members) != 2 {
		t.Errorf("Codecheckers has %d members, want 2", len(members))
	}
}

// A second confirm must not re-follow or re-add anyone: follow, like
// announce, reads the world afresh and so has to notice what is already true.
func TestFollowConfirmTwiceAddsNobodyTwice(t *testing.T) {
	server, toots := followServer(t)

	followAs(server, "nuest", "1970-001", "confirm")
	codecheckers := toots.lists["Codecheckers"]
	firstCount := len(toots.listAccounts[codecheckers.ID])

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "followed: nobody new") {
		t.Errorf("a second confirm followed someone again: %s", reply)
	}
	if !strings.Contains(reply, "lists: already up to date") {
		t.Errorf("a second confirm reported list changes: %s", reply)
	}
	if got := len(toots.listAccounts[codecheckers.ID]); got != firstCount {
		t.Errorf("Codecheckers gained members on a second confirm: %d, was %d", got, firstCount)
	}
}

func TestFollowConfirmSkipsAlreadyListedAccounts(t *testing.T) {
	server, toots := followServer(t)
	toots.lists = map[string]mastodon.List{"Codecheckers": {ID: "1", Title: "Codecheckers"}}
	toots.listAccounts = map[string][]mastodon.Account{"1": {{ID: "id-@else@example.social"}}}

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if strings.Contains(reply, "added to the Codecheckers list: `@else@example.social`") {
		t.Errorf("an already-listed account was reported as added: %s", reply)
	}
	if !strings.Contains(reply, "added to the Codecheckers list: `@inst@example.social`") {
		t.Errorf("the missing account was not added: %s", reply)
	}
	if len(toots.listAccounts["1"]) != 2 {
		t.Errorf("Codecheckers has %d members, want 2", len(toots.listAccounts["1"]))
	}
}

// An account the directory matched can still fail to resolve on the instance
// - deleted, suspended or moved. That must not abandon the whole command:
// everyone else is still previewed, followed and listed.
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
		"added to the Codecheckers list:",
		"added to the Venues list: `@venue@example.social`",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "added to the Authors list") {
		t.Errorf("the unresolved author was added to a list: %s", reply)
	}
	if len(toots.followed) != 3 {
		t.Errorf("%d accounts followed, want 3 (not the unresolved one)", len(toots.followed))
	}
}

// findOrCreateList used to ask for the account's lists once per title; a
// single confirm must ask once and reuse it for all three.
func TestFollowConfirmReadsListsOnce(t *testing.T) {
	server, toots := followServer(t)

	followAs(server, "nuest", "1970-001", "confirm")
	if toots.listsCalls != 1 {
		t.Errorf("Lists was called %d times, want 1", toots.listsCalls)
	}
}

// A token of the wrong account must not follow or list anyone, mirroring the
// guard announce gets for free through RecentStatuses.
func TestFollowConfirmRefusesATokenOfTheWrongAccount(t *testing.T) {
	server, toots := followServer(t)
	toots.verifyErr = errors.New("the token belongs to @someone, but this deployment posts as @codecheck")

	reply := followAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "the token belongs to @someone") {
		t.Errorf("the reply does not surface the wrong-account error: %s", reply)
	}
	if len(toots.followed) != 0 || len(toots.lists) != 0 {
		t.Error("a wrong-account confirm followed or listed someone")
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
