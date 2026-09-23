// Package httpretry is the one retry the bot's writers share.
//
// internal/github and internal/mastodon both try a request twice, wait as the
// service asks between the two, and give up on a refusal that will not
// change. They used to do so in two copies, which had already drifted apart:
// one waited for the rate limit to refill after a server error, and waited
// however long Retry-After asked. What really differs between the services -
// GitHub's habit of saying "rate limit" only in the body of a 403, and whether
// a request that timed out may be sent twice - is in the Policy each client
// passes; everything else is here.
package httpretry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxRateLimitWait is how long a retry may wait, for a rate limit to refill or
// for the pause Retry-After asks for.
// Longer than that, the caller is better told that the request did not go out.
const maxRateLimitWait = 2 * time.Minute

// StatusError is a request a service refused.
type StatusError struct {
	// Service is who answered, GitHub or Mastodon, for the message.
	Service string
	Status  int
	Body    string
	// RetryAfter is how long the service asked to wait, zero when it did not.
	RetryAfter time.Duration
	// Reset is when a rate limit refills, from X-RateLimit-Reset. Both services
	// send it on every answer, so it says nothing about whether this one was a
	// rate limit.
	Reset time.Time
	// RateLimited is X-RateLimit-Remaining saying the budget is used up.
	RateLimited bool
}

func (e *StatusError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return fmt.Sprintf("%s answered %d: %s", e.Service, e.Status, body)
}

// FromResponse reads a refusal: its status, its body and the headers that say
// when trying again is worth it.
func FromResponse(service string, response *http.Response, body []byte) *StatusError {
	return &StatusError{
		Service:     service,
		Status:      response.StatusCode,
		Body:        strings.TrimSpace(string(body)),
		RetryAfter:  retryAfter(response),
		Reset:       rateLimitReset(response),
		RateLimited: strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining")) == "0",
	}
}

// IsStatus reports whether err is a refusal with a particular HTTP status.
func IsStatus(err error, status int) bool {
	var failure *StatusError
	return errors.As(err, &failure) && failure.Status == status
}

// Policy is how one client retries.
type Policy struct {
	// Wait is how long to wait before the retry when the service says nothing.
	Wait   time.Duration
	Logger *slog.Logger
	// BodySaysRateLimited, when set, is asked about a 403 whose headers say
	// nothing: whether its body calls it a rate limit.
	BodySaysRateLimited func(body string) bool
	// RetryTimeouts says whether a request that timed out is sent again. It may
	// have arrived, and only the answer been lost: GitHub would rather post a
	// reply twice than not at all, Mastodon must not create a second
	// collection on a public account.
	RetryTimeouts bool
}

// Do runs a request twice at most, waiting as the service asks between the
// two. A request lost to a hiccup is somebody waiting for an answer that never
// comes; a refusal is not retried, because it will not change.
func (p Policy) Do(ctx context.Context, what string, request func() error) error {
	var lastErr error
	for try := range 2 {
		if try > 0 {
			if err := Wait(ctx, p.backoff(lastErr)); err != nil {
				return err
			}
		}
		lastErr = request()
		if lastErr == nil {
			return nil
		}
		p.Logger.Warn(what+" failed", "attempt", try+1, "error", lastErr)
		if !p.retryable(ctx, lastErr) {
			break
		}
	}
	return lastErr
}

// retryable says whether trying again could work.
func (p Policy) retryable(ctx context.Context, err error) bool {
	var status *StatusError
	if !errors.As(err, &status) {
		// The caller's own context ending will not come back; a connection
		// that failed may.
		if ctx.Err() != nil {
			return false
		}
		return p.RetryTimeouts || !timedOut(err)
	}
	// A service asking for a longer pause than a retry may take is answered
	// now, with the refusal, rather than after it.
	if status.RetryAfter > maxRateLimitWait {
		return false
	}
	return status.Status >= 500 || p.rateLimited(status)
}

// timedOut reports whether a request failed for want of time: a client
// timeout, or a deadline further down than the caller's.
func timedOut(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
}

// rateLimited says whether a refusal means "not now" rather than "not ever".
// A 429 always does. A 403 is either, for GitHub at least, and the headers are
// what tell them apart; the body only when they are silent. X-RateLimit-
// Remaining is not read for any other status: a 422 that spent the last
// request of the window is still a 422.
func (p Policy) rateLimited(status *StatusError) bool {
	switch status.Status {
	case http.StatusTooManyRequests:
		return true
	case http.StatusForbidden:
		if status.RetryAfter > 0 || status.RateLimited {
			return true
		}
		return p.BodySaysRateLimited != nil && p.BodySaysRateLimited(status.Body)
	default:
		return false
	}
}

// backoff is how long to wait before the retry: as long as the service asked,
// or until a rate limit refills when that is close.
func (p Policy) backoff(err error) time.Duration {
	var status *StatusError
	if !errors.As(err, &status) {
		return p.Wait
	}
	if status.RetryAfter > 0 {
		return status.RetryAfter
	}
	// Reset is on every answer, so it is only worth waiting for when this
	// answer was the rate limit: a 502 must not wait for the window to turn.
	if p.rateLimited(status) {
		if until := time.Until(status.Reset); until > 0 && until <= maxRateLimitWait {
			return until
		}
	}
	return p.Wait
}

// Wait sleeps for a duration, or until the context ends.
func Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(response *http.Response) time.Duration {
	value := strings.TrimSpace(response.Header.Get("Retry-After"))
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// rateLimitReset reads X-RateLimit-Reset, zero when the service did not say.
// GitHub writes Unix seconds, Mastodon a timestamp.
func rateLimitReset(response *http.Response) time.Time {
	value := strings.TrimSpace(response.Header.Get("X-RateLimit-Reset"))
	if value == "" {
		return time.Time{}
	}
	if when, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return when
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Unix(seconds, 0)
	}
	return time.Time{}
}
