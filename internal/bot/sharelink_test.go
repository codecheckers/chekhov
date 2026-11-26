package bot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// zenodoWith gives a server a Zenodo that has record 1 and nothing else.
func zenodoWith(t *testing.T, server *Server) {
	t.Helper()
	stub := testserver.New(t)
	stub.Status("/zenodo/records/1", http.StatusOK)
	server.Services = &check.Services{Access: check.Access{HTTP: stub.Client(), Zenodo: stub.URL + "/zenodo"}}
}

const (
	liveLink = "https://zenodo.org/records/1?token=made-up"
	deadLink = "https://zenodo.org/records/404404?token=made-up"
)

// Only a comment whose every link surely points at nothing is left alone, and
// anything the bot cannot be sure of counts against that.
func TestWhichCommentsPointAtNothing(t *testing.T) {
	server, _ := testServer(t)
	zenodoWith(t, server)
	server.Services.ZenodoSandbox = "https://sandbox.example.invalid/api"
	other := func(n string) string { return "https://zenodo.org/records/" + n + "?token=made-up" }

	for name, text := range map[string]string{
		"a link":                     deadLink,
		"without a scheme":           "zenodo.org/records/404404?token=made-up",
		"in Markdown":                "see [the draft](" + deadLink + ") and `" + deadLink + "`",
		"in angle brackets":          "<" + deadLink + ">",
		"as many as are asked about": deadLink + "\n" + other("404405") + "\n" + other("404406"),
		"one ending a sentence, and again without": deadLink + ". " + deadLink + " " + other("404405") +
			" " + other("404406"),
	} {
		if !server.pointsAtNothing(context.Background(), text) {
			t.Errorf("%s: %q was not seen to point at nothing", name, text)
		}
	}
	for name, text := range map[string]string{
		"a record there is":                  liveLink,
		"one among the dead":                 deadLink + " " + liveLink,
		"two run together":                   deadLink + "," + liveLink,
		"a link the bot cannot read":         "https://zenodo.org/records/404404/files/a.pdf?token=made-up",
		"another beside a dead one":          deadLink + " https://zenodo.org/communities/codecheck?token=made-up",
		"a token outside a link":             deadLink + " https://zenodo.org/records/1/(copy)?token=made-up",
		"somewhere else that looks like it":  "https://evilzenodo.org/records/404404?token=made-up",
		"more than are asked about":          deadLink + " " + other("2") + " " + other("3") + " " + other("4"),
		"no token at all":                    "https://zenodo.org/records/404404",
		"the sandbox, which cannot be asked": "https://sandbox.zenodo.org/records/404404?token=made-up",
	} {
		if server.pointsAtNothing(context.Background(), text) {
			t.Errorf("%s: %q was taken to point at nothing", name, text)
		}
	}
	server.Services = nil
	if server.pointsAtNothing(context.Background(), deadLink) {
		t.Error("without services, a link was taken to point at nothing")
	}
}

// A stranger's link to nothing is not acted on: no deletion, and only the one
// refusal anybody gets, which says nothing of links.
func TestAStrangersLinkToNothingIsAnOrdinaryComment(t *testing.T) {
	server, path := gated(t)
	zenodoWith(t, server)
	comment := asking{by: stranger, body: "@chekhovbot check zenodo " + deadLink}

	comment.send(t, server)
	comment.send(t, server)

	if len(path.deleted) != 0 || path.count() != 1 {
		t.Fatalf("deleted %v, posted %d times: %s", path.deleted, path.count(), path.last())
	}
	says(t, path.last(), refusalStart)
	if command.ExplainsShareLink(path.last()) || strings.Contains(path.last(), "Zenodo") {
		t.Errorf("the refusal speaks of the link:\n%s", path.last())
	}
}

// A link that may be real is deleted every time, and explained as many times
// as the settings say, to each person on each issue.
func TestShareLinkRepliesStopAtTheLimit(t *testing.T) {
	for name, limit := range map[string]struct {
		set          *int
		afterFive    int
		explanations int
	}{
		"by default": {nil, 3, 3},
		"one":        {new(1), 1, 1},
		// Never explained, but still told once that they may not ask.
		"none": {new(0), 1, 0},
	} {
		server, path := gated(t)
		zenodoWith(t, server)
		server.Settings.Chekhov.Refusals.ShareLinkReplies = limit.set
		comment := asking{by: stranger, body: "@chekhovbot check zenodo " + liveLink}

		for range 5 {
			comment.send(t, server)
		}

		explanations := 0
		for _, posted := range path.recorder.comments {
			if command.ExplainsShareLink(posted.Body) {
				explanations++
			}
		}
		if len(path.deleted) != 5 || path.count() != limit.afterFive || explanations != limit.explanations {
			t.Errorf("%s: deleted %d of 5, %d posts of which %d explain, want %d and %d",
				name, len(path.deleted), path.count(), explanations, limit.afterFive, limit.explanations)
		}

		before := path.count()
		asking{by: coauthor, body: comment.body}.send(t, server)
		asking{by: stranger, body: comment.body, issue: 2}.send(t, server)
		if path.count() != before+2 {
			t.Fatalf("%s: another person and another issue got %d posts, want 2", name, path.count()-before)
		}
		for _, posted := range path.recorder.comments[before:] {
			if command.ExplainsShareLink(posted.Body) != (limit.explanations > 0) {
				t.Errorf("%s: a limit of their own was not given:\n%s", name, posted.Body)
			}
		}
	}
}

