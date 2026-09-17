// Package mastodon posts the bot's announcements.
//
// It is the one place that writes to Mastodon, in the same shape as
// internal/github: the rule that a development deployment posts nothing
// public, the retry, and the question "did this actually post" are answered
// here rather than once per caller.
package mastodon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client posts statuses as one account.
type Client struct {
	HTTP  *http.Client
	Token string
	// BaseURL is the instance, https://fediscience.org.
	BaseURL string
	// Account is the account the token must belong to, e.g. codecheck. A token
	// of another account is refused, so that a token pasted into the wrong
	// deployment cannot post in someone else's name.
	Account string
	// Visibility is the only visibility this client posts with. A development
	// deployment sets "direct", and a status asking for anything else is
	// refused before a request is made.
	Visibility string
	Logger     *slog.Logger
	// RetryWait is how long to wait before the one retry, unless the instance
	// says how long itself.
	RetryWait time.Duration
	// MediaWait bounds how long an upload may stay in processing.
	MediaWait time.Duration
}

// New builds a client for one account on one instance, posting with one
// visibility.
func New(baseURL, account, token, visibility string) *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 60 * time.Second},
		Token:      token,
		BaseURL:    strings.TrimSuffix(baseURL, "/"),
		Account:    strings.TrimPrefix(account, "@"),
		Visibility: visibility,
		Logger:     slog.Default(),
		RetryWait:  2 * time.Second,
		MediaWait:  time.Minute,
	}
}

// A Status is what the bot posts.
type Status struct {
	Text       string
	MediaIDs   []string
	Visibility string
	// IdempotencyKey makes a repeated request return the first status rather
	// than post a second one. Mastodon keeps it for an hour.
	IdempotencyKey string
}

// Posted is a status as the instance reports it.
type Posted struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Limits are what the instance allows, from /api/v2/instance.
type Limits struct {
	MaxCharacters    int
	CharactersPerURL int
	ImageSizeLimit   int64
}

// Post publishes a status and returns it.
func (c *Client) Post(ctx context.Context, status Status) (Posted, error) {
	if status.Visibility != c.Visibility {
		return Posted{}, fmt.Errorf("refusing to post with visibility %q: this deployment posts %q only",
			status.Visibility, c.Visibility)
	}
	if strings.TrimSpace(status.Text) == "" {
		return Posted{}, errors.New("refusing to post an empty status")
	}

	form := url.Values{}
	form.Set("status", status.Text)
	form.Set("visibility", status.Visibility)
	for _, id := range status.MediaIDs {
		form.Add("media_ids[]", id)
	}
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	if status.IdempotencyKey != "" {
		header.Set("Idempotency-Key", status.IdempotencyKey)
	}

	var posted Posted
	err := c.withRetry(ctx, "posting a status", func() error {
		_, err := c.do(ctx, http.MethodPost, "/api/v1/statuses", header, []byte(form.Encode()), &posted)
		return err
	})
	return posted, err
}

// UploadMedia uploads an attachment and waits until the instance has processed
// it, because a status naming an unprocessed attachment is refused.
func (c *Client) UploadMedia(ctx context.Context, name, mimeType string, content []byte, description string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, name)},
		"Content-Type":        {mimeType},
	})
	if err != nil {
		return "", err
	}
	if _, err := part.Write(content); err != nil {
		return "", err
	}
	if description != "" {
		if err := writer.WriteField("description", description); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	header := http.Header{"Content-Type": {writer.FormDataContentType()}}

	var media struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	var status int
	err = c.withRetry(ctx, "uploading media", func() error {
		status, err = c.do(ctx, http.MethodPost, "/api/v2/media", header, body.Bytes(), &media)
		return err
	})
	if err != nil {
		return "", err
	}
	// 200 is processed already; 202 is accepted and still processing, and the
	// media endpoint answers 206 until it is done.
	if status == http.StatusAccepted || media.URL == "" {
		if err := c.awaitMedia(ctx, media.ID); err != nil {
			return "", err
		}
	}
	return media.ID, nil
}

func (c *Client) awaitMedia(ctx context.Context, id string) error {
	deadline := time.Now().Add(c.MediaWait)
	for {
		status, err := c.do(ctx, http.MethodGet, "/api/v1/media/"+url.PathEscape(id), nil, nil, nil)
		if err != nil {
			return fmt.Errorf("waiting for media %s: %w", id, err)
		}
		if status == http.StatusOK {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("media %s was still processing after %s", id, c.MediaWait)
		}
		if err := wait(ctx, c.RetryWait); err != nil {
			return err
		}
	}
}

