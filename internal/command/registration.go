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

	var rest []registration.Finding
	for _, finding := range report.Findings {
		if !finding.Blocks() {
			rest = append(rest, finding)
		}
	}

	if report.Complete() {
		next := "to the list and to the team"
		if report.InTeam {
			next = "to the list"
		}
		fmt.Fprintf(&out, "✔ **Your registration has everything the list needs.** "+
			"An editor will add you %s.\n", next)
	} else {
		out.WriteString("**Before you can be listed, your registration needs this:**\n\n")
		writeFindings(&out, report.Blocking())
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
	return fmt.Sprintf("On %s I answer `%s %s`, in a registration issue, and `%s %s` for an editor. "+
		"Everything else I do is for the checks on %s.\n", Code(lists), Bot, IncludeCheck, Bot, Include, Code(register))
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

// The replies to `include`, on the registration issue (#49). The registrant
// reads them too, so they are written to both: what the editor asked for,
// what the bot did, and what is still somebody else's to do.

// IncludeHeading opens the reply to `include`.
func IncludeHeading(handle string) string {
	return fmt.Sprintf("**Including %s as a codechecker**\n\n", mention(handle))
}

// IncludeIncomplete refuses `include` for a registration that still needs
// something: the same findings the registrant was given, and nothing written.
func IncludeIncomplete(report registration.Report) string {
	var out strings.Builder
	out.WriteString("I have not included anybody: the registration still needs this.\n\n")
	writeFindings(&out, report.Blocking())
	fmt.Fprintf(&out, "\nOnce the row in the first post has been edited, `%s %s` looks again, "+
		"and `%s %s` includes them.\n", Bot, IncludeCheck, Bot, Include)
	return out.String()
}

// ListPullRequestTitle is the title of the pull request adding somebody to
// the list. Their name, not their handle: a handle in a title reads as a
// mention, and the handle is in the row.
func ListPullRequestTitle(name, file string) string {
	return fmt.Sprintf("Add %s to %s", name, file)
}

// ListCommitMessage is the commit adding the row.
func ListCommitMessage(login, file string, issue int) string {
	return fmt.Sprintf("add %s to %s, registered in #%d", login, file, issue)
}

// ListPullRequestBody says where the row came from and who asked. It shows
// no row: the diff does, and a contact is never repeated anywhere else.
func ListPullRequestBody(handle, file string, issue int, by string) string {
	return fmt.Sprintf("Adds %s to `%s`, from the registration in #%d, as %s asked with `%s %s`.\n\n"+
		"An editor reviews and merges it; I merge nothing.\n",
		mention(handle), file, issue, mention(by), Bot, Include)
}

// ListPullRequestOpened is the pull request adding the row, opened or brought
// up to date.
func ListPullRequestOpened(file string, number int, updated, unchanged bool) string {
	did := "Opened the pull request adding the row to"
	switch {
	case unchanged:
		did = "The pull request already adds this row to"
	case updated:
		did = "Brought up to date the pull request adding the row to"
	}
	return Item(fmt.Sprintf("✔ %s `%s`: #%d. An editor merges it.", did, file, number))
}

// AlreadyListed is somebody the list has a row for already: their pull
// request merged, or the row there before. Not a failure of `include`, which
// goes on to the organisation.
func AlreadyListed(handle, file string) string {
	return Item(fmt.Sprintf("✔ `%s` has a row for %s, so there is no pull request to open.",
		file, mention(handle)))
}

// TeamNotManaged is a codecheckers team the bot may not add to, or none.
func TeamNotManaged(organisation, team string) string {
	if team == "" {
		return Item("… No codecheckers team is configured, so the team is left to an editor.")
	}
	return Item(fmt.Sprintf("… I am not set up to put anybody in `%s/%s`, so the team is left to an editor.",
		organisation, team))
}

// IncludeTakesNoArguments refuses `include` with anything after it: it
// includes the person who opened the registration, and a mistyped
// `include check` must not open a pull request and notify the owners.
func IncludeTakesNoArguments() string {
	return fmt.Sprintf("`%s %s` takes nothing after it: it includes the person who opened this "+
		"registration. `%s %s` says what the registration still needs.\n", Bot, Include, Bot, IncludeCheck)
}

// ListBranchNotOurs is BranchNotOurs as an item of the reply to `include`.
func ListBranchNotOurs(branch string) string { return Item("✖ " + branchNotOurs(branch)) }

// ListPullRequestFailed is PullRequestFailed as an item.
func ListPullRequestFailed(why Failure) string { return Item(pullRequestNotDone(why)) }

// Item is one line of a reply made of a list: what was done, or not.
func Item(text string) string { return "- " + text + "\n" }

// NotFromHere is something a reply path cannot do - the command line
// preview, a test - said rather than pretended.
func NotFromHere(what string) string { return Item("… I cannot " + what + " from here.") }

// IncludeInTeam says the person is in the team already: nothing to do there.
func IncludeInTeam(handle, organisation, team string) string {
	return Item(fmt.Sprintf("✔ %s is in `%s/%s` already.", mention(handle), organisation, team))
}

// IncludeOwner notes somebody who is an owner of the organisation: more than
// a codechecker needs, which may well be meant, and is not the bot's to
// change either way.
func IncludeOwner(handle, organisation string) string {
	return Item(fmt.Sprintf("ℹ %s is an owner of `%s`, which is more than a codechecker needs; "+
		"that role is the owners' to change, not mine.", mention(handle), organisation))
}

// IncludeInvited says an invitation is out and not yet accepted: the owners
// have done their half, and the rest waits for the registrant.
func IncludeInvited(handle, organisation, team string) string {
	return Item(fmt.Sprintf("… %s has been invited to `%s` and has not accepted yet. Once they have, "+
		"`%s %s` again puts them in `%s/%s`.", mention(handle), organisation, Bot, Include, organisation, team))
}

// IncludeUnknown is an organisation that could not be asked about them.
func IncludeUnknown(handle, organisation, why string) string {
	return Item(NotDone(fmt.Sprintf("read whether %s is in `%s`", mention(handle), organisation), why))
}

// IncludeOutsideReply asks the owners to invite somebody the organisation
// shows neither as a member nor as invited. One of the few replies that
// notifies anybody, for the reason OutsideReply does: only an owner can do
// this, so asking them is the point.
func IncludeOutsideReply(request OutsideRequest) string {
	var out strings.Builder
	out.WriteString(Item(fmt.Sprintf("✖ %s is not in the `%s` organisation, and I see no invitation for them.",
		mention(request.Handle), request.Organisation)))
	if request.ask(&out, fmt.Sprintf("please invite `@%s` to the organisation, as a member. "+
		"Once they accept, `%s %s` again puts them in `%s/%s`.",
		request.Handle, Bot, Include, request.Organisation, request.Team)) {
		// Whether GitHub shows an invitation to a bot that is not an owner is
		// not certain; said, so that an owner who did invite is not left
		// wondering.
		out.WriteString("\nIf an invitation is out already, it is waiting for them to accept it.\n")
	}
	return out.String()
}
