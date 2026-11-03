package github

import (
	"context"
	"errors"
	"net/http"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// Role is the account's own role on the configured repository, as GitHub
// reports it to the account itself: admin, maintain, write, triage or read.
//
// It is asked because a fine-grained token's permissions are a ceiling, not a
// grant: the token may do what both it and the account may do. A token with
// Contents, Issues and Pull requests at write, for an account that is only
// `read` on the register, comments and does nothing else - which is how
// production ran from 0.22.0 until codecheckers/register#238 found it, every
// write answered 403 while the token's page showed every permission in place.
// The token's side cannot be read through the API; the account's can, from
// the repository's own answer, with nothing more than read.
func (c *Client) Role(ctx context.Context) (string, error) {
	var repository struct {
		Permissions *struct {
			Admin    bool `json:"admin"`
			Maintain bool `json:"maintain"`
			Push     bool `json:"push"`
			Triage   bool `json:"triage"`
			Pull     bool `json:"pull"`
		} `json:"permissions"`
	}
	if err := c.read(ctx, c.BaseURL+"/repos/"+c.Repository, &repository); err != nil {
		return "", err
	}
	switch p := repository.Permissions; {
	case p == nil:
		// Sent only to an authenticated caller; without it the answer is
		// that of a stranger, which is not the account's role.
		return "", errors.New("GitHub did not say what the account may do on " + c.Repository)
	case p.Admin:
		return "admin", nil
	case p.Maintain:
		return "maintain", nil
	case p.Push:
		return "write", nil
	case p.Triage:
		return "triage", nil
	case p.Pull:
		return "read", nil
	default:
		return "none", nil
	}
}

// Writes reports whether a role may do everything the bot writes: labels, the
// assignee, the title, deleting a comment and the register-YYYY-NNN branch.
// Triage covers the first three and not the branch.
func Writes(role string) bool {
	return role == "admin" || role == "maintain" || role == "write"
}

// Refused reports whether GitHub refused a request for want of permission: a
// 403 that is not a rate limit. Asking again does not change it, so a reply
// must not suggest that it might.
func Refused(err error) bool {
	return httpretry.IsStatus(err, http.StatusForbidden) && !Limited(err)
}

// Limited reports whether GitHub said "not now": a 429, or a 403 its headers
// or body call a rate limit - read as the retry reads it, so that the reply
// and the retry cannot disagree about one answer.
func Limited(err error) bool {
	return httpretry.RateLimited(err, bodySaysRateLimited)
}

// Unauthorised reports whether GitHub did not accept the token at all: a 401,
// which is a token expired, revoked or regenerated, not a permission.
func Unauthorised(err error) bool {
	return httpretry.IsStatus(err, http.StatusUnauthorized)
}
