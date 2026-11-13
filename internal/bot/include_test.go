package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// `include`, an editor adding a registered codechecker (#49).

// listWriter is the lists' reply path with the pull request, recording what
// it was asked to propose.
type listWriter struct {
	recorder
	proposed []github.RowProposal
	refuse   error
	// open is a pull request already adding the row, by somebody else
	// when its By is set.
	open    github.RowPullRequest
	openErr error
	// reviewers are the review requests, as "#N a,b", and reviewErr GitHub
	// refusing one.
	reviewers []string
	reviewErr error
}

// Reviewed says a review was asked of somebody already.
func (l *listWriter) Reviewed(_ context.Context, _ string, _ int) (bool, error) {
	return len(l.reviewers) > 0, nil
}

// RequestReview records whom a review was asked of.
func (l *listWriter) RequestReview(_ context.Context, _ string, number int, reviewers []string) error {
	if l.reviewErr != nil {
		return l.reviewErr
	}
	l.reviewers = append(l.reviewers, fmt.Sprintf("#%d %s", number, strings.Join(reviewers, ",")))
	return nil
}

func (l *listWriter) OpenRowPullRequest(_ context.Context, _, _ string) (github.RowPullRequest, error) {
	return l.open, l.openErr
}

func (l *listWriter) ProposeRow(_ context.Context, repository string, proposal github.RowProposal) (github.RowPullRequest, error) {
	if repository != testLists {
		return github.RowPullRequest{}, errors.New("proposed on " + repository)
	}
	if l.refuse != nil {
		return github.RowPullRequest{Branch: "codechecker-a-made-up-registrant"}, l.refuse
	}
	l.proposed = append(l.proposed, proposal)
	return github.RowPullRequest{Number: 12, Updated: len(l.proposed) > 1}, nil
}

// including is a bot on the lists whose register client reads and changes
// the organisation, and whose lists client opens pull requests.
func including(t *testing.T) (*Server, *organisation, *listWriter) {
	t.Helper()
	server, _, _, _ := listsServer(t)
	org := &organisation{
		issueReader: &issueReader{recorder: &recorder{}},
		members:     map[string]bool{"nuest": true, "a-codechecker": true},
		inTeam:      map[string]bool{"a-codechecker": true},
		pending:     map[string]bool{},
		maintains:   map[string]bool{},
		owners:      []string{"an-owner", "another-owner"},
		// The editor running `include` is an owner, as an editor asking for
		// an invitation has to be.
		invitations: map[string]github.Invitation{"nuest": {State: "active", Role: "admin"}},
	}
	server.Replies = org
	// As in organised: the real list of owners is read, not the override.
	server.Settings.Chekhov.Teams.Owners = nil
	lists := &listWriter{}
	server.Lists = lists
	return server, org, lists
}

// includeBy is `include` on the registration, asked by somebody.
func includeBy(t *testing.T, server *Server, lists *listWriter, by string) string {
	t.Helper()
	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include", by, "registration")
	if code := deliverEvent(t, server, eventType, payload); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	return lists.last()
}

func says(t *testing.T, reply string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(reply, w) {
			t.Errorf("the reply does not say %q:\n%s", w, reply)
		}
	}
}

func saysNot(t *testing.T, reply string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(reply, u) {
			t.Errorf("the reply says %q:\n%s", u, reply)
		}
	}
}

// Somebody with no invitation: the row is proposed and the editor who ran
// `include` is asked to see to the invitation - the one person notified. The
// owners, who alone can invite, are named without being notified. Nobody is
// put in a team, because the write that would do it invites them.
func TestIncludeProposesTheRowAndAsksTheEditor(t *testing.T) {
	server, org, lists := including(t)

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "Including `@a-made-up-registrant` as a codechecker",
		"Opened the pull request adding the row to `codecheckers.csv`: #12",
		"I see no invitation for them", "\n@nuest — please invite", "as a member")
	saysNot(t, reply, "an-owner")
	notifies(t, reply, "nuest")
	if len(org.added) != 0 {
		t.Errorf("put somebody outside the organisation in a team: %q", org.added)
	}
	if len(lists.proposed) != 1 {
		t.Fatalf("proposed %d rows", len(lists.proposed))
	}
	proposed := lists.proposed[0]
	if proposed.Row["handle"] != "@a-made-up-registrant" || proposed.Row["ORCID"] != "0000-0002-1825-0097" ||
		proposed.Row["contact"] != "see ORCID page" || proposed.Row["languages"] != "R, Python" {
		t.Errorf("the row is %+v", proposed.Row)
	}
	if !strings.Contains(proposed.Body, "#7") || !strings.Contains(proposed.Body, "`@nuest`") ||
		strings.Contains(proposed.Title, "@") {
		t.Errorf("the pull request is %q / %q", proposed.Title, proposed.Body)
	}
	if strings.Contains(proposed.Body, "example.invalid") || strings.Contains(reply, "example.invalid") {
		t.Error("a contact was quoted")
	}
}

