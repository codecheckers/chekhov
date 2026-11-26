// Package safe runs a goroutine so that a panic in it costs its own work and
// not the process.
//
// Go ends the whole process on a panic nobody recovers, and a recover only
// catches a panic on its own goroutine: the guard around a command cannot see
// one in a goroutine the command started. Every goroutine the deployment
// starts goes through here or recovers for itself, and
// TestEveryGoroutineRecovers in internal/bot holds the code to that (#73).
//
// What is caught is logged at Error with its stack, which is how the private
// log repository hears of it; see internal/logbook.
package safe

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// ErrPanicked is what a goroutine that panicked failed with, as the caller
// reports it: the person who asked is told the step failed, and the log has
// the rest.
var ErrPanicked = errors.New("it panicked; the trace has been logged")

// Go runs f on a goroutine of its own, counted in wg when wg is not nil. A
// panic in f is logged, and handed to failed as an error when failed is not
// nil, so that the caller can report the work as failed rather than as
// having found nothing.
func Go(wg *sync.WaitGroup, what string, f func(), failed func(error)) {
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer Recover(what, failed)
		f()
	}()
}

// Recover is deferred directly by a goroutine that is not started by Go: it
// logs a panic and hands it to failed, as Go does. Deferred directly, because
// recover only works in a function the panicking one deferred.
func Recover(what string, failed func(error)) {
	r := recover()
	if r == nil {
		return
	}
	// Nothing here may panic in turn: this is the last frame there is.
	defer func() { recover() }()
	slog.Error("panicked "+what, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	if failed != nil {
		failed(ErrPanicked)
	}
}
