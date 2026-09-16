package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/rules"
)

// commandTimeout bounds one command. GitHub gives a webhook 10 seconds to be
// answered, which is why the work happens after the answer; a check that asks
// Crossref and Zenodo can take a minute, and one that hangs must not pile up.
const commandTimeout = 5 * time.Minute

// Poster is the part of the reply path the bot needs, so a test can watch what
// would be posted without a server.
type Poster interface {
	Comment(ctx context.Context, repository string, issue int, body string) (int64, error)
}

// Server is the whole bot: one webhook endpoint, one health endpoint, no state.
type Server struct {
	Settings   *config.Settings
	Secret     string
	Replies    Poster
	Deployment command.Deployment
	Logger     *slog.Logger

	// Services lets the check command reach Crossref, ORCID, Zenodo and the
	// register. Nil keeps the bot offline, which is how the tests run it.
	Services *check.Services

	// LocalPaths allows a command to name a file on this machine. It is off
	// for a deployment on purpose: a path in a comment is written by anyone on
	// the internet, and the bot's disk is none of their business. The command
	// line preview turns it on, because there the path is the user's own.
	LocalPaths bool

	// done is closed when a background command finishes, for the tests.
	done chan struct{}
}

// New builds a server from the settings and a reply path.
func New(settings *config.Settings, secret string, replies Poster, deployment command.Deployment) *Server {
	return &Server{
		Settings:   settings,
		Secret:     secret,
		Replies:    replies,
		Deployment: deployment,
		Logger:     slog.Default(),
	}
}

// Handler is the bot's HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /dispatch", s.dispatch)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/codecheckers/chekhov", http.StatusFound)
	})
	return mux
}

