package httpretry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// WriteInterval is the least time between two writes the bot makes, to
// GitHub or to Mastodon.
//
// A burst of posts from a young account is what spam detection looks for:
// GitHub's hid @chekhovbot and everything it had written after a live test
// posted replies seconds apart (testing-dev-register#207). Three seconds
// costs a reply nothing a person would notice, and turns a burst into a
// trickle. Here rather than in each client because it is one rule about how
// the bot behaves, whichever service it is talking to.
const WriteInterval = 3 * time.Second

// A Pacer lets one write through at a time, WriteInterval after the last one
// finished, and queues the rest.
//
// One Pacer belongs to one client, and a deployment builds one client per
// service, so every command of every editor shares it: two editors asking at
// the same moment get their replies one after the other, not together. The
// zero value is ready to use and safe for concurrent use.
type Pacer struct {
	once sync.Once
	// slot is the turn: whoever has put a token in it is writing, or waiting
	// out the interval before writing. A channel rather than a mutex, so that
	// a write anywhere in the queue - not only at its head - can give up when
	// its context ends.
	slot chan struct{}
	// next is when the next write may leave. Only the holder of slot touches
	// it, so the channel is all the synchronisation it needs.
	next time.Time
	// queued is how many writes are waiting for the slot, for a test to know
	// the queue it means to test is really there.
	queued atomic.Int32
}

// ErrGaveUp is a write that was never sent: its context ended while it waited
// for its turn.
var ErrGaveUp = errors.New("gave up waiting for a turn to write")

// Turn waits for this write's turn and returns the function that ends it,
// which the caller must call exactly once, when the write has finished. It
// takes how long the service asked everyone to hold off - a Retry-After, a
// rate limit's reset, see Hold - so that the whole queue waits, not only the
// write that was told.
//
// The interval runs from when a write finished rather than when it started,
// so a slow write is never overlapped by the next. An interval of 0 still
// lets one write through at a time, without waiting; tests use it.
func (p *Pacer) Turn(ctx context.Context, interval time.Duration) (done func(hold time.Duration), err error) {
	p.once.Do(func() { p.slot = make(chan struct{}, 1) })
	p.queued.Add(1)
	select {
	case p.slot <- struct{}{}:
		p.queued.Add(-1)
	case <-ctx.Done():
		p.queued.Add(-1)
		return nil, fmt.Errorf("%w: %w", ErrGaveUp, ctx.Err())
	}
	release := func() { <-p.slot }

	if wait := time.Until(p.next); wait > 0 {
		if err := Wait(ctx, wait); err != nil {
			release()
			return nil, fmt.Errorf("%w: %w", ErrGaveUp, err)
		}
	}
	// A context that ended while the one before was writing has no business
	// taking a turn: it would fail at once, and hold the next write up for
	// nothing.
	if err := ctx.Err(); err != nil {
		release()
		return nil, fmt.Errorf("%w: %w", ErrGaveUp, err)
	}
	return func(hold time.Duration) {
		p.next = time.Now().Add(max(interval, hold))
		release()
	}, nil
}

// Hold is how long a failed write says everyone should wait before the next:
// what the service asked for in Retry-After, or the reset of a rate limit it
// said is used up, and nothing for any other failure. A pause longer than a
// retry may take is capped at that, so that a service asking for an hour does
// not stop the bot for an hour on its say-so.
func Hold(err error) time.Duration {
	var status *StatusError
	if !errors.As(err, &status) {
		return 0
	}
	hold := status.RetryAfter
	if hold == 0 && status.RateLimited {
		hold = time.Until(status.Reset)
	}
	return min(max(hold, 0), maxRateLimitWait)
}
