package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Reading the issues of the register.
//
// Two questions, both asked by `codecheckers suggest`: what is this check
// about, when nobody said, and who is busy. Reading only - the issues belong to
// the codecheckers who opened them, and the bot's business with them is its own
// comments, which are in comments.go.

// issuesPerPage is GitHub's maximum page size for issues.
const issuesPerPage = 100

// An Issue is a checks issue, as much of it as a command needs.
type Issue struct {
	Number int
	Title  string
	Body   string
	// Author is who opened the issue: on a checks issue, most often an
	// author of the paper, and somebody on the check whatever role they hold.
	Author    string
	Assignees []string
	// Labels are the register's own labels on this check, which say things
	// about it nothing else does: `institution` says an arrangement covers
	// it, which decides which team a codechecker on it belongs in.
	Labels []string
	// Closed says the check is over. A question about what still has to
	// happen on a check - who should be doing it, whether the queue can see
	// it - has no business being asked about one that is finished.
	Closed bool
}

// Issue reads one issue of the configured repository.
func (c *Client) Issue(ctx context.Context, repository string, issue int) (Issue, error) {
	if err := c.mine(repository, "read the issue"); err != nil {
		return Issue{}, err
	}

	url := fmt.Sprintf("%s/repos/%s/issues/%d", strings.TrimSuffix(c.BaseURL, "/"), repository, issue)
	what := fmt.Sprintf("reading %s#%d", repository, issue)

	var read wireIssue
	err := c.attempt(ctx, what, func() error {
		raw, err := c.do(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &read)
	})
	if err != nil {
		return Issue{}, fmt.Errorf("could not read %s#%d: %w", repository, issue, err)
	}
	return read.issue(), nil
}

// OpenIssues returns the open issues of the configured repository, pull
// requests left out: a pull request is not a check, and GitHub serves both
// from this endpoint.
func (c *Client) OpenIssues(ctx context.Context, repository string) ([]Issue, error) {
	return c.issues(ctx, repository, "open", "")
}

// AllIssues returns every issue of the configured repository, open and
// closed, pull requests left out. A closed check still holds its certificate
// identifier: an abandoned one leaves its number unused, never free.
func (c *Client) AllIssues(ctx context.Context, repository string) ([]Issue, error) {
	return c.issues(ctx, repository, "all", "")
}

// ClosedIssuesSince returns the closed issues of the configured repository
// that changed since a time, pull requests left out. The register pull
// request closes a check, and an archive reminder (#27) is due after that:
// "changed since" keeps the walk to the checks somebody is still talking
// about, and a reminder is itself a change, so a check being reminded stays
// in it.
func (c *Client) ClosedIssuesSince(ctx context.Context, repository string, since time.Time) ([]Issue, error) {
	return c.issues(ctx, repository, "closed", "&since="+since.UTC().Format(time.RFC3339))
}

func (c *Client) issues(ctx context.Context, repository, state, query string) ([]Issue, error) {
	if err := c.mine(repository, "read the issues"); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/repos/%s/issues?state=%s&per_page=%d%s",
		strings.TrimSuffix(c.BaseURL, "/"), repository, state, issuesPerPage, query)
	what := fmt.Sprintf("reading the %s issues of %s", state, repository)

	var issues []Issue
	err := paged(ctx, c, what, url, issuesPerPage, func(batch []wireIssue) {
		for _, read := range batch {
			if read.PullRequest == nil {
				issues = append(issues, read.issue())
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("could not read the %s issues of %s: %w", state, repository, err)
	}
	return issues, nil
}

// wireIssue is what GitHub sends, named apart from the Issue a caller sees.
type wireIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	// State is "open" or "closed".
	State string `json:"state"`
	// PullRequest is present, and non-nil, only on a pull request.
	PullRequest *struct{} `json:"pull_request"`
}

func (i wireIssue) issue() Issue {
	read := Issue{Number: i.Number, Title: i.Title, Body: i.Body, Author: i.User.Login,
		Closed: strings.EqualFold(i.State, "closed")}
	for _, assignee := range i.Assignees {
		read.Assignees = append(read.Assignees, assignee.Login)
	}
	for _, label := range i.Labels {
		if label.Name != "" {
			read.Labels = append(read.Labels, label.Name)
		}
	}
	return read
}

// eventsPerPage is GitHub's maximum page size for an issue's events.
const eventsPerPage = 100

// LabeledAt is when a label was last put on an issue, zero when it never was.
// The newest time, because a label taken off and put back - a codechecker
// who withdrew - starts the wait again. See codecheckers/chekhov#25.
func (c *Client) LabeledAt(ctx context.Context, repository string, issue int, label string) (time.Time, error) {
	if err := c.mine(repository, "read the events of an issue"); err != nil {
		return time.Time{}, err
	}

	url := fmt.Sprintf("%s/repos/%s/issues/%d/events?per_page=%d",
		strings.TrimSuffix(c.BaseURL, "/"), repository, issue, eventsPerPage)
	what := fmt.Sprintf("reading the events of %s#%d", repository, issue)

	type event struct {
		Event string `json:"event"`
		Label struct {
			Name string `json:"name"`
		} `json:"label"`
		Created time.Time `json:"created_at"`
	}
	var newest time.Time
	err := paged(ctx, c, what, url, eventsPerPage, func(batch []event) {
		for _, read := range batch {
			if read.Event == "labeled" && strings.EqualFold(read.Label.Name, label) && read.Created.After(newest) {
				newest = read.Created
			}
		}
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("could not read the events of %s#%d: %w", repository, issue, err)
	}
	return newest, nil
}
