package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// Picking up what a reply left outstanding.
//
// The bot has no database, so the state is the thread: a reply that left
// something outstanding carries a signed record saying what, and this walks
// the open issues looking for them. See codecheckers/chekhov#48.
//
// Two rules make the walk safe to repeat, which matters because it runs
// nightly and on demand:
//
//   - The **newest** record for a subject is the state. A reminder posts a
//     fresh one, so "three days" is measured from the last time anybody was
//     asked rather than from the first.
//   - A handler decides by **looking at the world**, not by bookkeeping. When
//     the thing is done, it says nothing - so running twice in a day sends
//     nothing twice, without a "resolved" flag anybody could forge.

// Reader is what the sweep needs of the reply path: the open issues, and the
// comments of one. A reply path that cannot read them sweeps nothing and says
// so, rather than reporting an empty register as nothing to do.
type Reader interface {
	Issues
	Comments(ctx context.Context, repository string, issue int) ([]github.Comment, error)
}

// The reply path a deployment uses can do both.
var _ Reader = (*github.Client)(nil)

// A follower resumes one kind of follow-up. It answers with what to say -
// empty when there is nothing to say - and whether the follow-up is still
// waiting on somebody: nothing to do *yet* is not the same as nothing left to
// do, and only the first is outstanding. An error means it could not tell,
// which is a problem to report rather than a silence.
type follower func(ctx context.Context, event mention, record followup.Record) (result answered, waiting bool, err error)

// followers are the kinds this bot knows how to resume. Adding one is an entry
// here and a method; the sweep itself does not change. Built once per walk
// rather than per record, because a sweep asks for it as often as it finds
// something.
func (s *Server) followers() map[string]follower {
	return map[string]follower{
		followup.KindOrganisation: s.followOrganisation,
	}
}

// A Sweep is what one walk of the register found, for the endpoint that
// reports whether the nightly one is still running.
type Sweep struct {
	Started time.Time
	// What the walk came to is the reply's own struct: the counts are
	// reported to an editor and to the endpoint alike, and a fifth of them
	// should not have to be added in two places.
	command.Swept
}

// problem records something that could not be read or answered, named by the
// issue it was on.
func (s *Sweep) problem(event mention, format string, args ...any) {
	s.Problems = append(s.Problems, event.check()+": "+fmt.Sprintf(format, args...))
}

// nudged is the reply to `@chekhovbot nudge`, which runs the same walk the
// nightly one does - so that it can be tried without waiting a night.
func (s *Server) nudged(ctx context.Context) string {
	return command.SweptReply(s.nudge(ctx).Swept)
}

// nudge walks the open issues of the register and resumes what it finds.
func (s *Server) nudge(ctx context.Context) Sweep {
	sweep := Sweep{Started: time.Now().UTC()}
	// One walk at a time. The nightly ticker and an editor running `nudge`
	// can otherwise both find the same record ready for a reminder and post
	// one each, which notifies the owners twice for the same person.
	if !s.sweeping.TryLock() {
		sweep.Problems = append(sweep.Problems, "a sweep is already running, so I did not start another")
		return sweep
	}
	defer s.sweeping.Unlock()

	// The bound belongs to the walk: a service that hangs must not hold the
	// next night's sweep. A command's walk is bounded by commandTimeout
	// already, and the sooner of the two is what applies.
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	reader, ok := s.Replies.(Reader)
	if !ok {
		sweep.Problems = append(sweep.Problems, "this deployment cannot read the register's issues")
		return sweep
	}
	register := s.Settings.TargetRepository()

	issues, err := reader.OpenIssues(ctx, register)
	if err != nil {
		// Never "nothing to do": a register that could not be read is a
		// register whose follow-ups are all still outstanding.
		sweep.Problems = append(sweep.Problems, fmt.Sprintf("could not read the issues of %s: %s", register, err))
		return sweep
	}

	sweep.Issues = len(issues)
	followers := s.followers()
	for _, issue := range issues {
		event := mention{Repository: register, Issue: issue.Number}
		for _, record := range s.outstandingIn(ctx, reader, event, &sweep) {
			s.resume(ctx, event, record, followers, &sweep)
		}
	}
	return sweep
}

// outstandingIn is the newest record per subject and kind on one issue.
//
// Per subject, because a reminder posts a fresh record beside the old one and
// only the last one is the state; the older ones are the history of what was
// asked, which is worth keeping in the thread and not worth acting on.
func (s *Server) outstandingIn(ctx context.Context, reader Reader, event mention,
	sweep *Sweep) []followup.Record {
	comments, err := reader.Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		sweep.problem(event, "could not read the comments: %s", err)
		return nil
	}

	bot := s.Settings.BotUser()
	if strings.TrimSpace(bot) == "" {
		// Without the bot's own name there is no way to tell its comments
		// from anybody else's, and a record is only a record because the bot
		// wrote it.
		sweep.problem(event, "I do not know my own account name, so I cannot tell my records from anybody else's")
		return nil
	}

	check := event.check()
	newest := map[string]followup.Record{}
	for _, comment := range comments {
		// The signature is the second defence and not the first: a deployment
		// without a record key signs nothing, and anybody who can comment
		// could otherwise write themselves a follow-up for the sweep to act
		// on. The roles record is read the same way; see people.Checks.Read.
		if !strings.EqualFold(comment.Author, bot) {
			continue
		}
		record, err := followup.Parse(comment.Body, check, s.signer())
		switch {
		case err == followup.ErrNoRecord:
			continue
		case err != nil:
			// A record somebody edited is not one to act on, and saying so is
			// the point: silence would look like there was nothing there.
			sweep.problem(event, "%s", err)
			continue
		}
		key := record.Kind + "\x00" + record.Subject
		if held, seen := newest[key]; !seen || record.Raised().After(held.Raised()) {
			newest[key] = record
		}
	}

	records := make([]followup.Record, 0, len(newest))
	for _, record := range newest {
		records = append(records, record)
	}
	// A stable order, so that two sweeps of the same issue read the same.
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		return records[i].Subject < records[j].Subject
	})
	return records
}

// resume hands one record to its handler and posts whatever it says.
func (s *Server) resume(ctx context.Context, event mention, record followup.Record,
	followers map[string]follower, sweep *Sweep) {
	handler, known := followers[record.Kind]
	if !known {
		sweep.problem(event, "I do not know how to follow up %q", record.Kind)
		return
	}

	result, waiting, err := handler(ctx, event, record)
	if err != nil {
		sweep.problem(event, "%s", err)
		return
	}
	if waiting {
		// Still on somebody else: the owners have been asked and have not
		// acted yet. A follow-up that is finished is not counted, or the
		// number would only ever grow.
		sweep.Outstanding++
	}
	if result.body == "" {
		// Nothing to say: either nothing to do yet, or nothing left to do.
		// The thread is not the place to report either every night.
		return
	}

	if _, err := s.post(ctx, event, result); err != nil {
		sweep.problem(event, "could not answer: %s", err)
		return
	}
	sweep.Acted++
}
