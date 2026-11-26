package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
)

// Every made-up handle here was checked to belong to nobody.
const (
	stranger = "a-made-up-stranger"
	coauthor = "a-made-up-coauthor"
	member   = "a-made-up-member"
)

// heard is a reply path that lists what it posted, issue by issue, as GitHub
// would - which is how the bot knows whom it has told already - and deletes a
// comment when asked.
type heard struct {
	*organisation
	listErr   error
	deleteErr error
	deleted   []int64
}

func (h *heard) Comments(_ context.Context, _ string, issue int) ([]github.Comment, error) {
	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	var written []github.Comment
	for i, posted := range h.recorder.comments {
		if posted.Issue == issue {
			written = append(written, github.Comment{ID: int64(i + 1), Author: "chekhovbot", Body: posted.Body})
		}
	}
	return written, h.listErr
}

func (h *heard) DeleteComment(_ context.Context, _ string, comment int64) error {
	if h.deleteErr != nil {
		return h.deleteErr
	}
	h.deleted = append(h.deleted, comment)
	return nil
}

func gated(t *testing.T) (*Server, *heard) {
	t.Helper()
	server, replies := organised(t)
	path := &heard{organisation: replies}
	server.Replies = path
	server.Checks.Comments = &threads{bot: server.Settings.BotUser(), by: map[int][]people.Comment{}}
	return server, path
}

// asking is a comment on a check, by somebody, on an issue somebody else opened.
type asking struct {
	by, body  string
	issue     int
	opener    string
	assignees []string
	pull      bool
}

func (a asking) send(t *testing.T, server *Server) {
	t.Helper()
	if a.issue == 0 {
		a.issue = 1
	}
	if a.opener == "" {
		a.opener = "a-made-up-author"
	}
	assignees := []map[string]string{}
	for _, assignee := range a.assignees {
		assignees = append(assignees, map[string]string{"login": assignee})
	}
	issue := map[string]any{"number": a.issue, "user": map[string]string{"login": a.opener}, "assignees": assignees}
	if a.pull {
		issue["pull_request"] = map[string]string{"url": "https://example.invalid/pull"}
	}
	deliverEvent(t, server, "issue_comment", map[string]any{
		"action":     "created",
		"issue":      issue,
		"comment":    map[string]any{"id": 99, "body": a.body, "user": map[string]string{"login": a.by}},
		"repository": map[string]string{"full_name": server.Settings.TargetRepository()},
	})
}

const refusalStart = "I have not run that"

// answeredHello says whether a hello by this comment was answered.
func answeredHello(t *testing.T, server *Server, path *heard, a asking) bool {
	t.Helper()
	before := path.count()
	a.body = "@chekhovbot hello"
	a.send(t, server)
	return path.count() == before+1 && strings.Contains(path.last(), "I am awake")
}

func TestWhoMayAsk(t *testing.T) {
	for name, a := range map[string]asking{
		"an editor":                       {by: "nuest"},
		"a codechecker":                   {by: "a-codechecker"},
		"a member in neither team":        {by: member},
		"whoever opened the issue":        {by: stranger, opener: stranger},
		"an assignee":                     {by: stranger, assignees: []string{"A-Made-Up-Stranger"}},
		"a member, on a pull request":     {by: member, pull: true},
		"an assignee, however it's cased": {by: "A-Made-Up-Stranger", assignees: []string{stranger}},
	} {
		server, path := gated(t)
		path.members[member] = true
		if !answeredHello(t, server, path, a) {
			t.Errorf("%s was not answered: %s", name, path.last())
		}
	}
	for name, a := range map[string]asking{
		"a stranger":                             {by: stranger},
		"an account with no name":                {by: "", opener: ""},
		"whoever opened a pull request":          {by: stranger, opener: stranger, pull: true},
		"an assignee of a pull request":          {by: stranger, assignees: []string{stranger}, pull: true},
		"somebody else's assignee, on its issue": {by: stranger, assignees: []string{coauthor}},
	} {
		server, path := gated(t)
		a.body = "@chekhovbot hello"
		a.send(t, server)
		if !strings.Contains(path.last(), refusalStart) {
			t.Errorf("%s was answered: %s", name, path.last())
		}
	}
}