// RecentStatuses returns the account's latest statuses, newest first.
//
// With direct visibility the conversations are read as well: whether an
// account's own direct statuses appear in its timeline depends on the server,
// and the duplicate check must not depend on that.
func (c *Client) RecentStatuses(ctx context.Context, limit int) ([]Posted, error) {
	// The bot reads the recent toots before every post, so this is where a
	// token of the wrong account stops it.
	accountID, err := c.verifiedAccountID(ctx)
	if err != nil {
		return nil, err
	}

	var statuses []Posted
	// Boosts are left out: their text is the boosted toot's, and ten of them
	// would push an announcement out of the window the duplicate check reads.
	path := fmt.Sprintf("/api/v1/accounts/%s/statuses?limit=%d&exclude_reblogs=true", url.PathEscape(accountID), limit)
	if _, err := c.do(ctx, http.MethodGet, path, nil, nil, &statuses); err != nil {
		return nil, err
	}

	if c.Visibility == "direct" {
		var conversations []struct {
			LastStatus *Posted `json:"last_status"`
		}
		path := fmt.Sprintf("/api/v1/conversations?limit=%d", limit)
		if _, err := c.do(ctx, http.MethodGet, path, nil, nil, &conversations); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, status := range statuses {
			seen[status.ID] = true
		}
		for _, conversation := range conversations {
			if last := conversation.LastStatus; last != nil && !seen[last.ID] {
				statuses = append(statuses, *last)
			}
		}
	}
	return statuses, nil
}

