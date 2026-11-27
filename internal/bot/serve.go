package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/build"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/logbook"
	"github.com/codecheckers/chekhov/internal/mastodon"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/registration"
	"github.com/codecheckers/chekhov/internal/safe"
)

// Serve runs the bot until the process is asked to stop.
//
// It lives here rather than in the command so that the server is not tangled
// with the tool around it: the deployment runs "chekhov serve" through the
// Procfile, and a person on a terminal runs the same thing.
func Serve(address string) (failure error) {
	// The log first, before anything can be logged around it: every record
	// to stderr, which is the platform's log, and the recent ones kept in
	// memory, with the errors offered to the private log repository once
	// there is a client to write there with (#73). Every package logs
	// through slog.Default, so this one line covers all of them.
	stderr := slog.NewTextHandler(os.Stderr, nil)
	buffer := logbook.NewBuffer()
	tee := logbook.NewHandler(stderr, buffer)
	slog.SetDefault(slog.New(tee))

	settings, err := config.Current()
	if err != nil {
		return err
	}
	token := os.Getenv("CHEKHOV_GH_ACCESS_TOKEN")
	secret := os.Getenv("CHEKHOV_GH_SECRET_TOKEN")
	if token == "" || secret == "" {
		return fmt.Errorf("the bot needs CHEKHOV_GH_ACCESS_TOKEN and CHEKHOV_GH_SECRET_TOKEN, see docs/github-token.md")
	}

	deployment := deploymentFor(settings)
	// Only a development deployment signs its comments with what it is; see
	// command.Deployment.
	//
	// One client, and everything that writes to GitHub goes through it -
	// replies, the record, the teams, the nightly sweep - because the pace of
	// writes is kept by the client: every command of every editor queues on
	// the same httpretry.Pacer. A second client would bring a second queue,
	// and two bursts are a burst. The same holds for Toots below.
	replies := github.New(token, settings.TargetRepository(), deployment.Signature())
	// The one organisation whose membership this deployment may change, and
	// the teams within it it may add anybody to. The editors team is refused
	// from that list at load, because naming it would let the bot hand out its
	// own permissions; see config.validate and internal/github/team.go.
	replies.Organisation, replies.ManagedTeams = settings.TeamOrganisation(), settings.ManagedTeams()
	// The labels it may add and remove, by name; every other label is read
	// only. See internal/github/comments.go.
	replies.AddableLabels, replies.RemovableLabels = settings.AddableLabels(), settings.RemovableLabels()
	// The one file it may write, on a branch of its own, and whose commits
	// on such a branch are its own to replace; see internal/github/register.go.
	replies.Rows, replies.Account = github.RegisterRows(settings.RegisterFile()), settings.BotUser()

	server := New(settings, secret, replies, deployment)
	server.Log = buffer
	if repository := settings.LogRepository(); repository != "" {
		server.logSink = logSinkFor(replies, repository, deployment, buffer, slog.New(stderr))
		tee.Attach(server.logSink)
		// A start that fails from here on is an error worth reporting too,
		// and the reports still queued are given a few seconds on the way
		// out, whichever way that is.
		defer func() {
			if failure != nil {
				slog.Error("chekhov could not start", "error", failure)
			}
			draining, cancel := context.WithTimeout(context.Background(), sinkDrain)
			defer cancel()
			server.logSink.Close(draining)
		}()
	}
	// The codechecker lists are a second repository, answered through a
	// client of their own that writes there and nowhere else, in the same
	// queue as everything above. See internal/bot/lists.go and #49.
	if lists := settings.ListsRepository(); lists != "" {
		// Only to sign the replies there: in this copy Register is the
		// lists, so it is not one to render the help page from.
		onLists := deployment
		onLists.Register = lists
		listsClient := replies.For(lists, onLists.Signature())
		// The one list `include` adds rows to, on a codechecker-<login>
		// branch; see internal/github/register.go.
		listsClient.Rows = github.ListRows(registration.RegistrationList)
		// And closes the registration it welcomes; the register's client
		// closes nothing.
		listsClient.Closes = true
		// And opens a registration for somebody who has joined, labelled as
		// the template labels one, which is the one label it may put on.
		listsClient.Opens, listsClient.AddableLabels = true, []string{settings.RegistrationLabel()}
		server.Lists = listsClient
	}
	// A deployment is always online: the checks it runs read repositories and
	// ask Crossref, ORCID and Zenodo. The command line makes that a choice per
	// run instead.
	server.Services = check.Online()

	// Announcing needs its own token, and a deployment without one simply
	// does not post: the preview still works.
	if token := os.Getenv("CHEKHOV_MASTODON_TOKEN"); token != "" && settings.Mastodon().Instance != "" &&
		settings.Mastodon().Account != "" {
		mastodonSettings := settings.Mastodon()
		server.Toots = mastodon.New(mastodonSettings.Instance, mastodonSettings.Account, token, mastodonSettings.Visibility)
	}

	// Who holds a standing role is the organisation's to say. The cache is the
	// transient copy of it; the reload below is how the transient copy is
	// rebuilt from the persistent one at boot.
	server.Teams = TeamsFor(settings, replies, slog.Default())
	// The per-check roles live in each issue, in a comment only the bot can
	// edit. Nothing caches them: the issue is the record.
	signer, err := people.NewSigner(os.Getenv("CHEKHOV_RECORD_KEY"),
		strings.Split(os.Getenv("CHEKHOV_RECORD_KEYS_RETIRED"), ",")...)
	if err != nil {
		return fmt.Errorf("the record key could not be read: %w", err)
	}
	server.Checks = &people.Checks{Comments: replies, Bot: settings.BotUser(), Signer: signer}
	if !signer.Signs() {
		slog.Warn("no record key: the record of a check will be written unsigned, " +
			"and an edit of them will go unnoticed. See docs/record-key.md")
	}
	reloadTeams(server, settings)
	go server.checkRole(context.Background())

	// The nightly sweep lives in this process: the reply path is here and so
	// is the token, and a second scheduled job would need a copy of both. It
	// stops when the process does; the weekly workflow behind GET /nudges is
	// what notices if it stops sooner. See internal/bot/nudges.go.
	sweeping, stopSweeping := context.WithCancel(context.Background())
	defer stopSweeping()
	go server.sweepNightly(sweeping)

	slog.Info("chekhov is listening", "address", address, "version", deployment.Version,
		"register", deployment.Register,
		"environment", deployment.Environment,
		"announcing", server.Toots != nil, "visibility", settings.Mastodon().Visibility)

	return listen(address, server.Handler(), tee)
}

