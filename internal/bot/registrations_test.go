package bot

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// Registrations the bot opens (#49).

// opening is a bot on the lists whose lists' reply path opens and reads
// issues, with the list as given.
func opening(t *testing.T, list string) (*Server, *organisation, *listsThread) {
	t.Helper()
	server, org, _ := sweepingLists(t, day, list)
	lists := server.Lists.(*listsThread)
	lists.thread = nil
	// A member: `include open` is for somebody who has joined.
	org.members[madeUpRegistrar] = true
	return server, org, lists
}

// commentOnLists is a comment on the lists' issue #7, by somebody, on an issue
// the bot opened when botOpened.
func commentOnLists(t *testing.T, server *Server, body, by string, botOpened bool) {
	t.Helper()
	eventType, payload := registrationEvent(testLists, registrationRow, body, by, "registration")
	if botOpened {
		issue := payload["issue"].(map[string]any)
		issue["user"] = map[string]string{"login": "chekhovbot"}
		issue["body"] = command.RegistrationInvite(madeUpRegistrar, "codecheckers.csv")
	}
	if code := deliverEvent(t, server, eventType, payload); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
}

// An editor opens a registration for somebody: labelled, naming them so they
// are notified, asking for the list's columns, and with the record of whom it
// is for in the bot's first comment on it.
func TestIncludeOpenOpensARegistration(t *testing.T) {
	server, _, lists := opening(t, "")
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)

	if len(lists.opened) != 1 {
		t.Fatalf("opened %d issues: %q", len(lists.opened), lists.opened)
	}
	opened := lists.opened[0]
	for _, want := range []string{"Registration as a codechecker: " + madeUpRegistrar, "@" + madeUpRegistrar + ", welcome",
		"`@chekhovbot include check` on its first line", "ecr_until records", "\nregistration"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the issue does not say %q:\n%s", want, opened)
		}
	}
	record, err := followup.Parse(lists.thread[0].Body, testLists+"#21", server.signer())
	if err != nil || record.Kind != followup.KindRegistration || record.Subject != madeUpRegistrar {
		t.Errorf("the record of whom it is for: %+v, %v", record, err)
	}
	says(t, lists.last(), "I have opened "+testLists+"#21 for `@"+madeUpRegistrar+"`")
}

// Never two for one person: one the bot opened, or theirs, open or closed -
// and none for somebody on the list already. Only an editor opens one.
func TestIncludeOpenNeverOpensTwo(t *testing.T) {
	server, _, lists := opening(t, "")
	lists.issues = []github.Issue{{Number: 3, Title: "Registration as a codechecker: A-Made-Up-Registrant",
		Author: "chekhovbot", Closed: true}}
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)
	says(t, lists.last(), "already has a registration, "+testLists+"#3")

	server, _, lists = opening(t, "")
	lists.issues = []github.Issue{{Number: 4, Author: madeUpRegistrar, Labels: []string{"registration"}}}
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)
	says(t, lists.last(), "already has a registration, "+testLists+"#4")

	server, _, lists = opening(t, registrationRow+"\n")
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)
	says(t, lists.last(), "is on `codecheckers.csv` already")

	server, _, lists = opening(t, "")
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "a-codechecker", false)
	says(t, lists.last(), "`@chekhovbot include open` is for")
	if len(lists.opened) != 0 {
		t.Errorf("opened %q", lists.opened)
	}
}