// The owners' half is checked: an invitation out is not asked for again.
func TestIncludeSeesAnInvitationWaiting(t *testing.T) {
	server, org, lists := including(t)
	org.invitations[madeUpRegistrar] = github.Invitation{State: "pending", Role: "member"}

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "has been invited to `codecheckers` and has not accepted yet")
	saysNot(t, reply, "@an-owner and @another-owner —")
	if len(org.added) != 0 {
		t.Errorf("put somebody who has not accepted in a team: %q", org.added)
	}
}

// Once they have joined, the bot does the team; an owner is noted, and the
// role left alone.
func TestIncludePutsAMemberInTheTeam(t *testing.T) {
	server, org, lists := including(t)
	org.members[madeUpRegistrar] = true

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "I have put `@a-made-up-registrant` in `codecheckers/codecheckers`")
	saysNot(t, reply, "@an-owner and @another-owner —", "is an owner")
	if strings.Join(org.added, ",") != madeUpRegistrar {
		t.Errorf("added %q", org.added)
	}

	server, org, lists = including(t)
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	org.invitations[madeUpRegistrar] = github.Invitation{State: "active", Role: "admin"}
	reply = includeBy(t, server, lists, "nuest")
	says(t, reply, "is in `codecheckers/codecheckers` already", "is an owner of `codecheckers`")
	if len(org.added) != 0 {
		t.Errorf("added somebody in the team already: %q", org.added)
	}
}

// What cannot be done is said, each on its own line, and nothing is guessed.
func TestIncludeSaysWhatItCouldNotDo(t *testing.T) {
	server, org, lists := including(t)
	lists.refuse = github.ErrRowExists
	org.membersErr = errors.New("GitHub is having a bad day")

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "`codecheckers.csv` has a row for `@a-made-up-registrant`, so there is no pull request",
		"✖ I could not read whether `@a-made-up-registrant` is in `codecheckers`")
	saysNot(t, reply, "@an-owner")
	if len(org.added) != 0 {
		t.Errorf("acted on an organisation it could not read: %q", org.added)
	}
}

// Only an editor includes anybody, and a registration that is not complete
// is not included by anybody.
func TestIncludeIsForEditorsAndCompleteRegistrations(t *testing.T) {
	server, _, lists := including(t)
	reply := includeBy(t, server, lists, "a-codechecker")
	says(t, reply, "`@chekhovbot include` is for")
	if len(lists.proposed) != 0 {
		t.Errorf("a codechecker had a row proposed: %+v", lists.proposed)
	}

	server, org, lists := including(t)
	stubbed, _, _, stub := listsServer(t)
	server.Services = stubbed.Services
	stub.JSON("/orcid/0000-0002-1825-0097/person", `{"emails": {"email": []}}`)
	server.Settings.Chekhov.Lists.Files = stubbed.Settings.Chekhov.Lists.Files
	reply = includeBy(t, server, lists, "nuest")
	says(t, reply, "I have not included anybody", "didn't find an email", "`@chekhovbot include check`")
	saysNot(t, reply, "@an-owner")
	if len(lists.proposed) != 0 || len(org.added) != 0 {
		t.Errorf("an incomplete registration was acted on: %+v %q", lists.proposed, org.added)
	}
}

// A branch with somebody else's commits is named as the writer named it.
func TestIncludeNamesABranchThatIsNotItsOwn(t *testing.T) {
	server, _, lists := including(t)
	lists.refuse = github.ErrBranchNotOurs
	says(t, includeBy(t, server, lists, "nuest"),
		"- ✖ The branch `codechecker-a-made-up-registrant` has commits that are not mine")
}

// Once the row is merged, `include` still does the organisation's half: the
// listing is done, not a reason to stop.
func TestIncludeAfterTheRowWasMergedStillDoesTheTeam(t *testing.T) {
	server, org, lists := including(t)
	stubbed, _, _, stub := listsServer(t)
	stub.Handle("/lists/codecheckers.csv", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n" +
			registrationRow + "\n"))
	})
	server.Services, server.Settings.Chekhov.Lists.Files = stubbed.Services, stubbed.Settings.Chekhov.Lists.Files
	org.members[madeUpRegistrar] = true

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "has a row for `@a-made-up-registrant`", "I have put `@a-made-up-registrant` in `codecheckers/codecheckers`")
	if len(lists.proposed) != 0 {
		t.Errorf("proposed a row that is merged: %+v", lists.proposed)
	}
}

