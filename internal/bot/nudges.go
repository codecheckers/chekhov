package bot

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
)

// The nightly sweep, and the endpoint that says whether it is still running.
//
// Two schedules guard two different failures. The ticker below is the sweep
// itself: in this process, with the reply path the bot already has, so that
// nothing needs a second copy of the token. A weekly GitHub Actions run is the
// other half - it exists to notice when this goroutine has stopped, which is
// the failure an in-process timer has and a workflow does not, and it reads
// GET /nudges without any credential at all. See codecheckers/chekhov#48.

const (
	// nightly is how often the sweep runs by itself.
	nightly = 24 * time.Hour
	// firstSweep is how long after boot the first one runs. Not immediately:
	// a deploy should answer its health check before it starts walking the
	// register. Soon enough that a deployment restarted every day still
	// sweeps every day.
	firstSweep = 5 * time.Minute
	// sweepTimeout bounds one walk, wherever it was asked for; see
	// Server.nudge. Every issue is one request and the register is small, but
	// a service that hangs must not hold the next night's sweep - or the
	// goroutine answering the `nudge` command.
	sweepTimeout = 15 * time.Minute
	// remembered is how many sweeps the endpoint reports. A few weeks of one
	// a night, which is as far back as anybody looks. Memory only, so a
	// deploy loses them - which is consistent with a bot that has no
	// database, and which the weekly check tolerates by looking at the
	// process's age as well.
	remembered = 21
	// detailed is how many problems are kept per sweep. The count is what
	// the endpoint reports; the words are for a person reading the log of a
	// development deployment, and a broken token must not pin a string per
	// issue for three weeks.
	detailed = 20
)

// sweeps is what this process has done, in memory and under a lock because the
// ticker writes it while a request reads it.
type sweeps struct {
	mutex   sync.Mutex
	started time.Time
	done    []Sweep
}

// record keeps one sweep and forgets the ones nobody would look at any more.
func (h *sweeps) record(sweep Sweep) {
	if len(sweep.Problems) > detailed {
		sweep.Problems = sweep.Problems[:detailed]
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.done = append(h.done, sweep)
	if len(h.done) > remembered {
		h.done = h.done[len(h.done)-remembered:]
	}
}

// state is what the endpoint reports.
//
// Counts, not words: a problem names the person it is about, and this endpoint
// is unauthenticated. Development says more, as everywhere else in the bot.
func (h *sweeps) state(development bool) map[string]any {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	walked := make([]map[string]any, 0, len(h.done))
	for _, sweep := range h.done {
		one := map[string]any{
			"started":     sweep.Started.Format(time.RFC3339),
			"issues":      sweep.Issues,
			"acted":       sweep.Acted,
			"outstanding": sweep.Outstanding,
			"problems":    len(sweep.Problems),
		}
		if development {
			one["problem"] = sweep.Problems
		}
		walked = append(walked, one)
	}

	// Due from the last sweep, or from the boot when there has not been one.
	due := h.started.Add(firstSweep)
	state := map[string]any{
		"status":  "ok",
		"started": h.started.Format(time.RFC3339),
		"every":   nightly.String(),
		"sweeps":  walked,
	}
	if last := len(h.done); last > 0 {
		state["last_sweep"] = h.done[last-1].Started.Format(time.RFC3339)
		due = h.done[last-1].Started.Add(nightly)
	}
	state["next_due"] = due.Format(time.RFC3339)
	return state
}

// nudgeState answers GET /nudges.
func (s *Server) nudgeState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.sweeps.state(s.Deployment.Development()))
}

// sweepNightly runs the sweep until the process is asked to stop.
//
// Only what this runs is remembered: the `nudge` command walks the same
// register, and counting it here would let a manual run hide a ticker that
// has stopped - which is the one thing the history exists to show.
func (s *Server) sweepNightly(ctx context.Context) {
	timer := time.NewTimer(firstSweep)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.sweepOnce(ctx)
			timer.Reset(nightly)
		}
	}
}

// sweepOnce walks the register and remembers what it found.
//
// A panic in the walk, or in any follow-up handler it calls, costs this sweep
// and not the process: the timer still fires the next night, and the failed
// walk is recorded like any other, so that GET /nudges shows a sweep that
// keeps panicking as well as one that has stopped. See
// codecheckers/chekhov#50.
func (s *Server) sweepOnce(ctx context.Context) {
	// What the walk came to, pessimistically until it comes to something: a
	// panic leaves this standing, and the recover below runs first, so the
	// failed walk is recorded and logged like any other. The problem says
	// where to read it rather than quoting it, because this endpoint counts
	// rather than names.
	sweep := Sweep{Started: time.Now().UTC(), Swept: command.Swept{
		Problems: []string{"the sweep panicked; the trace is on the admin issue"}}}
	defer func() {
		s.sweeps.record(sweep)
		s.Logger.Info("the nightly sweep ran", "issues", sweep.Issues, "acted", sweep.Acted,
			"outstanding", sweep.Outstanding, "problems", len(sweep.Problems))
		for _, problem := range sweep.Problems {
			s.Logger.Warn("the nightly sweep could not finish something", "problem", problem)
		}
	}()
	defer s.recoverBackground("The nightly sweep panicked.")

	sweep = s.nudge(ctx)
}
