package bot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
)

// /help is its own route, not the redirect everything else gets.
func TestHelpIsServed(t *testing.T) {
	server := New(nil, "", nil, command.Deployment{Bot: "chekhovbot"})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/help", nil))

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("status %d, type %q", response.Code, response.Header().Get("Content-Type"))
	}
	if !strings.Contains(response.Body.String(), "@chekhovbot commands") {
		t.Error("the page does not list the commands")
	}
}
