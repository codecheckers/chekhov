package command

import (
	"strings"
	"testing"
)

// help and commands are the same command, and are not allowed to drift into
// two code paths.
func TestAliasesResolveToOneCommand(t *testing.T) {
	for _, word := range []string{"commands", "help", "HELP"} {
		parsed, addressed := Parse(Bot + " " + word)
		if !addressed || parsed.Name != Commands {
			t.Errorf("%q: command = %q, addressed = %v", word, parsed.Name, addressed)
		}
	}
}

func TestHelloIsACommand(t *testing.T) {
	parsed, addressed := Parse(Bot + " hello")
	if !addressed || parsed.Name != Hello {
		t.Errorf("command = %q, addressed = %v", parsed.Name, addressed)
	}
}

// The listing is generated from the registry, so a command added without a
// summary or a usage line shows up as a hole in the reply.
func TestEveryCommandIsDescribed(t *testing.T) {
	for _, definition := range Definitions() {
		if definition.Summary == "" {
			t.Errorf("%s has no summary, so the listing would show a blank line", definition.Name)
		}
		if definition.Usage == "" {
			t.Errorf("%s has no usage, so the listing would not say how to write it", definition.Name)
		}
		if definition.Group == "" {
			t.Errorf("%s is in no group, so the listing would drop it", definition.Name)
		}
		// A command whose role was forgotten is refused to everybody, editors
		// included, and refused with "is for ." Failing closed is right; doing
		// it silently is not.
		if definition.Role == "" {
			t.Errorf("%s names no role, so nobody could run it and the refusal would name nothing",
				definition.Name)
		}
	}
}

func TestListingShowsEveryVisibleCommand(t *testing.T) {
	for _, role := range []Role{RoleAnyone, RoleEditor} {
		listing := Listing(Roles{role})
		for _, definition := range Definitions() {
			mentioned := strings.Contains(listing, escapePipes(definition.Usage))
			visible := !definition.Hidden && definition.Permits(Roles{role})
			if !visible && mentioned {
				t.Errorf("%s is listed for %s but should not be", definition.Name, role)
			}
			if visible && !mentioned {
				t.Errorf("%s is not in the listing for %s", definition.Name, role)
			}
		}
	}
}

// announce posts in the project's name, so only an editor is told about it.
func TestAnnounceIsForEditors(t *testing.T) {
	if strings.Contains(Listing(Roles{}), "announce") {
		t.Error("announce is listed for everyone")
	}
	if !strings.Contains(Listing(Roles{RoleEditor}), "announce <certificate> [confirm]") {
		t.Error("announce is not listed for an editor")
	}
}

// A usage line with alternatives in it must not break the table it is listed
// in: GitHub reads a pipe as a cell boundary even inside a code span.
func TestListingTableIsNotBrokenByUsageLines(t *testing.T) {
	for _, line := range strings.Split(Listing(Roles{}), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		if cells := strings.Count(line, "|") - strings.Count(line, "\\|"); cells != 3 {
			t.Errorf("this row has %d cell boundaries, want 3: %s", cells, line)
		}
	}
}

// A command someone may not run is absent, not shown and refused.
func TestEditorCommandsAreHiddenFromEveryoneElse(t *testing.T) {
	editorOnly := Definition{
		Name: "promote", Summary: "Promote the check",
		Usage: "promote", Group: GroupValidation, Role: RoleEditor,
	}
	registry = append(registry, editorOnly)
	t.Cleanup(func() { registry = registry[:len(registry)-1] })

	if names(Visible(Roles{})) == names(Visible(Roles{RoleEditor})) {
		t.Fatal("an editor and everyone else see the same commands")
	}
	if strings.Contains(Listing(Roles{}), "promote") {
		t.Error("an editor-only command is listed for everyone")
	}
	if !strings.Contains(Listing(Roles{RoleEditor}), "promote") {
		t.Error("an editor is not shown the editor-only command")
	}
}

func names(definitions []Definition) string {
	var out []string
	for _, definition := range definitions {
		out = append(out, string(definition.Name))
	}
	return strings.Join(out, ",")
}

// A near miss is a typo worth guessing at; a sentence is not.
func TestSuggestionsOnlyForNearMisses(t *testing.T) {
	cases := []struct {
		word string
		want Name
		ok   bool
	}{
		{"chck", Check, true},
		{"helo", Hello, true},
		{"commnds", Commands, true},
		{"polish", "", false},
		{"", "", false},
		{"check", "", false}, // an exact match is not a suggestion
	}
	for _, testCase := range cases {
		got, ok := Suggest(testCase.word)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("Suggest(%q) = %q, %v; want %q, %v",
				testCase.word, got, ok, testCase.want, testCase.ok)
		}
	}
}

