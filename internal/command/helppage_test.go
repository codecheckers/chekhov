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

func TestHelpPageHasTheSiteFooter(t *testing.T) {
	page, err := HelpPage(Deployment{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<footer id="site-footer">`, "creativecommons.org/licenses/by-sa/4.0/", `src="https://codecheck.org.uk/img/by-sa.svg"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	_, footer, _ := strings.Cut(string(page), `<footer id="site-footer">`)
	if _, afterFAQs, _ := strings.Cut(footer, "FAQs</a>"); !strings.Contains(afterFAQs, "source and documentation") {
		t.Error("the version and source are not in the footer, after the FAQs")
	}
	// A link left relative would point at the bot rather than at the site.
	if strings.Contains(string(page), `href="/`) || strings.Contains(string(page), `src="/`) {
		t.Error("a link of the footer is relative")
	}
}

// The commands reply points at the page only where it knows its address.
func TestCommandsReplyPointsAtTheHelpPage(t *testing.T) {
	const url = "https://chekhov-dev.pqapp.dev/help"
	reply := Deployment{HelpURL: url}.CommandsReply(nil)
	if !strings.HasPrefix(reply, Listing(nil)) || !strings.HasSuffix(reply, "[help page]("+url+").\n") {
		t.Errorf("the reply does not end with the help page:\n%s", reply)
	}
	if got := (Deployment{}).CommandsReply(nil); got != Listing(nil) {
		t.Errorf("without an address:\n%s", got)
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
