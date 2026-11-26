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
func TestWhichCommentsOpenNothing(t *testing.T) {
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
		if !server.opensNothing(context.Background(), text) {
			t.Errorf("%s: %q was not seen to open nothing", name, text)
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
		if server.opensNothing(context.Background(), text) {
			t.Errorf("%s: %q was taken to open nothing", name, text)
		}
	}
	server.Services = nil
	if server.opensNothing(context.Background(), deadLink) {
		t.Error("without services, a link was taken to open nothing")
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
	says(t, reply, "So I have left the comment as it is", "delete the comment")
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

// A share link is dealt with once for the whole comment, before any command in
// it is run or quoted: also one past the most that are run, which used to stay
// up and be repeated, token and all, in the note on what was not run.
func TestAShareLinkPastTheMostCommandsIsDeletedAndNeverRepeated(t *testing.T) {
	ask := "@chekhovbot check zenodo " + liveLink + "SECRET"
	for name, body := range map[string]string{
		"past the most that are run": strings.Repeat("@chekhovbot hello\n", command.MostCommands) + ask,
		"quoted by another command":  strings.Repeat("@chekhovbot hello\n", command.MostCommands) + "@chekhovbot hello " + liveLink + "SECRET\n" + ask,
		"asked twice":                ask + "\n" + ask,
	} {
		server, path := gated(t)
		zenodoWith(t, server)
		server.done = make(chan struct{}, 10)
		commands, _ := command.Capped(command.Split(body))

		asking{by: "nuest", body: body}.send(t, server)
		for range len(commands) - 1 {
			<-server.done
		}

		if len(path.deleted) != 1 {
			t.Errorf("%s: deleted %v, want the comment once", name, path.deleted)
		}
		said := false
		for _, posted := range path.recorder.comments {
			if strings.Contains(posted.Body, "SECRET") {
				t.Errorf("%s: a reply repeats the token:\n%s", name, posted.Body)
			}
			said = said || command.ExplainsDeletion(posted.Body)
		}
		if !said {
			t.Errorf("%s: nothing says the comment was deleted", name)
		}
	}
}

// listsDeleting is a reply path of the lists that deletes comments.
type listsDeleting struct {
	*recorder
	deleted []string
}

func (l *listsDeleting) DeleteComment(_ context.Context, repository string, _ int64) error {
	l.deleted = append(l.deleted, repository)
	return nil
}

// A comment on the lists is deleted through the lists' own path: the
// register's refuses any repository but its own, so it never could.
func TestAShareLinkOnTheListsIsDeletedThroughTheLists(t *testing.T) {
	for name, by := range map[string]string{"a stranger": stranger, "a member": "nuest"} {
		server, register, recorded, _ := listsServer(t)
		lists := &listsDeleting{recorder: recorded}
		server.Lists = lists
		registers := &deleting{Poster: register}
		server.Replies = registers

		eventType, payload := registrationEvent(testLists, registrationRow,
			"@chekhovbot check zenodo "+liveLink+"SECRET", by, "registration")
		payload["comment"].(map[string]any)["id"] = 99
		deliverEvent(t, server, eventType, payload)

		if len(lists.deleted) != 1 || lists.deleted[0] != testLists || len(registers.deleted) != 0 {
			t.Errorf("%s: deleted through the lists %v, through the register %v", name, lists.deleted, registers.deleted)
		}
		if reply := lists.last(); !command.ExplainsDeletion(reply) || strings.Contains(reply, "SECRET") {
			t.Errorf("%s: the reply:\n%s", name, reply)
		}
	}
}

// A reminder keeps its note in a hidden record in the bot's own comment, which
// anyone can read from the API: the token does not go in there either.
func TestAReminderKeepsNoShareToken(t *testing.T) {
	server, path := gated(t)

	asking{by: "nuest", body: "@chekhovbot remind me in 3 days\n" + liveLink + "SECRET"}.send(t, server)

	if reply := path.last(); !strings.Contains(reply, "chekhov:") || strings.Contains(reply, "SECRET") {
		t.Errorf("the reminder, record and all:\n%s", reply)
	}
}

// What a link is wrapped in or runs into must not be read as part of its
// token: Zenodo would say the damaged token opens nothing, and the link, which
// GitHub may well show without it, would stay up. So a token that is not
// plainly a whole one is doubt, and every doubt deletes.
func TestATokenThatMayNotBeWholeIsDoubt(t *testing.T) {
	server, _ := testServer(t)
	zenodoWith(t, server)

	for _, text := range []string{
		"**" + deadLink + "**", "**my draft: " + deadLink + "**", "_draft " + deadLink + "_", "__ " + deadLink + "__",
		"~~old " + deadLink + "~~", deadLink + `\`, deadLink + "}", deadLink + "|x", deadLink + "”",
		deadLink + "—thanks", deadLink + "*", deadLink + "-", deadLink + "…", deadLink + "%2A", deadLink + "=",
		"{" + deadLink + "}", "|" + deadLink + "|",
	} {
		if server.opensNothing(context.Background(), text) {
			t.Errorf("%q was taken to open nothing", text)
		}
	}
	for _, text := range []string{deadLink + ".", deadLink + "&preview=1", deadLink + "#files",
		"https://zenodo.org/records/404404?preview=1&token=aB3-x_y.eyJ9.Zz0"} {
		if !server.opensNothing(context.Background(), text) {
			t.Errorf("%q, a whole token, was not asked about", text)
		}
	}
}
