package httpretry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func policy(wait time.Duration) Policy {
	return Policy{Wait: wait, Logger: quiet, BodySaysRateLimited: func(body string) bool {
		return strings.Contains(body, "rate limit")
	}}
}

// Which failures are worth a second attempt.
func TestDoRetriesWhatMayPassAndNothingElse(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		attempts int
	}{
		{"a server error", &StatusError{Status: http.StatusBadGateway}, 2},
		{"a rate limit", &StatusError{Status: http.StatusTooManyRequests}, 2},
		{"a refusal", &StatusError{Status: http.StatusForbidden}, 1},
		{"a 403 with Retry-After", &StatusError{Status: http.StatusForbidden, RetryAfter: time.Millisecond}, 2},
		{"a 403 with the budget spent", &StatusError{Status: http.StatusForbidden, RateLimited: true}, 2},
		{"a 403 whose body says rate limit", &StatusError{Status: http.StatusForbidden, Body: "secondary rate limit"}, 2},
		// The budget is reported on every answer; spending the last of it on a
		// refusal does not make the refusal a rate limit.
		{"a 422 with the budget spent", &StatusError{Status: http.StatusUnprocessableEntity, RateLimited: true}, 1},
		{"a validation error", &StatusError{Status: http.StatusUnprocessableEntity}, 1},
		{"a dropped connection", errors.New("connection reset by peer"), 2},
		{"a pause longer than a retry may wait", &StatusError{Status: http.StatusTooManyRequests, RetryAfter: time.Hour}, 1},
		// A deadline, but not the caller's: it may pass, and only a client
		// that would rather send twice tries it again.
		{"a timeout, where timeouts are not retried", context.DeadlineExceeded, 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			attempts := 0
			err := policy(0).Do(context.Background(), "testing", func() error {
				attempts++
				return testCase.err
			})
			if !errors.Is(err, testCase.err) {
				t.Errorf("err = %v, want %v", err, testCase.err)
			}
			if attempts != testCase.attempts {
				t.Errorf("%d attempts, want %d", attempts, testCase.attempts)
			}
		})
	}
}

// What an http.Client.Timeout really returns, rather than what it is assumed
// to: the request may have arrived, so it is only sent again when asked.
func TestAClientTimeoutIsRetriedOnlyWhenThePolicySaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Millisecond}

	for _, retry := range []bool{false, true} {
		attempts := 0
		p := policy(0)
		p.RetryTimeouts = retry
		err := p.Do(context.Background(), "testing", func() error {
			attempts++
			response, err := client.Get(server.URL)
			if err == nil {
				response.Body.Close()
			}
			return err
		})
		want := 1
		if retry {
			want = 2
		}
		if err == nil || attempts != want {
			t.Errorf("RetryTimeouts %v: err = %v after %d attempts, want an error after %d", retry, err, attempts, want)
		}
	}
}

