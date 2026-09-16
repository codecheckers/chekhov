// Command chekhovd is the bot as a deployment: it takes no arguments and
// listens.
//
// It exists because a buildpack runs the binary it built with no arguments,
// and "chekhov" with no arguments is a command line tool printing its usage.
// Rather than teach the tool to guess what was meant, the deployment has its
// own entry point; both run the same server, in internal/bot.
package main

import (
	"log/slog"
	"os"

	"github.com/codecheckers/chekhov/internal/bot"
	"github.com/codecheckers/chekhov/internal/command"
)

// version and commit are injected at build time with
// -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = ""
)

func main() {
	if err := bot.Serve(bot.Address(), command.Deployment{Version: version, Commit: commit}); err != nil {
		slog.Error("chekhov stopped", "error", err)
		os.Exit(1)
	}
}
