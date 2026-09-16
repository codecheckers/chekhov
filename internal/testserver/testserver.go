// Package testserver serves canned answers to the offline tests.
//
// One server stands in for every service a test needs, each under its own
// path prefix, and its client refuses any request that is not addressed to it:
// an offline test that reaches the real internet is not an offline test.
//
// Routes live in a map rather than in a ServeMux, so that a test can serve
// everything a case in good order gets and then replace the one route it is
// about; a ServeMux panics on the second registration of a path.
//
// It is imported by tests only.
package testserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// A Server answers from its routes, and 404 for everything else.
type Server struct {
	// URL is the server's address, with no trailing slash.
	URL string

	server *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
}

// New starts a server that stops when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{routes: map[string]http.HandlerFunc{}}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		handler, ok := s.routes[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(s.server.Close)
	s.URL = s.server.URL
	return s
}

// Client reaches this server and nothing else. Anything not addressed to it
// fails, and says which URL it was.
func (s *Server) Client() *http.Client {
	client := s.server.Client()
	client.Transport = localOnly{host: s.server.Listener.Addr().String(), base: client.Transport}
	return client
}

type localOnly struct {
	host string
	base http.RoundTripper
}

func (l localOnly) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != l.host {
		return nil, fmt.Errorf("offline test tried to reach %s; point it at the stub server", request.URL)
	}
	return l.base.RoundTrip(request)
}

// At is the server's address for a path, for a fixture that has to carry a
// link to one of its own routes.
func (s *Server) At(path string) string { return s.URL + path }

// Handle answers a path with a handler, replacing whatever answered it before.
func (s *Server) Handle(path string, handler http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = handler
}

// Remove makes a path answer 404 again.
func (s *Server) Remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routes, path)
}

// Bytes answers a path with a body.
func (s *Server) Bytes(path string, body []byte) {
	s.Handle(path, func(w http.ResponseWriter, _ *http.Request) { w.Write(body) })
}

// Text answers a path with a body.
func (s *Server) Text(path, body string) { s.Bytes(path, []byte(body)) }

// JSON answers a path with a JSON body.
func (s *Server) JSON(path, body string) {
	s.Handle(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
}

// Status answers a path with a status and no body.
func (s *Server) Status(path string, code int) {
	s.Handle(path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
}
