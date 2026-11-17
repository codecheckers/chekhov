package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/github"
)

// The pull request adding a registration's row, kept in step with the
// registration (#49).

// blockedRow is registrationRow with an ORCID whose check digit is wrong,
// which holds the registration up.
var blockedRow = strings.Replace(registrationRow, "0000-0002-1825-0097", "0000-0002-1825-0098", 1)

// ownPullRequest is the bot's own pull request adding the row, as GitHub
// lists it.
var ownPullRequest = github.RowPullRequest{Number: 12, Branch: "codechecker-a-made-up-registrant",
	Body: "Adds `@a-made-up-registrant`, as `@nuest` asked."}

// checkWith is `include check` on the registration with this row, by the
// registrant.
func checkWith(t *testing.T, server *Server, row string) {
	t.Helper()
	eventType, payload := registrationEvent(testLists, row, "@chekhovbot include check", madeUpRegistrar, "registration")
	deliverEvent(t, server, eventType, payload)
}

// commentsOn are the bodies the lists' reply path posted on one issue or
// pull request.
func commentsOn(lists *listWriter, number int) []string {
	lists.mu.Lock()
	defer lists.mu.Unlock()
	var bodies []string
	for _, comment := range lists.comments {
		if comment.Issue == number {
			bodies = append(bodies, comment.Body)
		}
	}
	return bodies
}

// A registration that needs something makes the bot's own pull request a
// draft, said on it and in the answer; asked again, it is said to wait and
// is not turned twice.
func TestABlockedRegistrationMakesItsPullRequestADraft(t *testing.T) {
	server, _, lists := including(t)
	lists.open = ownPullRequest

	checkWith(t, server, blockedRow)
	says(t, lists.last(), "Before you can be listed",
		"#12, the pull request adding your row, is a draft until this is fixed")
	if strings.Join(lists.drafted, ";") != "#12 draft" {
		t.Errorf("turns: %q", lists.drafted)
	}
	on := commentsOn(lists, 12)
	if len(on) != 1 || !strings.Contains(on[0], "I have made this a draft: the registration in #7 needs something") {
		t.Errorf("said on the pull request: %q", on)
	}
	if len(lists.proposed) != 0 {
		t.Errorf("wrote a row that needs something: %+v", lists.proposed)
	}

	checkWith(t, server, blockedRow)
	says(t, lists.last(), "is a draft until this is fixed")
	if len(lists.drafted) != 1 || len(commentsOn(lists, 12)) != 1 {
		t.Errorf("turned again: %q", lists.drafted)
	}
}

// Fixed, the row on the bot's branch is brought up to date - its body, which
// says who asked, kept - and the draft is ready for review again.
func TestAFixedRegistrationMakesItsDraftReadyAgain(t *testing.T) {
	server, _, lists := including(t)
	lists.open = ownPullRequest
	lists.open.Draft = true

	checkWith(t, server, registrationRow)
	says(t, lists.last(), "✔", "#12, the pull request adding your row, has it as your registration has it now, "+
		"and is ready for an editor's review again")
	if strings.Join(lists.drafted, ";") != "#12 ready" {
		t.Errorf("turns: %q", lists.drafted)
	}
	if len(lists.proposed) != 1 || lists.proposed[0].Body != ownPullRequest.Body ||
		lists.proposed[0].Row["ORCID"] != "0000-0002-1825-0097" {
		t.Errorf("proposed: %+v", lists.proposed)
	}
	on := commentsOn(lists, 12)
	if len(on) != 1 || !strings.Contains(on[0], "Ready for review again: the registration in #7") {
		t.Errorf("said on the pull request: %q", on)
	}
}

// Not a draft, an edit to the row reaches the pull request and is said; a
// row the branch has already is said nothing about.
func TestAnEditedRowReachesItsPullRequest(t *testing.T) {
	server, _, lists := including(t)
	lists.open = ownPullRequest
	checkWith(t, server, registrationRow)
	says(t, lists.last(), "#12, the pull request adding your row, now has it as your registration has it.")

	server, _, lists = including(t)
	lists.open, lists.unchanged = ownPullRequest, true
	checkWith(t, server, registrationRow)
	saysNot(t, lists.last(), "#12")
	if len(lists.drafted) != 0 {
		t.Errorf("turned: %q", lists.drafted)
	}
}

// A pull request somebody else opened from the bot's branch is theirs: said
// to wait, never turned nor written to.
func TestAPullRequestSomebodyElseOpenedIsOnlySaidToWait(t *testing.T) {
	server, _, lists := including(t)
	lists.open = github.RowPullRequest{Number: 41, By: "a-made-up-other-person", Branch: "codechecker-a-made-up-registrant"}
	checkWith(t, server, blockedRow)
	says(t, lists.last(), "#41, the pull request adding your row, waits until this is fixed; "+
		"`@a-made-up-other-person` opened it")
	if len(lists.drafted)+len(lists.proposed)+len(commentsOn(lists, 41)) != 0 {
		t.Errorf("touched it: %q %+v", lists.drafted, lists.proposed)
	}
}

