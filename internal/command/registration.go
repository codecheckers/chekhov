package command

import (
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/registration"
)

// The replies on the codechecker lists, where people register (#49).
//
// They are read by the registrant, on their own issue: there is no private
// place on a public issue for a note to the editors, so nothing here is a
// draft or a hint for somebody else. Anything personal - a first name, an
// event in common, an answer to a question - is the handling editor's own
// comment. And nothing here quotes a contact: an address is somebody's to
// publish, and the runbook's rule is to link the page that carries it rather
// than repeat it.

// RegistrationReply is what a registration still needs. First says it is the
// answer to the issue being opened, which thanks them for it.
func RegistrationReply(report registration.Report, first bool) string {
	var out strings.Builder
	if first {
		out.WriteString("Thanks for your interest in becoming a codechecker! :heart:\n\n")
	}

	var blocking, rest []registration.Finding
	for _, finding := range report.Findings {
		if finding.Blocks() {
			blocking = append(blocking, finding)
		} else {
			rest = append(rest, finding)
		}
	}

	if report.Complete() {
		out.WriteString("✔ **Your registration has everything the list needs.** " +
			"An editor will add you to the list and to the team.\n")
	} else {
		out.WriteString("**Before you can be listed, your registration needs this:**\n\n")
		writeFindings(&out, blocking)
		fmt.Fprintf(&out, "\nPlease edit the row in the first post of this issue, and then comment "+
			"`%s %s` for me to look again.\n", Bot, IncludeCheck)
	}
	if len(rest) > 0 {
		out.WriteString("\n")
		writeFindings(&out, rest)
	}
	return out.String()
}

func writeFindings(out *strings.Builder, findings []registration.Finding) {
	for _, finding := range findings {
		fmt.Fprintf(out, "- %s %s\n", finding.Outcome, finding.Text)
	}
}

// NotARegistrationReply answers `include check` on an issue of the lists that
// does not carry the registration label.
func NotARegistrationReply(label string) string {
	return fmt.Sprintf("This issue is not a registration: it does not carry the %s label. "+
		"To register as a codechecker, please open an issue with the registration template.\n", Code(label))
}

// OnListsReply answers a command of the register asked on the lists: there
// the bot answers a registration and nothing else.
func OnListsReply(lists, register string) string {
	return fmt.Sprintf("On %s I answer `%s %s`, in a registration issue. Everything else I do "+
		"is for the checks on %s.\n", Code(lists), Bot, IncludeCheck, Code(register))
}

// ListsCommandReply answers a command of the lists asked on the register.
func ListsCommandReply(asked Name, lists string) string {
	if lists == "" {
		return fmt.Sprintf("`%s %s` is for a registration as a codechecker, and this deployment "+
			"does not work on the codechecker lists.\n", Bot, asked)
	}
	return fmt.Sprintf("`%s %s` is for a registration issue on %s, where people register as "+
		"codecheckers.\n", Bot, asked, Code(lists))
}
