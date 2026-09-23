package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// Adding somebody to a team, and nothing else.
//
// This is the first thing the bot writes outside the register, and the only
// thing it writes that changes what a person may do. Everything here is about
// keeping it to that.
//
// The dangerous neighbour is `PUT /orgs/{org}/memberships/{username}`, one
// path segment away, which sets a person's *organisation* role and with
// `role: admin` makes them an owner of the organisation. This package never
// calls it. Nor does it ever ask for `maintainer` on a team, which would let
// the invited person add and remove others. The role sent is always
// TeamRoleMember, it is not a parameter of any function here, and
// team_test.go watches the requests that leave to prove both.
//
// See codecheckers/chekhov#42 and docs/github-token.md.

// Errors from this file are spliced into comments the bot posts, so every
// handle in one is written in backticks: `@someone` renders without notifying
// the account, a bare @someone notifies a real person who never asked to be in
// the thread. The two log lines below are not posted anywhere and write the
// handle plainly.

// TeamRoleMember is the only team role this bot will ever ask for: a plain
// member, who is in the team and may do nothing to it.
const TeamRoleMember = "member"

// membershipActive is what GitHub calls a membership that is in effect. The
// bot only ever adds people who are already in the organisation - an
// invitation from outside is an owner's to send - so this is the only state it
// can produce, and a "pending" one would mean somebody changed what this file
// does.
const membershipActive = "active"

// ErrNotPermitted is a token that may not manage membership. Its own error,
// because the reply has to say so plainly rather than let a 403 read as a
// failed invitation.
var ErrNotPermitted = errors.New("this bot's token may not manage team membership")

// ErrNotAUser is a handle that is not a person's account: an organisation, or
// nothing at all. Neither can be in a team.
//
// Its words are the consequence rather than the category, because a wrapped
// sentinel is printed after the sentence that wrapped it: "not a user
// account" only said the sentence again.
var ErrNotAUser = errors.New("only a person's account can be in a team")

// handleShape is what a GitHub login may look like: letters, digits and
// single hyphens, at most 39 characters.
//
// internal/command carries the same pattern for the handles it stores in a
// comment. The repetition is deliberate: this one guards a request path, where
// a handle carrying a slash or a dot segment would not merely 404 but move the
// request to a different endpoint - and the endpoints next door are the ones
// that change what somebody may do. A guard like that must not depend on
// another package agreeing with it, and internal/command has no business
// importing the code that writes to GitHub.
var handleShape = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}$`)

// IsHandle reports whether a string is shaped like a GitHub handle.
func IsHandle(handle string) bool { return handleShape.MatchString(handle) }

// slugShape is an organisation or team slug, which GitHub writes the way it
// writes a handle but allowing underscores and runs of hyphens.
//
// Checked for the same reason the handle is: both become path segments. These
// come from the settings file rather than from a comment, so this is the
// cheaper kind of guard - it makes the paths below safe by inspection instead
// of by trusting whoever edits the file.
var slugShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,98}$`)

// resolveUser reports that a handle is a person's account on GitHub.
//
// Asked before anything is written, as the registration runbook does by hand:
// a handle that does not resolve cannot be invited, and cannot be an assignee
// either, so the mistake is worth catching before a team is touched.
func (c *Client) resolveUser(ctx context.Context, handle string) (string, error) {
	if !IsHandle(handle) {
		return "", fmt.Errorf("%q is not a GitHub handle", handle)
	}

	var account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	url := fmt.Sprintf("%s/users/%s", strings.TrimSuffix(c.BaseURL, "/"), handle)
	err := c.attempt(ctx, "looking up @"+handle, func() error {
		raw, err := c.do(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &account)
	})
	switch {
	case httpretry.IsStatus(err, http.StatusNotFound):
		return "", fmt.Errorf("there is no GitHub account `@%s`: %w", handle, ErrNotAUser)
	case err != nil:
		return "", fmt.Errorf("could not look up `@%s`: %w", handle, err)
	case !strings.EqualFold(account.Type, "User"):
		// An organisation cannot be in a team, and GitHub answers 422 to the
		// attempt. Refused here, in words, rather than passed on as a puzzle.
		return "", fmt.Errorf("`@%s` is a GitHub %s: %w", handle, strings.ToLower(account.Type), ErrNotAUser)
	case account.Login == "":
		return "", fmt.Errorf("GitHub did not say who `@%s` is", handle)
	}
	// GitHub's own capitalisation, which is how the reply should write it.
	return account.Login, nil
}

