package github

import (
	"context"
	"fmt"
	"strings"
)

// Reading a team is the one thing this package does that is not writing.
//
// It lives here because it needs the bot's credential, which no other package
// holds, and because "who is an editor" is answered by the organisation rather
// than by anything the bot stores. See docs/github-token.md: this is what the
// Members: read permission on the token is for.

// membersPerPage is GitHub's maximum page size for team membership.
const membersPerPage = 100

// TeamMembers returns the handles of an organisation team.
//
// A team the token may not read is an error, never an empty team: the caller
// decides what to do about it, and the answer must never be "nobody is in this
// team, so this command is open to everyone".
func (c *Client) TeamMembers(ctx context.Context, organisation, team string) ([]string, error) {
	url := fmt.Sprintf("%s/orgs/%s/teams/%s/members?per_page=%d",
		strings.TrimSuffix(c.BaseURL, "/"), organisation, team, membersPerPage)
	handles, err := c.logins(ctx, fmt.Sprintf("reading the %s/%s team", organisation, team), url)
	if err != nil {
		return nil, fmt.Errorf("could not read the %s/%s team: %w", organisation, team, err)
	}
	return handles, nil
}

// logins reads a paginated list of accounts and returns their handles. Two
// endpoints answer that shape - a team's members and an organisation's owners
// - and the paging was written twice before it was written here.
func (c *Client) logins(ctx context.Context, what, url string) ([]string, error) {
	var handles []string
	type member struct {
		Login string `json:"login"`
	}
	err := paged(ctx, c, what, url, membersPerPage, func(batch []member) {
		for _, read := range batch {
			if read.Login != "" {
				handles = append(handles, read.Login)
			}
		}
	})
	return handles, err
}
