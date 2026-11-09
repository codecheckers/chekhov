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
		invitations: map[string]github.Invitation{},
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

// Somebody with no invitation: the row is proposed and the owners are asked,
// mentioned because only they can do it. Nobody is put in a team, because the
// write that would do it invites them.
func TestIncludeProposesTheRowAndAsksTheOwners(t *testing.T) {
	server, org, lists := including(t)

	reply := includeBy(t, server, lists, "nuest")
	says(t, reply, "Including `@a-made-up-registrant` as a codechecker",
		"Opened the pull request adding the row to `codecheckers.csv`: #12",
		"I see no invitation for them", "@an-owner", "@another-owner", "as a member")
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
	// pending is the number of the row's pull request still open, 0 for none.
	pending int
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
	return github.RowPullRequest{Number: l.pending}, nil
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
	return github.Issue{Number: issue}, nil
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
func sweepingLists(t *testing.T, ago time.Duration, list string) (*Server, *organisation, *listsThread) {
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
	if record.Kind != followup.KindInclusion || record.Subject != madeUpRegistrar || record.Team != "codecheckers" {
		t.Errorf("the record is %+v", record)
	}
}

// No invitation three days on: the owners are reminded, with a fresh record.
func TestTheSweepRemindsTheOwnersOfAnInclusion(t *testing.T) {
	server, _, lists := sweepingLists(t, 4*day, "")
	swept := server.nudge(context.Background())
	if swept.Acted != 1 || swept.Outstanding != 1 {
		t.Fatalf("swept %+v", swept)
	}
	says(t, lists.last(), "Still waiting, 4 days on", "@an-owner", "I see no invitation")
	record, err := followup.Parse(lists.last(), testLists+"#7", server.signer())
	if err != nil || record.Reminders != 1 {
		t.Errorf("the reminder's record: %+v, %v", record, err)
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
	org.owners = []string{madeUpRegistrar}
	says(t, includeBy(t, server, lists, "nuest"), "The only owner I could ask to review #12 is the registrant")
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
	server.Settings.Chekhov.Teams.Owners = []string{"an-owner"}
	says(t, includeBy(t, server, lists, "nuest"),
		"✔ Asked `@an-owner` to review #12 before it is merged - the people this deployment is configured to ask")
}
