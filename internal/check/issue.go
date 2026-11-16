package check

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// IssueText is an issue's first post or one of its comments: who wrote it,
// and what. Read for `chekhov include check <issue URL>`, which checks a
// registration from the command line as the bot would on the issue
// (codecheckers/chekhov#49).
type IssueText struct {
	Author string
	Body   string
}

type githubText struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body string `json:"body"`
}

func (t githubText) text() IssueText { return IssueText{Author: t.User.Login, Body: t.Body} }

// Issue reads a public issue's first post and all its comments, oldest
// first, a page of a hundred at a time. Read through the GitHub API, with the
// token when there is one, as every other GitHub read.
func (s *Services) Issue(ctx context.Context, repository string, number int) (IssueText, []IssueText, error) {
	if !s.Enabled() {
		return IssueText{}, nil, fmt.Errorf("external services not enabled, this needs the GitHub API")
	}
	header := map[string]string{"Accept": "application/vnd.github+json"}
	if s.GitHubToken != "" {
		header["Authorization"] = "Bearer " + s.GitHubToken
	}
	base := fmt.Sprintf("%s/repos/%s/issues/%d", s.GitHub, repository, number)
	var issue githubText
	if err := s.getJSONContext(ctx, base, header, &issue); err != nil {
		return IssueText{}, nil, err
	}
	var comments []IssueText
	for page := 1; ; page++ {
		var listed []githubText
		if err := s.getJSONContext(ctx, fmt.Sprintf("%s/comments?per_page=%d&page=%d", base, commentsPerPage, page),
			header, &listed); err != nil {
			return IssueText{}, nil, err
		}
		for _, comment := range listed {
			comments = append(comments, comment.text())
		}
		if len(listed) < commentsPerPage {
			return issue.text(), comments, nil
		}
	}
}

// commentsPerPage is the most GitHub gives in one page.
const commentsPerPage = 100

// RawFile is where a repository serves a file on its default branch, as
// plain bytes.
func (s *Services) RawFile(repository, path string) string { return s.rawURL(repository, "HEAD", path) }

// IssueURL reads `https://github.com/owner/repo/issues/N`, with or without
// its scheme and with anything after the number, into the repository and the
// number; ok is false for anything else.
func IssueURL(text string) (repository string, number int, ok bool) {
	match := issueURL.FindStringSubmatch(text)
	if match == nil {
		return "", 0, false
	}
	number, err := strconv.Atoi(match[2])
	return match[1], number, err == nil && number > 0
}

var issueURL = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([A-Za-z0-9-]+/[A-Za-z0-9._-]+)/issues/([0-9]+)(?:[/?#].*)?$`)