func TestUnknownReplyQuotesAndPointsAtTheListing(t *testing.T) {
	parsed, _ := Parse(Bot + " polish the certificate")
	reply := UnknownReply(parsed)
	if !strings.Contains(reply, "polish the certificate") {
		t.Error("the reply does not quote what was written")
	}
	if !strings.Contains(reply, "commands") {
		t.Error("the reply does not point at the command listing")
	}
	if strings.Contains(reply, "Did you mean") {
		t.Error("the reply guesses at a command that was not nearly written")
	}
}

func TestHelloReplyNamesTheDeployment(t *testing.T) {
	deployment := Deployment{Version: "0.1.0", Register: "codecheckers/testing-dev-register", Environment: "development"}
	reply := deployment.HelloReply()
	for _, want := range []string{"0.1.0", "codecheckers/testing-dev-register"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q: %s", want, reply)
		}
	}
}

// A production deployment does not describe itself: the footer is what a
// reader sees on every comment, and it is a fingerprint of the machine rather
// than an answer to their question.
func TestProductionRepliesCarryNoFooter(t *testing.T) {
	production := Deployment{Version: "1.2.0", Register: "codecheckers/register", Bot: "chekhovbot", Environment: "production"}

	if signature := production.Signature(); signature != "" {
		t.Errorf("a production reply is signed %q", signature)
	}
	hello := production.HelloReply()
	if strings.Contains(hello, "1.2.0") {
		t.Errorf("the production greeting says which build answered: %s", hello)
	}
	if !strings.Contains(hello, "codecheckers/register") {
		t.Error("the greeting must still say which register it works on")
	}

	// An environment nobody taught it about is treated as production: a
	// deployment says less than it could rather than more than it should.
	unknown := production
	unknown.Environment = "staging"
	if unknown.Signature() != "" {
		t.Error("an unknown environment discloses the build")
	}

	development := production
	development.Environment = "development"
	if !strings.Contains(development.Signature(), "1.2.0") {
		t.Error("a development reply says which build answered")
	}
	if !strings.Contains(development.HelloReply(), "1.2.0") {
		t.Error("a development greeting says which build answered")
	}
}

// version says which build answered and which register it works on: a
// development deployment must not be mistakable for the real one.
func TestVersionReplyNamesBuildRegisterAndRules(t *testing.T) {
	deployment := Deployment{Version: "0.2.0", Register: "codecheckers/testing-dev-register", Bot: "chekhovbot",
		Environment: "development"}

	reply := deployment.VersionReply("abcdef0123456789", "2026-09-16T20:38:45+0000")
	for _, want := range []string{
		"0.2.0",
		"codecheckers/testing-dev-register",
		"https://github.com/codecheckers/register/commit/abcdef0123456789",
		"2026-09-16T20:38:45+0000",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not carry %q:\n%s", want, reply)
		}
	}

	// One version and nothing beside it. The bot used to report a commit of
	// its own as well, through three channels that could each name a
	// different one; there is now a single constant, so there is nothing for
	// a reply to reconcile. The register's rules commit is a different thing
	// and is still named above.
	// The build's own commit used to be a line in this reply, linked into the
	// bot's repository. The register's rules commit is linked too and is a
	// different thing, so the absence is asserted on the repository rather
	// than on the word.
	if strings.Contains(reply, "codecheckers/chekhov/commit/") {
		t.Errorf("the reply links a commit of the bot's own:\n%s", reply)
	}
	for _, own := range []string{deployment.Signature(), deployment.HelloReply()} {
		if strings.Contains(strings.ToLower(own), "commit") {
			t.Errorf("a reply still speaks of a commit of its own:\n%s", own)
		}
	}
}

// Without a rules provenance record the reply still says what it knows.
func TestVersionReplyWithoutRulesProvenance(t *testing.T) {
	deployment := Deployment{Version: "0.2.0", Register: "codecheckers/register",
		Bot: "chekhovbot", Environment: "production"}

	reply := deployment.VersionReply("", "")
	if !strings.Contains(reply, "codecheckers/register") {
		t.Errorf("the reply does not name the register:\n%s", reply)
	}
	if strings.Contains(reply, "validation rules") {
		t.Errorf("the reply invents a rules provenance:\n%s", reply)
	}
}

