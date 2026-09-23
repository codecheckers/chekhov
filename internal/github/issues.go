package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Reading the issues of the register.
//
// Two questions, both asked by `suggest codecheckers`: what is this check
// about, when nobody said, and who is busy. Reading only - the issues belong to
// the codecheckers who opened them, and the bot's business with them is its own
// comments, which are in comments.go.

// issuesPerPage is GitHub's maximum page size for issues.
const issuesPerPage = 100

// An Issue is a checks issue, as much of it as a command needs.
type Issue struct {
	Number    int
	Title     string
	Body      string
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
	return c.issues(ctx, repository, "open")
}

// AllIssues returns every issue of the configured repository, open and
// closed, pull requests left out. A closed check still holds its certificate
// identifier: an abandoned one leaves its number unused, never free.
func (c *Client) AllIssues(ctx context.Context, repository string) ([]Issue, error) {
	return c.issues(ctx, repository, "all")
}

func (c *Client) issues(ctx context.Context, repository, state string) ([]Issue, error) {
	if err := c.mine(repository, "read the issues"); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/repos/%s/issues?state=%s&per_page=%d",
		strings.TrimSuffix(c.BaseURL, "/"), repository, state, issuesPerPage)
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
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
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
	read := Issue{Number: i.Number, Title: i.Title, Body: i.Body,
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
