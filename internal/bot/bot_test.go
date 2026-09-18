package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
)

const secret = "a-test-webhook-secret"

// recorder stands in for the reply path, so a test can read what the bot would
// have posted without a GitHub.
type recorder struct {
	mu       sync.Mutex
	comments []struct {
		Repository string
		Issue      int
		Body       string
	}
	err error
}

func (r *recorder) Comment(_ context.Context, repository string, issue int, body string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comments = append(r.comments, struct {
		Repository string
		Issue      int
		Body       string
	}{repository, issue, body})
	return int64(len(r.comments)), r.err
}

func (r *recorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.comments) == 0 {
		return ""
	}
	return r.comments[len(r.comments)-1].Body
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.comments)
}

func testServer(t *testing.T) (*Server, *recorder) {
	t.Helper()
	settings, err := config.Load("development")
	if err != nil {
		t.Fatal(err)
	}
	replies := &recorder{}
	server := New(settings, secret, replies, command.Deployment{
		Version: "0.1.0-test", Commit: "0123456789abcdef",
		Register: settings.TargetRepository(), Bot: settings.BotUser(),
		Environment: "development",
	})
	server.Logger = slog.New(slog.DiscardHandler)
	server.done = make(chan struct{}, 1)
	// The standing roles come from the organisation; the tests stand in for it
	// rather than reaching GitHub.
	server.Teams = &people.Teams{
		Reader: teamList{
			settings.EditorsTeam():      {"nuest"},
			settings.CodecheckersTeam(): {"nuest", "a-codechecker"},
		},
		Organisation: settings.TeamOrganisation(),
		Logger:       server.Logger,
	}
	// The per-check roles live in the issue; the test stands in for it.
	server.Checks = &people.Checks{
		Comments: &issueComments{bot: settings.BotUser()},
		Bot:      settings.BotUser(),
		Signer:   mustSign(t),
	}
	return server, replies
}

// mustSign is a key for a test bot that signs what it records.
func mustSign(t *testing.T) *people.Signer {
	t.Helper()
	signer, err := people.GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// issueComments is one issue's comments, as a test knows them.
type issueComments struct {
	bot      string
	comments []people.Comment
}

func (c *issueComments) Comments(context.Context, string, int) ([]people.Comment, error) {
	return c.comments, nil
}

func (c *issueComments) Post(ctx context.Context, repository string, issue int, body string) (int64, error) {
	return c.Comment(ctx, repository, issue, body)
}

func (c *issueComments) Comment(_ context.Context, _ string, _ int, body string) (int64, error) {
	c.comments = append(c.comments, people.Comment{ID: 1, Author: c.bot, Body: body})
	return 1, nil
}

func (c *issueComments) Edit(_ context.Context, _ string, comment int64, body string) error {
	for i := range c.comments {
		if c.comments[i].ID == comment {
			c.comments[i].Body = body
		}
	}
	return nil
}

// teamList is the organisation's teams, as a test knows them.
type teamList map[string][]string

func (l teamList) TeamMembers(_ context.Context, _, team string) ([]string, error) {
	members, known := l[team]
	if !known {
		return nil, fmt.Errorf("no such team %q", team)
	}
	return members, nil
}

// deliver posts a recorded payload the way GitHub would, signature and all.
func deliver(t *testing.T, server *Server, eventType, fixture string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "webhook", fixture))
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/dispatch", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", eventType)
	request.Header.Set("X-GitHub-Delivery", "test-delivery")
	request.Header.Set("X-Hub-Signature-256", Sign(secret, body))

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code == http.StatusAccepted {
		// The command runs after the delivery is answered; wait for it rather
		// than sleeping.
		select {
		case <-server.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the command did not finish")
		}
	}
	return response
}

