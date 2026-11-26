package bot

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
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
func (s *Server) admit(event mention, commands []command.Command) (admitted bool) {
	defer s.recoverBackground("Deciding who may ask me panicked on " + event.check() + ".")

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	may, refused := s.mayTask(ctx, event)
	if !may {
		s.turnAway(ctx, event, commands, refused)
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
// and that is said every time, since the person has to revoke the link.
func (s *Server) turnAway(ctx context.Context, event mention, commands []command.Command, refused command.Refused) {
	s.Logger.Info("no commands are taken from this account", "repository", event.Repository,
		"issue", event.Issue, "author", event.Author, "unread", refused.Unread, "record_edited", refused.Edited)

	// event.Body is still the whole comment here, so one look covers every
	// command in it, those past the most that are run included.
	var link command.ShareLink
	if i := slices.IndexFunc(commands, func(parsed command.Command) bool {
		return parsed.Name == command.CheckZenodo
	}); i >= 0 {
		link = s.deleteShareLink(ctx, event, commands[i])
	}

	if !link.Held {
		// Claimed from looking to telling, so that two comments at once are
		// told once. A claim rather than a lock: the post waits in the queue
		// every write shares, and nobody else's refusal waits with it.
		claim := event.check() + " " + strconv.Quote(command.Handle(event.Author))
		if _, telling := s.turningAway.LoadOrStore(claim, struct{}{}); telling {
			return
		}
		defer s.turningAway.Delete(claim)
		if s.toldAlready(ctx, event, refused.Unread) {
			return
		}
	}
	if _, err := s.post(ctx, event, said(command.TurnedAway(refused)+command.ShareLinkNote(link))); err != nil {
		s.Logger.Error("the refusal could not be posted", "issue", event.Issue, "error", err)
	}
}

// toldAlready says the bot has told this person on this issue already, which a
// refusal that could not look counts as only for another such one. Comments
// that cannot be listed count as told: staying silent costs somebody an
// explanation, and the other way round lets a stranger draw a post each time.
// A reply path that cannot list at all - a test's recorder - has told nobody.
func (s *Server) toldAlready(ctx context.Context, event mention, unread bool) bool {
	reader, ok := s.posterFor(event.Repository).(Reader)
	if !ok {
		return false
	}
	written, err := reader.Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		s.Logger.Warn("whether this account was told already could not be read",
			"issue", event.Issue, "error", err)
		return true
	}
	return slices.ContainsFunc(written, func(comment github.Comment) bool {
		return strings.EqualFold(comment.Author, s.Settings.BotUser()) &&
			(strings.HasPrefix(comment.Body, command.TurnedAwayOpening(event.Author, false)) ||
				unread && strings.HasPrefix(comment.Body, command.TurnedAwayOpening(event.Author, true)))
	})
}
