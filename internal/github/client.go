// Package github posts what the bot has to say.
//
// It is the one place that writes to GitHub. Rate limiting, truncation and the
// question "did this actually post" are answered here rather than once per
// command, and so is the rule that the bot writes to one repository and no
// other.
//
// Reading is elsewhere: internal/check talks to the API too, but only to ask
// questions, and caches the answers for a run.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxCommentLength is GitHub's limit on the body of an issue comment.
const MaxCommentLength = 65536

// Client posts comments as the bot account.
type Client struct {
	HTTP  *http.Client
	Token string
	// BaseURL is the API root, https://api.github.com unless a test says
	// otherwise.
	BaseURL string
	// Repository is the only repository this client will write to, owner/repo.
	Repository string
	// Signature is appended to every comment: which bot, which build, which
	// register. A reply read years later has to say what answered it.
	Signature string
	Logger    *slog.Logger
	// RetryWait is how long to wait before the one retry, unless GitHub sends
	// a Retry-After of its own.
	RetryWait time.Duration

	// tokenExpiry is what GitHub last said about the token's lifetime. A
	// fine-grained token expires, and a bot that goes quiet on a Monday is
	// hard to diagnose from the outside.
	mu          sync.Mutex
	tokenExpiry string
}

// New builds a client for one repository.
func New(token, repository, signature string) *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		Token:      token,
		BaseURL:    "https://api.github.com",
		Repository: repository,
		Signature:  signature,
		Logger:     slog.Default(),
		RetryWait:  time.Second,
	}
}

// Comment posts a comment on an issue of the configured repository and returns
// the identifier of the comment it created.
//
// Repository is a parameter as well as a field so that every caller has to say
// which repository it thinks it is writing to, and a mismatch is refused
// before the request rather than discovered in the register afterwards.
func (c *Client) Comment(ctx context.Context, repository string, issue int, body string) (int64, error) {
	if repository != c.Repository {
		return 0, fmt.Errorf("refusing to comment on %s: this bot writes to %s only",
			repository, c.Repository)
	}
	if issue <= 0 {
		return 0, fmt.Errorf("refusing to comment on issue number %d", issue)
	}

	payload, err := json.Marshal(map[string]string{"body": c.compose(body)})
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", strings.TrimSuffix(c.BaseURL, "/"), repository, issue)

	// One retry: a comment lost to a hiccup is a codechecker waiting for an
	// answer that never comes, and posting twice is a smaller sin than that.
	var lastErr error
	for attempt := range 2 {
		if attempt > 0 {
			if err := wait(ctx, c.backoff(lastErr)); err != nil {
				return 0, err
			}
		}
		id, err := c.post(ctx, url, payload)
		if err == nil {
			return id, nil
		}
		lastErr = err
		c.Logger.Warn("posting a comment failed",
			"repository", repository, "issue", issue, "attempt", attempt+1, "error", err)
		if !retryable(err) {
			break
		}
	}
	return 0, fmt.Errorf("could not comment on %s#%d: %w", repository, issue, lastErr)
}

func (c *Client) post(ctx context.Context, url string, payload []byte) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}

	response, err := c.HTTP.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	c.noteTokenExpiry(response)
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, &statusError{
			Status:     response.StatusCode,
			Body:       strings.TrimSpace(string(raw)),
			RetryAfter: retryAfter(response),
		}
	}

	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return 0, fmt.Errorf("the comment posted but the answer could not be read: %w", err)
	}
	return created.ID, nil
}

// compose puts the signature under the reply and keeps the whole within
// GitHub's limit, cutting at a line boundary and saying so.
func (c *Client) compose(body string) string {
	body = strings.TrimRight(body, "\n")
	footer := ""
	if c.Signature != "" {
		footer = "\n\n---\n" + c.Signature + "\n"
	}

	budget := MaxCommentLength - len(footer)
	if len(body) > budget {
		notice := "\n\n*(this reply was too long for a GitHub comment and was cut here)*"
		body = cutAtLine(body, budget-len(notice)) + notice
	}
	return body + footer
}

// cutAtLine trims to at most limit bytes, at the last line boundary before it,
// so that a truncated table or list does not end mid-word.
func cutAtLine(body string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(body) <= limit {
		return body
	}
	cut := body[:limit]
	if boundary := strings.LastIndex(cut, "\n"); boundary > 0 {
		return cut[:boundary]
	}
	return cut
}

// TokenExpiry is what GitHub last said about when the token stops working,
// empty when the token does not expire or nothing has been posted yet.
func (c *Client) TokenExpiry() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokenExpiry
}

// noteTokenExpiry remembers the expiry header and warns when it is close.
func (c *Client) noteTokenExpiry(response *http.Response) {
	expiry := response.Header.Get("github-authentication-token-expiration")
	if expiry == "" {
		return
	}
	c.mu.Lock()
	c.tokenExpiry = expiry
	c.mu.Unlock()

	// The header is "2026-12-24 15:04:05 UTC", and GitHub has changed its
	// shape before; a date that cannot be read is not worth an error.
	when, err := time.Parse("2006-01-02 15:04:05 MST", expiry)
	if err != nil {
		return
	}
	if time.Until(when) < 14*24*time.Hour {
		c.Logger.Warn("the GitHub token expires soon, see docs/github-token.md",
			"expires", expiry)
	}
}

// statusError is a request GitHub refused.
type statusError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *statusError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return fmt.Sprintf("GitHub answered %d: %s", e.Status, body)
}

// retryable says whether trying again could work. A refusal - the token is
// wrong, the issue is locked - will be refused again.
func retryable(err error) bool {
	var status *statusError
	if !errors.As(err, &status) {
		return true // a connection that failed may succeed
	}
	switch {
	case status.Status == http.StatusTooManyRequests:
		return true
	case status.Status == http.StatusForbidden:
		// GitHub answers 403 both for "not now" and for "not ever", and the
		// difference is in the body: the secondary rate limit says so, while a
		// token without the right permission will say the same thing however
		// often it is asked.
		return status.rateLimited()
	case status.Status >= 500:
		return true
	default:
		return false
	}
}

// rateLimited reports whether a refusal is a rate limit rather than a
// permission. Observed in the live deployment: a token lacking Issues: write
// answers 403 "Resource not accessible by personal access token", which was
// retried once for nothing.
func (e *statusError) rateLimited() bool {
	if e.RetryAfter > 0 {
		return true
	}
	body := strings.ToLower(e.Body)
	return strings.Contains(body, "secondary rate limit") || strings.Contains(body, "abuse")
}

// backoff is how long to wait before the retry, honouring Retry-After when
// GitHub sends one.
func (c *Client) backoff(err error) time.Duration {
	var status *statusError
	if errors.As(err, &status) && status.RetryAfter > 0 {
		return status.RetryAfter
	}
	return c.RetryWait
}

func retryAfter(response *http.Response) time.Duration {
	value := response.Header.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
