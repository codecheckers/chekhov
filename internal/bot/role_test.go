package bot

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roleReplies is the reply path with the account's role on the register.
type roleReplies struct {
	*recorder
	role string
	err  error
}

func (r roleReplies) Role(context.Context) (string, error) { return r.role, r.err }

// The account's role is checked at startup, because the token's page cannot
// show it: codecheckers/register#238 ran with an account that was `read` and a
// token with every write, and found out from three 403s. Below write the log
// says so; development /healthz reports it either way.
func TestTheStartupCheckSaysWhenTheAccountCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		role, err string
		warns     bool
		health    string
	}{
		{role: "write", health: `"role": "write"`},
		{role: "read", warns: true, health: `"writes": false`},
		{role: "triage", warns: true, health: `"role": "triage"`},
		{err: "GitHub answered 401", warns: true, health: "GitHub answered 401"},
	} {
		server, replies := testServer(t)
		var log bytes.Buffer
		server.Logger = slog.New(slog.NewTextHandler(&log, nil))
		reader := roleReplies{recorder: replies, role: tc.role}
		if tc.err != "" {
			reader.err = errors.New(tc.err)
		}
		server.Replies = reader

		server.checkRole(context.Background())

		if warned := strings.Contains(log.String(), "level=WARN"); warned != tc.warns {
			t.Errorf("%s%s: warned %v, want %v:\n%s", tc.role, tc.err, warned, tc.warns, log.String())
		}
		if tc.role == "read" && !strings.Contains(log.String(), "docs/github-token.md") {
			t.Errorf("the warning does not say where to look:\n%s", log.String())
		}

		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if body := response.Body.String(); !strings.Contains(body, tc.health) {
			t.Errorf("%s%s: healthz does not report %q: %s", tc.role, tc.err, tc.health, body)
		}

		// Production says nothing of it: /healthz is unauthenticated.
		server.Deployment.Environment = "production"
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if body := response.Body.String(); strings.Contains(body, "repository_permission") {
			t.Errorf("production health reports the role: %s", body)
		}
	}
}