func TestCommandsIsAnswered(t *testing.T) {
	server, replies := testServer(t)

	if code := deliver(t, server, "issue_comment", "issue-comment-commands.json").Code; code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	reply := replies.last()
	if !strings.Contains(reply, "Commands I understand") {
		t.Errorf("the reply is not the listing: %s", reply)
	}
	if !strings.Contains(reply, "check") {
		t.Error("the listing does not mention the check command")
	}
	if strings.Contains(reply, "hello") {
		t.Error("the listing shows a hidden command")
	}
}

func TestHelloNamesTheDeployment(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issue_comment", "issue-comment-hello.json")
	reply := replies.last()
	for _, want := range []string{"0.1.0-test", "codecheckers/testing-dev-register"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q: %s", want, reply)
		}
	}
}

// An opened issue is a comment made of its own body: people write the first
// command into the issue they open.
func TestAnOpenedIssueIsRead(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issues", "issue-opened-hello.json")
	if replies.count() != 1 {
		t.Fatalf("%d replies, want 1", replies.count())
	}
	if comment := replies.comments[0]; comment.Issue != 43 {
		t.Errorf("answered issue %d, want 43", comment.Issue)
	}
}

func TestUnknownCommandIsAnswered(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issue_comment", "issue-comment-unknown.json")
	if !strings.Contains(replies.last(), "I do not know the command") {
		t.Errorf("the reply is not the unknown-command hint: %s", replies.last())
	}
}

// Everything that must not produce a reply, and must not be an error either.
func TestDeliveriesThatAreNotActedOn(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		fixture   string
	}{
		{"a comment that is conversation", "issue_comment", "issue-comment-conversation.json"},
		{"the bot's own comment", "issue_comment", "issue-comment-from-the-bot.json"},
		{"another repository", "issue_comment", "issue-comment-wrong-repository.json"},
		{"an edited comment", "issue_comment", "issue-comment-edited.json"},
		{"an event the bot does not handle", "pull_request", "pull-request-opened.json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, replies := testServer(t)

			response := deliver(t, server, testCase.eventType, testCase.fixture)
			if response.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", response.Code)
			}
			if replies.count() != 0 {
				t.Errorf("the bot answered: %s", replies.last())
			}
		})
	}
}

// The signature is checked before the body is read, so an unsigned delivery is
// never data the bot acts on.
func TestABadSignatureIsRefused(t *testing.T) {
	server, replies := testServer(t)
	body, err := os.ReadFile(filepath.Join("testdata", "webhook", "issue-comment-commands.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, signature := range []string{"", "sha256=deadbeef", Sign("the wrong secret", body)} {
		request := httptest.NewRequest(http.MethodPost, "/dispatch", strings.NewReader(string(body)))
		request.Header.Set("X-GitHub-Event", "issue_comment")
		request.Header.Set("X-Hub-Signature-256", signature)

		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("signature %q: status = %d, want 401", signature, response.Code)
		}
	}
	if replies.count() != 0 {
		t.Error("an unsigned delivery was acted on")
	}
}

func TestHealthSaysWhichBotThisIs(t *testing.T) {
	server, _ := testServer(t)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{"codecheckers/testing-dev-register", "0.1.0-test", "development", "rules_commit", `"announce"`} {
		if !strings.Contains(body, want) {
			t.Errorf("healthz does not report %q: %s", want, body)
		}
	}
}

// The check command needs to be told what to check until chekhov#13 lands, and
// says so rather than checking the wrong thing.
func TestCheckWithoutATargetExplainsItself(t *testing.T) {
	server, _ := testServer(t)

	reply := server.answer(context.Background(), mention{Repository: "codecheckers/testing-dev-register", Issue: 1},
		command.Command{Name: command.Check})
	if !strings.Contains(reply, "github::owner/repo") {
		t.Errorf("the reply does not say how to name a repository: %s", reply)
	}
}

// A target written without a platform is read as the repository it can only
// be, and the answer names what it resolved to (chekhov#19).
func TestAShortcutTargetIsResolved(t *testing.T) {
	server, _ := testServer(t)

	reply := server.answer(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1},
		command.Command{Name: command.Check, Args: []string{"config", "codecheckers/Piccolo-2020"}})
	if !strings.Contains(reply, "github::codecheckers/Piccolo-2020") {
		t.Errorf("the shortcut was not read as a GitHub repository: %s", reply)
	}
}