// `include` with anything after it - a mistyped `include check` - does
// nothing, and notifies nobody.
func TestIncludeWithArgumentsDoesNothing(t *testing.T) {
	server, org, lists := including(t)
	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include chekc", "nuest", "registration")
	deliverEvent(t, server, eventType, payload)
	reply := lists.last()
	says(t, reply, "takes nothing after it", "`@chekhovbot include check`")
	saysNot(t, reply, "@an-owner")
	if len(lists.proposed)+len(org.added) != 0 {
		t.Errorf("acted on a mistyped command: %+v %q", lists.proposed, org.added)
	}
}

// A codecheckers team the bot may not add to is said, and not claimed done.
func TestIncludeDoesNotClaimATeamItCannotChange(t *testing.T) {
	server, org, lists := including(t)
	org.members[madeUpRegistrar] = true
	server.Settings.Chekhov.Teams.Managed = nil

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "I am not set up to put anybody in `codecheckers/codecheckers`")
	saysNot(t, reply, "already.")
	if len(org.added) != 0 {
		t.Errorf("added to an unmanaged team: %q", org.added)
	}
}

// The title carries the registrant's own text, and is defused like the body.
func TestIncludeDefusesTheTitle(t *testing.T) {
	server, _, lists := including(t)
	eventType, payload := registrationEvent(testLists,
		strings.Replace(registrationRow, "Josiah Carberry", "Josiah <!-- Carberry", 1),
		"@chekhovbot include", "nuest", "registration")
	deliverEvent(t, server, eventType, payload)
	if len(lists.proposed) != 1 {
		t.Fatalf("proposed %d rows:\n%s", len(lists.proposed), lists.last())
	}
	if strings.Contains(lists.proposed[0].Title, "<!--") {
		t.Errorf("the title looks like a record: %q", lists.proposed[0].Title)
	}
}

// The lists' reply path as the sweep uses it: it reads the open issues and
// their comments, keeps what the bot posts in the thread, and closes.
type listsThread struct {
	listWriter
	thread []github.Comment
	closed []int
	// pending is the number of the row's pull request still open, 0 for none,
	// and pendingSince when it was opened.
	pending      int
	pendingSince time.Time
	// body is the registration's first post, by the registrant.
	body string
	// issues are the lists' issues, open and closed, and opened the ones
	// the bot opened here, as "title\nbody".
	issues []github.Issue
	opened []string
}

func (l *listsThread) LabelledIssues(_ context.Context, _, _ string) ([]github.Issue, error) {
	return l.issues, nil
}

func (l *listsThread) CreateIssue(_ context.Context, _, title, body string, labels []string) (int, error) {
	l.opened = append(l.opened, title+"\n"+body+"\n"+strings.Join(labels, ","))
	l.issues = append(l.issues, github.Issue{Number: 20 + len(l.opened), Title: title, Author: "chekhovbot", Labels: labels})
	return 20 + len(l.opened), nil
}

func (l *listsThread) OpenRowPullRequest(_ context.Context, _, _ string) (github.RowPullRequest, error) {
	return github.RowPullRequest{Number: l.pending, Created: l.pendingSince}, nil
}

func (l *listsThread) Comment(ctx context.Context, repository string, issue int, body string) (int64, error) {
	id, err := l.listWriter.Comment(ctx, repository, issue, body)
	l.thread = append(l.thread, github.Comment{ID: id, Author: "chekhovbot", Body: body, Created: time.Now()})
	return id, err
}

func (l *listsThread) Comments(_ context.Context, _ string, _ int) ([]github.Comment, error) {
	return l.thread, nil
}

func (l *listsThread) Issue(_ context.Context, _ string, issue int) (github.Issue, error) {
	return github.Issue{Number: issue, Author: madeUpRegistrar, Body: l.body}, nil
}

func (l *listsThread) OpenIssues(_ context.Context, _ string) ([]github.Issue, error) {
	return []github.Issue{{Number: 7}}, nil
}

func (l *listsThread) CloseIssue(_ context.Context, _ string, issue int) error {
	l.closed = append(l.closed, issue)
	return nil
}

