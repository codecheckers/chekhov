package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	var handles []string
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/orgs/%s/teams/%s/members?per_page=%d&page=%d",
			strings.TrimSuffix(c.BaseURL, "/"), organisation, team, membersPerPage, page)

		var batch []struct {
			Login string `json:"login"`
		}
		err := c.attempt(ctx, fmt.Sprintf("reading the %s/%s team", organisation, team), func() error {
			raw, err := c.do(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &batch)
		})
		if err != nil {
			return nil, fmt.Errorf("could not read the %s/%s team: %w", organisation, team, err)
		}

		for _, member := range batch {
			if member.Login != "" {
				handles = append(handles, member.Login)
			}
		}
		if len(batch) < membersPerPage {
			return handles, nil
		}
	}
}
