package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// The pull request that adds a certificate's row to the register (#21).
//
// The one place the bot writes anything but an issue of the register: a
// commit on a branch of its own, and a pull request from it. Fenced like
// team.go - here, and again in guard.go, which reads the bodies of these
// requests because they, not their paths, say which branch and which file -
// since a token that can write a file can write any file:
//
//   - the branch is register-YYYY-NNN, built from a certificate identifier
//     checked here, so it can never name the default branch;
//   - the commit changes the configured register file and nothing else, on
//     top of the default branch;
//   - a branch with a commit by anybody but the bot is somebody's work, and is
//     refused rather than replaced;
//   - nothing is merged, and nothing is deleted: an editor reviews and
//     merges, which is the point of a pull request.
//
// The commit is built with the git data API - a tree on the default branch's,
// a commit with the default branch as its parent - and the branch moved once,
// onto it. Resetting the branch to the default branch and committing on top
// would leave an open pull request for a moment with nothing in it, which
// GitHub answers by closing it.

// registerBranch is a branch the bot writes: register-YYYY-NNN. Repeated from
// the certificate identifier the rules check rather than imported, as
// handleShape is in team.go: the fence must not depend on another package
// agreeing with it.
var registerBranch = regexp.MustCompile(`^register-[0-9]{4}-[0-9]{3}$`)

// A RowProposal is the row to add, and what to say about it.
type RowProposal struct {
	// Row is the value of each column by its name in the file's header. A
	// column the file has and Row does not is left empty, so the row is
	// written in the file's own shape, whatever that is. Its Certificate
	// names the branch and is checked against the file.
	Row map[string]string
	// Title and Body are the pull request's, and Message the commit's.
	Title, Body, Message string
}

// A RowPullRequest is what ProposeRow left behind.
type RowPullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// Updated says the pull request was there already and has been brought
	// up to date, rather than opened; Unchanged that it already proposed
	// exactly this, and nothing was written.
	Updated   bool `json:"-"`
	Unchanged bool `json:"-"`
}

// ErrAlreadyRegistered is a certificate the default branch's register file
// already has a row for.
var ErrAlreadyRegistered = errors.New("the certificate is already in the register file")

// ErrBranchNotOurs is a register branch with commits by somebody other than
// the bot, which a second `register` would otherwise throw away.
var ErrBranchNotOurs = errors.New("the branch has commits by somebody else")

// ProposeRow opens a pull request adding one row to the register file, or
// brings the one it opened before up to date: the row is added to the default
// branch as it is now, so a register that moved on in between is the one the
// row is added to. A branch that already holds exactly that is left alone.
func (c *Client) ProposeRow(ctx context.Context, repository string, proposal RowProposal) (RowPullRequest, error) {
	if err := c.mine(repository, "propose a row"); err != nil {
		return RowPullRequest{}, err
	}
	if c.RegisterFile == "" || c.Account == "" {
		return RowPullRequest{}, refused("no register file or no account of the bot's own is configured, " +
			"so I write none: without the account a branch of mine cannot be told from somebody's work")
	}
	branch := "register-" + proposal.Row["Certificate"]
	if !registerBranch.MatchString(branch) {
		return RowPullRequest{}, fmt.Errorf("%q is not a certificate identifier", proposal.Row["Certificate"])
	}
	api := strings.TrimSuffix(c.BaseURL, "/") + "/repos/" + repository

	var about struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.read(ctx, api, &about); err != nil {
		return RowPullRequest{}, err
	}
	base, err := c.refSHA(ctx, api, about.DefaultBranch)
	if err != nil {
		return RowPullRequest{}, err
	}
	var baseCommit gitCommit
	if err := c.read(ctx, api+"/git/commits/"+base, &baseCommit); err != nil {
		return RowPullRequest{}, err
	}
	// The file as that commit has it, not as the branch has it by the time
	// the request arrives: one snapshot, whatever is merged meanwhile.
	text, err := c.registerFile(ctx, api, base)
	if err != nil {
		return RowPullRequest{}, err
	}
	content, err := withRow(text, proposal.Row)
	if err != nil {
		return RowPullRequest{}, err
	}

	current, err := c.refSHA(ctx, api, branch)
	exists := err == nil
	if err != nil && !httpretry.IsStatus(err, http.StatusNotFound) {
		return RowPullRequest{}, err
	}
	if exists {
		if err := c.onlyOurs(ctx, api, about.DefaultBranch, branch); err != nil {
			return RowPullRequest{}, err
		}
	}
	held := exists && c.holds(ctx, api, current, base, content)
	if !held {
		commit, err := c.commitRow(ctx, api, baseCommit.Tree.SHA, base, content, proposal.Message)
		if err != nil {
			return RowPullRequest{}, err
		}
		if err := c.moveBranch(ctx, api, branch, commit, exists); err != nil {
			return RowPullRequest{}, err
		}
	}
	pull, err := c.pullRequest(ctx, api, repository, about.DefaultBranch, branch, proposal)
	pull.Unchanged = held && pull.Unchanged
	return pull, err
}