// sweepingLists is a bot whose lists hold one registration, #7, with an
// `include` left on it however long ago, and the list as given.
func sweepingLists(t *testing.T, ago time.Duration, list string, by ...string) (*Server, *organisation, *listsThread) {
	t.Helper()
	server, org, _ := including(t)
	lists := &listsThread{}
	server.Lists = lists
	stubbed, _, _, stub := listsServer(t)
	stub.Handle("/lists/codecheckers.csv", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("name,handle,ORCID,contact,fields,languages,ecr_until,ecr_checked,fediverse\n" + list))
	})
	server.Services, server.Settings.Chekhov.Lists.Files = stubbed.Services, stubbed.Settings.Chekhov.Lists.Files
	block, err := followup.Render(followup.Record{
		Kind: followup.KindInclusion, Check: testLists + "#7", Subject: madeUpRegistrar,
		Team: "codecheckers", Asked: time.Now().UTC().Add(-ago).Format(time.RFC3339),
		By: strings.Join(by, ""),
	}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	lists.thread = append(lists.thread, github.Comment{ID: 1, Author: "chekhovbot", Body: block + "Including"})
	return server, org, lists
}

// `include` leaves the record the sweep follows it up from.
func TestIncludeLeavesARecordForTheSweep(t *testing.T) {
	server, _, lists := including(t)
	reply := includeBy(t, server, lists, "nuest")
	record, err := followup.Parse(reply, testLists+"#7", server.signer())
	if err != nil {
		t.Fatalf("no record: %v\n%s", err, reply)
	}
	if record.Kind != followup.KindInclusion || record.Subject != madeUpRegistrar || record.Team != "codecheckers" ||
		record.By != "nuest" {
		t.Errorf("the record is %+v", record)
	}
}

// An editor who is not an owner may not run `include` for somebody with no
// invitation, since only an owner can send one: refused, with nothing done -
// no pull request, no record - and nobody notified.
func TestIncludeByAnEditorWhoIsNotAnOwnerIsRefused(t *testing.T) {
	server, org, lists := including(t)
	org.invitations["nuest"] = github.Invitation{State: "active", Role: "member"}
	// Named in teams.owners is not being an owner: that list says only whom
	// a development deployment notifies.
	server.Settings.Chekhov.Teams.Owners = []string{"nuest"}

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "`@chekhovbot include` is for an owner of `codecheckers` while `@a-made-up-registrant` "+
		"is not in the organisation and has no invitation", "I have done nothing")
	notifies(t, reply)
	if len(lists.proposed) != 0 || len(lists.reviewers) != 0 {
		t.Errorf("did half of it: %+v %q", lists.proposed, lists.reviewers)
	}
	if _, err := followup.Parse(reply, testLists+"#7", server.signer()); err == nil {
		t.Errorf("left a record:\n%s", reply)
	}
}

// An editor whose role cannot be read is refused as well, and told that
// asking again may do: whether they are an owner cannot be told.
func TestIncludeIsRefusedWhenTheEditorsRoleCannotBeRead(t *testing.T) {
	server, org, lists := including(t)
	org.membershipErr = map[string]error{"nuest": errors.New("GitHub answered 403")}

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "I have done nothing", "I could not read whether you are an owner",
		"`@chekhovbot include` asks again")
	if len(lists.proposed) != 0 {
		t.Errorf("proposed a row: %+v", lists.proposed)
	}
}

// Somebody invited already needs no owner: any editor may go on.
func TestIncludeByAnEditorWhoIsNotAnOwnerForSomebodyInvited(t *testing.T) {
	server, org, lists := including(t)
	org.invitations["nuest"] = github.Invitation{State: "active", Role: "member"}
	org.invitations[madeUpRegistrar] = github.Invitation{State: "pending", Role: "member"}

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "Opened the pull request adding the row", "has been invited to `codecheckers`")
	notifies(t, reply)
}

// No invitation three days on: the editor who ran `include` is reminded, and
// the record they are reminded from keeps who they are.
func TestTheSweepRemindsTheEditorOfAnInclusion(t *testing.T) {
	server, _, lists := sweepingLists(t, 4*day, "", "nuest")
	swept := server.nudge(context.Background())
	if swept.Acted != 1 || swept.Outstanding != 1 {
		t.Fatalf("swept %+v", swept)
	}
	says(t, lists.last(), "Still waiting, 4 days on", "@nuest — please invite", "I see no invitation")
	notifies(t, lists.last(), "nuest")
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || record.Reminders != 1 || record.By != "nuest" {
		t.Errorf("the reminder's record: %+v, %v", record, err)
	}
}