// What the record of a check makes somebody lets them ask there, and only
// there, and only while the record can be trusted.
func TestTheRecordOfACheckLetsPeopleAsk(t *testing.T) {
	for name, record := range map[string]people.Record{
		"the handling editor":      {HandlingEditor: coauthor},
		"the assigned codechecker": {AssignedCodechecker: coauthor},
		"an author":                {Authors: []string{coauthor}},
		"somebody allowed":         {Allowlist: []string{coauthor}},
	} {
		server, path := gated(t)
		if _, err := server.Checks.Update(context.Background(), server.Settings.TargetRepository(), 1,
			func(people.Record) (people.Record, error) { return record, nil }); err != nil {
			t.Fatal(err)
		}
		if !answeredHello(t, server, path, asking{by: coauthor}) {
			t.Errorf("%s was not answered: %s", name, path.last())
		}
		if answeredHello(t, server, path, asking{by: coauthor, issue: 2}) {
			t.Errorf("%s was answered on another check", name)
		}

		store := server.Checks.Comments.(*threads)
		store.by[1][0].Body += "\nedited by hand\n"
		if answeredHello(t, server, path, asking{by: coauthor}) {
			t.Errorf("%s was answered on an edited record", name)
		}
	}
}

// A membership that cannot be read lets nobody in, and the refusal says so.
func TestAnUnreadableMembershipRefuses(t *testing.T) {
	server, path := gated(t)
	path.membersErr = errors.New("GitHub is having a bad minute")

	asking{by: stranger, body: "@chekhovbot hello"}.send(t, server)

	says(t, path.last(), refusalStart, "asking again later may help")
}

// One refusal for a comment, however much it asks, one for a person on an
// issue, and nothing that notifies anybody.
func TestARefusalIsSaidOnce(t *testing.T) {
	server, path := gated(t)

	asking{by: stranger, body: strings.Repeat("@chekhovbot frobnicate\n", 7)}.send(t, server)
	if path.count() != 1 {
		t.Fatalf("%d posts for one comment: %s", path.count(), path.last())
	}
	says(t, path.last(), refusalStart, "`@chekhovbot allowlist add a-made-up-stranger`")
	if command.Mentions(path.last(), stranger) || command.Mentions(path.last(), "chekhovbot") {
		t.Errorf("the refusal notifies somebody:\n%s", path.last())
	}

	asking{by: stranger, body: "@chekhovbot commands"}.send(t, server)
	if path.count() != 1 {
		t.Errorf("told twice: %s", path.last())
	}
	asking{by: coauthor, body: "@chekhovbot commands"}.send(t, server)
	asking{by: stranger, body: "@chekhovbot commands", issue: 2}.send(t, server)
	if path.count() != 3 {
		t.Errorf("%d posts, want one more for another person and one for another issue", path.count())
	}

	path.listErr = errors.New("no comments today")
	asking{by: stranger, body: "@chekhovbot commands", issue: 3}.send(t, server)
	if path.count() != 3 {
		t.Errorf("told somebody without knowing whether they had been: %s", path.last())
	}
}

// A share link goes whoever posted it, every time, and past the most commands
// that are run.
func TestAShareLinkFromAStrangerIsDeletedAllTheSame(t *testing.T) {
	server, path := gated(t)
	asking{by: stranger, body: "@chekhovbot hello"}.send(t, server)

	asking{by: stranger, body: strings.Repeat("@chekhovbot hello\n", 6) +
		"@chekhovbot check zenodo https://zenodo.org/records/1?token=made-up"}.send(t, server)

	if len(path.deleted) != 1 || path.deleted[0] != 99 {
		t.Fatalf("deleted %v, want the comment with the link", path.deleted)
	}
	says(t, path.last(), refusalStart, "I deleted the comment that asked")
	if strings.Contains(path.last(), "token=") {
		t.Errorf("the refusal quotes the link:\n%s", path.last())
	}
}

// A refusal answers nothing: not a registrant's row, and not the label note.
func TestARefusalIsNotAnAnswer(t *testing.T) {
	refusal := command.TurnedAway(command.Refused{Handle: stranger, Organisation: "codecheckers", OnLists: true})
	row := github.Comment{ID: 1, Author: madeUpRegistrar, Body: "```csv\n" + registrationRow + "\n```"}
	thread := []github.Comment{row, {ID: 2, Author: "chekhovbot", Body: refusal}}

	if _, answered := latestRowAnswered(thread, madeUpRegistrar, "chekhovbot"); answered {
		t.Error("a refusal counted as the answer to the registrant's row")
	}
	if !command.IsTurnedAway(refusal) {
		t.Errorf("not known as a refusal:\n%s", refusal)
	}

	server, path := gated(t)
	note := "a made-up label note"
	path.recorder.Comment(context.Background(), "", 1, "an answer\n\n"+note)
	path.recorder.Comment(context.Background(), "", 1, refusal)
	if !server.justSaid(context.Background(), mention{Repository: server.Settings.TargetRepository(), Issue: 1}, note, nil) {
		t.Error("a refusal in between made the label note due again")
	}
}