// logSinkFor is the sink for the errors, whose client `log` writes with too,
// writing to the log repository
// through a client of its own: it opens issues there and nowhere else, puts
// on only this deployment's label and `fatal`, waits in the one write queue,
// and logs to stderr alone - so that a failure to report cannot be reported.
func logSinkFor(replies *github.Client, repository string, deployment command.Deployment,
	buffer *logbook.Buffer, stderr *slog.Logger) *fatalSink {
	client := replies.For(repository, deployment.Signature())
	client.Opens, client.AddableLabels = true, []string{logLabel(deployment), labelFatal}
	client.Logger = stderr
	return newFatalSink(repository, deployment, client, buffer, stderr)
}

// reloadTeams fills the membership cache before the first command needs it.
//
// In the background, because the platform's health check must not wait for
// GitHub, and because a bot that cannot read the teams still answers
// everything that does not depend on them. What it finds is logged either way:
// a token without Members: read turns every editor into a stranger, and that
// should be visible at startup rather than the first time somebody is refused.
func reloadTeams(server *Server, settings *config.Settings) {
	teams := settings.Teams()
	if len(teams) == 0 {
		return
	}
	go func() {
		// Its window is only the first seconds after a start, but a panic
		// here would end the process just as surely; see the sweep's.
		defer server.recoverBackground("The team load at startup panicked.")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, state := range server.Teams.Refresh(ctx, teams...) {
			if state.Err != nil {
				slog.Warn("a team could not be read at startup; nobody holds that role until it can be",
					"team", state.Team, "error", state.Err)
				continue
			}
			slog.Info("team loaded", "team", state.Team, "members", state.Members)
		}
	}()
}

// listen serves until the process is asked to stop, and then lets the commands
// in flight finish.
//
// What net/http logs - a handler that panicked, which it recovers itself - goes
// to the log as an Error, so that it reaches the log repository like every
// other; without ErrorLog it would go to stderr past slog.
func listen(address string, router http.Handler, handler slog.Handler) error {
	httpServer := &http.Server{
		Addr:              address,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(httpErrors{slog.New(handler)}, "", 0),
	}

	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, os.Interrupt, syscall.SIGTERM)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		// A Shutdown that panics must still stop the server, or the signal
		// would be swallowed and the process never end.
		defer safe.Recover("stopping the server", func(error) { _ = httpServer.Close() })
		<-stopping
		slog.Info("chekhov is stopping")
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ListenAndServe returns as soon as Shutdown begins; the twenty seconds
	// are Shutdown's, so wait for it.
	<-stopped
	return nil
}

// shutdownGrace is what Shutdown is given of the twenty seconds a redeploy
// allows: the rest is the log sink's, to drain what is queued (sinkDrain).
const shutdownGrace = 20*time.Second - sinkDrain

// httpErrors is where net/http's own log goes. A handler that panicked, which
// net/http recovers itself, is an Error and reaches the log repository; every
// other line it writes - a client's malformed request, a URL with a semicolon
// - is anybody's to cause, so it is a Warn, and cannot fill the repository
// or the hourly budget a real error needs.
type httpErrors struct{ logger *slog.Logger }

func (h httpErrors) Write(line []byte) (int, error) {
	message := strings.TrimSpace(string(line))
	level := slog.LevelWarn
	if strings.HasPrefix(message, "http: panic serving") {
		level = slog.LevelError
	}
	h.logger.Log(context.Background(), level, message)
	return len(line), nil
}

// deploymentFor is the one place a Deployment is assembled, so that no caller
// can leave out the environment - which decides how much a reply discloses -
// and have the omission read as "development".
func deploymentFor(settings *config.Settings) command.Deployment {
	revision, dirty := build.Revision()
	return command.Deployment{
		Version:       build.Version,
		Revision:      revision,
		RevisionDirty: dirty,
		Register:      settings.TargetRepository(),
		Lists:         settings.ListsRepository(),

		RegistrationLabel: settings.RegistrationLabel(),
		HelpURL:           helpURL(settings),
		Bot:               settings.BotUser(),
		Environment:       config.Environment(),
	}
}

// helpURL is the address of this deployment's help page, empty where the
// settings name no public URL.
func helpURL(settings *config.Settings) string {
	if base := settings.PublicURL(); base != "" {
		return base + command.HelpPath
	}
	return ""
}

// Address is where to listen: the port the platform asks for, or 8080.
func Address() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}
