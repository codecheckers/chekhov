package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/bot"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// runServe starts the webhook listener: the bot proper, as opposed to the
// command line tool the rest of this file is.
func runServe(args []string, out io.Writer) error {
	address := ":" + port()
	for i := 0; i < len(args); i++ {
		if args[i] == "--addr" && i+1 < len(args) {
			i++
			address = args[i]
		}
	}

	settings, err := config.Current()
	if err != nil {
		return err
	}
	token := os.Getenv("CHEKHOV_GH_ACCESS_TOKEN")
	secret := os.Getenv("CHEKHOV_GH_SECRET_TOKEN")
	if token == "" || secret == "" {
		return fmt.Errorf("serve needs CHEKHOV_GH_ACCESS_TOKEN and CHEKHOV_GH_SECRET_TOKEN, see docs/github-token.md")
	}

	deployment := command.Deployment{
		Version:  version,
		Commit:   commit,
		Register: settings.TargetRepository(),
		Bot:      settings.BotUser(),
	}
	replies := github.New(token, settings.TargetRepository(),
		fmt.Sprintf("`@%s` %s · working on `%s`", settings.BotUser(),
			deployment.Version, settings.TargetRepository()))

	server := bot.New(settings, secret, replies, deployment)
	// The bot reads repositories and asks Crossref, ORCID and Zenodo, which
	// the command line makes a choice per run; a deployment is always online.
	server.Services = check.Online()

	slog.Info("chekhov is listening", "address", address, "version", version,
		"commit", commit, "register", settings.TargetRepository(),
		"environment", config.Environment())
	fmt.Fprintf(out, "chekhov %s listening on %s, working on %s\n",
		version, address, settings.TargetRepository())

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

// port is what the platform says to listen on. runway.horse sets PORT, as most
// of them do.
func port() string {
	if value := os.Getenv("PORT"); value != "" {
		return value
	}
	return "8080"
}
