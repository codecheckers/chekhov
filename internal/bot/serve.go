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
// It lives here rather than in a command so that the deployed daemon
// (cmd/chekhovd) and "chekhov serve" are the same bot, started two ways: the
// platform runs a container whose process takes no arguments, and a person on
// a terminal wants a subcommand.
func Serve(address string, deployment command.Deployment) error {
	settings, err := config.Current()
	if err != nil {
		return err
	}
	token := os.Getenv("CHEKHOV_GH_ACCESS_TOKEN")
	secret := os.Getenv("CHEKHOV_GH_SECRET_TOKEN")
	if token == "" || secret == "" {
		return fmt.Errorf("the bot needs CHEKHOV_GH_ACCESS_TOKEN and CHEKHOV_GH_SECRET_TOKEN, see docs/github-token.md")
	}

	deployment.Register = settings.TargetRepository()
	deployment.Bot = settings.BotUser()
	deployment.Environment = config.Environment()
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
		"environment", config.Environment())

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

// Address is where to listen: the port the platform asks for, or 8080.
func Address() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}
