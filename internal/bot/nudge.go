package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
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
//   - The **newest** record for a subject is the state - for a kind, a
//     subject and, for a reminder, when it is due; see followup.Record.About.
//     A reminder posts a
//     fresh one, so "three days" is measured from the last time anybody was
//     asked rather than from the first.
//   - A handler decides by **looking at the world**, not by bookkeeping. When
//     the thing is done, it says nothing - so running twice in a day sends
//     nothing twice. A kind that announces its end out loud, as the archive
//     reminder does, leaves a settled record with the announcement, signed
//     like every other, so that it is said once; see followup.Record.Settled.

// Reader is what the sweep needs of the reply path: the open issues, and the
// comments of one. A reply path that cannot read them sweeps nothing and says
// so, rather than reporting an empty register as nothing to do.
type Reader interface {
	Issues
	people.Lister
}

// The reply path a deployment uses can do both.
var _ Reader = (*github.Client)(nil)

// RecentlyClosed is the part of the reply path that reads the checks closed
// lately, for the follow-ups that outlive their check. Asked for apart from
// Reader, because a path without it still sweeps the open ones.
type RecentlyClosed interface {
	ClosedIssuesSince(ctx context.Context, repository string, since time.Time) ([]github.Issue, error)
}

var _ RecentlyClosed = (*github.Client)(nil)

// recentlyClosed is how far back the sweep reads the closed checks. A
// reminder is a comment, and a comment is a change, so a check being reminded
// every two weeks stays inside it for as long as the reminding goes on; one
// that has gone quiet drops out by itself. Long enough, too, for a check that
// was closed by hand before its register pull request was merged: nothing is
// posted on it while it waits for the row, and the reminder must still come
// when the row does.
const recentlyClosed = 90 * 24 * time.Hour

// A follower resumes one kind of follow-up. It answers with what to say -
// empty when there is nothing to say - and whether the follow-up is still
// waiting on somebody: nothing to do *yet* is not the same as nothing left to
// do, and only the first is outstanding. An error means it could not tell,
// which is a problem to report rather than a silence.
type follower func(ctx context.Context, event mention, record followup.Record) (result answered, waiting bool, err error)