// A record from before it said who ran `include` notifies nobody: the owners
// are named, not mentioned.
func TestTheSweepRemindsNobodyFromAnOldInclusionRecord(t *testing.T) {
	server, _, lists := sweepingLists(t, 4*day, "")
	server.nudge(context.Background())
	says(t, lists.last(), "Still waiting, 4 days on",
		"An owner has to invite `@a-made-up-registrant` to the organisation, as a member: "+
			"`@an-owner`, `@another-owner`.")
	notifies(t, lists.last())
}

// notifies fails unless the handles a reply notifies are exactly these.
func notifies(t *testing.T, reply string, want ...string) {
	t.Helper()
	got := strings.Join(notified(reply), " ")
	if wanted := strings.Join(want, " "); wanted != "" {
		wanted = "@" + strings.ReplaceAll(wanted, " ", " @")
		if got != wanted {
			t.Errorf("the reply notifies %q, want %q:\n%s", got, wanted, reply)
		}
	} else if got != "" {
		t.Errorf("the reply notifies %q, want nobody:\n%s", got, reply)
	}
}

// An invitation waiting a week is the registrant's to accept: they are told
// once, on their own issue, and the owners are not asked again.
func TestTheSweepTellsTheRegistrantOnceAboutTheInvitation(t *testing.T) {
	server, org, lists := sweepingLists(t, 8*day, "")
	org.invitations[madeUpRegistrar] = github.Invitation{State: "pending", Role: "member"}

	server.nudge(context.Background())
	says(t, lists.last(), "waiting for you", "https://github.com/orgs/codecheckers/invitation")
	saysNot(t, lists.last(), "@an-owner", "@"+madeUpRegistrar)

	told := lists.count()
	server.nudge(context.Background())
	if lists.count() != told {
		t.Errorf("told twice:\n%s", lists.last())
	}
}

// Joined but the row not merged: the bot does the team, and waits.
func TestTheSweepPutsAJoinedRegistrantInTheTeam(t *testing.T) {
	server, org, lists := sweepingLists(t, day, "")
	org.members[madeUpRegistrar] = true

	swept := server.nudge(context.Background())
	if swept.Outstanding != 1 || strings.Join(org.added, ",") != madeUpRegistrar {
		t.Fatalf("swept %+v, added %q", swept, org.added)
	}
	says(t, lists.last(), "I have put `@a-made-up-registrant` in `codecheckers/codecheckers`", "is what is left")
	if len(lists.closed) != 0 {
		t.Error("closed a registration whose row is not merged")
	}

	// In the team and still not listed: nothing to say.
	org.inTeam[madeUpRegistrar] = true
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("said something with nothing done:\n%s", lists.last())
	}
}

// In the organisation, in the team, on the list: the welcome, once, and the
// issue closed.
func TestTheSweepWelcomesAndClosesOnceAllHolds(t *testing.T) {
	server, org, lists := sweepingLists(t, day, registrationRow+"\n")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true

	swept := server.nudge(context.Background())
	if swept.Acted != 1 || swept.Outstanding != 0 {
		t.Fatalf("swept %+v", swept)
	}
	says(t, lists.last(), "Welcome! :heart:", "https://github.com/orgs/codecheckers/teams/codecheckers",
		"https://github.com/"+testLists+"/blob/HEAD/codecheckers.csv", "community CODECHECK")
	if fmt.Sprint(lists.closed) != "[7]" {
		t.Errorf("closed %v", lists.closed)
	}
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || !record.Settled {
		t.Errorf("the welcome's record: %+v, %v", record, err)
	}

	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("welcomed twice:\n%s", lists.last())
	}
}

// A team the registrant could not be put in - an invitation to it not
// accepted - is said every three days at most, not every night.
func TestTheSweepSaysATeamProblemEveryThreeDaysAtMost(t *testing.T) {
	server, org, lists := sweepingLists(t, day, "")
	org.members[madeUpRegistrar], org.pending[madeUpRegistrar] = true, true
	before := lists.count()
	server.nudge(context.Background())
	if lists.count() != before {
		t.Errorf("said a day-old team problem:\n%s", lists.last())
	}

	server, org, lists = sweepingLists(t, 4*day, "")
	org.members[madeUpRegistrar], org.pending[madeUpRegistrar] = true, true
	server.nudge(context.Background())
	says(t, lists.last(), "has been invited to `codecheckers/codecheckers` and has not accepted yet")
	if _, err := followup.Parse(lists.last(), testLists+"#7", server.signer()); err != nil {
		t.Errorf("the note carries no record to measure the next from: %v", err)
	}
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("said it again the same night:\n%s", lists.last())
	}
}

