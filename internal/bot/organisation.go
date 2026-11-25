package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
)

// Following up an invitation the owners were asked for.
//
// `assign` asks the organisation's owners to invite somebody and records what
// it was for; this is the other end, which notices when they have joined and
// finishes the job - the team, the role, the issue's assignee. See
// codecheckers/chekhov#47 and #48.

const (
	// remindAfter is how long an ask is left before the owners are asked
	// again. Long enough that a weekend does not count as being ignored.
	remindAfter = 3 * 24 * time.Hour
	// mostReminders is how many times the owners are asked again before the
	// bot stops asking. Notifying three people every three days for ever is
	// not a reminder, it is a nuisance; after this it says so and goes quiet,
	// and an editor running `assign` again starts it over.
	mostReminders = 4
)

// followOrganisation resumes an ask to the owners.
//
// It decides by looking rather than by bookkeeping: has the person joined, do
// they hold the role, are they in the team. So a sweep that runs twice in a
// day says nothing the second time, and the finished job is its own
// announcement. Except when the team could not be done: it still is not the
// next night, so looking alone would say the whole join again every night.
// The reply marks the join said, Told, and the team done at last is said
// once, Settled (#69).
func (s *Server) followOrganisation(ctx context.Context, event mention,
	record followup.Record) (answered, bool, error) {
	invitation, known := s.whereStands(ctx, record.Subject)
	if !known {
		return answered{}, false, fmt.Errorf("could not read whether `@%s` is in the organisation yet",
			record.Subject)
	}
	if !invitation.Joined() {
		if !record.Older(remindAfter, time.Now()) || invitation.Invited() {
			// Not yet due, or the owners have done their half: an invitation
			// is out, and accepting it is the person's (#49).
			return answered{}, true, nil
		}
		result, err := s.remindTheOwners(ctx, event, record)
		// Still on the owners either way: a reminder does not resolve it, and
		// neither does having given up on reminding.
		return result, true, err
	}
	if record.Told {
		result, waiting := s.followTeam(ctx, event, record)
		return result, waiting, nil
	}
	return s.finish(ctx, event, record)
}

// whereStands is where somebody stands with the organisation - member, invited,
// neither - in one read, and whether it could be read at all.
func (s *Server) whereStands(ctx context.Context, handle string) (github.Invitation, bool) {
	organisation := s.Settings.TeamOrganisation()
	reader, ok := s.Replies.(Organisation)
	if !ok || organisation == "" {
		return github.Invitation{}, false
	}
	invitation, err := reader.OrganisationMembership(ctx, organisation, handle)
	if err != nil {
		s.Logger.Warn("could not read where somebody stands with the organisation",
			"handle", handle, "organisation", organisation, "error", err)
		return github.Invitation{}, false
	}
	return invitation, true
}

// remindTheOwners asks again, and leaves a fresh record so that the next
// reminder is three days from now rather than three days from the first ask.
func (s *Server) remindTheOwners(ctx context.Context, event mention,
	record followup.Record) (answered, error) {
	if askedOut(record) {
		// Asked often enough, and said so already: the owners are not read
		// for a reminder that will not be posted.
		return answered{}, nil
	}
	role := command.Role(record.Role)
	ask := s.outsideReply(ctx, record.Subject, role, record.Team)
	if ask == "" {
		return answered{}, nil
	}
	return s.reminder(event, record, ask, command.AskAgain(record.Subject, role)), nil
}

// askedOut is an ask that has been repeated as often as it will be, and said
// so: the sweep posts nothing more for it, and `invite` may start it over.
func askedOut(record followup.Record) bool { return record.Reminders > mostReminders }

// reminder is the rhythm every ask to the owners keeps, `assign`'s and
// `include`'s alike: asked again with a fresh record, so that the next time is
// measured from now; the last time said to be the last, with the command that
// asks once more; and after that, nothing, while the record stays so that the
// sweep still notices when they join.
func (s *Server) reminder(event mention, record followup.Record, ask, rerun string) answered {
	if askedOut(record) {
		return answered{}
	}
	next := s.followUp(event, record)
	if next != nil {
		// Asking again means there is no invitation to accept: a
		// registrant told about an earlier one is told about the next.
		next.Reminders, next.Told = record.Reminders+1, false
	}
	if record.Reminders == mostReminders {
		return answered{body: command.LastAsk(since(record), record.Subject, record.Reminders+1, rerun),
			follow: next}
	}
	return answered{body: command.StillWaiting(since(record), ask), follow: next}
}

