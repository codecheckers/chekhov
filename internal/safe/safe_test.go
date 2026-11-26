package safe

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// A panic in a goroutine is logged at Error with its stack, handed to the
// caller as an error, and costs nothing but its own work.
func TestGoRecoversAndSays(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	var wg sync.WaitGroup
	var failed error
	ran := false
	Go(&wg, "in a test", func() { panic("made up") }, func(err error) { failed = err })
	Go(&wg, "beside it", func() { ran = true }, nil)
	wg.Wait()

	if failed == nil || !strings.Contains(failed.Error(), "panicked") {
		t.Errorf("failed = %v, want the panic as an error", failed)
	}
	if !ran {
		t.Error("the other goroutine did not run")
	}
	for _, want := range []string{"level=ERROR", "panicked in a test", "made up", "stack=", "safe_test.go"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log is missing %q:\n%s", want, logged.String())
		}
	}
}

// Without a wait group and a failed, it still recovers.
func TestGoNeedsNeither(t *testing.T) {
	done := make(chan struct{})
	Go(nil, "alone", func() { defer close(done); panic("again") }, nil)
	<-done
}