func TestDoStopsAtTheFirstSuccess(t *testing.T) {
	attempts := 0
	err := policy(0).Do(context.Background(), "testing", func() error {
		attempts++
		if attempts == 1 {
			return &StatusError{Status: http.StatusServiceUnavailable}
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Errorf("err = %v after %d attempts, want nil after 2", err, attempts)
	}
}

// The caller's context ending will not come back, so it is not retried.
func TestDoGivesUpWhenTheContextHasEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := policy(0).Do(ctx, "testing", func() error {
		attempts++
		cancel()
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Errorf("err = %v after %d attempts, want context.Canceled after 1", err, attempts)
	}
}

func TestDoGivesUpWhenTheContextEndsDuringTheWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	attempts := 0
	err := policy(time.Hour).Do(ctx, "testing", func() error {
		attempts++
		return &StatusError{Status: http.StatusBadGateway}
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Errorf("err = %v after %d attempts, want the deadline after 1", err, attempts)
	}
}

func TestBackoff(t *testing.T) {
	soon := time.Now().Add(time.Minute)
	cases := []struct {
		name string
		err  error
		// want is the wait, or -1 for "until soon".
		want time.Duration
	}{
		{"a dropped connection waits the default", errors.New("reset"), time.Second},
		{"Retry-After is honoured", &StatusError{Status: http.StatusBadGateway, RetryAfter: 7 * time.Second}, 7 * time.Second},
		{"a rate limit waits for the refill", &StatusError{Status: http.StatusTooManyRequests, Reset: soon}, -1},
		// Both services send the reset on every answer: a server error near
		// the end of the window must not wait for it.
		{"a server error does not wait for the refill", &StatusError{Status: http.StatusBadGateway, Reset: soon}, time.Second},
		{"a refill too far off is not waited for", &StatusError{Status: http.StatusTooManyRequests, Reset: time.Now().Add(time.Hour)}, time.Second},
		{"a refill in the past is not waited for", &StatusError{Status: http.StatusTooManyRequests, Reset: time.Now().Add(-time.Minute)}, time.Second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := policy(time.Second).backoff(testCase.err)
			if testCase.want == -1 {
				if got <= 50*time.Second || got > time.Minute {
					t.Errorf("waits %s, want about a minute", got)
				}
				return
			}
			if got != testCase.want {
				t.Errorf("waits %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestFromResponseReadsTheHeaders(t *testing.T) {
	when := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		headers map[string]string
		want    StatusError
	}{
		{"nothing said", nil, StatusError{}},
		{"GitHub's reset, in Unix seconds",
			map[string]string{"x-ratelimit-reset": strconv.FormatInt(when.Unix(), 10), "x-ratelimit-remaining": "0"},
			StatusError{Reset: when, RateLimited: true}},
		{"Mastodon's reset, as a timestamp",
			map[string]string{"X-RateLimit-Reset": when.Format(time.RFC3339Nano), "X-RateLimit-Remaining": "12"},
			StatusError{Reset: when}},
		{"a reset that cannot be read", map[string]string{"X-RateLimit-Reset": "soon"}, StatusError{}},
		{"Retry-After in seconds", map[string]string{"Retry-After": "3"}, StatusError{RetryAfter: 3 * time.Second}},
		{"Retry-After as a date is ignored", map[string]string{"Retry-After": "Wed, 23 Sep 2026 12:00:00 GMT"}, StatusError{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
			for key, value := range testCase.headers {
				response.Header.Set(key, value)
			}
			got := FromResponse("GitHub", response, []byte("  refused \n"))
			want := testCase.want
			want.Service, want.Status, want.Body = "GitHub", http.StatusForbidden, "refused"
			if !got.Reset.Equal(want.Reset) {
				t.Errorf("Reset = %v, want %v", got.Reset, want.Reset)
			}
			got.Reset, want.Reset = time.Time{}, time.Time{}
			if *got != want {
				t.Errorf("got %+v, want %+v", *got, want)
			}
		})
	}
}

func TestTheMessageNamesTheServiceAndCutsTheBody(t *testing.T) {
	err := &StatusError{Service: "Mastodon", Status: http.StatusBadRequest, Body: strings.Repeat("x", 300)}
	message := err.Error()
	if !strings.HasPrefix(message, "Mastodon answered 400: ") {
		t.Errorf("message = %q, want it to name the service and the status", message)
	}
	if !strings.HasSuffix(message, strings.Repeat("x", 200)+"...") || strings.Contains(message, strings.Repeat("x", 201)) {
		t.Errorf("the body was not cut at 200 characters: %q", message)
	}
}

func TestIsStatus(t *testing.T) {
	err := errors.Join(errors.New("context"), &StatusError{Status: http.StatusNotFound})
	if !IsStatus(err, http.StatusNotFound) || IsStatus(err, http.StatusForbidden) || IsStatus(errors.New("x"), http.StatusNotFound) {
		t.Error("IsStatus does not read a wrapped status")
	}
}