// dispatch receives a webhook delivery.
//
// The order matters: verify, then read. A delivery that is not signed by the
// secret is not the bot's business, and its body is not parsed at all.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "could not read the delivery", http.StatusBadRequest)
		return
	}

	if err := verifySignature(s.Secret, r.Header.Get("X-Hub-Signature-256"), body); err != nil {
		s.Logger.Warn("a delivery was refused", "delivery", r.Header.Get("X-GitHub-Delivery"), "error", err)
		http.Error(w, "signature check failed", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	event, handled, err := parseEvent(eventType, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !handled {
		// Not an error: GitHub sends a ping on setup, and whatever else the
		// webhook is subscribed to.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if reason := s.refuse(event); reason != "" {
		s.Logger.Warn("a delivery was not acted on", "reason", reason,
			"repository", event.Repository, "issue", event.Issue, "author", event.Author)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	parsed, addressed := command.Parse(event.Body)
	if !addressed {
		// The common case on a busy issue, and it has to stay cheap and silent.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Answer now, work afterwards.
	w.WriteHeader(http.StatusAccepted)
	go s.act(event, parsed)
}

// refuse says why a delivery is not acted on, or returns an empty string.
func (s *Server) refuse(event mention) string {
	switch {
	case event.Repository != s.Settings.TargetRepository():
		// A misconfigured webhook must not reach the production register.
		return fmt.Sprintf("this bot works on %s", s.Settings.TargetRepository())
	case strings.EqualFold(event.Author, s.Settings.BotUser()):
		// Otherwise a reply is a comment, and a comment gets a reply.
		return "the comment is the bot's own"
	case event.Issue <= 0:
		return "the event names no issue"
	default:
		return ""
	}
}

// act runs one command and posts the answer.
func (s *Server) act(event mention, parsed command.Command) {
	defer func() {
		if s.done != nil {
			s.done <- struct{}{}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	started := time.Now()
	body := s.answer(ctx, event, parsed)
	if body == "" {
		return
	}

	id, err := s.Replies.Comment(ctx, event.Repository, event.Issue, body)
	if err != nil {
		s.Logger.Error("the answer could not be posted", "command", parsed.Name,
			"issue", event.Issue, "error", err)
		return
	}
	s.Logger.Info("answered", "command", parsed.Name, "issue", event.Issue,
		"author", event.Author, "comment", id, "took", time.Since(started))
}

// answer produces the reply to one command.
func (s *Server) answer(ctx context.Context, event mention, parsed command.Command) string {
	role := command.RoleAnyone
	if s.Settings.IsEditor(event.Author) {
		role = command.RoleEditor
	}

	if definition, found := command.Lookup(string(parsed.Name)); found && !definition.Permits(role) {
		// Not reachable while every command is open to everyone, but the rule
		// belongs next to the listing that hides them.
		return fmt.Sprintf("`%s %s` is for editors.\n", command.Bot, parsed.Name)
	}

	switch parsed.Name {
	case command.Commands:
		return command.Listing(role)
	case command.Hello:
		return s.Deployment.HelloReply()
	case command.Version:
		return s.version()
	case command.Check:
		return s.check(ctx, parsed)
	default:
		return command.UnknownReply(parsed)
	}
}

// version says which build is answering and which catalogue it judges by.
func (s *Server) version() string {
	commit, retrieved := "", ""
	if provenance, err := rules.Provenance(); err == nil {
		commit, retrieved = provenance.Commit(), provenance.Retrieved
	}
	return s.Deployment.VersionReply(commit, retrieved)
}

// check validates a codecheck.yml named in the comment.
//
// The target may be written however a person finds natural - a repository
// spec, a shortcut, or a certificate identifier the register knows - and
// check.ResolveTarget decides which. Working out from the issue alone which
// configuration it is about is still to come.
func (s *Server) check(ctx context.Context, parsed command.Command) string {
	part, target := "", ""
	for _, argument := range parsed.Args {
		// A part name is never a target: the catalogue's own words come first,
		// and everything else is what to read.
		if argument == "" {
			continue
		}
		if check.IsPart(argument) {
			part = argument
		} else if target == "" {
			// The first target wins. A comment is prose, and "check 2020-001
			// please" must not be read as a request to check "please".
			target = argument
		}
	}
	if target == "" {
		return fmt.Sprintf("I need to be told what to check:\n\n"+
			"    %s check codecheckers/repository\n"+
			"    %s check 2020-001\n\n"+
			"A repository may also be named the way `register.csv` does, "+
			"`github::owner/repo`, with `|sub/dir` when the configuration is not at the root.\n",
			command.Bot, command.Bot)
	}

	context, err := s.read(target)
	if err != nil {
		return fmt.Sprintf("I could not read `%s`: %s\n", target, err)
	}
	report, err := check.RunPart(context, "", false, part)
	if err != nil {
		return fmt.Sprintf("%s\n", err)
	}
	return report.Markdown()
}

// read loads the configuration a command names. A deployment does not read its
// own disk, so there a target is always a repository; check.Load is the one
// place that decides.
func (s *Server) read(target string) (check.Context, error) {
	return check.Load(target, s.LocalPaths, s.Services)
}

// health says which bot this is, in enough detail that a development
// deployment cannot be mistaken for the real one.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated, so it says only what a stranger may know: which bot,
	// which register, which build. The rest - the commit, where the rules came
	// from, when the token expires, whether the services are reachable - is
	// for whoever is developing the thing, and is a gift to anyone else.
	state := s.Deployment.Facts()
	state["status"] = "ok"
	if s.Deployment.Development() {
		state["online"] = s.Services.Enabled()
		if provenance, err := rules.Provenance(); err == nil {
			state["rules_commit"] = provenance.Commit()
			state["rules_retrieved"] = provenance.Retrieved
		}
		if client, ok := s.Replies.(*github.Client); ok && client.TokenExpiry() != "" {
			state["token_expires"] = client.TokenExpiry()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(state)
}

// Preview renders what the bot would reply to a comment, without posting
// anything.
//
// It exists so that `chekhov comment` and the deployed bot answer through the
// same code: a reply that can be read on the command line is only worth
// reading if it is the reply that would be posted.
func Preview(settings *config.Settings, version, commit string, services *check.Services, author, body string) (string, bool) {
	parsed, addressed := command.Parse(body)
	if !addressed {
		return "", false
	}

	server := New(settings, "", nil, deploymentFor(settings, version, commit))
	server.Services = services
	// On the command line the path in the comment is the user's own.
	server.LocalPaths = true
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return server.answer(ctx, mention{Author: author}, parsed), true
}
