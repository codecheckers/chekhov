package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
)

// Who may task the bot at all, asked once per comment before any command in it
// is looked at. Members of the organisation anywhere; on an issue, also those
// the issue has made part of it: whoever opened it or is assigned to it (which
// counts for nothing on a pull request), on the register the people on the record of the check and those an editor put
// on its allowlist, and on the lists whom a registration is for. Which of the
// commands each may then run is the registry's, in answer.
//
// What it does not reach is the nightly sweep: a reminder or an ask that is
// already recorded is acted on, whoever asked for it.

// admit says whether the author of a comment may task the bot, and answers
// them when not. A panic in deciding admits nobody and posts nothing: it is
// reported, and there is nobody here to apologise to yet.
func (s *Server) admit(event mention) (admitted bool) {
	defer s.recoverBackground("Deciding who may ask me panicked on " + event.check() + ".")

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	may, refused := s.mayTask(ctx, event)
	if !may {
		s.turnAway(ctx, event, refused)
	}
	return may
}

// mayTask is the decision, the cheapest question first: what the event itself
// says, the cached teams, one request about the organisation, and last the
// comments of the issue. When the answer is no, refused is who was turned away
// and what kept the bot from knowing more, which the refusal says; none of
// that ever lets anybody in.
func (s *Server) mayTask(ctx context.Context, event mention) (may bool, refused command.Refused) {
	refused = command.Refused{
		Handle: event.Author, Organisation: s.Settings.TeamOrganisation(),
		OnLists: s.onLists(event), PullRequest: event.PullRequest,
	}
	handle := command.Handle(event.Author)
	// An empty handle would match an issue whose author the event left out.
	if handle == "" {
		return false, refused
	}
	// Anybody may open a pull request from a fork, so having opened one says
	// nothing about being part of anything.
	if !event.PullRequest && openedOrAssigned(event.IssueAuthor, event.Assignees, handle) {
		return true, refused
	}
	if len(s.standingRoles(ctx, event)) > 0 {
		return true, refused
	}
	inside, known := s.membership(ctx, handle)
	if inside {
		return true, refused
	}
	part, err := s.partOfTheIssue(ctx, event, handle)
	refused.Edited = errors.Is(err, people.ErrTampered)
	refused.Unread = !known || (err != nil && !refused.Edited)
	return part, refused
}

// partOfTheIssue is what the comments of an issue make somebody: on the
// register a role in the record of the check or a place on its allowlist, on
// the lists the one a registration the bot opened is for - one they opened
// themselves they opened. A record that cannot be read or trusted lets nobody
// in.
func (s *Server) partOfTheIssue(ctx context.Context, event mention, handle string) (bool, error) {
	if event.Issue <= 0 {
		return false, nil
	}
	if s.onLists(event) {
		if !strings.EqualFold(event.IssueAuthor, s.Settings.BotUser()) {
			return false, nil
		}
		registrant, _, _, err := s.registrantOf(ctx, event)
		if err != nil {
			s.Logger.Warn("whom this registration is for could not be read", "issue", event.Issue, "error", err)
			return false, err
		}
		return command.Handle(registrant) == handle, nil
	}
	if s.Checks == nil {
		return false, nil
	}
	record, err := s.trustedRecord(ctx, event)
	if err != nil {
		s.Logger.Warn("the record of this check lets nobody in",
			"repository", event.Repository, "issue", event.Issue, "error", err)
		return false, err
	}
	return len(record.RolesOf(handle)) > 0 || record.Allows(handle), nil
}