// AddToTeam adds somebody to one of the teams this bot may add to, as a plain
// member, and returns the account as GitHub capitalises it, which is how a
// reply should write it.
//
// Only somebody already in the organisation can be added this way; bringing
// somebody in from outside is an organisation owner's to do, and `assign` asks
// them. See the note at the top of this file.
//
// The organisation and the team are parameters as well as fields, as the
// repository is for Comment, so that the caller has to say which it thinks it
// is writing to and a mismatch is refused before the request rather than
// discovered in the organisation afterwards.
//
// There is deliberately no role parameter, and no method here that removes
// anybody: taking a role away is done by a person, in the organisation's own
// settings, where it is logged against their name.
func (c *Client) AddToTeam(ctx context.Context, organisation, team, handle string) (login string, err error) {
	if err := c.mayAddTo(organisation, team); err != nil {
		return "", err
	}
	// Resolved first: the handle is checked against GitHub, and its shape
	// against handleShape, before any path is built from it.
	login, err = c.resolveUser(ctx, handle)
	if err != nil {
		return "", err
	}

	// The only body this bot ever sends here. Written as a literal rather than
	// composed, so that no caller and no future field can turn it into
	// maintainer.
	payload := []byte(`{"role":"` + TeamRoleMember + `"}`)
	url := fmt.Sprintf("%s/orgs/%s/teams/%s/memberships/%s",
		strings.TrimSuffix(c.BaseURL, "/"), organisation, team, login)

	var answered struct {
		State string `json:"state"`
		Role  string `json:"role"`
	}
	what := fmt.Sprintf("adding @%s to %s/%s", login, organisation, team)
	err = c.attempt(ctx, what, func() error {
		raw, err := c.do(ctx, http.MethodPut, url, payload)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &answered)
	})
	switch {
	case httpretry.IsStatus(err, http.StatusForbidden), httpretry.IsStatus(err, http.StatusUnauthorized):
		return login, fmt.Errorf("%w, so I cannot add `@%s` to %s/%s",
			ErrNotPermitted, login, organisation, team)
	case httpretry.IsStatus(err, http.StatusUnprocessableEntity):
		return login, fmt.Errorf("GitHub refused to put `@%s` in %s/%s, "+
			"which is what it answers for an account that cannot be in a team: %w",
			login, organisation, team, ErrNotAUser)
	case err != nil:
		return login, fmt.Errorf("could not add `@%s` to %s/%s: %w", login, organisation, team, err)
	case answered.Role != "" && answered.Role != TeamRoleMember:
		// Never seen, and worth saying loudly if it ever is: the bot asked for
		// a member and the answer describes somebody with more than that.
		return login, fmt.Errorf(
			"I asked for `@%s` to be a %s of %s/%s and GitHub answered %q; "+
				"check the team in the organisation's settings",
			login, TeamRoleMember, organisation, team, answered.Role)
	}
	if answered.State != "" && answered.State != membershipActive {
		// Never seen either: the bot adds an existing member, and GitHub calls
		// that active. Anything else means this file no longer does what its
		// comment says.
		return login, fmt.Errorf("I added `@%s` to %s/%s and GitHub answered state %q, "+
			"which it should not for somebody already in the organisation",
			login, organisation, team, answered.State)
	}
	return login, nil
}

// TeamMembership reports whether somebody's membership of the team is in
// effect, and whether one is merely pending.
//
// The two are separate because GitHub answers 200 for both: an owner who
// invited somebody through the team leaves a membership in state "pending"
// until they accept, and reading that as "in the team" would have the bot say
// nothing while the person cannot act. Behind the same guard as the write: the
// bot has no reason to read the membership of any other team either.
func (c *Client) TeamMembership(ctx context.Context, organisation, team, handle string) (active, pending bool, err error) {
	if err := c.mayAddTo(organisation, team); err != nil {
		return false, false, err
	}
	if !IsHandle(handle) {
		return false, false, fmt.Errorf("%q is not a GitHub handle", handle)
	}

	var membership struct {
		State string `json:"state"`
	}
	url := fmt.Sprintf("%s/orgs/%s/teams/%s/memberships/%s",
		strings.TrimSuffix(c.BaseURL, "/"), organisation, team, handle)
	err = c.attempt(ctx, fmt.Sprintf("reading `@%s`'s place in %s/%s", handle, organisation, team),
		func() error {
			raw, err := c.do(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &membership)
		})
	switch {
	case httpretry.IsStatus(err, http.StatusNotFound):
		// Not a failure: GitHub answers 404 for somebody who is not in the
		// team, which is the answer that was asked for.
		return false, false, nil
	case httpretry.IsStatus(err, http.StatusForbidden), httpretry.IsStatus(err, http.StatusUnauthorized):
		return false, false, fmt.Errorf("%w, so I cannot read `@%s`'s place in %s/%s",
			ErrNotPermitted, handle, organisation, team)
	case err != nil:
		return false, false, fmt.Errorf("could not read `@%s`'s place in %s/%s: %w",
			handle, organisation, team, err)
	}
	return membership.State == membershipActive, membership.State != membershipActive, nil
}

