package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// The last fence, in do: every request this package makes passes it.
//
// The methods above each refuse another repository themselves - mine, the
// team fence, the label lists - and that holds exactly as long as every
// method remembers to. This does not depend on remembering. Whatever built
// the request, a write leaves only for the few places the bot writes to: an
// issue of the configured repository, the membership of a managed team in the
// configured organisation, and the pull request adding a row to the register -
// see registerWrite for exactly how far that reaches. A method added later
// that builds its own URL and forgets mine is stopped here rather than only by
// the token.
//
// Reads are left alone: the bot reads the organisation's teams and owners, and
// a read changes nothing. The token, though, goes nowhere but the API: a
// request to any other host is refused whatever its method, because its
// Authorization header would go with it.

// guard refuses a request the bot has no business sending. The payload is
// read for the few writes whose body, not their path, says what they touch.
func (c *Client) guard(method string, target *url.URL, payload []byte) error {
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
	if c.issueWrite(method, segments) || method == http.MethodPut && c.membershipWrite(segments) ||
		c.registerWrite(method, segments, payload) {
		return nil
	}
	return refused("refusing to %s %s: this bot writes to the issues of %s, to the "+
		"membership of its managed teams, and the register file on a branch of its own, nothing else",
		method, path, c.Repository)
}

// registerWrite is the pull request adding a row, register.go's. Its writes
// say in their bodies what they touch, so the bodies are read here too. What
// this holds without keeping state is where a change can land: no branch but
// a register-YYYY-NNN one moves, a pull request is opened only from such a
// branch, and an existing one has only its title and body changed - never its
// state, nor its base, and nothing is merged. What it cannot hold is what is
// in the commit: a tree names its own base and a commit its own tree, and
// telling those apart from the default branch's would take a read of the
// repository. That is the method's to get right and the merging editor's to
// see; the default branch is reached only through their review. No payload is
// no write of these: a redirected one, whose body this never sees, is refused.
func (c *Client) registerWrite(method string, segments []string, payload []byte) bool {
	if len(segments) < 4 || segments[0] != "repos" || c.RegisterFile == "" ||
		c.mine(segments[1]+"/"+segments[2], "write") != nil {
		return false
	}
	// Each write's own field, decoded only for that write: a commit's tree is
	// a sha, a tree's is a list.
	var keys map[string]json.RawMessage
	if payload == nil || json.Unmarshal(payload, &keys) != nil {
		return false
	}
	field := func(name string, into any) bool {
		raw, present := keys[name]
		return present && json.Unmarshal(raw, into) == nil
	}

	rest := strings.Join(segments[3:], "/")
	switch {
	case method == http.MethodPost && rest == "git/trees":
		var tree []struct {
			Path string `json:"path"`
		}
		if !field("tree", &tree) || len(tree) == 0 {
			return false
		}
		for _, entry := range tree {
			if entry.Path != c.RegisterFile {
				return false
			}
		}
		return true
	case method == http.MethodPost && rest == "git/commits":
		return true
	case method == http.MethodPost && rest == "git/refs":
		var ref string
		if !field("ref", &ref) {
			return false
		}
		branch, isBranch := strings.CutPrefix(ref, "refs/heads/")
		return isBranch && registerBranch.MatchString(branch)
	case method == http.MethodPatch && strings.HasPrefix(rest, "git/refs/heads/"):
		return registerBranch.MatchString(strings.TrimPrefix(rest, "git/refs/heads/"))
	case method == http.MethodPost && rest == "pulls":
		var head string
		return field("head", &head) && registerBranch.MatchString(head)
	// Which pull request a number names is not in the request; ProposeRow
	// patches only the one it found by its register branch and base.
	case method == http.MethodPatch && pullNumber.MatchString(rest):
		for key := range keys {
			if key != "title" && key != "body" {
				return false
			}
		}
		return true
	}
	return false
}

// pullNumber is the path of one pull request: digits, no sign.
var pullNumber = regexp.MustCompile(`^pulls/[0-9]+$`)

// issueWrite is /repos/<the configured repository>/issues/..., which covers
// every comment, label, assignee and title the bot writes. A DELETE reaches
// less: a label, an assignee, and one comment, which DeleteComment deletes
// because it holds a share link - never an issue, nor anything else under it.
// Which repository is the configured one is mine's to say, so the two fences
// cannot disagree.
func (c *Client) issueWrite(method string, segments []string) bool {
	if len(segments) <= 3 || segments[0] != "repos" || segments[3] != "issues" ||
		c.mine(segments[1]+"/"+segments[2], "write") != nil {
		return false
	}
	if method != http.MethodDelete {
		return true
	}
	rest := strings.Join(segments[4:], "/")
	return issueComment.MatchString(rest) || issueDeletable.MatchString(rest)
}

// issueComment is the path of one comment; issueDeletable a label on an
// issue, or its assignees.
var (
	issueComment   = regexp.MustCompile(`^comments/[0-9]+$`)
	issueDeletable = regexp.MustCompile(`^[0-9]+/(?:labels/[^/]+|assignees)$`)
)

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
		// The hop's body is out of sight here, so a write that has to show
		// its body to pass is refused rather than let through blind.
		return c.guard(next.Method, next.URL, nil)
	}
	return &client
}

// refusal is a request the guard would not let leave. A type of its own because
// it has to be told from a failure of the network once http.Client has wrapped
// it: see do.
type refusal struct{ error }

func (r *refusal) Unwrap() error { return r.error }

func refused(format string, args ...any) error {
	return &refusal{fmt.Errorf(format, args...)}
}