// On a registration the bot opened, the row is the registrant's comment: the
// latest one of theirs that holds one, and nobody else's.
func TestARegistrationTheBotOpenedReadsTheRegistrantsComment(t *testing.T) {
	server, _, lists := opening(t, "")
	record, err := followup.Render(followup.Record{Kind: followup.KindRegistration, Check: testLists + "#7",
		Subject: madeUpRegistrar, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	row := "@chekhovbot include check\n\n```csv\n" + registrationRow + "\n```\n"
	lists.thread = []github.Comment{
		{ID: 1, Author: "chekhovbot", Body: record + "This registration is for"},
		{ID: 2, Author: "a-made-up-other-person", Body: row},
	}
	commentOnLists(t, server, "@chekhovbot include check", "a-made-up-other-person", true)
	says(t, lists.last(), "I found no row", "comment here with `@chekhovbot include check` on its first line")

	lists.thread = append(lists.thread, github.Comment{ID: 3, Author: madeUpRegistrar, Body: row})
	commentOnLists(t, server, row, madeUpRegistrar, true)
	says(t, lists.last(), "has everything the list needs")

	commentOnLists(t, server, "@chekhovbot include", "nuest", true)
	says(t, lists.last(), "Opened the pull request adding the row")
}

// A registration the bot opened, with no row a week on, reminds its
// registrant once; a row ends the waiting.
func TestTheSweepRemindsARegistrantOnceForTheirRow(t *testing.T) {
	server, _, lists := opening(t, "")
	record, err := followup.Render(followup.Record{Kind: followup.KindRegistration, Check: testLists + "#7",
		Subject: madeUpRegistrar, Asked: time.Now().UTC().Add(-8 * day).Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	lists.thread = []github.Comment{{ID: 1, Author: "chekhovbot", Body: record + "This registration is for"}}

	server.nudge(context.Background())
	says(t, lists.last(), "still waiting for your row", "I will not ask again")
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("asked twice:\n%s", lists.last())
	}

	lists.thread = append(lists.thread, github.Comment{ID: 9, Author: madeUpRegistrar,
		Body: "```csv\n" + registrationRow + "\n```"})
	if swept := server.nudge(context.Background()); swept.Outstanding != 0 {
		t.Errorf("a registration with its row is still waiting: %+v", swept)
	}
}

// Somebody an `assign` asked the owners for, joining as a codechecker, is
// asked for their row on the lists.
func TestJoiningAsACodecheckerOpensTheirRegistration(t *testing.T) {
	server, replies := organised(t)
	lists := &listsThread{}
	server.Lists = lists
	// The list, to say they are not on it.
	stubbed, _, _, _ := listsServer(t)
	server.Services, server.Settings.Chekhov.Lists.Files = stubbed.Services, stubbed.Settings.Chekhov.Lists.Files
	asked(t, server, replies, "a-made-up-newcomer", 24*time.Hour)
	replies.members["a-made-up-newcomer"] = true

	server.nudge(context.Background())
	says(t, replies.last(), "has joined", "I have opened "+testLists+"#21 for `@a-made-up-newcomer`")
	if len(lists.opened) != 1 || !strings.Contains(lists.opened[0], "@a-made-up-newcomer, welcome") {
		t.Errorf("opened %q", lists.opened)
	}
}

// Only for somebody who has joined: a mistyped handle must not notify a
// stranger. And without a key to sign whom it is for, none is opened.
func TestIncludeOpenIsForMembersAndNeedsAKey(t *testing.T) {
	server, org, lists := opening(t, "")
	org.members[madeUpRegistrar] = false
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)
	says(t, lists.last(), "is not a member of `codecheckers`, so I have opened no registration")

	server, _, lists = opening(t, "")
	server.Checks.Signer = nil
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)
	says(t, lists.last(), "I cannot sign the record of whom a registration is for")
	if len(lists.opened) != 0 {
		t.Errorf("opened %q", lists.opened)
	}
}

// A registration the bot opened whose record did not get posted is given it
// when somebody asks again, rather than left unreadable for good.
func TestIncludeOpenMendsARegistrationWithoutItsRecord(t *testing.T) {
	server, _, lists := opening(t, "")
	lists.issues = []github.Issue{{Number: 7, Title: "Registration as a codechecker: " + madeUpRegistrar,
		Author: "chekhovbot"}}
	commentOnLists(t, server, "@chekhovbot include open @"+madeUpRegistrar, "nuest", false)
	says(t, lists.last(), "already has a registration")
	record, err := followup.Parse(lists.thread[0].Body, testLists+"#7", server.signer())
	if err != nil || record.Subject != madeUpRegistrar {
		t.Errorf("the record was not mended: %+v, %v", record, err)
	}
	if len(lists.opened) != 0 {
		t.Errorf("opened a second one: %q", lists.opened)
	}
}

// A row posted without the command line is answered by the sweep, once.
func TestTheSweepAnswersARowNobodyAskedAbout(t *testing.T) {
	server, _, lists := opening(t, "")
	record, err := followup.Render(followup.Record{Kind: followup.KindRegistration, Check: testLists + "#7",
		Subject: madeUpRegistrar, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
	if err != nil {
		t.Fatal(err)
	}
	lists.thread = []github.Comment{{ID: 1, Author: "chekhovbot", Body: record + "This registration is for"},
		{ID: 2, Author: madeUpRegistrar, Body: "```csv\n" + registrationRow + "\n```"}}

	server.nudge(context.Background())
	says(t, lists.last(), "has everything the list needs")
	said := lists.count()
	server.nudge(context.Background())
	if lists.count() != said {
		t.Errorf("answered twice:\n%s", lists.last())
	}
}

// Two records naming different people: the bot reads neither.
func TestARegistrationWithTwoSubjectsIsReadForNeither(t *testing.T) {
	server, _, lists := opening(t, "")
	for i, who := range []string{madeUpRegistrar, "a-made-up-other-person"} {
		record, err := followup.Render(followup.Record{Kind: followup.KindRegistration, Check: testLists + "#7",
			Subject: who, Due: who, Asked: time.Now().UTC().Format(time.RFC3339)}, server.signer())
		if err != nil {
			t.Fatal(err)
		}
		lists.thread = append(lists.thread, github.Comment{ID: int64(i + 1), Author: "chekhovbot", Body: record + "for"})
	}
	commentOnLists(t, server, "@chekhovbot include check", madeUpRegistrar, true)
	says(t, lists.last(), "records for two different people")
}