// A plain refusal does not use up the explanations, and where the comments
// cannot be listed a deletion goes unexplained rather than uncounted.
func TestShareLinkRepliesAreCountedFromTheIssue(t *testing.T) {
	server, path := gated(t)
	zenodoWith(t, server)
	server.Settings.Chekhov.Refusals.ShareLinkReplies = new(1)
	asking{by: stranger, body: "@chekhovbot hello"}.send(t, server)
	asking{by: stranger, body: "@chekhovbot check zenodo " + liveLink}.send(t, server)
	if path.count() != 2 || !command.ExplainsShareLink(path.last()) {
		t.Fatalf("%d posts, the last:\n%s", path.count(), path.last())
	}

	path.listErr = errors.New("no comments today")
	asking{by: coauthor, body: "@chekhovbot check zenodo " + liveLink}.send(t, server)
	if path.count() != 2 || len(path.deleted) != 2 {
		t.Errorf("deleted %v, %d posts", path.deleted, path.count())
	}
}

// Somebody who may ask, with a link to nothing, keeps their comment and is
// told why.
func TestCheckZenodoLeavesALinkToNothing(t *testing.T) {
	server := auditing(t)
	deleter := &deleting{Poster: server.Replies}
	server.Replies = deleter

	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest", Comment: 99,
		Body: "@chekhovbot check zenodo https://zenodo.org/records/404404?token=" + shareSecret}
	parsed, _ := command.Parse(event.Body)
	reply := server.checkZenodo(context.Background(), event, parsed, server.Services.Fresh()).body

	if len(deleter.deleted) != 0 {
		t.Errorf("deleted %v", deleter.deleted)
	}
	says(t, reply, "so I have left the comment as it is", "delete the comment")
	if strings.Contains(reply, shareSecret) {
		t.Errorf("the reply repeats the token:\n%s", reply)
	}
}

// A limit that is spent leaves the one plain refusal, which is said once like
// any other: while one is on its way, a link adds no second.
func TestASpentLimitDoesNotRepeatThePlainRefusal(t *testing.T) {
	server, path := gated(t)
	zenodoWith(t, server)
	server.Settings.Chekhov.Refusals.ShareLinkReplies = new(0)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: stranger}
	// A plain refusal on its way holds its claim.
	server.turningAway.Store(refusalClaim(event, refusalPlain), struct{}{})

	asking{by: stranger, body: "@chekhovbot check zenodo " + liveLink}.send(t, server)

	if path.count() != 0 || len(path.deleted) != 1 {
		t.Errorf("deleted %v, %d posts:\n%s", path.deleted, path.count(), path.last())
	}
}

// A link that could not be deleted is still up, so the warning of it is not a
// deletion's explanation: deletions neither use it up nor silence it, it is
// given even where those are never explained, and where the comments cannot be
// listed. It is bounded all the same, or a failing deletion would be a way to
// make the bot post for ever.
func TestAFailedDeletionIsWarnedOfWhateverTheLimitOnDeletionsSays(t *testing.T) {
	for name, limit := range map[string]struct {
		set      *int
		warnings int
	}{"by default": {nil, 3}, "deletions never explained": {new(0), 1}} {
		server, path := gated(t)
		zenodoWith(t, server)
		server.Settings.Chekhov.Refusals.ShareLinkReplies = limit.set
		comment := asking{by: stranger, body: "@chekhovbot check zenodo " + liveLink}
		for range 4 {
			comment.send(t, server)
		}
		path.deleteErr = errors.New("GitHub says no")

		before := path.count()
		for range 5 {
			comment.send(t, server)
		}

		warnings := path.count() - before
		if warnings != limit.warnings {
			t.Errorf("%s: %d warnings of a link left up, want %d", name, warnings, limit.warnings)
		}
		for _, posted := range path.recorder.comments[before:] {
			says(t, posted.Body, "could not delete the comment")
		}
	}

	server, path := gated(t)
	zenodoWith(t, server)
	path.deleteErr, path.listErr = errors.New("GitHub says no"), errors.New("no comments today")
	asking{by: stranger, body: "@chekhovbot check zenodo " + liveLink}.send(t, server)
	if path.count() != 1 || !strings.Contains(path.last(), "could not delete the comment") {
		t.Errorf("unlisted: %d posts:\n%s", path.count(), path.last())
	}
}

// A link to nothing in an issue's description is advised on as one there.
func TestALinkToNothingInADescriptionSaysWhereItIs(t *testing.T) {
	server, _ := testServer(t)
	zenodoWith(t, server)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest",
		Body: "@chekhovbot check zenodo " + deadLink}
	parsed, _ := command.Parse(event.Body)

	note := command.ShareLinkNote(server.deleteShareLink(context.Background(), event, parsed))

	says(t, note, "left the issue's description as it is", "edit it out of the description")
}
