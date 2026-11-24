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
// sweep puts them in the team and asks nothing else of the check.

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
			return said(command.Item(command.NotDone("read who has taken part in this issue, so I have opened "+
				"nothing", whyNot(err))))
		case !taken:
			return said(command.InviteNotInThread(login, organisation))
		}
	}

	note, _ := s.openRegistration(ctx, login, outside)
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
	// The ordinary team whatever the check: a registration is for the list
	// registrations go on, and the institutional one is kept by hand.
	team := s.Settings.CodecheckersTeam()
	asked := s.askedFor(ctx, event, login)
	if invitation.Invited() {
		reply += command.InviteInvited(login, organisation)
		if asked {
			return said(reply)
		}
		// Nobody needs asking, and the sweep still has to put them in the
		// team once they accept: the record is what it finds.
		return answered{body: reply, follow: s.outstanding(event, login, "", team)}
	}
	if asked {
		return said(reply + command.InviteAskedAlready(login))
	}
	ask := s.outsideReply(ctx, login, "", team)
	if ask == "" {
		return said(reply)
	}
	return answered{body: reply + "\n" + ask, follow: s.outstanding(event, login, "", team)}
}

// inThread is whether somebody has taken part in the issue a command is on:
// opened it, commented, or been mentioned so that GitHub notified them - and
// on a check, assigned to it or holding a role in its record. The comment
// asking is left out, since it mentions them by asking, and so is the issue's
// first post when it is that comment.
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
		// comments, as onTheCheck does for `remind`.
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
	if event.Comment != 0 && command.Mentions(event.IssueBody, login) {
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
		if command.Handle(comment.Author) == login || command.Mentions(comment.Body, login) {
			return true, nil
		}
	}
	return false, nil
}

// askedFor is whether an ask to the owners for somebody is still running on
// this check, `assign`'s or `invite`'s: the sweep reminds the owners of it,
// and a second would notify them twice for one person. One that has asked as
// often as it will is not running - its last reminder offers `invite` to
// start it over - and nor is a settled one. Only this check is looked at: an
// ask for them on another check, or `include`'s on the lists, is not seen.
func (s *Server) askedFor(ctx context.Context, event mention, login string) bool {
	reader, ok := s.Replies.(Reader)
	if !ok {
		return false
	}
	record, asked := s.threadOf(ctx, reader, event).newestAbout(
		followup.Record{Kind: followup.KindOrganisation, Subject: login})
	return asked && !record.Settled && !askedOut(record)
}