// A comment is prose. The first target wins, so a trailing pleasantry cannot
// displace the repository somebody asked about.
func TestATrailingWordDoesNotBecomeTheTarget(t *testing.T) {
	server, _ := testServer(t)

	reply := server.answer(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1},
		command.Command{Name: command.Check, Args: []string{"codecheckers/Piccolo-2020", "please"}})
	if !strings.Contains(reply, "github::codecheckers/Piccolo-2020") {
		t.Errorf("the target was displaced: %s", reply)
	}
}

// A path in a comment is written by anyone on the internet. A deployment does
// not read its own disk on their say-so.
func TestADeploymentRefusesALocalPath(t *testing.T) {
	server, _ := testServer(t)

	reply := server.answer(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1},
		command.Command{Name: command.Check, Args: []string{"/etc/passwd"}})
	if !strings.Contains(reply, "github::owner/repo") {
		t.Errorf("a path was taken as a target: %s", reply)
	}
}

// The health endpoint is unauthenticated. In production it says only what a
// stranger may know.
func TestHealthSaysLessInProduction(t *testing.T) {
	server, _ := testServer(t)
	server.Deployment.Environment = "production"

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	body := response.Body.String()
	for _, secret := range []string{"commit", "rules_commit", "token_expires", "online", "announce"} {
		if strings.Contains(body, secret) {
			t.Errorf("production health reports %q: %s", secret, body)
		}
	}
	for _, want := range []string{"status", "version", "register"} {
		if !strings.Contains(body, want) {
			t.Errorf("production health does not report %q: %s", want, body)
		}
	}
}

// version is answered from a comment, and names the register it works on.
func TestVersionIsAnswered(t *testing.T) {
	server, replies := testServer(t)

	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "acodechecker"},
		command.Command{Name: command.Version})
	if !strings.Contains(reply, "codecheckers/testing-dev-register") {
		t.Errorf("the version reply does not name the register:\n%s", reply)
	}
	if !strings.Contains(reply, "validation rules") {
		t.Errorf("the version reply does not name the rules catalogue:\n%s", reply)
	}
	_ = replies
}

// A panic in one command must cost that command's answer, not the process:
// every other command in flight on a shared deployment must not be dropped
// with it. See codecheckers/chekhov#32.
func TestAPanicInACommandIsRecoveredAndReported(t *testing.T) {
	server, replies := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 7, Author: "acodechecker"}

	func() {
		defer server.recoverCommand(event, command.Command{Name: command.Hello})
		panic("boom")
	}()

	if replies.count() != 1 {
		t.Fatalf("%d replies, want 1", replies.count())
	}
	reply := replies.last()
	if !strings.Contains(reply, "went wrong") {
		t.Errorf("reply = %q, want an apology rather than silence", reply)
	}
	if !strings.Contains(reply, string(command.Hello)) {
		t.Errorf("reply = %q, want it to name the command that panicked", reply)
	}
}

// Being thanked is answered once, with a quote from the bot's namesake. A
// comment carries one command however many times it mentions the bot: the
// convention is the first line, and a second line is conversation.
func TestThanksIsAnsweredOnceWithAQuote(t *testing.T) {
	server, replies := testServer(t)

	deliver(t, server, "issue_comment", "issue-comment-thanks.json")
	if replies.count() != 1 {
		t.Fatalf("%d replies, want 1", replies.count())
	}
	reply := replies.last()
	if !strings.Contains(reply, "Anton Chekhov") || !strings.Contains(reply, "wikiquote.org") {
		t.Errorf("the reply is not an attributed quote: %s", reply)
	}
}