// turnAway tells somebody once on an issue that the bot takes no commands from
// them, and after that only logs it: a refusal is a post the bot makes at a
// stranger's asking, and must not be one they can ask for again and again. One
// that could not look at everything is told once too, and does not count as
// that once: two at most.
//
// A comment asking `check zenodo` with a share link is deleted all the same,
// and every one of them, unless Zenodo shows nothing for its link. The person
// has to revoke a link, so a deletion is explained even to somebody told
// already - as often as refusals.share_link_replies says on one issue, and
// then no more, or the deletions would be a way to make the bot post for ever.
//
// A deletion that failed leaves the link up, so its warning is counted apart
// from those: given at least once whatever the setting says, and even where
// the comments cannot be listed to count them.
func (s *Server) turnAway(ctx context.Context, event mention, refused command.Refused) {
	s.Logger.Info("no commands are taken from this account", "repository", event.Repository,
		"issue", event.Issue, "author", event.Author, "unread", refused.Unread, "record_edited", refused.Edited)

	var link command.ShareLink
	if event.linkDealt != nil {
		link = *event.linkDealt
	}

	// explain is whether this reply tells of the link; link stays what became
	// of it.
	explain := link.Held
	leftUp := link.Held && !link.Deleted
	limit := s.Settings.ShareLinkReplies()
	if leftUp {
		limit = max(limit, 1)
	}
	for {
		// Claimed from looking to telling, so that of two comments arriving at
		// once only one gets past the look; the other is not answered. A claim
		// rather than a lock: the post waits in the queue every write shares,
		// and nobody else's refusal waits with it. Explanations have a claim
		// of their own, so that a plain refusal on its way does not keep a
		// deletion from being explained.
		kind := refusalPlain
		switch {
		case explain && leftUp:
			kind = refusalLeftUp
		case explain:
			kind = refusalDeleted
		}
		claim := refusalClaim(event, kind)
		if _, telling := s.turningAway.LoadOrStore(claim, struct{}{}); telling {
			return
		}
		defer s.turningAway.Delete(claim)

		told, readable := s.refusalsTo(ctx, event)
		if !readable {
			if explain && leftUp {
				break
			}
			return
		}
		if explain && explained(told, link.Deleted) >= limit {
			// Spent: what is left is the one refusal anybody gets, claimed
			// and looked for as that one.
			explain = false
			continue
		}
		if !explain && toldAlready(told, event.Author, refused.Unread) {
			return
		}
		break
	}
	note := ""
	if explain {
		note = command.ShareLinkNote(link)
	}
	if _, err := s.post(ctx, event, said(command.TurnedAway(refused)+note)); err != nil {
		s.Logger.Error("the refusal could not be posted", "issue", event.Issue, "error", err)
	}
}

// The kinds of refusal that are claimed apart: see turnAway.
const (
	refusalPlain   = "plain"
	refusalDeleted = "deleted"
	refusalLeftUp  = "left up"
)

func refusalClaim(event mention, kind string) string {
	return fmt.Sprintf("%s %q %s", event.check(), command.Handle(event.Author), kind)
}

// refusalsTo are the refusals the bot has posted to this person on this issue.
// Comments that cannot be listed are not readable, and then nothing is posted:
// staying silent costs somebody an explanation, and the other way round lets a
// stranger draw a post each time. The one exception is turnAway's, and
// deliberate: while a link cannot be deleted either, each is warned of, with no
// count to go by, because a link left up in silence is the worse failure. A reply path that cannot list at all - a
// test's recorder - has told nobody.
func (s *Server) refusalsTo(ctx context.Context, event mention) (told []string, readable bool) {
	reader, ok := s.posterFor(event.Repository).(Reader)
	if !ok {
		return nil, true
	}
	written, err := reader.Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		s.Logger.Warn("what this account was told already could not be read",
			"issue", event.Issue, "error", err)
		return nil, false
	}
	for _, comment := range written {
		if strings.EqualFold(comment.Author, s.Settings.BotUser()) &&
			(strings.HasPrefix(comment.Body, command.TurnedAwayOpening(event.Author, false)) ||
				strings.HasPrefix(comment.Body, command.TurnedAwayOpening(event.Author, true))) {
			told = append(told, comment.Body)
		}
	}
	return told, true
}

// toldAlready says one of these refusals is the one this one would repeat. A
// refusal that could not look counts only for another such one.
func toldAlready(told []string, handle string, unread bool) bool {
	return slices.ContainsFunc(told, func(body string) bool {
		return unread || strings.HasPrefix(body, command.TurnedAwayOpening(handle, false))
	})
}

// explained is how many of these refusals told of a share link whose comment
// was deleted, or of one that was left up.
func explained(told []string, deleted bool) int {
	count := 0
	for _, body := range told {
		if command.ExplainsShareLink(body) && command.ExplainsDeletion(body) == deleted {
			count++
		}
	}
	return count
}
