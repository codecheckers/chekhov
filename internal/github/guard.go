package github

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// The last fence, in do: every request this package makes passes it.
//
// The methods above each refuse another repository themselves - mine, the
// team fence, the label lists - and that holds exactly as long as every
// method remembers to. This does not depend on remembering. Whatever built
// the request, a write leaves only for the few places the bot writes to: an
// issue of the configured repository, and the membership of a managed team in
// the configured organisation. A method added later that builds its own URL
// and forgets mine is stopped here rather than only by the token.
//
// Reads are left alone: the bot reads the organisation's teams and owners, and
// a read changes nothing. The token, though, goes nowhere but the API: a
// request to any other host is refused whatever its method, because its
// Authorization header would go with it.

// guard refuses a request the bot has no business sending.
func (c *Client) guard(method string, target *url.URL) error {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return refused("the API address %q cannot be read: %v", c.BaseURL, err)
	}
	if target.Scheme != base.Scheme || !strings.EqualFold(target.Host, base.Host) {
		return refused("refusing to send a request to %s: this bot talks to %s only",
			target.Host, base.Host)
	}
	if method == http.MethodGet {
		return nil
	}

	path := strings.TrimPrefix(target.Path, strings.TrimSuffix(base.Path, "/"))
	segments := strings.Split(strings.Trim(path, "/"), "/")
	// A path that climbs, or has an empty part, is not one a method here
	// builds; whatever it would resolve to is refused rather than worked out.
	if slices.ContainsFunc(segments, func(s string) bool { return s == "" || s == "." || s == ".." }) {
		return refused("refusing to %s %s: not a path this bot writes to", method, path)
	}
	if c.issueWrite(segments) || method == http.MethodPut && c.membershipWrite(segments) {
		return nil
	}
	return refused("refusing to %s %s: this bot writes to the issues of %s and to the "+
		"membership of its managed teams, nothing else", method, path, c.Repository)
}

// issueWrite is /repos/<the configured repository>/issues/..., which covers
// every comment, label, assignee and title the bot writes. Which repository
// is the configured one is mine's to say, so the two fences cannot disagree.
func (c *Client) issueWrite(segments []string) bool {
	return len(segments) > 3 && segments[0] == "repos" && segments[3] == "issues" &&
		c.mine(segments[1]+"/"+segments[2], "write") == nil
}

// membershipWrite is /orgs/<organisation>/teams/<team>/memberships/<somebody>,
// the one organisation write there is, for a team mayAddTo allows - the fence
// team.go stands on, asked again at the last moment.
func (c *Client) membershipWrite(segments []string) bool {
	return len(segments) == 6 && segments[0] == "orgs" && segments[2] == "teams" &&
		segments[4] == "memberships" && c.mayAddTo(segments[1], segments[3]) == nil
}

// maxRedirects is Go's own limit, which a CheckRedirect of our own replaces.
const maxRedirects = 10

// guarded is c.HTTP with every hop of a redirect put to the guard as well.
//
// Go follows a 307 or 308 with the same method, body and token, and GitHub
// answers a write to a renamed or transferred repository with one - to
// /repositories/<id>/..., a path the bot never writes to. Checking only the
// first URL would let that write land somewhere the guard never saw. A copy
// rather than a change to c.HTTP, which a caller or a test may have supplied:
// it shares the transport, so connections are still pooled.
func (c *Client) guarded() *http.Client {
	client := *c.HTTP
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return refused("stopped after %d redirects", maxRedirects)
		}
		return c.guard(next.Method, next.URL)
	}
	return &client
}

func refused(format string, args ...any) error {
	return &httpretry.Refused{Err: fmt.Errorf(format, args...)}
}