// The standing roles come from the organisation, and an editor command asks
// the cache rather than a list in the settings file.
func TestRefreshTeamsIsForEditors(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1}

	event.Author = "nuest"
	reply := server.answer(context.Background(), event, command.Command{Name: command.Refresh, Args: []string{"teams"}})
	if !strings.Contains(reply, server.Settings.EditorsTeam()) || !strings.Contains(reply, "| 1 |") {
		t.Errorf("the reply does not say what was read: %s", reply)
	}

	event.Author = "a-stranger"
	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Refresh, Args: []string{"teams"}}); !strings.Contains(reply, "for editors") {
		t.Errorf("a stranger was allowed to refresh the teams: %s", reply)
	}
}

// A bot that cannot read the teams refuses the editor commands. The other
// direction would hand the register to anyone who can type.
func TestWithoutTheTeamsNobodyIsAnEditor(t *testing.T) {
	server, _ := testServer(t)
	server.Teams = nil
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}

	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Refresh, Args: []string{"teams"}}); !strings.Contains(reply, "for editors") {
		t.Errorf("an editor command ran without the teams: %s", reply)
	}
	// And the listing does not advertise what it would refuse.
	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Commands}); strings.Contains(reply, "refresh teams") {
		t.Errorf("the listing offers a command nobody may run: %s", reply)
	}
}

// Only teams can be refreshed, and asking for anything else says so rather
// than quietly refreshing the teams.
func TestRefreshSaysWhatItCanRefresh(t *testing.T) {
	server, _ := testServer(t)
	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"},
		command.Command{Name: command.Refresh, Args: []string{"everything"}})
	if !strings.Contains(reply, "`teams`") {
		t.Errorf("the reply does not say what can be refreshed: %s", reply)
	}
}

// A person is everything the organisation says they are, at once: the editor
// who also performs checks holds both roles, and a command open to either is
// open to them.
func TestSomebodyCanHoldSeveralRoles(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1}

	event.Author = "nuest"
	roles := server.standingRoles(context.Background(), event)
	if !roles.Has(command.RoleEditor) || !roles.Has(command.RoleCodechecker) {
		t.Errorf("an editor who is also a codechecker holds %v", roles)
	}

	event.Author = "a-codechecker"
	roles = server.standingRoles(context.Background(), event)
	if roles.Has(command.RoleEditor) || !roles.Has(command.RoleCodechecker) {
		t.Errorf("a codechecker who is not an editor holds %v", roles)
	}

	event.Author = "a-stranger"
	if held := server.standingRoles(context.Background(), event); len(held) != 0 {
		t.Errorf("somebody in no team holds %v", held)
	}
}

// A refusal says what the asker would have to be, rather than always saying
// "editors" whatever the command needs.
func TestARefusalNamesTheRoleItNeeds(t *testing.T) {
	server, _ := testServer(t)
	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-codechecker"},
		command.Command{Name: command.Refresh, Args: []string{"teams"}})
	if !strings.Contains(reply, "is for editors") {
		t.Errorf("the refusal does not name the role: %s", reply)
	}
}

// An editor assigns a codechecker, and the check records it where the next
// command - and the next deployment - can read it back.
func TestAssigningRecordsTheRoleInTheIssue(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}

	reply := server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@a-codechecker", "as", "codechecker"}})
	if !strings.Contains(reply, "a-codechecker") || !strings.Contains(reply, "assigned codechecker") {
		t.Errorf("the reply does not say what happened: %s", reply)
	}

	// Read back the way a later command would.
	roles := server.checkRoles(context.Background(),
		mention{Repository: event.Repository, Issue: 1, Author: "a-codechecker"}, nil)
	if !roles.Has(command.RoleAssignedCodechecker) {
		t.Errorf("the codechecker holds %v after being assigned", roles)
	}

	// And `roles` says so, naming where each role comes from.
	listing := server.answer(context.Background(), event, command.Command{Name: command.ListRoles})
	if !strings.Contains(listing, "a-codechecker") || !strings.Contains(listing, "teams on GitHub") {
		t.Errorf("the listing does not report the check: %s", listing)
	}
}