// gitCommit is as much of a commit as the writer needs.
type gitCommit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

// refSHA is the commit a branch points at.
func (c *Client) refSHA(ctx context.Context, api, branch string) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.read(ctx, api+"/git/ref/heads/"+url.PathEscape(branch), &ref); err != nil {
		return "", err
	}
	return ref.Object.SHA, nil
}

// registerFile is the register file as one commit has it.
func (c *Client) registerFile(ctx context.Context, api, ref string) (string, error) {
	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.read(ctx, api+"/contents/"+escapePath(c.RegisterFile)+"?ref="+url.QueryEscape(ref), &file); err != nil {
		return "", err
	}
	if file.Encoding != "base64" {
		return "", fmt.Errorf("%s came back as %q, not base64: too large to edit through the API",
			c.RegisterFile, file.Encoding)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("%s could not be decoded: %w", c.RegisterFile, err)
	}
	return string(raw), nil
}

// escapePath escapes each segment of a path in the repository, so that a
// register file in a directory is still the path GitHub and the guard see.
func escapePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// withRow is the file with the row appended in the order of its own header,
// in its own line endings. The copy is the default branch's as it is now, so
// a certificate somebody registered since the bot last read the register is
// caught here.
func withRow(text string, row map[string]string) (string, error) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comment = '#'
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("the register file is not CSV I can read: %w", err)
	}
	if len(records) == 0 {
		return "", errors.New("the register file is empty")
	}
	header := make([]string, len(records[0]))
	for i, column := range records[0] {
		header[i] = strings.TrimSpace(strings.TrimPrefix(column, "\ufeff"))
	}
	certificate := slices.Index(header, "Certificate")
	if certificate < 0 {
		return "", errors.New("the register file has no Certificate column")
	}
	for _, record := range records[1:] {
		if certificate < len(record) && strings.TrimSpace(record[certificate]) == row["Certificate"] {
			return "", ErrAlreadyRegistered
		}
	}

	values := make([]string, len(header))
	for i, column := range header {
		values[i] = row[column]
	}
	var line bytes.Buffer
	writer := csv.NewWriter(&line)
	writer.UseCRLF = strings.Contains(text, "\r\n")
	if err := writer.Write(values); err != nil {
		return "", err
	}
	writer.Flush()
	// A file that does not end in a newline - the testing register's does
	// not - would otherwise have the row glued to its last line.
	if text != "" && !strings.HasSuffix(text, "\n") {
		if writer.UseCRLF {
			text += "\r\n"
		} else {
			text += "\n"
		}
	}
	return text + line.String(), nil
}

// holds reports whether the branch already is the row on the base: its one
// commit sits on base and its file is the content. Asking again with nothing
// changed then writes nothing, rather than a commit that changes nothing.
func (c *Client) holds(ctx context.Context, api, head, base, content string) bool {
	var commit gitCommit
	if err := c.read(ctx, api+"/git/commits/"+head, &commit); err != nil {
		return false
	}
	if len(commit.Parents) != 1 || commit.Parents[0].SHA != base {
		return false
	}
	text, err := c.registerFile(ctx, api, head)
	return err == nil && text == content
}

