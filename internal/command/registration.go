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
//
// inComment says the row is read from the registrant's comment, on a
// registration the bot opened, rather than from the first post.
func RegistrationReply(report registration.Report, first, inComment bool) string {
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
		fmt.Fprintf(&out, "\n%s\n", askAgain(inComment))
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
	return fmt.Sprintf("On %s I answer `%s %s`, in a registration issue, and `%s %s` and `%s %s` for an editor. "+
		"Everything else I do is for the checks on %s.\n", Code(lists), Bot, IncludeCheck, Bot, Include, Bot, IncludeOpen,
		Code(register))
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
func IncludeIncomplete(report registration.Report, inComment bool) string {
	var out strings.Builder
	out.WriteString("I have not included anybody: the registration still needs this.\n\n")
	writeFindings(&out, report.Blocking())
	fmt.Fprintf(&out, "\n%s Then `%s %s` includes them.\n", askAgain(inComment), Bot, Include)
	return out.String()
}

// IncludeWarnings are a registration's warnings, as items of the reply to
// `include`: nothing that holds it up, and something the editor merging the
// row should know - another list, the institutional team.
func IncludeWarnings(report registration.Report) string {
	var warnings []registration.Finding
	for _, finding := range report.Findings {
		if finding.Outcome == registration.Warning {
			warnings = append(warnings, finding)
		}
	}
	var out strings.Builder
	writeFindings(&out, warnings)
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

// ListPullRequestBySomebody is a pull request already open to add the row,
// opened by somebody else - by hand, on a branch of their own - or with their
// commits on the bot's branch: the one to merge, and no second one opened.
func ListPullRequestBySomebody(file string, number int, by string) string {
	who := "somebody else"
	if by != "" {
		who = mention(by)
	}
	return Item(fmt.Sprintf("✔ #%d, by %s, already adds the row to `%s`, so I have opened no second "+
		"pull request. An editor merges it.", number, who, file))
}

// ListBranchWorkedOn is the bot's branch with somebody else's commits on it
// and its pull request open: theirs to finish, and left alone.
func ListBranchWorkedOn(branch string, number int) string {
	return Item(fmt.Sprintf("… #%d, on `%s`, has commits that are not mine, so I have left it alone: "+
		"whoever is working on it finishes it, and an editor merges it.", number, branch))
}

// ReviewRequested says who was asked to review the pull request. In
// backticks: GitHub's own review request is what notifies them.
//
// configured says the reviewers are the ones the settings name rather than
// the organisation's owners, which is said, as OutsideReply says it.
func ReviewRequested(number int, reviewers []string, configured bool) string {
	whose := ""
	if configured {
		whose = " - the people this deployment is configured to ask, not the organisation's owners"
	}
	return Item(fmt.Sprintf("✔ Asked %s to review #%d before it is merged%s.", mentions(reviewers), number, whose))
}

// NoReviewer is a pull request with nobody to review it but the registrant.
func NoReviewer(number int) string {
	return Item(fmt.Sprintf("… The only owner I could ask to review #%d is the registrant, who cannot confirm "+
		"their own row, so I have asked nobody: an editor reviews it.", number))
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
	return Item(fmt.Sprintf("… %s has been invited to `%s` and has not accepted yet. I look every night, "+
		"and put them in `%s/%s` once they have; `%s %s` does it sooner.",
		mention(handle), organisation, organisation, team, Bot, Include))
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
	// An invitation out would have been seen: GitHub shows the bot a pending
	// one (#49), so this asks only when there is none.
	request.ask(&out, fmt.Sprintf("please invite `@%s` to the organisation, as a member. "+
		"I look every night, and put them in `%s/%s` once they have accepted.",
		request.Handle, request.Organisation, request.Team))
	return out.String()
}

// The sweep's side of `include` (#49): what it says while the inclusion is
// not finished, and the welcome once it is.

// StillWaiting reminds the owners of an ask, `assign`'s or `include`'s.
func StillWaiting(since string, ask string) string {
	return fmt.Sprintf("Still waiting, %s on.\n\n%s", since, ask)
}

// LastAsk is the last reminder to the owners the sweep posts by itself, and
// the command that asks once more.
func LastAsk(since, handle string, times int, rerun string) string {
	return fmt.Sprintf("Still waiting, %s on. I have asked %d times now, so this is the last time I will "+
		"ask by myself: %s is still not in the organisation, and `%s` asks once more.\n",
		since, times, mention(handle), rerun)
}

// InvitationWaiting tells the registrant, on their own issue - which notifies
// them without a mention - that the invitation is there to accept.
func InvitationWaiting(organisation string) string {
	return fmt.Sprintf("The invitation to the `%s` organisation is waiting for you: you can accept it at "+
		"https://github.com/orgs/%s/invitation. Once you have, I put you in the team and welcome you here.\n",
		organisation, organisation)
}

// NoRowPullRequest says a registrant is in the team and not on the list, and
// no pull request is open to put them there.
func NoRowPullRequest(handle, file string) string {
	return fmt.Sprintf("%s is in the team, and not on `%s`: no pull request is open to add the row - it "+
		"could not be opened, or was closed without being merged. `%s %s` opens it again.\n",
		mention(handle), file, Bot, Include)
}

// IncludeTeamNote is what putting a joined registrant in the team said, while
// the row is still to be merged.
func IncludeTeamNote(note string) string {
	return note + "\n\nThe pull request adding the row is what is left; I welcome you here once it is merged.\n"
}

// Welcome is the end of a registration: in the team, on the list. The wording
// is not-a-bot.md's in codecheckers/codecheckers, the snippet the editors
// posted by hand, with its second sentence saying what is now so rather than
// what the editor did. Fixed text, as everything on a registration is: what
// is personal is the handling editor's own comment.
func Welcome(organisation, team, lists, file, closing string) string {
	return fmt.Sprintf("You are in [the team](https://github.com/orgs/%s/teams/%s) and on "+
		"[the list](https://github.com/%s/blob/HEAD/%s). Welcome! :heart:\n\n"+
		"We will get in touch when we have a paper matching your expertise, which strongly depends on "+
		"the ongoing conversations with journals and publishers - we'll keep you posted. Until then, feel "+
		"free to keep an eye open for interesting preprints in your domain, which you can introduce to a "+
		"[community CODECHECK](https://codecheck.org.uk/guide/community-workflow). If you're engaged as "+
		"reviewer, why not suggest a CODECHECK with your next review? If you are editor at a journal, "+
		"we'd be happy to chat with you about establishing CODECHECKs there! :muscle:\n%s",
		organisation, team, lists, file, closing)
}

// askAgain is how to have the row looked at again: edit the first post, or -
// on a registration the bot opened, whose first post is the bot's - comment.
func askAgain(inComment bool) string {
	if inComment {
		return "Please comment here with " + rowInComment + ", for me to look again."
	}
	return fmt.Sprintf("Please edit the row in the first post of this issue, and then comment "+
		"`%s %s` for me to look again.", Bot, IncludeCheck)
}

// rowInComment is how a row comes on a registration the bot opened, said the
// same way wherever it is asked for.
var rowInComment = fmt.Sprintf("`%s %s` on its first line and your row below it, in a `csv` code block",
	Bot, IncludeCheck)

// RegistrationNotOpened is a registration GitHub would not open.
func RegistrationNotOpened(login, why string) string {
	return Item(NotDone("open a registration for "+mention(login), why))
}

// RegistrationUnread is a registration whose row could not be read.
func RegistrationUnread(why string) string {
	return NotDone("read this registration", why) + "\n"
}

// RegistrationTitle names a registration the bot opened. The login without
// its @: a title is no place for a mention.
func RegistrationTitle(login string) string {
	return "Registration as a codechecker: " + login
}

// RegistrationInvite is the first post of a registration the bot opens for
// somebody who has joined: what the list needs and what each column is for,
// in the registration template's own words. It names them so that they are
// notified - the one person who has to act on it, and whom an editor or an
// owner just brought in.
func RegistrationInvite(login, file string) string {
	return fmt.Sprintf("%s, welcome to CODECHECK! :heart: To be listed in `%s`, please answer here with a "+
		"comment, %s:\n\n```csv\n%s\n```\n\n"+
		"What each column is for:\n\n%s\n"+
		"I check the row and say what it still needs, and an editor then includes you. If something in it "+
		"changes, comment again the same way.\n",
		notifying([]string{login}), file, rowInComment, templateRow, columnHelp)
}

// templateRow and columnHelp are the registration template's, from
// codecheckers/codecheckers, so that a registration asks the same whichever
// way it was opened.
const templateRow = `name,@GitHub-handle,ORCID ID (not url),contact (or "see ORCID page" - ONLY IF your profile has a public email),"fields","languages",ecr_until (YYYY-MM, or open/expired/NA),ecr_checked (<current month>;<source URL>, or NA),fediverse (@user@instance, optional)`

const columnHelp = `- ORCID is needed for the checks anyway, and we hope this will have updated contact information in the future
- Contact is important if your ORCID does not have an email, e.g., institutional or personal website or ideally just an email that will work for a long time
- Fields/domains of expertise is a lower case list of stuff you have experience with, which may help us to increase the chance of you being familiar with a workflow/tool you shall check
- Programming language(s) and experience levels, e.g., "R (intermediate)", "nomic data analysis with MatLab", "spatial data analysis with Python (basic)"; don't worry about experience level too much, anyone should be able to check any workflow given proper documentation; we acknowledge that a CODECHECK can be conducted more efficiently if there is some familiarity with the used tools
- ecr_until records until when you are an early career researcher (ECR), i.e., within 8 years of the award of your PhD and not yet transitioned to an independent researcher. Please put the month 8 years after your PhD was awarded as YYYY-MM, e.g. "2029-04" if you got your PhD in April 2021. Write "open" if you do not have a PhD yet, "expired" if you have one but are not sure when the 8 years ran out, and "NA" if you'd rather not say. Career breaks such as parental leave extend the window - just say so below and we will adjust the date.
- ecr_checked is where that information comes from, as "<current month>;<URL>". If the PhD date is on your ORCID profile, use that, e.g. "2026-08;https://orcid.org/0000-0001-0002-0003". Otherwise just write "NA" and we will fill in the link to this issue.
- fediverse is optional: your Mastodon or other fediverse account as @user@instance, e.g. "@christina@fediscience.org". We use it to mention you when we announce a CODECHECK you worked on. Leave it empty if you prefer.
`

// RegistrationFor is the bot's first comment on a registration it opened,
// which carries the record of who it is for.
func RegistrationFor(login string) string {
	return fmt.Sprintf("This registration is for %s: I read the row from their comments.\n", mention(login))
}

// RegistrationOpened says a registration was opened for somebody, as an item
// of the reply that asked for it or of the one saying they joined.
func RegistrationOpened(lists string, number int, login string) string {
	return Item(fmt.Sprintf("✔ I have opened %s#%d for %s to register on the codechecker list.",
		lists, number, mention(login)))
}

// RegistrationExists is somebody who has a registration already, opened by
// them or by the bot: never a second one.
func RegistrationExists(lists string, number int, login string) string {
	return Item(fmt.Sprintf("✔ %s already has a registration, %s#%d, so I have opened no second one.",
		mention(login), lists, number))
}

// RegistrationNotNeeded is somebody on the list already.
func RegistrationNotNeeded(login, file string) string {
	return Item(fmt.Sprintf("✔ %s is on `%s` already, so there is no registration to open.", mention(login), file))
}

// IncludeOpenNotAMember refuses to open a registration for somebody outside
// the organisation: it would notify them, and a mistyped handle is a stranger.
func IncludeOpenNotAMember(login, organisation string) string {
	return fmt.Sprintf("%s is not a member of `%s`, so I have opened no registration: `%s %s` is for "+
		"somebody who has joined, and the registration notifies them.\n", mention(login), organisation, Bot, IncludeOpen)
}

// IncludeOpenUsage is `include open` without one handle after it.
func IncludeOpenUsage() string {
	return fmt.Sprintf("`%s %s @handle` opens a registration for one person who has joined the organisation.\n",
		Bot, IncludeOpen)
}

// RowAsked reminds somebody, once, that their registration waits for their
// row. On the issue they were named on, which notifies them without a second
// mention.
func RowAsked() string {
	return "This registration is still waiting for your row: a comment with " + rowInComment +
		", as the first post says. I will not ask again.\n"
}