// Limits reads what the instance allows.
func (c *Client) Limits(ctx context.Context) (Limits, error) {
	var instance struct {
		Configuration struct {
			Statuses struct {
				MaxCharacters            int `json:"max_characters"`
				CharactersReservedPerURL int `json:"characters_reserved_per_url"`
			} `json:"statuses"`
			MediaAttachments struct {
				ImageSizeLimit int64 `json:"image_size_limit"`
			} `json:"media_attachments"`
		} `json:"configuration"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/api/v2/instance", nil, nil, &instance); err != nil {
		return Limits{}, err
	}
	configuration := instance.Configuration
	return Limits{
		MaxCharacters:    configuration.Statuses.MaxCharacters,
		CharactersPerURL: configuration.Statuses.CharactersReservedPerURL,
		ImageSizeLimit:   configuration.MediaAttachments.ImageSizeLimit,
	}, nil
}

// An Account is a Mastodon account, https://docs.joinmastodon.org/entities/Account/.
type Account struct {
	ID   string `json:"id"`
	Acct string `json:"acct"`
}

// A List is one of the account's own lists, https://docs.joinmastodon.org/entities/List/.
type List struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// A Relationship says whether the token's account follows another one,
// https://docs.joinmastodon.org/entities/Relationship/.
type Relationship struct {
	ID        string `json:"id"`
	Following bool   `json:"following"`
}

// VerifyAccount checks that the token belongs to the configured account,
// refusing before a caller follows, lists or otherwise writes as the wrong
// account. RecentStatuses does this check itself, ahead of every announce;
// a caller that skips RecentStatuses - follow does - needs it explicitly.
func (c *Client) VerifyAccount(ctx context.Context) error {
	_, err := c.verifiedAccountID(ctx)
	return err
}

// verifiedAccountID reads the token's own account and refuses it if it does
// not belong to c.Account, otherwise returning its id for a caller (like
// RecentStatuses) that needs it next.
func (c *Client) verifiedAccountID(ctx context.Context) (string, error) {
	var account struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, nil, &account); err != nil {
		return "", err
	}
	if !strings.EqualFold(account.Username, c.Account) {
		return "", fmt.Errorf("the token belongs to @%s, but this deployment posts as @%s",
			account.Username, c.Account)
	}
	return account.ID, nil
}

// ResolveAccount finds the account a handle names, @user@instance.
//
// /api/v1/accounts/lookup only knows accounts this instance has already seen;
// search with resolve does the federation lookup that following someone new
// needs.
func (c *Client) ResolveAccount(ctx context.Context, handle string) (Account, error) {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	var result struct {
		Accounts []Account `json:"accounts"`
	}
	path := "/api/v2/search?type=accounts&resolve=true&limit=1&q=" + url.QueryEscape(handle)
	if _, err := c.do(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
		return Account{}, err
	}
	if len(result.Accounts) == 0 {
		return Account{}, fmt.Errorf("no account found for %s", handle)
	}
	return result.Accounts[0], nil
}

// Relationships reports, for each account id, whether the token's account
// follows it already.
func (c *Client) Relationships(ctx context.Context, accountIDs []string) ([]Relationship, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	values := url.Values{}
	for _, id := range accountIDs {
		values.Add("id[]", id)
	}
	var relationships []Relationship
	path := "/api/v1/accounts/relationships?" + values.Encode()
	if _, err := c.do(ctx, http.MethodGet, path, nil, nil, &relationships); err != nil {
		return nil, err
	}
	return relationships, nil
}

// Follow makes the token's account follow another one.
func (c *Client) Follow(ctx context.Context, accountID string) error {
	return c.withRetry(ctx, "following an account", func() error {
		_, err := c.do(ctx, http.MethodPost, "/api/v1/accounts/"+url.PathEscape(accountID)+"/follow", nil, nil, nil)
		return err
	})
}

// Lists returns the token's account's own lists.
func (c *Client) Lists(ctx context.Context) ([]List, error) {
	var lists []List
	if _, err := c.do(ctx, http.MethodGet, "/api/v1/lists", nil, nil, &lists); err != nil {
		return nil, err
	}
	return lists, nil
}

// CreateList makes a new, empty list.
func (c *Client) CreateList(ctx context.Context, title string) (List, error) {
	form := url.Values{"title": {title}}
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	var list List
	err := c.withRetry(ctx, "creating a list", func() error {
		_, err := c.do(ctx, http.MethodPost, "/api/v1/lists", header, []byte(form.Encode()), &list)
		return err
	})
	return list, err
}

// ListAccounts returns who is on a list already, so a caller can add only
// who is missing. It reads one page: Mastodon's maximum per page is 80, which
// the lists this bot maintains are not expected to outgrow by hand; paging
// past it would need to follow the response's Link header, not implemented.
func (c *Client) ListAccounts(ctx context.Context, listID string) ([]Account, error) {
	var accounts []Account
	path := "/api/v1/lists/" + url.PathEscape(listID) + "/accounts?limit=80"
	if _, err := c.do(ctx, http.MethodGet, path, nil, nil, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// AddToList adds accounts to a list. Mastodon only allows adding an account
// the token's account already follows.
func (c *Client) AddToList(ctx context.Context, listID string, accountIDs []string) error {
	if len(accountIDs) == 0 {
		return nil
	}
	form := url.Values{}
	for _, id := range accountIDs {
		form.Add("account_ids[]", id)
	}
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	return c.withRetry(ctx, "adding accounts to a list", func() error {
		_, err := c.do(ctx, http.MethodPost, "/api/v1/lists/"+url.PathEscape(listID)+"/accounts", header, []byte(form.Encode()), nil)
		return err
	})
}

// withRetry runs a write once more after a failure that may pass: a dropped
// connection, a 5xx, a rate limit. A refusal - 401, 403, 422 - is final.
func (c *Client) withRetry(ctx context.Context, what string, attempt func() error) error {
	var lastErr error
	for try := range 2 {
		if try > 0 {
			if err := wait(ctx, c.backoff(lastErr)); err != nil {
				return err
			}
		}
		lastErr = attempt()
		if lastErr == nil {
			return nil
		}
		c.Logger.Warn(what+" failed", "attempt", try+1, "error", lastErr)
		if !retryable(lastErr) {
			break
		}
	}
	return fmt.Errorf("%s: %w", what, lastErr)
}

// do sends one request and decodes a 2xx answer into `into`, when given.
func (c *Client) do(ctx context.Context, method, path string, header http.Header, body []byte, into any) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return 0, err
	}
	for key, values := range header {
		request.Header[key] = values
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "chekhov (+https://github.com/codecheckers/chekhov; the CODECHECK register bot)")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}

	response, err := c.HTTP.Do(request)
	if err != nil {
		// The URL is in the error, the token is not: it only ever travels in
		// the header.
		return 0, err
	}
	defer response.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, &StatusError{
			Status: response.StatusCode,
			Body:   strings.TrimSpace(string(raw)),
			Reset:  rateLimitReset(response),
		}
	}
	if into != nil && response.StatusCode != http.StatusPartialContent && len(raw) > 0 {
		if err := json.Unmarshal(raw, into); err != nil {
			return response.StatusCode, fmt.Errorf("the answer to %s %s could not be read: %w", method, path, err)
		}
	}
	return response.StatusCode, nil
}

// StatusError is a request the instance refused.
type StatusError struct {
	Status int
	Body   string
	// Reset is when a rate limit refills, from X-RateLimit-Reset.
	Reset time.Time
}

func (e *StatusError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return fmt.Sprintf("Mastodon answered %d: %s", e.Status, body)
}

func retryable(err error) bool {
	var status *StatusError
	if !errors.As(err, &status) {
		// A context that ended will not come back; a connection that failed may.
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return status.Status == http.StatusTooManyRequests || status.Status >= 500
}

// maxRateLimitWait is how long a retry may wait for a rate limit to refill.
// Longer than that, the editor is better told that the toot did not go out.
const maxRateLimitWait = 2 * time.Minute

func (c *Client) backoff(err error) time.Duration {
	var status *StatusError
	if errors.As(err, &status) && status.Status == http.StatusTooManyRequests {
		if until := time.Until(status.Reset); until > 0 && until <= maxRateLimitWait {
			return until
		}
	}
	return c.RetryWait
}

// rateLimitReset reads X-RateLimit-Reset, which Mastodon writes as a
// timestamp.
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
