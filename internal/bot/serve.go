package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// Serve runs the bot until the process is asked to stop.
//
// It lives here rather than in the command so that the server is not tangled
// with the tool around it: the deployment runs "chekhov serve" through the
// Procfile, and a person on a terminal runs the same thing.
func Serve(address, version, commit string) error {
	settings, err := config.Current()
	if err != nil {
		return err
	}
	token := os.Getenv("CHEKHOV_GH_ACCESS_TOKEN")
	secret := os.Getenv("CHEKHOV_GH_SECRET_TOKEN")
	if token == "" || secret == "" {
		return fmt.Errorf("the bot needs CHEKHOV_GH_ACCESS_TOKEN and CHEKHOV_GH_SECRET_TOKEN, see docs/github-token.md")
	}

	deployment := deploymentFor(settings, version, commit)
	// Only a development deployment signs its comments with what it is; see
	// command.Deployment.
	replies := github.New(token, settings.TargetRepository(), deployment.Signature())

	server := New(settings, secret, replies, deployment)
	// A deployment is always online: the checks it runs read repositories and
	// ask Crossref, ORCID and Zenodo. The command line makes that a choice per
	// run instead.
	server.Services = check.Online()

	slog.Info("chekhov is listening", "address", address, "version", deployment.Version,
		"commit", deployment.Commit, "register", deployment.Register,
		"environment", deployment.Environment)

	return listen(address, server.Handler())
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
func deploymentFor(settings *config.Settings, version, commit string) command.Deployment {
	return command.Deployment{
		Version:     version,
		Commit:      commit,
		Register:    settings.TargetRepository(),
		Bot:         settings.BotUser(),
		Environment: config.Environment(),
	}
}

// Address is where to listen: the port the platform asks for, or 8080.
func Address() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}
