package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// Nudging a check that has stalled.
//
// The follow-ups are things a reply promised; these are things nobody asked
// for, which the sweep notices on its own: a check whose codechecker has gone
// quiet, and a check that has waited in the queue. Both are off until the
// settings give them a threshold, and both decide by looking at the thread,
// as every follower does, so that running the sweep twice says nothing twice:
// a quiet check stops being quiet when the nudge is posted, and a nudge to the
// editors leaves a settled record that the next one waits an interval from.
// See codecheckers/chekhov#25.

// day is how the thresholds are counted.
const day = 24 * time.Hour

// LabelHistory is the part of the reply path that says when a label was put
// on an issue, for how long a check has waited in the queue.
type LabelHistory interface {
	LabeledAt(ctx context.Context, repository string, issue int, label string) (time.Time, error)
}

var _ LabelHistory = (*github.Client)(nil)

// nudgeStalled posts the nudges one open check is due, while the walk has
// nudges left to post. Once it has none, a quiet check is still counted as
// held - the record it reads is in the listing already read - and the queue
// is not looked at, because telling whether a check has waited long enough
// costs a request, and a count is not worth one. Held is therefore a floor.
func (s *Server) nudgeStalled(ctx context.Context, event mention, issue github.Issue, read issueThread, sweep *Sweep) {
	if !s.Settings.NudgesOn() || !s.Settings.Nudges(issue.Number) {
		return
	}
	now := time.Now()
	full := func() bool { return sweep.Nudged >= s.Settings.NudgesPerSweep() }

	quiet, err := s.quietNudge(ctx, event, issue, read, now)
	s.postNudge(ctx, event, quiet, err, full(), sweep)

	queue, err := s.queueNudge(ctx, event, issue, read, now, full())
	s.postNudge(ctx, event, queue, err, full(), sweep)
}

// postNudge posts one nudge, or counts it as held when the walk has posted as
// many as it may: the first walk after nudges are switched on finds every
// stalled check at once.
func (s *Server) postNudge(ctx context.Context, event mention, result answered, err error, full bool, sweep *Sweep) {
	switch {
	case err != nil:
		sweep.problem(event, "%s", err)
	case result.body == "":
	case full:
		sweep.Held++
	default:
		if _, err := s.post(ctx, event, result); err != nil {
			sweep.problem(event, "could not nudge: %s", err)
			return
		}
		sweep.Nudged++
	}
}

// mostQuietNudges is how many times in a row the assigned codechecker is
// asked about a quiet check without anybody answering. After that the bot
// stops by itself: asking for ever would be noise, and a check that has had
// three unanswered nudges is the editors' to look at.
const mostQuietNudges = 3

// quietNudge asks the assigned codechecker about a check they are working on
// that no person has commented on for the configured number of days. The
// bot's own comments are not an answer, so they do not count; a settled
// record says when the codechecker was last asked, so that the next nudge
// waits an interval, and how many times in a row.
func (s *Server) quietNudge(ctx context.Context, event mention, issue github.Issue, read issueThread,
	now time.Time) (answered, error) {
	after := s.Settings.QuietAfter()
	// Working on it is what the label says: a check that has finished, or
	// waits on metadata, has nothing to be asked about.
	if after == 0 || s.Checks == nil || !command.HasLabel(issue.Labels, s.Settings.WorkInProgressLabel()) {
		return answered{}, nil
	}
	var spoken time.Time
	for _, comment := range read.comments {
		if !strings.EqualFold(comment.Author, s.Settings.BotUser()) && comment.Created.After(spoken) {
			spoken = comment.Created
		}
	}
	if spoken.IsZero() || now.Sub(spoken) < after {
		return answered{}, nil
	}
	reading, err := s.checks(event).Read(ctx, event.Repository, event.Issue)
	if err != nil {
		return answered{}, err
	}
	codechecker := reading.Record.AssignedCodechecker
	if reading.Tampered != nil || codechecker == "" {
		// Nobody to ask, or nobody the record can be trusted to name: a check
		// without a codechecker is the queue nudge's business.
		return answered{}, nil
	}

	about := followup.Record{Kind: followup.KindQuietNudge, Subject: codechecker, Settled: true, Reminders: 1}
	if told, ok := read.newestAbout(about); ok && told.Raised().After(spoken) {
		// Asked since anybody last spoke: wait an interval from the asking,
		// and stop once it has gone unanswered often enough.
		if !told.Older(after, now) || told.Reminders >= mostQuietNudges {
			return answered{}, nil
		}
		about.Reminders = told.Reminders + 1
	}
	record := s.followUp(event, about)
	if record == nil {
		return answered{}, fmt.Errorf("I cannot sign a record here, so I will not ask %s about "+
			"this check every night", command.Code("@"+codechecker))
	}
	return answered{body: command.QuietNudge(codechecker, daysSince(spoken, now),
		about.Reminders == mostQuietNudges), follow: record}, nil
}

// queueNudge tells the editors about a check that has carried the queue's
// label for the configured number of days, once an interval. full says the
// walk will post nothing more, so there is nothing to look at.
func (s *Server) queueNudge(ctx context.Context, event mention, issue github.Issue, read issueThread,
	now time.Time, full bool) (answered, error) {
	after := s.Settings.QueueAfter()
	label := s.Settings.NeedsCodecheckerLabel()
	if after == 0 || full || !command.HasLabel(issue.Labels, label) {
		return answered{}, nil
	}
	// The thread first: it is already read, and a nudge inside the interval
	// settles the question without asking GitHub when the label went on.
	about := followup.Record{Kind: followup.KindQueueNudge, Subject: label}
	if told, ok := read.newestAbout(about); ok && !told.Older(after, now) {
		return answered{}, nil
	}
	history, ok := s.Replies.(LabelHistory)
	if !ok {
		return answered{}, fmt.Errorf("I cannot read when %s was put on here", command.Code(label))
	}
	since, err := history.LabeledAt(ctx, event.Repository, event.Issue, label)
	if err != nil {
		return answered{}, err
	}
	if since.IsZero() {
		// The label is on and GitHub has no record of it going on - renamed
		// since, most likely. Said, because a check that never reaches the
		// editors is exactly what this nudge is for.
		return answered{}, fmt.Errorf("%s is on this check, and I cannot tell since when", command.Code(label))
	}
	if now.Sub(since) < after {
		return answered{}, nil
	}
	about.Settled = true
	record := s.followUp(event, about)
	if record == nil {
		// Without a record the next walk could not tell it had already said
		// this, and would say it every night.
		return answered{}, fmt.Errorf("I cannot sign a record here, so I will not tell the editors "+
			"about %s every night", command.Code(label))
	}
	editors, configured := s.Settings.EditorsToTell()
	return answered{body: command.QueueNudge(editors, configured, label, daysSince(since, now)),
		follow: record}, nil
}

// daysSince is how many whole days lie between two times, as a nudge says it.
func daysSince(then, now time.Time) int { return int(now.Sub(then) / day) }
