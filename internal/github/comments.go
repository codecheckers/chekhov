package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// Reading and editing the bot's own comments.
//
// The bot has no database, so the per-check roles live in a comment it posted;
// see internal/people/record.go, which does not trust a bot-authored comment
// merely for being one. Note that a comment by the bot is not beyond everyone:
// a repository collaborator can edit it, GitHub keeping the bot as its author.

// commentsPerPage is GitHub's maximum page size for issue comments.
const commentsPerPage = 100

// A Comment is one comment on an issue, as much of it as the bot needs.
type Comment struct {
	ID     int64
	Author string
	Body   string
}

// Comments returns the comments of an issue, oldest first.
func (c *Client) Comments(ctx context.Context, repository string, issue int) ([]Comment, error) {
	if err := c.mine(repository, "read the comments"); err != nil {
		return nil, err
	}

	var comments []Comment
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=%d",
		strings.TrimSuffix(c.BaseURL, "/"), repository, issue, commentsPerPage)
	what := fmt.Sprintf("reading the comments of %s#%d", repository, issue)

	type comment struct {
		ID   int64 `json:"id"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Body string `json:"body"`
	}
	err := paged(ctx, c, what, url, commentsPerPage, func(batch []comment) {
		for _, read := range batch {
			comments = append(comments, Comment{ID: read.ID, Author: read.User.Login, Body: read.Body})
		}
	})
	if err != nil {
		return nil, fmt.Errorf("could not read the comments of %s#%d: %w", repository, issue, err)
	}
	return comments, nil
}

// Post writes a comment exactly, without the signature a reply carries and
// without truncating it.
//
// The record of a check is read back byte for byte, so nothing may be appended to
// it or cut off it; Comment is for replies, which are read by people.
func (c *Client) Post(ctx context.Context, repository string, issue int, body string) (int64, error) {
	if err := c.mine(repository, "comment"); err != nil {
		return 0, err
	}
	if issue <= 0 {
		return 0, fmt.Errorf("refusing to comment on issue number %d", issue)
	}
	if len(body) > MaxCommentLength {
		return 0, fmt.Errorf("refusing to post %d characters, over GitHub's limit of %d",
			len(body), MaxCommentLength)
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments",
		strings.TrimSuffix(c.BaseURL, "/"), repository, issue)

	var id int64
	err = c.attempt(ctx, fmt.Sprintf("posting a record on %s#%d", repository, issue), func() error {
		var err error
		id, err = c.post(ctx, url, payload)
		return err
	})
	return id, err
}

// Edit rewrites a comment the bot posted, exactly.
//
// Unlike a reply, this does not truncate: what it writes is read back as a
// record, and the record is at the end of the body. A comment that would be
// cut is refused, because a truncated record is a lost one and the caller has
// to know that rather than be told the edit succeeded.
//
// GitHub refuses an edit by anyone without write access to the repository, so
// a comment by the bot is safe from a passer-by - but not from a collaborator,
// who may edit it with the bot left as its author.
func (c *Client) Edit(ctx context.Context, repository string, comment int64, body string) error {
	if err := c.mine(repository, "edit a comment"); err != nil {
		return err
	}
	if len(body) > MaxCommentLength {
		return fmt.Errorf("refusing to edit comment %d: %d characters is over GitHub's limit of %d, "+
			"and cutting it would lose what is at the end", comment, len(body), MaxCommentLength)
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/repos/%s/issues/comments/%d",
		strings.TrimSuffix(c.BaseURL, "/"), repository, comment)

	return c.attempt(ctx, fmt.Sprintf("editing comment %d on %s", comment, repository), func() error {
		_, err := c.do(ctx, http.MethodPatch, url, payload)
		return err
	})
}

// Assign puts a handle in the issue's assignees, so that a role is visible
// where people look rather than only in a comment. It returns who is assigned
// afterwards: GitHub silently ignores an assignee who has no access to the
// repository, so the caller is told what actually stuck.
func (c *Client) Assign(ctx context.Context, repository string, issue int, handle string) ([]string, error) {
	raw, err := c.assignees(ctx, http.MethodPost, repository, issue, handle)
	if err != nil {
		return nil, err
	}
	var state struct {
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("the assignees could not be read back: %w", err)
	}
	assigned := make([]string, 0, len(state.Assignees))
	for _, assignee := range state.Assignees {
		assigned = append(assigned, assignee.Login)
	}
	return assigned, nil
}

// Unassign takes a handle out of the issue's assignees.
func (c *Client) Unassign(ctx context.Context, repository string, issue int, handle string) error {
	_, err := c.assignees(ctx, http.MethodDelete, repository, issue, handle)
	return err
}

// assignees adds or removes one assignee, which are the same request with a
// different verb.
func (c *Client) assignees(ctx context.Context, method, repository string, issue int, handle string) ([]byte, error) {
	what := "assign"
	if method == http.MethodDelete {
		what = "unassign"
	}
	return c.writeIssue(ctx, method, repository, issue, "/assignees", what+" "+handle,
		map[string][]string{"assignees": {strings.TrimPrefix(strings.TrimSpace(handle), "@")}})
}

// AddLabel puts a label on an issue, leaving the labels it has.
//
// Only a label in AddableLabels: the labels are the register's, and the bot
// changes one only where the settings say it may. GitHub creates a label the
// repository does not have yet, so the list is also what keeps a typo from
// becoming a label.
func (c *Client) AddLabel(ctx context.Context, repository string, issue int, label string) error {
	if err := allowed(label, c.AddableLabels, "add"); err != nil {
		return err
	}
	_, err := c.writeIssue(ctx, http.MethodPost, repository, issue, "/labels", "label",
		map[string][]string{"labels": {label}})
	return err
}

// RemoveLabel takes a label off an issue, only one in RemovableLabels. A
// label the issue does not carry is not an error: GitHub answers 404, and the
// issue is as asked.
func (c *Client) RemoveLabel(ctx context.Context, repository string, issue int, label string) error {
	if err := allowed(label, c.RemovableLabels, "remove"); err != nil {
		return err
	}
	_, err := c.writeIssue(ctx, http.MethodDelete, repository, issue,
		"/labels/"+url.PathEscape(label), "unlabel", nil)
	if httpretry.IsStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// allowed refuses a label change the settings do not list, before any request.
// The name is quoted with %q because it reaches a posted comment, where it is
// somebody's free text.
func allowed(label string, list []string, verb string) error {
	for _, name := range list {
		if strings.EqualFold(strings.TrimSpace(label), name) {
			return nil
		}
	}
	return fmt.Errorf("refusing to %s the label %q: it is not in labels.managed.%s", verb, label, verb)
}

// SetTitle changes the title of an issue, and nothing else about it.
func (c *Client) SetTitle(ctx context.Context, repository string, issue int, title string) error {
	_, err := c.writeIssue(ctx, http.MethodPatch, repository, issue, "", "retitle",
		map[string]string{"title": title})
	return err
}

// writeIssue is one write to an issue of the configured repository, or to
// what hangs off it at path: the fence, the payload, the retries, once.
func (c *Client) writeIssue(ctx context.Context, method, repository string, issue int,
	path, what string, payload any) ([]byte, error) {
	if err := c.mine(repository, what); err != nil {
		return nil, err
	}
	// A nil payload is a request with no body, a DELETE's; marshalled it
	// would be the word null.
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return nil, err
		}
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d%s", strings.TrimSuffix(c.BaseURL, "/"), repository, issue, path)

	var raw []byte
	err := c.attempt(ctx, fmt.Sprintf("%s on %s#%d", what, repository, issue), func() error {
		var err error
		raw, err = c.do(ctx, method, endpoint, body)
		return err
	})
	return raw, err
}
