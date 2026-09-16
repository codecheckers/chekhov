package bot

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/codecheckers/chekhov/internal/announce/announcetest"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/mastodon"
)

// fakeToots stands in for Mastodon and records what would have been posted.
type fakeToots struct {
	mu       sync.Mutex
	recent   []mastodon.Posted
	uploads  int
	posts    []mastodon.Status
	limitErr error
}

func (f *fakeToots) Post(_ context.Context, status mastodon.Status) (mastodon.Posted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, status)
	posted := mastodon.Posted{ID: fmt.Sprint(len(f.posts)), URL: "https://example.social/@codecheck/1", Content: status.Text}
	f.recent = append([]mastodon.Posted{posted}, f.recent...)
	return posted, nil
}

func (f *fakeToots) UploadMedia(context.Context, string, string, []byte, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads++
	return "media-1", nil
}

func (f *fakeToots) RecentStatuses(context.Context, int) ([]mastodon.Posted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recent, nil
}

func (f *fakeToots) Limits(context.Context) (mastodon.Limits, error) {
	return mastodon.Limits{MaxCharacters: 1500, CharactersPerURL: 23, ImageSizeLimit: 16 << 20}, f.limitErr
}

// announceServer is a test server whose services read the published
// certificate 1970-001, with one page image, and whose toots go to a fake.
func announceServer(t *testing.T) (*Server, *fakeToots) {
	t.Helper()
	server, _ := testServer(t)

	published := announcetest.New(t)
	published.Pages(t, 1)
	server.Services = published.Services
	server.Settings.Chekhov.Env.Certificates = published.Settings.Chekhov.Env.Certificates
	server.Settings.Chekhov.Env.CodecheckerLists = published.Settings.CodecheckerLists()

	toots := &fakeToots{}
	server.Toots = toots
	return server, toots
}

func announceAs(server *Server, author string, args ...string) string {
	return server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: author},
		command.Command{Name: command.Announce, Args: args})
}

func TestAnnouncePreviewPostsNothing(t *testing.T) {
	server, toots := announceServer(t)

	reply := announceAs(server, "nuest", "1970-001")
	for _, want := range []string{
		"Preview of the announcement of certificate 1970-001",
		"carberry@example.social",
		"visibility: `direct`, so the mentions are written without `@`",
		"no fediverse account on record for: Fake Author Without ORCID",
		"1 page",
		"@chekhovbot announce 1970-001 confirm",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the preview does not say %q:\n%s", want, reply)
		}
	}
	// The mention list is in code spans, which GitHub does not link; the toot
	// itself must carry no mention at all.
	if _, toot, _ := strings.Cut(reply, "```text\n"); regexp.MustCompile(`(^|[\s(])@`).MatchString(toot[:strings.Index(toot, "```")]) {
		t.Errorf("a direct toot carries a live mention:\n%s", reply)
	}
	if toots.uploads != 0 || len(toots.posts) != 0 {
		t.Error("a preview posted something")
	}
}

func TestAnnounceConfirmPostsOnceAndThenRefuses(t *testing.T) {
	server, toots := announceServer(t)

	reply := announceAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "Announced certificate 1970-001: https://example.social/@codecheck/1") {
		t.Fatalf("confirm did not post: %s", reply)
	}
	if len(toots.posts) != 1 || toots.uploads != 1 {
		t.Fatalf("%d posts and %d uploads, want 1 each", len(toots.posts), toots.uploads)
	}
	status := toots.posts[0]
	if !strings.HasPrefix(status.IdempotencyKey, "codecheck-1970-001-") || status.Visibility != "direct" ||
		len(status.MediaIDs) != 1 || status.MediaIDs[0] != "media-1" {
		t.Errorf("posted %+v", status)
	}

	again := announceAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(again, "Already announced: https://example.social/@codecheck/1") {
		t.Errorf("a second confirm was not refused: %s", again)
	}
	if len(toots.posts) != 1 || toots.uploads != 1 {
		t.Errorf("a second confirm posted or uploaded: %d posts, %d uploads", len(toots.posts), toots.uploads)
	}
}

// announce is the first editor-only command, so the refusal is reachable.
func TestAnnounceIsRefusedForANonEditor(t *testing.T) {
	server, toots := announceServer(t)

	reply := announceAs(server, "someone-else", "1970-001", "confirm")
	if !strings.Contains(reply, "is for editors") {
		t.Errorf("a non-editor was not refused: %s", reply)
	}
	if len(toots.posts) != 0 {
		t.Error("a non-editor's confirm posted")
	}
}

func TestAnnounceWithoutTootsPreviewsButDoesNotPost(t *testing.T) {
	server, _ := announceServer(t)
	server.Toots = nil

	reply := announceAs(server, "nuest", "1970-001", "confirm")
	if !strings.Contains(reply, "Announcing is switched off for this deployment") {
		t.Errorf("the reply does not say announcing is off: %s", reply)
	}
}

func TestAnnounceNeedsACertificate(t *testing.T) {
	server, _ := announceServer(t)
	if reply := announceAs(server, "nuest", "please"); !strings.Contains(reply, "announce 2020-001") {
		t.Errorf("the reply does not say how to name a certificate: %s", reply)
	}
	if reply := announceAs(server, "nuest", "1970-099"); !strings.Contains(reply, "could not read certificate 1970-099") {
		t.Errorf("an unpublished certificate was not reported: %s", reply)
	}
}
