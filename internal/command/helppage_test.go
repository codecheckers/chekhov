package command

import (
	"html"
	"strings"
	"testing"
)

func TestHelpPageShowsEveryCommandThatIsNotHidden(t *testing.T) {
	rendered, err := HelpPage(Deployment{Version: "1.2.3", Revision: "abcdef0123456789", Register: "codecheckers/testing-dev-register", Lists: "codecheckers/testing-dev-codecheckers", RegistrationLabel: "registration", Bot: "chekhovbot"})
	if err != nil {
		t.Fatal(err)
	}
	// The templates escape more than html.EscapeString, so compare the text.
	page := html.UnescapeString(strings.ReplaceAll(string(rendered), "<wbr>", ""))
	onRegister, onLists, found := strings.Cut(page, `id="lists"`)
	if !found {
		t.Fatal("the lists have no part of their own")
	}
	for _, definition := range Definitions() {
		row := "<code>" + Bot + " " + definition.Usage + "</code></td>"
		inRegister, inLists := strings.Contains(onRegister, row), strings.Contains(onLists, row)
		if inRegister != (!definition.Hidden && !definition.Lists) || inLists != (!definition.Hidden && definition.Lists) {
			t.Errorf("%s: on the register %v, on the lists %v", definition.Name, inRegister, inLists)
		}
	}
	if strings.Contains(page, "`") {
		t.Error("a backtick of a summary was not rendered as code")
	}
	if strings.Contains(page, "abcdef01") {
		t.Error("the page names the commit, which production does not say")
	}
	if strings.Contains(string(rendered), "<repository>") {
		t.Error("a placeholder of a usage is not escaped")
	}
}

// A deployment that does not work on the lists says nothing of them.
func TestHelpPageWithoutLists(t *testing.T) {
	page, err := HelpPage(Deployment{Register: "codecheckers/testing-dev-register"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), `id="lists"`) || strings.Contains(string(page), " include") {
		t.Error("the page offers the commands of the lists")
	}
}

// The page renders backtick spans only, so an unpaired one would turn the rest
// of its cell into code.
func TestSummariesPairTheirBackticks(t *testing.T) {
	for _, definition := range Definitions() {
		if strings.Count(definition.Summary, "`")%2 != 0 {
			t.Errorf("%s: unpaired backtick in %q", definition.Name, definition.Summary)
		}
	}
}
