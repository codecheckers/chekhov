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
	}
}

func TestListingShowsEveryVisibleCommand(t *testing.T) {
	listing := Listing(RoleAnyone)
	for _, definition := range Definitions() {
		mentioned := strings.Contains(listing, escapePipes(definition.Usage))
		if definition.Hidden && mentioned {
			t.Errorf("%s is hidden but is listed", definition.Name)
		}
		if !definition.Hidden && !mentioned {
			t.Errorf("%s is not in the listing", definition.Name)
		}
	}
}

// A usage line with alternatives in it must not break the table it is listed
// in: GitHub reads a pipe as a cell boundary even inside a code span.
func TestListingTableIsNotBrokenByUsageLines(t *testing.T) {
	for _, line := range strings.Split(Listing(RoleAnyone), "\n") {
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

	if names(Visible(RoleAnyone)) == names(Visible(RoleEditor)) {
		t.Fatal("an editor and everyone else see the same commands")
	}
	if strings.Contains(Listing(RoleAnyone), "promote") {
		t.Error("an editor-only command is listed for everyone")
	}
	if !strings.Contains(Listing(RoleEditor), "promote") {
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
	deployment := Deployment{Version: "0.1.0", Commit: "0123456789abcdef",
		Register: "codecheckers/testing-dev-register", Environment: "development"}
	reply := deployment.HelloReply()
	for _, want := range []string{"0.1.0", "01234567", "codecheckers/testing-dev-register"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q: %s", want, reply)
		}
	}
}

// A production deployment does not describe itself: the footer is what a
// reader sees on every comment, and it is a fingerprint of the machine rather
// than an answer to their question.
func TestProductionRepliesCarryNoFooter(t *testing.T) {
	production := Deployment{Version: "1.2.0", Commit: "0123456789abcdef",
		Register: "codecheckers/register", Bot: "chekhovbot", Environment: "production"}

	if signature := production.Signature(); signature != "" {
		t.Errorf("a production reply is signed %q", signature)
	}
	hello := production.HelloReply()
	for _, leak := range []string{"01234567", "1.2.0"} {
		if strings.Contains(hello, leak) {
			t.Errorf("the production greeting says %q: %s", leak, hello)
		}
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
	if !strings.Contains(development.HelloReply(), "01234567") {
		t.Error("a development greeting says which commit answered")
	}
}