// followers are the kinds this bot knows how to resume. Adding one is an entry
// here and a method; the sweep itself does not change. Built once per walk
// rather than per record, because a sweep asks for it as often as it finds
// something - and with the walk's services, read afresh once per walk, so
// that register.csv is downloaded once a night rather than once per record.
func (s *Server) followers(services *check.Services) map[string]follower {
	return map[string]follower{
		followup.KindOrganisation: s.followOrganisation,
		followup.KindReminder:     s.followReminder,
		followup.KindArchive: func(ctx context.Context, event mention,
			record followup.Record) (answered, bool, error) {
			return s.followArchive(ctx, event, record, services)
		},
		followup.KindRegistration: s.followRegistration,
		followup.KindReady:        s.followReady,
		followup.KindInclusion: func(ctx context.Context, event mention,
			record followup.Record) (answered, bool, error) {
			return s.followInclusion(ctx, event, record, services)
		},
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
	// The closed checks somebody is still talking about, too, for the
	// follow-ups that are due after a check is closed. Counted apart, so
	// that "open issues" in a reply and in GET /nudges still means that.
	if closed, ok := reader.(RecentlyClosed); ok {
		recent, err := closed.ClosedIssuesSince(ctx, register, time.Now().Add(-recentlyClosed))
		if err != nil {
			sweep.Problems = append(sweep.Problems,
				fmt.Sprintf("could not read the recently closed issues of %s: %s", register, err))
		}
		sweep.Closed = len(recent)
		issues = append(issues, recent...)
	}

	followers := s.followers(s.Services.Fresh())
	for _, issue := range issues {
		event := mention{Repository: register, Issue: issue.Number}
		// One listing of the comments per issue, for the follow-ups, the
		// record of the check and the nudges alike.
		event.comments = s.commentsOnce(event)
		thread := s.threadOf(ctx, reader, event)
		for _, problem := range thread.unread {
			sweep.problem(event, "%s", problem)
		}
		for _, record := range thread.outstanding() {
			if issue.Closed && !followup.OutlivesTheCheck(record.Kind) {
				// Asking the owners to invite somebody onto a check that is
				// over would be a reminder about nothing.
				continue
			}
			s.resume(ctx, event, record, followers, &sweep)
		}
		if !issue.Closed {
			s.nudgeStalled(ctx, event, issue, thread, &sweep)
		}
	}
	s.sweepLists(ctx, followers, &sweep)
	return sweep
}

// sweepLists walks the open registrations on the codechecker lists for what
// an `include` left outstanding (#49). Open ones only: the welcome closes the
// issue it is posted on. Read through the lists' own client, because a client
// reads the issues of its own repository and no other; counted apart, so
// that "open issues" in a reply and in GET /nudges still means the register's.
func (s *Server) sweepLists(ctx context.Context, followers map[string]follower, sweep *Sweep) {
	lists := s.Settings.ListsRepository()
	if lists == "" || s.Lists == nil {
		return
	}
	reader, ok := s.Lists.(Reader)
	if !ok {
		sweep.Problems = append(sweep.Problems, "this deployment cannot read the issues of the lists")
		return
	}
	issues, err := reader.OpenIssues(ctx, lists)
	if err != nil {
		sweep.Problems = append(sweep.Problems, fmt.Sprintf("could not read the issues of %s: %s", lists, err))
		return
	}
	sweep.Registrations = len(issues)
	for _, issue := range issues {
		event := mention{Repository: lists, Issue: issue.Number}
		// One listing of the comments per issue, as on the register: the
		// handlers read them again.
		event.comments = &people.Listing{Source: reader, Repository: lists, Issue: issue.Number}
		records, unread := s.outstandingIn(ctx, reader, event)
		for _, problem := range unread {
			sweep.problem(event, "%s", problem)
		}
		for _, record := range records {
			s.resume(ctx, event, record, followers, sweep)
		}
	}
}

// An issueThread is what the sweep reads out of one issue's comments: the comments
// themselves, the newest record about each thing, settled or not, and what
// could not be read while looking.
type issueThread struct {
	comments []people.Comment
	newest   map[string]followup.Record
	unread   []string
}

// outstanding are the records still waiting on something, in a stable order,
// so that two sweeps of the same issue read the same.
func (t issueThread) outstanding() []followup.Record {
	records := make([]followup.Record, 0, len(t.newest))
	for _, record := range t.newest {
		// Done and said to be done: nothing for the sweep to resume, and
		// nothing for `status` to list as waiting.
		if !record.Settled {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		if records[i].Subject != records[j].Subject {
			return records[i].Subject < records[j].Subject
		}
		return records[i].Due < records[j].Due
	})
	return records
}

// newestAbout is the newest record about what one would be about, settled
// or not, and whether there is one.
func (t issueThread) newestAbout(like followup.Record) (followup.Record, bool) {
	record, ok := t.newest[like.About()]
	return record, ok
}

// outstandingIn is what is still waiting on one issue, and what could not be
// read while looking.
func (s *Server) outstandingIn(ctx context.Context, reader Reader,
	event mention) (records []followup.Record, unread []string) {
	thread := s.threadOf(ctx, reader, event)
	return thread.outstanding(), thread.unread
}

// threadOf reads one issue: the newest record per subject and kind, and what
// could not be read while looking.
//
// Per subject, because a reminder posts a fresh record beside the old one and
// only the last one is the state; the older ones are the history of what was
// asked, which is worth keeping in the thread and not worth acting on.
//
// The problems come back rather than going into the sweep, because two
// callers want them said differently: the nightly walk counts them across the
// register, and `status` shows them under the one check it is about. This is
// the only place that knows a record is only a record because the bot wrote
// it, and neither caller may grow a second copy of that rule.
func (s *Server) threadOf(ctx context.Context, reader Reader, event mention) issueThread {
	comments, err := comments(event, reader).Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		return issueThread{unread: []string{fmt.Sprintf("could not read the comments: %s", err)}}
	}

	bot := s.Settings.BotUser()
	if strings.TrimSpace(bot) == "" {
		// Without the bot's own name there is no way to tell its comments
		// from anybody else's, and a record is only a record because the bot
		// wrote it.
		return issueThread{comments: comments, unread: []string{
			"I do not know my own account name, so I cannot tell my records from anybody else's"}}
	}

	check := event.check()
	read := issueThread{comments: comments, newest: map[string]followup.Record{}}
	for _, comment := range comments {
		// The signature is the second defence and not the first: a deployment
		// without a record key signs nothing, and anybody who can comment
		// could otherwise write themselves a follow-up for the sweep to act
		// on. The record of a check is read the same way; see people.Checks.Read.
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
			read.unread = append(read.unread, err.Error())
			continue
		}
		key := record.About()
		if held, seen := read.newest[key]; !seen || record.Raised().After(held.Raised()) {
			read.newest[key] = record
		}
	}
	return read
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