// mayAddTo is the guard the rest of this file rests on: the right
// organisation, and one of the teams the bot may add to.
//
// Refusing any other team is not tidiness. The editors team is what grants the
// editor-only commands, so a bot that could add to any team could hand out its
// own permissions. The list is an allow-list rather than one name because
// which team a codechecker belongs in follows from the check; config refuses a
// list that contains the editors team, which is the property this rests on.
func (c *Client) mayAddTo(organisation, team string) error {
	switch {
	case c.Organisation == "" || len(c.ManagedTeams) == 0:
		return fmt.Errorf("this bot is not configured to add anybody to a team")
	case !slugShape.MatchString(c.Organisation):
		return fmt.Errorf("refusing to change membership of %q: that is not an organisation",
			c.Organisation)
	case organisation != c.Organisation:
		return fmt.Errorf("refusing to change membership of %s: this bot works on %s only",
			organisation, c.Organisation)
	}
	for _, managed := range c.ManagedTeams {
		if team == managed && slugShape.MatchString(team) {
			return nil
		}
	}
	return fmt.Errorf("refusing to add anybody to %s/%s: this bot may only add to %s",
		organisation, team, strings.Join(c.withinOrganisation(), ", "))
}

// withinOrganisation names the managed teams as a reader sees them.
func (c *Client) withinOrganisation() []string {
	named := make([]string, 0, len(c.ManagedTeams))
	for _, team := range c.ManagedTeams {
		named = append(named, c.Organisation+"/"+team)
	}
	return named
}

// Owners are the organisation's owners, who are the only people who can invite
// somebody from outside it into a team - see the note at the top of this file.
// Read so that a reply asking them to act names the organisation's current
// owners rather than a list written down here.
func (c *Client) Owners(ctx context.Context, organisation string) ([]string, error) {
	if organisation != c.Organisation {
		return nil, fmt.Errorf("refusing to read the owners of %s: this bot works on %s only",
			organisation, c.Organisation)
	}
	url := fmt.Sprintf("%s/orgs/%s/members?role=admin&per_page=%d",
		strings.TrimSuffix(c.BaseURL, "/"), organisation, membersPerPage)
	handles, err := c.logins(ctx, "reading the owners of "+organisation, url)
	if err != nil {
		return nil, fmt.Errorf("could not read the owners of %s: %w", organisation, err)
	}
	return handles, nil
}

// InOrganisation reports whether somebody is a member of the organisation.
//
// The question `assign` has to ask before anything else: somebody outside the
// organisation cannot hold a role on a check, and only an owner can invite
// them in.
func (c *Client) InOrganisation(ctx context.Context, organisation, handle string) (bool, error) {
	if organisation != c.Organisation {
		return false, fmt.Errorf("refusing to read the members of %s: this bot works on %s only",
			organisation, c.Organisation)
	}
	if !IsHandle(handle) {
		return false, fmt.Errorf("%q is not a GitHub handle", handle)
	}

	url := fmt.Sprintf("%s/orgs/%s/members/%s", strings.TrimSuffix(c.BaseURL, "/"), organisation, handle)
	err := c.attempt(ctx, fmt.Sprintf("reading whether `@%s` is in %s", handle, organisation),
		func() error {
			_, err := c.do(ctx, http.MethodGet, url, nil)
			return err
		})
	switch {
	case err == nil:
		return true, nil
	case httpretry.IsStatus(err, http.StatusNotFound):
		// 404 is how GitHub says "not a member", and is the answer rather
		// than a failure.
		return false, nil
	default:
		return false, fmt.Errorf("could not read whether `@%s` is in %s: %w", handle, organisation, err)
	}
}