// In the team, not on the list, and no pull request open to put them there:
// said, every three days at most, rather than waited on in silence. A pull
// request still open is an editor's to merge, and nothing is said.
func TestTheSweepSaysWhenNoPullRequestWillAddTheRow(t *testing.T) {
	server, org, lists := sweepingLists(t, 4*day, "")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	lists.pending = 12
	before := lists.count()
	server.nudge(context.Background())
	if lists.count() != before {
		t.Errorf("said something with the pull request open:\n%s", lists.last())
	}

	lists.pending = 0
	server.nudge(context.Background())
	says(t, lists.last(), "no pull request is open to add the row", "`@chekhovbot include` opens it again")
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || record.Reminders != 1 {
		t.Errorf("the note's record: %+v, %v", record, err)
	}
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("said it again the same night:\n%s", lists.last())
	}
}

// A pull request somebody opened by hand already adds the row: it is the one
// to merge, and no second one is opened beside it. The organisation's half
// goes on as ever.
func TestIncludeLeavesAPullRequestOpenedByHandAlone(t *testing.T) {
	server, org, lists := including(t)
	org.members[madeUpRegistrar] = true
	lists.open = github.RowPullRequest{Number: 41, By: "a-made-up-other-person", Branch: "add-josiah"}

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "#41, by `@a-made-up-other-person`, already adds the row", "I have put `@a-made-up-registrant`")
	if len(lists.proposed) != 0 {
		t.Errorf("opened a second pull request: %+v", lists.proposed)
	}
}

// The bot's own pull request open is brought up to date, as asking again
// always did; with somebody's commits on its branch, it is theirs, and said
// to be on its way rather than refused.
func TestIncludeWithItsOwnPullRequestOpen(t *testing.T) {
	server, _, lists := including(t)
	lists.open = github.RowPullRequest{Number: 12, Branch: "codechecker-a-made-up-registrant"}
	includeBy(t, server, lists, "nuest")
	if len(lists.proposed) != 1 {
		t.Errorf("did not bring its own pull request up to date: %+v", lists.proposed)
	}

	server, _, lists = including(t)
	lists.open = github.RowPullRequest{Number: 12, Branch: "codechecker-a-made-up-registrant"}
	lists.refuse = github.ErrBranchNotOurs
	says(t, includeBy(t, server, lists, "nuest"),
		"#12, on `codechecker-a-made-up-registrant`, has commits that are not mine, so I have left it alone")
}

// Not knowing whether a pull request already adds the row is no reason to
// open a second one beside it.
func TestIncludeOpensNoPullRequestWhenItCannotTellWhetherOneIsOpen(t *testing.T) {
	server, _, lists := including(t)
	lists.openErr = errors.New("GitHub is having a bad day")
	says(t, includeBy(t, server, lists, "nuest"), "✖ I could not tell whether a pull request already adds the row")
	if len(lists.proposed) != 0 {
		t.Errorf("opened a pull request blind: %+v", lists.proposed)
	}
}

// A pull request the bot opened is reviewed by the owners - never by the
// registrant, who cannot confirm their own row - and asked for once: one
// brought up to date is not asked again.
func TestIncludeAsksTheOwnersToReviewThePullRequest(t *testing.T) {
	server, org, lists := including(t)
	server.Settings.Chekhov.Lists.Reviewers = []string{"owners"}
	org.owners = []string{"an-owner", madeUpRegistrar}
	says(t, includeBy(t, server, lists, "nuest"), "✔ Asked `@an-owner` to review #12 before it is merged")
	if strings.Join(lists.reviewers, ";") != "#12 an-owner" {
		t.Errorf("reviews asked: %q", lists.reviewers)
	}

	includeBy(t, server, lists, "nuest")
	if len(lists.reviewers) != 1 {
		t.Errorf("asked again for a pull request brought up to date: %q", lists.reviewers)
	}

	server, org, lists = including(t)
	server.Settings.Chekhov.Lists.Reviewers = []string{"owners"}
	org.owners = []string{madeUpRegistrar}
	says(t, includeBy(t, server, lists, "nuest"), "The only one I could ask to review #12 is the registrant")
	if len(lists.reviewers) != 0 {
		t.Errorf("asked the registrant: %q", lists.reviewers)
	}
}

// Somebody in the institutional team, or on another list, is warned about
// and included all the same: being in more than one group is possible.
func TestIncludeWarnsAboutTheInstitutionalTeamAndOtherLists(t *testing.T) {
	server, _, lists := including(t)
	server.Teams.Reader = teamList{
		server.Settings.EditorsTeam():       {"nuest"},
		server.Settings.CodecheckersTeam():  {"nuest"},
		server.Settings.InstitutionalTeam(): {madeUpRegistrar},
	}
	server.Teams.Refresh(context.Background())
	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "- ⚠ You are in `codecheckers/institutional-codecheckers` already",
		"Opened the pull request adding the row")
	if len(lists.proposed) != 1 {
		t.Errorf("a warning held the row up: %+v", lists.proposed)
	}
}