// The conflict of interest CODECHECK exists to prevent is refused before
// anything is recorded.
func TestAnAuthorCannotBeAssignedAsTheCodechecker(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}

	server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@an-author", "as", "author"}})
	reply := server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@an-author", "as", "codechecker"}})

	if !strings.Contains(reply, "cannot check a paper they wrote") {
		t.Errorf("the refusal does not say why: %s", reply)
	}
	roles := server.checkRoles(context.Background(),
		mention{Repository: event.Repository, Issue: 1, Author: "an-author"}, nil)
	if roles.Has(command.RoleAssignedCodechecker) {
		t.Errorf("the author was recorded as the codechecker anyway: %v", roles)
	}
}

// Only an editor can be made the handling editor, and the refusal says so.
func TestTheHandlingEditorHasToBeAnEditor(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}

	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@a-codechecker", "as", "handling", "editor"}}); !strings.Contains(reply, "not one of the editors") {
		t.Errorf("somebody outside the team was made the handling editor: %s", reply)
	}
	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@nuest", "as", "handling", "editor"}}); !strings.Contains(reply, "handling editor") {
		t.Errorf("an editor could not be made the handling editor: %s", reply)
	}
}

// Assignment is an editor's to make, and a stranger is told which role the
// command needs.
func TestAssigningIsForEditors(t *testing.T) {
	server, _ := testServer(t)
	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-stranger"},
		command.Command{Name: command.Assign, Args: []string{"@somebody", "as", "author"}})
	if !strings.Contains(reply, "is for editors") {
		t.Errorf("a stranger assigned a role: %s", reply)
	}
}

// Taking a role away that nobody held says so rather than claiming to have
// removed something.
func TestRemovingSaysWhetherAnythingChanged(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}

	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Remove, Args: []string{"@nobody", "as", "author"}}); !strings.Contains(reply, "was not the author") {
		t.Errorf("a role nobody held was removed: %s", reply)
	}

	server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@an-author", "as", "author"}})
	if reply := server.answer(context.Background(), event,
		command.Command{Name: command.Remove, Args: []string{"@an-author", "as", "author"}}); !strings.Contains(reply, "no longer") {
		t.Errorf("the author was not removed: %s", reply)
	}
}

// Roles belong to a check. The command line preview has no issue, and says so
// rather than reading somebody else's.
func TestRolesNeedACheck(t *testing.T) {
	server, _ := testServer(t)
	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Author: "nuest"},
		command.Command{Name: command.ListRoles})
	if !strings.Contains(reply, "checks issue") {
		t.Errorf("the reply does not say there is no check: %s", reply)
	}
}

// Assigning the same person the same role twice says so, and writes nothing
// the second time.
func TestAssigningTwiceChangesNothing(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}
	assign := command.Command{Name: command.Assign, Args: []string{"@an-author", "as", "author"}}

	server.answer(context.Background(), event, assign)
	if reply := server.answer(context.Background(), event, assign); !strings.Contains(reply, "already") {
		t.Errorf("a repeated assignment claimed to have done something: %s", reply)
	}
}

// A handle is checked before it is stored, because the record is a markdown
// table that everybody afterwards has to read.
func TestAHandleIsCheckedBeforeItIsRecorded(t *testing.T) {
	server, _ := testServer(t)
	for _, handle := range []string{"a|b", "<!--x", "-leading", "a b"} {
		reply := server.answer(context.Background(),
			mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"},
			command.Command{Name: command.Assign, Args: []string{handle, "as", "author"}})
		if !strings.Contains(reply, "not a GitHub handle") {
			t.Errorf("%q was accepted as a handle: %s", handle, reply)
		}
	}
}