// commitRow makes the commit: a tree that is base's with the register file
// replaced, and a commit of it whose parent is base. Neither moves a branch.
func (c *Client) commitRow(ctx context.Context, api, baseTree, base, content, message string) (string, error) {
	var tree struct {
		SHA string `json:"sha"`
	}
	err := c.write(ctx, http.MethodPost, api+"/git/trees", map[string]any{
		"base_tree": baseTree,
		"tree": []map[string]string{
			{"path": c.RegisterFile, "mode": "100644", "type": "blob", "content": content},
		},
	}, &tree)
	if err != nil {
		return "", fmt.Errorf("could not write %s: %w", c.RegisterFile, err)
	}
	var commit gitCommit
	err = c.write(ctx, http.MethodPost, api+"/git/commits", map[string]any{
		"message": message, "tree": tree.SHA, "parents": []string{base},
	}, &commit)
	if err != nil {
		return "", fmt.Errorf("could not commit %s: %w", c.RegisterFile, err)
	}
	return commit.SHA, nil
}

// moveBranch points the branch at the commit, creating it when it is not
// there.
func (c *Client) moveBranch(ctx context.Context, api, branch, commit string, exists bool) error {
	if exists {
		return c.write(ctx, http.MethodPatch, api+"/git/refs/heads/"+url.PathEscape(branch),
			map[string]any{"sha": commit, "force": true}, nil)
	}
	err := c.write(ctx, http.MethodPost, api+"/git/refs",
		map[string]string{"ref": "refs/heads/" + branch, "sha": commit}, nil)
	// A create that was retried after it landed is answered "Reference
	// already exists"; the branch being where it should is what counts.
	return landed(err, func() bool {
		now, err := c.refSHA(ctx, api, branch)
		return err == nil && now == commit
	})
}

// landed forgives a write that failed when a look afterwards finds it done:
// the retry of a write whose first try arrived is answered as a conflict.
func landed(err error, done func() bool) error {
	if err != nil && done() {
		return nil
	}
	return err
}

// onlyOurs refuses a branch with a commit the bot did not make. Commits by
// the bot are its own earlier rows, which it may replace; anybody else's is
// an editor's fix it must not throw away.
func (c *Client) onlyOurs(ctx context.Context, api, defaultBranch, branch string) error {
	var compared struct {
		Commits []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			Parents []struct{} `json:"parents"`
		} `json:"commits"`
	}
	path := api + "/compare/" + url.PathEscape(defaultBranch) + "..." + url.PathEscape(branch)
	if err := c.read(ctx, path, &compared); err != nil {
		return err
	}
	for _, commit := range compared.Commits {
		// A merge brings the default branch in - GitHub's "Update branch" -
		// and is nobody's change to the row; the row is rebuilt on the
		// default branch as it is now anyway.
		if len(commit.Parents) > 1 {
			continue
		}
		if commit.Author == nil || !strings.EqualFold(commit.Author.Login, c.Account) {
			return fmt.Errorf("%w: %s", ErrBranchNotOurs, branch)
		}
	}
	return nil
}

// pullRequest opens the pull request, or updates the one that is open.
func (c *Client) pullRequest(ctx context.Context, api, repository, defaultBranch, branch string,
	proposal RowProposal) (RowPullRequest, error) {
	owner, _, _ := strings.Cut(repository, "/")
	open := func() (RowPullRequest, error) {
		var pulls []RowPullRequest
		// By head and base both: a pull request from the branch into
		// somewhere else is not the one adding the row.
		err := c.read(ctx, api+"/pulls?state=open&head="+url.QueryEscape(owner+":"+branch)+
			"&base="+url.QueryEscape(defaultBranch), &pulls)
		if err != nil || len(pulls) == 0 {
			return RowPullRequest{}, err
		}
		return pulls[0], nil
	}

	existing, err := open()
	if err != nil {
		return RowPullRequest{}, err
	}
	if existing.Number != 0 {
		existing.Updated = true
		if existing.Title == proposal.Title && existing.Body == proposal.Body {
			existing.Unchanged = true
			return existing, nil
		}
		return existing, c.write(ctx, http.MethodPatch, fmt.Sprintf("%s/pulls/%d", api, existing.Number),
			map[string]string{"title": proposal.Title, "body": proposal.Body}, nil)
	}

	var created RowPullRequest
	err = c.write(ctx, http.MethodPost, api+"/pulls", map[string]string{
		"title": proposal.Title, "head": branch, "base": defaultBranch, "body": proposal.Body,
	}, &created)
	err = landed(err, func() bool {
		// Opened by the first try of a retried request: "A pull request
		// already exists".
		created, _ = open()
		return created.Number != 0
	})
	if err != nil {
		return RowPullRequest{}, fmt.Errorf("could not open the pull request: %w", err)
	}
	return created, nil
}
