package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
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
// day says nothing the second time, and no record has to be marked resolved -
// which is one fewer thing anybody could forge.
func (s *Server) followOrganisation(ctx context.Context, event mention,
	record followup.Record) (answered, bool, error) {
	inside, known := s.membership(ctx, record.Subject)
	if !known {
		return answered{}, false, fmt.Errorf("could not read whether `@%s` is in the organisation yet",
			record.Subject)
	}
	if !inside {
		if !record.Older(remindAfter, time.Now()) {
			return answered{}, true, nil
		}
		result, err := s.remindTheOwners(ctx, event, record)
		// Still on the owners either way: a reminder does not resolve it, and
		// neither does having given up on reminding.
		return result, true, err
	}
	result, err := s.finish(ctx, event, record)
	return result, false, err
}

// remindTheOwners asks again, and leaves a fresh record so that the next
// reminder is three days from now rather than three days from the first ask.
func (s *Server) remindTheOwners(ctx context.Context, event mention,
	record followup.Record) (answered, error) {
	if record.Reminders > mostReminders {
		// Asked often enough, and said so already. The record stays, so that
		// the sweep still notices if they join.
		return answered{}, nil
	}

	role := command.Role(record.Role)
	body := s.outsideReply(ctx, record.Subject, role, record.Team)
	if body == "" {
		return answered{}, nil
	}
	asked := s.outstanding(event, record.Subject, role, record.Team)
	if asked != nil {
		asked.Reminders = record.Reminders + 1
	}
	if record.Reminders == mostReminders {
		return answered{
			body: fmt.Sprintf("Still waiting, %s on. I have asked %d times now, so this is the "+
				"last time I will ask by myself: `@%s` is still not in the organisation, and "+
				"running `%s assign @%s as %s` again is what asks once more.\n",
				since(record), record.Reminders+1, record.Subject, command.Bot, record.Subject, role.Typed()),
			follow: asked,
		}, nil
	}
	return answered{
		body:   fmt.Sprintf("Still waiting, %s on.\n\n%s", since(record), body),
		follow: asked,
	}, nil
}

// finish does what the ask promised, now that they have joined: the team, the
// role, the issue's assignee.
//
// Nothing to say means nothing was left to do, which is how this stops
// repeating itself.
func (s *Server) finish(ctx context.Context, event mention, record followup.Record) (answered, error) {
	role := command.Role(record.Role)
	if s.Checks == nil {
		return answered{}, errors.New("this deployment cannot record the roles of a check")
	}

	// Who holds the role now decides what is left to do. An editor may have
	// assigned somebody else in the three days this took, and a record from
	// before that must not take the role back off them.
	taken := ""
	_, err := s.Checks.Update(ctx, event.Repository, event.Issue, func(held people.Record) (people.Record, error) {
		if holder := held.Holder(role); holder != "" && !strings.EqualFold(holder, record.Subject) {
			taken = holder
			return held, people.ErrUnchanged
		}
		updated, _, err := held.Grant(role, record.Subject)
		return updated, err
	})
	granted := false
	switch {
	case errors.Is(err, people.ErrUnchanged):
		// Already theirs, or somebody else's now: either way nothing to
		// record.
	case errors.Is(err, people.ErrTampered):
		return answered{}, fmt.Errorf("the roles of this check were edited, so I did not change them: %w", err)
	case err != nil:
		return answered{}, err
	default:
		granted = true
	}

	note := s.teamNote(ctx, event, record.Subject, role, record.Team, true)
	assigned := false
	if role == command.RoleAssignedCodechecker && granted {
		// Nobody to unassign: the role is granted here only when it was
		// nobody's, because one that has since gone to somebody else is left
		// with them.
		assigned = s.assignee(ctx, event, record.Subject, "")
	}
	if !granted && note == "" {
		// The role was already recorded and the team already right: somebody
		// got there first, and saying so every night would be noise.
		return answered{}, nil
	}
	return answered{body: command.JoinedReply(command.Joined{
		Handle: record.Subject, Role: role, Granted: granted, Taken: taken,
		Assigned: assigned, TeamNote: note, Lists: s.codecheckerListPages(),
		Organisation: s.Settings.TeamOrganisation(),
	})}, nil
}

// since is how long ago the ask was, in the words a reminder uses.
func since(record followup.Record) string {
	raised := record.Raised()
	if raised.IsZero() {
		return "a while"
	}
	days := int(time.Since(raised).Hours() / 24)
	if days == 1 {
		return "a day"
	}
	return fmt.Sprintf("%d days", days)
}
