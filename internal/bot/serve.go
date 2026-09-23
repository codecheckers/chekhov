package bot

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/codecheckers/chekhov/internal/mastodon"
	"github.com/codecheckers/chekhov/internal/people"
)

// Serve runs the bot until the process is asked to stop.
//
// It lives here rather than in the command so that the server is not tangled
// with the tool around it: the deployment runs "chekhov serve" through the
// Procfile, and a person on a terminal runs the same thing.
func Serve(address string) error {
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
	replies := github.New(token, settings.TargetRepository(), deployment.Signature())
	// The one organisation whose membership this deployment may change, and
	// the teams within it it may add anybody to. The editors team is refused
	// from that list at load, because naming it would let the bot hand out its
	// own permissions; see config.validate and internal/github/team.go.
	replies.Organisation, replies.ManagedTeams = settings.TeamOrganisation(), settings.ManagedTeams()
	// The labels it may add and remove, by name; every other label is read
	// only. See internal/github/comments.go.
	replies.AddableLabels, replies.RemovableLabels = settings.AddableLabels(), settings.RemovableLabels()

	server := New(settings, secret, replies, deployment)
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

	return listen(address, server.Handler())
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
func listen(address string, handler http.Handler) error {
	httpServer := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopping
		slog.Info("chekhov is stopping")
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
		Bot:           settings.BotUser(),
		Environment:   config.Environment(),
	}
}

// Address is where to listen: the port the platform asks for, or 8080.
func Address() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}