// finish does what the ask promised, now that they have joined: the team, the
// role, the issue's assignee. An `invite`'s ask wanted no role, so it is the
// team alone (#68).
//
// Nothing to say means nothing was left to do, which is how this stops
// repeating itself. A team that could not be done is the exception: it is
// still not done tomorrow, so the reply leaves a fresh record, Told, and
// followTeam takes it from there (#69). waiting says the team is not done.
func (s *Server) finish(ctx context.Context, event mention, record followup.Record) (answered, bool, error) {
	role := command.Role(record.Role)
	taken, granted := "", false
	if role != "" {
		var err error
		if taken, granted, err = s.grantOnJoining(ctx, event, record); err != nil {
			return answered{}, false, err
		}
	}

	note, inTeam := s.intoTheJoinedTeam(ctx, event, record)
	onTheIssue := ""
	if role == command.RoleAssignedCodechecker && granted {
		// Nobody to unassign: the role is granted here only when it was
		// nobody's, because one that has since gone to somebody else is left
		// with them.
		onTheIssue = s.codecheckerRecorded(ctx, event, record.Subject, "")
	}
	if !granted && note == "" {
		// The role was already recorded and the team already right: somebody
		// got there first, and saying so every night would be noise.
		return answered{}, false, nil
	}
	// A codechecker who joined through the ask is asked for their row on the
	// lists (#49): once, and only for the list registrations go on - the
	// institutional one has columns of its own and is kept by hand.
	registered, opened := "", false
	if (role == "" || role.Checks()) && record.Team == s.Settings.CodecheckersTeam() &&
		s.Settings.ListsRepository() != "" {
		registered, opened = s.openRegistration(ctx, record.Subject, false)
	}
	reply := answered{body: command.JoinedReply(command.Joined{
		Handle: record.Subject, Role: role, Granted: granted, Taken: taken,
		OnTheIssue: onTheIssue, TeamNote: note, Lists: s.codecheckerListPages(),
		Organisation: s.Settings.TeamOrganisation(), Registration: registered, Registered: opened,
	})}
	waiting := !inTeam && note != ""
	if waiting {
		// Said once: the next sweep finds it Told, and measures from now.
		if reply.follow = s.followUp(event, record); reply.follow != nil {
			reply.follow.Told = true
		}
	}
	return reply, waiting, nil
}

// followTeam resumes a join that was said while the team could not be done:
// the team alone, tried every night as `include`'s is, and said every three
// days at most, with a fresh record to measure from. Done at last, it is said
// once, settled. waiting says the team is still not done.
func (s *Server) followTeam(ctx context.Context, event mention, record followup.Record) (answered, bool) {
	note, inTeam := s.intoTheJoinedTeam(ctx, event, record)
	switch {
	case inTeam && note != "":
		done := s.followUp(event, record)
		if done != nil {
			done.Settled = true
		}
		return answered{body: note + "\n", follow: done}, false
	case note == "":
		// In the team by somebody else's hand, or a deployment that cannot
		// change teams: nothing to do here, and nothing to say.
		return answered{}, false
	case !record.Older(remindAfter, time.Now()):
		return answered{}, true
	}
	// The record is Told already: a fresh copy is the next three days.
	return answered{body: command.JoinedTeamStill(record.Subject, note),
		follow: s.followUp(event, record)}, true
}

// intoTheJoinedTeam puts somebody who joined in the team their ask was for:
// the one the role implies on this check, or the one the ask named when it
// wanted no role (`invite`, #68).
func (s *Server) intoTheJoinedTeam(ctx context.Context, event mention, record followup.Record) (note string, in bool) {
	team := record.Team
	if role := command.Role(record.Role); role != "" {
		team = s.destination(ctx, event, role, record.Team)
	}
	return s.intoTheTeam(ctx, record.Subject, team)
}

// grantOnJoining records the role the ask was for, unless it has gone to
// somebody else meanwhile - an editor may have assigned somebody else in the
// three days this took, and a record from before that must not take the role
// back off them. taken is who holds it then; granted says it was recorded now.
func (s *Server) grantOnJoining(ctx context.Context, event mention,
	record followup.Record) (taken string, granted bool, err error) {
	role := command.Role(record.Role)
	if s.Checks == nil {
		return "", false, errors.New("this deployment cannot record the roles of a check")
	}
	_, err = s.checks(event).Update(ctx, event.Repository, event.Issue, func(held people.Record) (people.Record, error) {
		if holder := held.Holder(role); holder != "" && !strings.EqualFold(holder, record.Subject) {
			taken = holder
			return held, people.ErrUnchanged
		}
		updated, _, err := held.Grant(role, record.Subject)
		return updated, err
	})
	switch {
	case errors.Is(err, people.ErrUnchanged):
		// Already theirs, or somebody else's now: either way nothing to
		// record.
		return taken, false, nil
	case errors.Is(err, people.ErrTampered):
		return "", false, fmt.Errorf("the roles of this check were edited, so I did not change them: %w", err)
	case err != nil:
		return "", false, err
	}
	return "", true, nil
}

// since is how long ago the ask was, in the words a reminder uses.
func since(record followup.Record) string {
	raised := record.Raised()
	if raised.IsZero() {
		return "a while"
	}
	return command.Days(daysSince(raised, time.Now()))
}
