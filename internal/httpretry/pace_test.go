package httpretry

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Many editors at once, one queue: however many commands write together, one
// write is ever in flight, and every one of them gets its turn.
func TestOneWriteAtATimeWhoeverAsks(t *testing.T) {
	var pacer Pacer
	var inFlight, most, finished atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, err := pacer.Turn(context.Background(), 0)
			if err != nil {
				t.Error(err)
				return
			}
			now := inFlight.Add(1)
			for {
				seen := most.Load()
				if now <= seen || most.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inFlight.Add(-1)
			finished.Add(1)
			done(0)
		}()
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d writes were in flight at once", most.Load())
	}
	if finished.Load() != 20 {
		t.Errorf("%d of 20 writes had their turn", finished.Load())
	}
}

// The interval runs from when a write finished, so a slow write is never
// overlapped by the next one.
func TestTheIntervalRunsFromTheEndOfAWrite(t *testing.T) {
	const interval = 40 * time.Millisecond
	var pacer Pacer
	done, err := pacer.Turn(context.Background(), interval)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * interval) // a slow write
	finished := time.Now()
	done(0)

	done, err = pacer.Turn(context.Background(), interval)
	if err != nil {
		t.Fatal(err)
	}
	defer done(0)
	if gap := time.Since(finished); gap < interval {
		t.Errorf("the next write went %v after the slow one finished, want at least %v", gap, interval)
	}
}

// A write anywhere in the queue - not only at its head - gives up when its
// context ends, and is never let through afterwards.
func TestAQueuedWriteGivesUpWhereverItWaits(t *testing.T) {
	var pacer Pacer
	holding, err := pacer.Turn(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer holding(0)

	// A second write queues behind it with no deadline, a third with a short
	// one: the third must not wait for the second.
	go func() {
		if done, err := pacer.Turn(context.Background(), 0); err == nil {
			done(0)
		}
	}()
	for pacer.queued.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = pacer.Turn(ctx, 0)
	if !errors.Is(err, ErrGaveUp) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the queued write did not give up as such: %v", err)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Errorf("giving up took %v", waited)
	}
}

// A rate limit told to one write holds back the whole queue, for as long as
// the service asked - not only the write that was told.
func TestARateLimitHoldsEveryone(t *testing.T) {
	var pacer Pacer
	done, err := pacer.Turn(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	limited := &StatusError{Service: "GitHub", Status: http.StatusTooManyRequests, RetryAfter: 60 * time.Millisecond}
	answered := time.Now()
	done(Hold(limited))

	next, err := pacer.Turn(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer next(0)
	if waited := time.Since(answered); waited < 60*time.Millisecond {
		t.Errorf("the next write went %v after a Retry-After of 60ms", waited)
	}
}

func TestHold(t *testing.T) {
	soon := time.Now().Add(time.Minute)
	cases := []struct {
		name string
		err  error
		want func(time.Duration) bool
	}{
		{"no failure", nil, func(d time.Duration) bool { return d == 0 }},
		{"a failure that is not an answer", errors.New("connection reset"), func(d time.Duration) bool { return d == 0 }},
		{"a 502", &StatusError{Status: 502, Reset: soon}, func(d time.Duration) bool { return d == 0 }},
		{"a Retry-After", &StatusError{Status: 429, RetryAfter: 5 * time.Second}, func(d time.Duration) bool { return d == 5*time.Second }},
		{"a used-up budget", &StatusError{Status: 403, RateLimited: true, Reset: soon},
			func(d time.Duration) bool { return d > 50*time.Second && d <= time.Minute }},
		{"an hour asked for", &StatusError{Status: 429, RetryAfter: time.Hour},
			func(d time.Duration) bool { return d == maxRateLimitWait }},
	}
	for _, testCase := range cases {
		if got := Hold(testCase.err); !testCase.want(got) {
			t.Errorf("%s: Hold = %v", testCase.name, got)
		}
	}
}