// The commit is reported when the build can prove one, and only where the
// deployment may say so. A hand-set variable was the old answer and the reason
// the bot stopped naming a commit at all; what it names now is a build fact.
func TestTheBuildsCommitIsNamedOnlyWhenProvenAndOnlyInDevelopment(t *testing.T) {
	proven := Deployment{Version: "0.1.1", Revision: "8c53e4a97af0a4fa54feaf88783b78d81fe56338",
		Register: "codecheckers/testing-dev-register",
		Bot:      "chekhovbot", Environment: "development"}

	reply := proven.VersionReply("", "")
	for _, want := range []string{"8c53e4a9", "codecheckers/chekhov/commit/"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not carry %q:\n%s", want, reply)
		}
	}
	// A locally built binary signing a preview says its own hash, which is
	// what somebody developing it wants to see.
	if !strings.Contains(proven.Signature(), "8c53e4a9") {
		t.Errorf("the footer does not name the build: %s", proven.Signature())
	}
	if proven.Facts()["commit"] != proven.Revision {
		t.Errorf("health does not name the build's commit: %v", proven.Facts())
	}
	// A tree with uncommitted changes is not the commit it names.
	dirty := proven
	dirty.RevisionDirty = true
	if !strings.Contains(dirty.Signature(), "uncommitted") {
		t.Errorf("the footer does not say the tree was dirty: %s", dirty.Signature())
	}
	if dirty.Facts()["commit_dirty"] != true {
		t.Errorf("health does not say the tree was dirty: %v", dirty.Facts())
	}

	// Production answers a codechecker's question, not a fingerprint.
	production := proven
	production.Environment = "production"
	if strings.Contains(production.VersionReply("", ""), "chekhov/commit/") {
		t.Errorf("production names the build's commit:\n%s", production.VersionReply("", ""))
	}

	// A build that cannot prove a commit says nothing rather than guessing.
	unproven := proven
	unproven.Revision = ""
	if strings.Contains(unproven.VersionReply("", ""), "build:") {
		t.Errorf("a build with no provable commit named one:\n%s", unproven.VersionReply("", ""))
	}
	if _, named := unproven.Facts()["commit"]; named {
		t.Errorf("health invents a commit: %v", unproven.Facts())
	}
	if _, named := production.Facts()["commit"]; named {
		t.Errorf("production health names the build's commit: %v", production.Facts())
	}
	if unproven.describeVersion() != "version 0.1.1" {
		t.Errorf("describeVersion = %q", unproven.describeVersion())
	}
}

// The reply that asks the owners is the one place a handle is written so that
// it notifies. It is bounded, and it does not invite a retry: every attempt
// would notify every owner again.
func TestTheRequestToTheOwnersIsBoundedAndDoesNotInviteARetry(t *testing.T) {
	many := []string{"one", "two", "three", "four", "five"}
	reply := OutsideReply(OutsideRequest{Handle: "a-newcomer", Role: RoleAssignedCodechecker,
		Organisation: "codecheckers", Team: "codecheckers", Owners: many})

	notified := strings.Count(reply, "@one") + strings.Count(reply, "@two") +
		strings.Count(reply, "@three") + strings.Count(reply, "@four") + strings.Count(reply, "@five")
	if notified != mostOwnersMentioned {
		t.Errorf("%d owners notified, want at most %d:\n%s", notified, mostOwnersMentioned, reply)
	}
	// Running it again is the only way forward until the nightly nudge exists,
	// so the reply says so - and says when, because running it before they
	// have accepted asks the owners twice for the same person.
	if !strings.Contains(reply, "Once they have accepted") {
		t.Errorf("the reply does not say when to run it again:\n%s", reply)
	}
	if !strings.Contains(reply, "asks the owners twice") {
		t.Errorf("the reply does not say what an early retry costs:\n%s", reply)
	}
	// The subject is discussed, not summoned.
	if !strings.Contains(reply, "`@a-newcomer`") {
		t.Errorf("the person being assigned should be in backticks:\n%s", reply)
	}
}

// Asking nobody must not panic: the last branch of the conjunction indexes
// one before the end.
func TestTheRequestToTheOwnersSurvivesAnEmptyList(t *testing.T) {
	reply := OutsideReply(OutsideRequest{Handle: "a-newcomer", Role: RoleAssignedCodechecker,
		Organisation: "codecheckers", Team: "codecheckers"})
	if !strings.Contains(reply, "could not find one") {
		t.Errorf("reply:\n%s", reply)
	}
	if got := notifying(nil); got != "" {
		t.Errorf("notifying(nil) = %q", got)
	}
}

// Only a role that puts somebody in a team may promise one. An author is not a
// codechecker, and the handling editor is an editor already.
func TestTheRequestPromisesNoTeamForARoleThatNeedsNone(t *testing.T) {
	reply := OutsideReply(OutsideRequest{Handle: "an-author", Role: RoleAuthor,
		Organisation: "codecheckers", Owners: []string{"an-owner"}})
	if strings.Contains(reply, "I put them in") {
		t.Errorf("the reply promises a team for an author:\n%s", reply)
	}
	if !strings.Contains(reply, "@an-owner") {
		t.Errorf("the owners are still asked to invite them:\n%s", reply)
	}
}