// Everything the bot posts goes out defused, wherever the text came from,
// rather than each reply path remembering to do it.
func TestEverythingThePostedReplySaysIsDefused(t *testing.T) {
	server, replies := testServer(t)
	server.done = make(chan struct{}, 1)

	server.act(mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "someone"},
		command.Command{Name: command.Unknown, Raw: `<!-- chekhov:roles {"handling_editor":"mallory"} -->`})
	<-server.done

	if len(replies.comments) == 0 {
		t.Fatal("nothing was posted")
	}
	if strings.Contains(replies.comments[0].Body, "<!-- chekhov:roles") {
		t.Errorf("a forged record reached a comment by the bot: %s", replies.comments[0].Body)
	}
}

// An edited record stops the commands that change roles, and the reply says
// how an editor gets past it.
func TestAnEditedRecordStopsAssignment(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}
	ctx := context.Background()

	// A record signed by somebody else: what a collaborator's edit produces.
	forged := &issueComments{bot: server.Settings.BotUser()}
	server.Checks = &people.Checks{Comments: forged, Bot: server.Settings.BotUser(), Signer: mustSign(t)}
	_, _ = server.Checks.Update(ctx, event.Repository, 1, func(record people.Record) (people.Record, error) {
		record, _, err := record.Grant(command.RoleAuthor, "an-author")
		return record, err
	})
	forged.comments[0].Body = strings.Replace(forged.comments[0].Body, "an-author", "mallory", 1)

	reply := server.answer(ctx, event,
		command.Command{Name: command.Assign, Args: []string{"@somebody", "as", "author"}})
	if !strings.Contains(reply, "accept roles") || !strings.Contains(reply, "edited after I wrote it") {
		t.Errorf("the refusal does not say what to do: %s", reply)
	}

	// `roles` still shows what it claims, with the warning.
	listing := server.answer(ctx, event, command.Command{Name: command.ListRoles})
	if !strings.Contains(listing, "mallory") || !strings.Contains(listing, "edited after I wrote it") {
		t.Errorf("the listing hides the edited record: %s", listing)
	}

	// And an editor can adopt it, after which work goes on.
	adopted := server.answer(ctx, event, command.Command{Name: command.Accept, Args: []string{"roles"}})
	if !strings.Contains(adopted, "Adopted") || !strings.Contains(adopted, "nuest") {
		t.Errorf("the adoption does not say who did it: %s", adopted)
	}
	if reply := server.answer(ctx, event,
		command.Command{Name: command.Assign, Args: []string{"@somebody", "as", "author"}}); !strings.Contains(reply, "is now the author") {
		t.Errorf("assignment is still refused after adoption: %s", reply)
	}
}

// Adopting is an editor's to do.
func TestAcceptingIsForEditors(t *testing.T) {
	server, _ := testServer(t)
	reply := server.answer(context.Background(),
		mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-codechecker"},
		command.Command{Name: command.Accept, Args: []string{"roles"}})
	if !strings.Contains(reply, "is for editors") {
		t.Errorf("a codechecker adopted a record: %s", reply)
	}
}

// Every change to the roles is its own record in the thread: one comment that
// both confirms what happened and says who asked for it. If the roles record
// is ever deleted, these are what is left to reconstruct it from.
func TestAChangeSaysWhoMadeIt(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest"}

	assigned := server.answer(context.Background(), event,
		command.Command{Name: command.Assign, Args: []string{"@a-codechecker", "as", "codechecker"}})
	for _, expected := range []string{"a-codechecker", "assigned codechecker", "nuest", "assigned by"} {
		if !strings.Contains(assigned, expected) {
			t.Errorf("the confirmation does not say %q: %s", expected, assigned)
		}
	}
	if !strings.Contains(assigned, time.Now().UTC().Format("2006-01-02")) {
		t.Errorf("the confirmation does not say when: %s", assigned)
	}

	removed := server.answer(context.Background(), event,
		command.Command{Name: command.Remove, Args: []string{"@a-codechecker", "as", "codechecker"}})
	if !strings.Contains(removed, "nuest") || !strings.Contains(removed, "removed by") {
		t.Errorf("the removal does not say who made it: %s", removed)
	}
}