// A review request that failed is asked again on the next `include`, and the
// configured list is said to be the configured one.
func TestIncludeAsksAgainForAReviewThatFailed(t *testing.T) {
	server, _, lists := including(t)
	lists.reviewErr = errors.New("GitHub is having a bad day")
	says(t, includeBy(t, server, lists, "nuest"), "✖ I could not ask for a review")

	lists.reviewErr = nil
	server.Settings.Chekhov.Lists.Reviewers = []string{"owners"}
	server.Settings.Chekhov.Teams.Owners = []string{"an-owner"}
	says(t, includeBy(t, server, lists, "nuest"),
		"✔ Asked `@an-owner` to review #12 before it is merged - the people this deployment is configured to ask")
}

// A complete registration's reply leaves a record; two days on with nobody
// having run `include`, the editors are told once - and not at all once an
// `include` has left its record, once a pull request adds the row, or once the
// row was edited incomplete; nor where the queue nudge is off.
func TestTheSweepTellsTheEditorsARegistrationIsReady(t *testing.T) {
	ready := func(t *testing.T, server *Server) string {
		block, err := followup.Render(followup.Record{Kind: followup.KindReady, Check: testLists + "#7",
			Subject: madeUpRegistrar, Asked: time.Now().UTC().Add(-3 * day).Format(time.RFC3339)}, server.signer())
		if err != nil {
			t.Fatal(err)
		}
		return block + "has everything"
	}
	complete := "Your data:\n\n```csv\n" + registrationRow + "\n```\n"
	setUp := func(t *testing.T, body string) (*Server, *listsThread) {
		server, _, lists := sweepingLists(t, day, "")
		server.Settings.Chekhov.Nudges.NeedsCodecheckerDays = 7
		lists.body = body
		lists.thread = []github.Comment{{ID: 1, Author: "chekhovbot", Body: ready(t, server)}}
		return server, lists
	}
	toldEditors := func(lists *listsThread, from int) bool {
		for _, comment := range lists.comments[from:] {
			if strings.Contains(comment.Body, "nobody has included it") {
				return true
			}
		}
		return false
	}

	server, lists := setUp(t, complete)
	server.nudge(context.Background())
	says(t, lists.last(), "@codecheckers/editors, this registration of `@a-made-up-registrant` has had everything",
		"`@chekhovbot include` opens the pull request")
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("told the editors twice:\n%s", lists.last())
	}

	for name, change := range map[string]func(*Server, *listsThread){
		"an include on it":    func(server *Server, l *listsThread) { l.thread = append(l.thread, sweepingRecord(t, server)) },
		"a pull request open": func(_ *Server, l *listsThread) { l.pending, l.pendingSince = 12, time.Now() },
		"edited incomplete":   func(_ *Server, l *listsThread) { l.body = "Your data:\n\n```csv\nJust a name\n```\n" },
		"the queue nudge off": func(server *Server, _ *listsThread) { server.Settings.Chekhov.Nudges.NeedsCodecheckerDays = 0 },
	} {
		server, lists := setUp(t, complete)
		change(server, lists)
		server.nudge(context.Background())
		if toldEditors(lists, 0) {
			t.Errorf("%s: told the editors", name)
		}
	}
}

