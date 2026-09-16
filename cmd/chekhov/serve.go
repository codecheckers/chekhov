package main

import (
	"fmt"
	"io"

	"github.com/codecheckers/chekhov/internal/bot"
)

// runServe starts the webhook listener from the command line. The deployment
// runs the same command through the Procfile, see docs/deployment.md.
func runServe(args []string, out io.Writer) error {
	address := bot.Address()
	for i := 0; i < len(args); i++ {
		if args[i] == "--addr" && i+1 < len(args) {
			i++
			address = args[i]
		}
	}

	fmt.Fprintf(out, "chekhov %s listening on %s\n", version, address)
	return bot.Serve(address, version, commit)
}
