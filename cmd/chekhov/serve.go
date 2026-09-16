package main

import (
	"fmt"
	"io"

	"github.com/codecheckers/chekhov/internal/bot"
	"github.com/codecheckers/chekhov/internal/command"
)

// runServe starts the webhook listener from the command line. The deployed bot
// is cmd/chekhovd, which is the same server without the surrounding tool.
func runServe(args []string, out io.Writer) error {
	address := bot.Address()
	for i := 0; i < len(args); i++ {
		if args[i] == "--addr" && i+1 < len(args) {
			i++
			address = args[i]
		}
	}

	fmt.Fprintf(out, "chekhov %s listening on %s\n", version, address)
	return bot.Serve(address, command.Deployment{Version: version, Commit: commit})
}