// GitHub refusing the turn is said, not hidden.
func TestADraftThatCannotBeMadeIsSaid(t *testing.T) {
	server, _, lists := including(t)
	lists.open, lists.draftErr = ownPullRequest, errors.New("GitHub is having a bad day")
	checkWith(t, server, blockedRow)
	says(t, lists.last(), "✖ I could not make #12, the pull request adding your row, a draft")
}

// `include` on a registration that needs something drafts the pull request
// as the check does; on one that passes, a draft is made ready again.
func TestIncludeKeepsThePullRequestInStepToo(t *testing.T) {
	server, org, lists := including(t)
	org.members[madeUpRegistrar] = true
	lists.open = ownPullRequest
	eventType, payload := registrationEvent(testLists, blockedRow, "@chekhovbot include", "nuest", "registration")
	deliverEvent(t, server, eventType, payload)
	says(t, lists.last(), "I have not included anybody", "#12, the pull request adding your row, is a draft")

	says(t, includeBy(t, server, lists, "nuest"), "#12, the pull request adding your row, has it as your registration has it now, and is ready for an editor's review again.")
	if strings.Join(lists.drafted, ";") != "#12 draft;#12 ready" {
		t.Errorf("turns: %q", lists.drafted)
	}
}

// The sweep checks the registration again each night: a pull request it
// turns is said on the registration with the verdict that turned it, once,
// and a draft is not reminded to the owners - it waits for the registrant.
func TestTheSweepKeepsThePullRequestInStep(t *testing.T) {
	server, org, lists := sweepingLists(t, 15*day, "")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	lists.pending, lists.pendingSince = 12, time.Now().Add(-15*day)
	lists.open.Body = ownPullRequest.Body
	lists.body = "Your data:\n\n```csv\n" + blockedRow + "\n```\n"

	server.nudge(context.Background())
	says(t, lists.last(), "Before you can be listed", "#12, the pull request adding your row, is a draft")
	if strings.Join(lists.drafted, ";") != "#12 draft" {
		t.Errorf("turns: %q", lists.drafted)
	}
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("said again, or reminded the owners of a draft:\n%s", lists.last())
	}

	lists.body = "Your data:\n\n```csv\n" + registrationRow + "\n```\n"
	server.nudge(context.Background())
	says(t, lists.last(), "✔", "is ready for an editor's review again")
	if strings.Join(lists.drafted, ";") != "#12 draft;#12 ready" {
		t.Errorf("turns: %q", lists.drafted)
	}
}

// Before the registrant has joined, the pull request `include` opened is
// kept in step too: it is open whatever the organisation's half is waiting
// for.
func TestTheSweepDraftsThePullRequestBeforeTheyHaveJoined(t *testing.T) {
	server, _, lists := sweepingLists(t, day, "")
	lists.pending, lists.pendingSince = 12, time.Now().Add(-day)
	lists.body = "Your data:\n\n```csv\n" + blockedRow + "\n```\n"
	server.nudge(context.Background())
	says(t, lists.last(), "#12, the pull request adding your row, is a draft")
	if strings.Join(lists.drafted, ";") != "#12 draft" {
		t.Errorf("turns: %q", lists.drafted)
	}
}

// Somebody's commits on the bot's branch: the draft stays a draft, since the
// row on it is not the bot's to vouch for; a row merged meanwhile is not
// reported as a failure.
func TestADraftWithSomebodysCommitsStaysADraft(t *testing.T) {
	server, _, lists := including(t)
	lists.open = ownPullRequest
	lists.open.Draft, lists.refuse = true, github.ErrBranchNotOurs
	checkWith(t, server, registrationRow)
	says(t, lists.last(), "#12, on `codechecker-a-made-up-registrant`, has commits that are not mine")
	if len(lists.drafted) != 0 {
		t.Errorf("turned: %q", lists.drafted)
	}

	server, _, lists = including(t)
	lists.open = ownPullRequest
	lists.open.Draft, lists.refuse = true, github.ErrRowExists
	checkWith(t, server, registrationRow)
	saysNot(t, lists.last(), "#12", "could not")
	if len(lists.drafted) != 0 {
		t.Errorf("turned: %q", lists.drafted)
	}
}

// No pull request open and a registration that needs something: the note
// says what, and does not point at an `include` that would refuse it
// (codecheckers/codecheckers#98).
func TestTheNoteOnAMissingPullRequestSaysWhatIsNeeded(t *testing.T) {
	server, org, lists := sweepingLists(t, 4*day, "")
	org.members[madeUpRegistrar], org.inTeam[madeUpRegistrar] = true, true
	lists.body = "Your data:\n\n```csv\n" + blockedRow + "\n```\n"
	server.nudge(context.Background())
	says(t, lists.last(), "no pull request is open to add the row, and the registration needs this first:", "✖",
		"Please edit the row in the first post of this issue", "Then an editor's `@chekhovbot include` opens the pull request again.")
	saysNot(t, lists.last(), "closed without being merged. `@chekhovbot include` opens it again.")
}