// On the lists a registration the bot opened is its registrant's, member or
// not, and nobody else's outside the organisation; and a stranger asking for
// the check of a registration changes nothing.
func TestWhoMayAskOnTheLists(t *testing.T) {
	server, org, lists := opening(t, "")
	delete(org.members, madeUpRegistrar)
	record, err := followup.Render(followup.Record{Kind: followup.KindRegistration, Check: testLists + "#7",
		Subject: madeUpRegistrar, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	lists.thread = []github.Comment{{ID: 1, Author: "chekhovbot", Body: record + "This registration is for"}}

	commentOnLists(t, server, "@chekhovbot include check", stranger, true)
	says(t, lists.last(), refusalStart, "the person a registration is for")
	if strings.Contains(lists.last(), "allowlist") {
		t.Errorf("the lists have no allowlist:\n%s", lists.last())
	}

	commentOnLists(t, server, "@chekhovbot include check", madeUpRegistrar, true)
	says(t, lists.last(), "I found no row")
}

// The type the preview reads through can ask who is in the organisation, for
// the gate, and has no method that writes. Preview itself builds a client for
// GitHub, so it is not run here.
func TestThePreviewsPathAsksAboutMembersAndCannotWrite(t *testing.T) {
	var path Poster = memberAware{}
	if !is[MemberReader](path) {
		t.Error("the preview cannot ask who is in the organisation")
	}
	for name, writes := range map[string]bool{
		"Organisation": is[Organisation](path),
		"Reserving":    is[Reserving](path),
		"Assigner":     is[Assigner](path),
		"Deleting":     is[Deleting](path),
	} {
		if writes {
			t.Errorf("the preview's reply path is a %s, and could write through it", name)
		}
	}
}

// failing is a record store whose comments cannot be listed for a while.
type failing struct {
	*threads
	err error
}

func (f *failing) Comments(ctx context.Context, repository string, issue int) ([]people.Comment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.threads.Comments(ctx, repository, issue)
}

// A refusal made without being able to look is not a verdict: it says so, it
// is itself said once, and it does not use up the one that is.
func TestARefusalThatCouldNotLookSaysSoAndIsNotTheLastWord(t *testing.T) {
	for name, broken := range map[string]func(*heard, *failing, error){
		"the record":     func(_ *heard, store *failing, err error) { store.err = err },
		"the membership": func(path *heard, _ *failing, err error) { path.membersErr = err },
	} {
		server, path := gated(t)
		store := &failing{threads: server.Checks.Comments.(*threads)}
		server.Checks.Comments = store
		again := asking{by: stranger, body: "@chekhovbot hello"}

		broken(path, store, errors.New("GitHub is having a bad minute"))
		again.send(t, server)
		says(t, path.last(), refusalStart, "I could not look up everything", "asking again later may help")
		again.send(t, server)
		if path.count() != 1 {
			t.Errorf("%s unread: told twice that I could not look:\n%s", name, path.last())
		}

		broken(path, store, nil)
		again.send(t, server)
		if path.count() != 2 || strings.Contains(path.last(), "asking again later") {
			t.Errorf("%s readable again: %d posts, the last:\n%s", name, path.count(), path.last())
		}
		again.send(t, server)
		if path.count() != 2 {
			t.Errorf("%s: told a third time:\n%s", name, path.last())
		}
		if !command.IsTurnedAway(path.recorder.comments[0].Body) {
			t.Errorf("%s: not known as a refusal:\n%s", name, path.recorder.comments[0].Body)
		}
	}
}

// An edited record lets nobody in, and the refusal says that rather than
// suggest an addition the record would not take.
func TestARefusalOnAnEditedRecordSaysWhy(t *testing.T) {
	server, path := gated(t)
	if _, err := server.Checks.Update(context.Background(), server.Settings.TargetRepository(), 1,
		func(record people.Record) (people.Record, error) { return record.Allow(coauthor) }); err != nil {
		t.Fatal(err)
	}
	server.Checks.Comments.(*threads).by[1][0].Body += "\nedited by hand\n"

	asking{by: coauthor, body: "@chekhovbot hello"}.send(t, server)

	says(t, path.last(), refusalStart, "was edited", "`@chekhovbot record accept`")
	if strings.Contains(path.last(), "allowlist add") || strings.Contains(path.last(), "asking again later") {
		t.Errorf("the refusal suggests what cannot work:\n%s", path.last())
	}
}
