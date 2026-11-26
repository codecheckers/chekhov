package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// Inviting somebody to be a codechecker (#68).
//
// `invite @handle` on a check, and `include open @handle` on the lists, open
// a registration for somebody, whether or not they are in the organisation
// yet: everything after it - `include`, the sweep's follow-up of the
// invitation and the team - copes with somebody outside already (#49). The
// registration names them so that they are notified, and a mistyped handle
// would notify a stranger, so for somebody outside the organisation the
// handle must have taken part in the issue already: an `assign` that asked
// the owners for them, a comment, a mention. They were notified once then,
// and asking for their row reaches nobody new. A member is nobody's stranger.
//
// On a check, `invite` also asks the owners for the invitation, as `assign`
// does, unless an ask for them is waiting on the check already or an
// invitation is out. The record it leaves has no role: once they join, the
// sweep puts them in the team and asks nothing else of the check - unless it
// starts over an `assign`'s ask that has run out, whose role it keeps. Somebody in
// the organisation already is put in the team at once, on a check or on the
// lists, so that everybody invited is in the team from the day they join.

// Accounts is the part of a reply path that can tell a person's account from
// a handle nobody owns. A reply path without it skips the question.
type Accounts interface {
	PersonAccount(ctx context.Context, handle string) (string, error)
}

var _ Accounts = (*github.Client)(nil)

// invite is the reply to `invite` on a check, and to `include open` on the
// lists, onLists; the record is the ask to the owners, when one was made.
func (s *Server) invite(ctx context.Context, event mention, parsed command.Command, onLists bool) answered {
	if len(parsed.Args) != 1 || !github.IsHandle(command.Handle(parsed.Args[0])) {
		return said(command.InviteUsage(parsed.Name))
	}
	login := command.Handle(parsed.Args[0])
	organisation := s.Settings.TeamOrganisation()
	invitation, known := s.whereStands(ctx, login)
	if !known {
		return said(command.Item(command.NotDone(fmt.Sprintf("read whether `@%s` is in the organisation, so I "+
			"have opened nothing", login), "asking again later may help")))
	}
	// A member is an account, and nobody's stranger; anybody else is asked
	// about both.
	outside := !invitation.Joined()
	if outside {
		if accounts, ok := s.Replies.(Accounts); ok {
			if _, err := accounts.PersonAccount(ctx, login); err != nil {
				return said(command.InviteNoAccount(whyNot(err)))
			}
		}
		switch taken, err := s.inThread(ctx, event, login, onLists); {
		case err != nil:
			return said(command.TakenPartUnread("issue", "opened nothing", whyNot(err)))
		case !taken:
			return said(command.InviteNotInThread(login, organisation))
		}
	}

	note, _ := s.openRegistration(ctx, login, outside, event)
	// The ordinary team, unless an earlier ask named one: a registration is
	// for the list registrations go on, and the institutional one is kept by
	// hand.
	team := s.Settings.CodecheckersTeam()
	if !outside {
		// In the organisation already, so into the team now: what somebody
		// outside gets from the sweep the night they join, so that where the
		// person stood when they were invited makes no difference (#68).
		if joined, _ := s.intoTheTeam(ctx, login, team); joined != "" {
			note += command.Item(joined)
		}
	}
	if onLists {
		if outside && !invitation.Invited() {
			note += command.InviteLater(login, organisation)
		}
		return said(note)
	}
	reply := command.InviteHeading(login) + note
	if !outside {
		return said(reply)
	}
	prior, asked := s.newestAsk(ctx, event, login)
	// An `assign`'s ask that has run out still promised them a role: a new
	// ask keeps it and its team, or the sweep would put them in the team and
	// forget the check they were assigned to.
	role := command.Role(prior.Role)
	if role != "" {
		team = prior.Team
	}
	if invitation.Invited() {
		reply += command.InviteInvited(login, organisation)
		if asked {
			return said(reply)
		}
		// Nobody needs asking, and the sweep still has to put them in the
		// team once they accept: the record is what it finds.
		return answered{body: reply, follow: s.outstanding(event, login, role, team)}
	}
	if asked {
		return said(reply + command.InviteAskedAlready(login))
	}
	ask := s.outsideReply(ctx, login, role, team)
	if ask == "" {
		return said(reply)
	}
	return answered{body: reply + "\n" + ask, follow: s.outstanding(event, login, role, team)}
}

// inThread is whether somebody has taken part in the issue a command is on:
// opened it, commented, or been mentioned so that GitHub notified them - and
// on a check, assigned to it or holding a role in its record. The one answer
// to "may the bot notify them here", for `invite`, `include open` and
// `remind` alike: each notifies the person it names, and the bot notifies only
// people the issue has brought in already.
//
// A mention in one of the commands this decides for - `invite`, `include
// open`, `remind`, each of which notifies whom it names - is left out: the one
// asking, the issue's first post when it is that comment, and any earlier one,
// or a command refused for a mistyped handle would let itself through when
// posted again. So is one in an `allowlist` command, which lets somebody ask
// the bot and says nothing about their having taken part. The registry says
// which these are. Any other command's mention counts, as any comment's: an
// `assign` naming somebody brings them in, which it means to.
//
// An error is a thread that could not be read, which is not the same as
// nobody in it: the reply says so rather than doubting the spelling.
func (s *Server) inThread(ctx context.Context, event mention, login string, onLists bool) (bool, error) {
	var reader Reader
	if onLists {
		if command.Handle(event.IssueAuthor) == login {
			return true, nil
		}
		lists, err := s.listsReader()
		if err != nil {
			return false, err
		}
		reader = lists
	} else {
		// An issue that could not be read answers from the record and the
		// comments alone.
		issue, _ := s.issue(ctx, event)
		if s.holdsOnTheCheck(ctx, event, issue, login) {
			return true, nil
		}
		replies, ok := s.Replies.(Reader)
		if !ok {
			return false, errors.New("this deployment cannot read the comments of an issue")
		}
		reader = replies
	}
	if mentionedOutsideAsking(event.IssueBody, login) {
		return true, nil
	}
	written, err := comments(event, reader).Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		return false, err
	}
	for _, comment := range written {
		if comment.ID == event.Comment {
			continue
		}
		if command.Handle(comment.Author) == login || mentionedOutsideAsking(comment.Body, login) {
			return true, nil
		}
	}
	return false, nil
}

// mentionedOutsideAsking is whether a post mentions somebody so that GitHub
// notified them, other than in a command the registry marks AsksAbout, whose
// mention is the asking rather than anybody's taking part.
func mentionedOutsideAsking(body, login string) bool {
	if !command.Mentions(body, login) {
		return false
	}
	parsed, addressed := command.Parse(body)
	if !addressed {
		return true
	}
	definition, _ := command.Lookup(string(parsed.Name))
	return !definition.AsksAbout
}

// newestAsk is the newest ask to the owners for somebody on this check,
// `assign`'s or `invite`'s, the zero record for none or a settled one; and
// whether it is still running - not yet asked as often as it will be, so that
// the sweep reminds the owners of it, and a second would notify them twice
// for one person. Only this check is looked at: an ask for them on another
// check, or `include`'s on the lists, is not seen.
func (s *Server) newestAsk(ctx context.Context, event mention, login string) (record followup.Record, running bool) {
	reader, ok := s.Replies.(Reader)
	if !ok {
		return followup.Record{}, false
	}
	record, asked := s.threadOf(ctx, reader, event).newestAbout(
		followup.Record{Kind: followup.KindOrganisation, Subject: login})
	if !asked || record.Settled {
		return followup.Record{}, false
	}
	return record, !askedOut(record)
}