// sweepingRecord is an `include`'s record on the lists' issue #7.
func sweepingRecord(t *testing.T, server *Server) github.Comment {
	t.Helper()
	block, err := followup.Render(followup.Record{Kind: followup.KindInclusion, Check: testLists + "#7",
		Subject: madeUpRegistrar, Team: "codecheckers", Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	return github.Comment{ID: 5, Author: "chekhovbot", Body: block + "Including"}
}

// The reply that finds a registration complete leaves the record.
func TestACompleteRegistrationLeavesAReadyRecord(t *testing.T) {
	server, _, lists, _ := listsServer(t)
	eventType, payload := registrationEvent(testLists, registrationRow, "", "", "registration")
	deliverEvent(t, server, eventType, payload)
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || record.Kind != followup.KindReady || record.Subject != madeUpRegistrar {
		t.Errorf("the ready record: %+v, %v", record, err)
	}
}

// The pull request open and unmerged a week on: the owners are reminded,
// with a fresh record; less than a week, nothing.
func TestTheSweepRemindsTheOwnersOfAWaitingPullRequest(t *testing.T) {
	server, org, lists := sweepingLists(t, 8*day, "")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	lists.pending, lists.pendingSince = 12, time.Now().Add(-4*day)
	before := lists.count()
	server.nudge(context.Background())
	if lists.count() != before {
		t.Errorf("reminded about a pull request four days old:\n%s", lists.last())
	}

	server, org, lists = sweepingLists(t, 8*day, "")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	lists.pending, lists.pendingSince = 12, time.Now().Add(-15*day)
	server.nudge(context.Background())
	// Its own age, not the record's.
	says(t, lists.last(), "@an-owner and @another-owner, #12 has waited 15 days to be reviewed and merged")
	// A fresh record, so that the next reminder is a week from this one.
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || record.Older(day, time.Now()) {
		t.Errorf("the reminder's record: %+v, %v", record, err)
	}
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("reminded twice in a night:\n%s", lists.last())
	}
}

// Checking again after the editors were told leaves a record that says they
// were: a re-check does not tell them a second time.
func TestARecheckKeepsTheEditorsTold(t *testing.T) {
	server, _, _, _ := listsServer(t)
	lists := &listsThread{}
	server.Lists = lists
	told, err := followup.Render(followup.Record{Kind: followup.KindReady, Check: testLists + "#7",
		Subject: madeUpRegistrar, Told: true, Settled: true, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	lists.thread = []github.Comment{{ID: 1, Author: "chekhovbot", Body: told + "told"}}
	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include check", madeUpRegistrar, "registration")
	deliverEvent(t, server, eventType, payload)
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || !record.Told || !record.Settled {
		t.Errorf("the re-check's record forgot the editors were told: %+v, %v", record, err)
	}
}

// By default the editor who ran `include` reviews it; the settings may name
// accounts instead, or the editors team - and never the registrant, whoever
// is named.
func TestIncludeAsksTheReviewersTheSettingsName(t *testing.T) {
	server, _, lists := including(t)
	says(t, includeBy(t, server, lists, "nuest"), "✔ Asked `@nuest` to review #12 before it is merged.")

	server, _, lists = including(t)
	server.Settings.Chekhov.Lists.Reviewers = []string{"@a-made-up-community-manager", madeUpRegistrar}
	says(t, includeBy(t, server, lists, "nuest"), "✔ Asked `@a-made-up-community-manager` to review #12")
	if strings.Join(lists.reviewers, ";") != "#12 a-made-up-community-manager" {
		t.Errorf("reviews asked: %q", lists.reviewers)
	}

	server, _, lists = including(t)
	server.Settings.Chekhov.Lists.Reviewers = []string{"editors"}
	server.Teams.Reader = teamList{server.Settings.EditorsTeam(): {"nuest", "a-codechecker"},
		server.Settings.CodecheckersTeam(): {"nuest"}}
	server.Teams.Refresh(context.Background())
	includeBy(t, server, lists, "nuest")
	if strings.Join(lists.reviewers, ";") != "#12 a-codechecker,nuest" {
		t.Errorf("reviews asked of the editors: %q", lists.reviewers)
	}

	// The editor including themselves asks nobody.
	server, _, lists = including(t)
	eventType, payload := registrationEvent(testLists, strings.Replace(registrationRow, "@"+madeUpRegistrar, "@nuest", 1),
		"@chekhovbot include", "nuest", "registration")
	payload["issue"].(map[string]any)["user"] = map[string]string{"login": "nuest"}
	deliverEvent(t, server, eventType, payload)
	says(t, lists.last(), "The only one I could ask to review #12 is the registrant")
}

// `include` asked again after the merge, the registrant in the team: the
// welcome now, settled, and the issue closed - not a night's wait for the
// sweep.
func TestIncludeAfterTheMergeWelcomesAtOnce(t *testing.T) {
	server, org, lists := sweepingLists(t, day, registrationRow+"\n")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	lists.thread = nil

	eventType, payload := registrationEvent(testLists, registrationRow, "@chekhovbot include", "nuest", "registration")
	deliverEvent(t, server, eventType, payload)
	reply := lists.last()
	says(t, reply, "has a row for `@a-made-up-registrant`", "is in `codecheckers/codecheckers` already",
		"Welcome! :heart:")
	if fmt.Sprint(lists.closed) != "[7]" {
		t.Errorf("closed %v", lists.closed)
	}
	record, err := followup.Parse(reply, testLists+"#7", server.signer())
	if err != nil || record.Kind != followup.KindInclusion || !record.Settled {
		t.Errorf("the record: %+v, %v", record, err)
	}
	// And the sweep says nothing more.
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("welcomed twice:\n%s", lists.last())
	}
}
