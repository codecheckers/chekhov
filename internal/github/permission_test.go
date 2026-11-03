package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/httpretry"
)

// The role is the highest the repository's answer grants, read from the
// configured repository and nowhere else.
func TestRoleReadsTheAccountsRoleOnTheRegister(t *testing.T) {
	for _, tc := range []struct {
		permissions string
		want        string
		writes      bool
	}{
		{`{"admin": false, "maintain": false, "push": true, "triage": true, "pull": true}`, "write", true},
		{`{"admin": false, "maintain": true, "push": true, "triage": true, "pull": true}`, "maintain", true},
		{`{"admin": false, "maintain": false, "push": false, "triage": true, "pull": true}`, "triage", false},
		// codecheckers/register#238: every write a 403, the token in order.
		{`{"admin": false, "maintain": false, "push": false, "triage": false, "pull": true}`, "read", false},
	} {
		var path string
		client := stub(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"full_name": "codecheckers/testing-dev-register", "permissions": ` + tc.permissions + `}`))
		})
		role, err := client.Role(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want := "/repos/codecheckers/testing-dev-register"; path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
		if role != tc.want || Writes(role) != tc.writes {
			t.Errorf("%s: role %q writes %v, want %q writes %v", tc.permissions, role, Writes(role), tc.want, tc.writes)
		}
	}
}

// An answer without permissions is a stranger's view, not a role of "none".
func TestRoleWithoutPermissionsIsAnError(t *testing.T) {
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"full_name": "codecheckers/testing-dev-register"}`))
	})
	if role, err := client.Role(context.Background()); err == nil {
		t.Errorf("role %q, want an error", role)
	}
}

// A 403 for want of permission is a refusal; a 403 that is a rate limit is
// not, because waiting does change that one - whether the body says so or
// only the headers do. A 401 is neither: the token itself was not accepted.
func TestAFailureIsReadAsThePersonHasToHearIt(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		err                            error
		refused, limited, unauthorised bool
	}{
		{name: "permission", err: &httpretry.StatusError{Status: http.StatusForbidden,
			Body: `{"message": "Resource not accessible by personal access token"}`}, refused: true},
		{name: "secondary limit in the body", err: &httpretry.StatusError{Status: http.StatusForbidden,
			Body: `{"message": "You have exceeded a secondary rate limit"}`}, limited: true},
		{name: "limit in Retry-After", err: &httpretry.StatusError{Status: http.StatusForbidden,
			Body: `{"message": "Forbidden"}`, RetryAfter: time.Minute}, limited: true},
		{name: "budget used up", err: &httpretry.StatusError{Status: http.StatusForbidden,
			Body: `{"message": "Forbidden"}`, RateLimited: true}, limited: true},
		{name: "429", err: &httpretry.StatusError{Status: http.StatusTooManyRequests}, limited: true},
		{name: "401", err: &httpretry.StatusError{Status: http.StatusUnauthorized,
			Body: `{"message": "Bad credentials"}`}, unauthorised: true},
		{name: "502", err: &httpretry.StatusError{Status: http.StatusBadGateway}},
	} {
		err := fmt.Errorf("wrapped: %w", tc.err)
		if Refused(err) != tc.refused || Limited(err) != tc.limited || Unauthorised(err) != tc.unauthorised {
			t.Errorf("%s: refused %v limited %v unauthorised %v, want %v %v %v", tc.name,
				Refused(err), Limited(err), Unauthorised(err), tc.refused, tc.limited, tc.unauthorised)
		}
	}
}
