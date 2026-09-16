// Command chekhov is the CODECHECK register bot.
//
// The bot itself is not deployed yet. What runs today is the check command,
// both as a subcommand here and as the reply to "@chekhovbot check
// codecheck.yml" on a checks issue, so that the validation can be exercised
// long before there is a webhook to carry it.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/rules"
)

// version is the bot's version, overridden at build time with
// -ldflags "-X main.version=...".
var version = "dev"

const usage = `chekhov - the CODECHECK register bot

Usage:
  chekhov check [--spec <version>] [--strict] [--markdown] [--online] <path to codecheck.yml>
  chekhov comment <path to a comment file, or - for stdin>
  chekhov rules [--spec <version>]
  chekhov version

The check command exits non-zero when a rule failed as an error. Without
--online it reads nothing but the file and its bundle, and the rules that need
Crossref, ORCID, Zenodo, the GitHub API or the register report that they were
not checked.
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}

	switch args[0] {
	case "check":
		return runCheck(args[1:], out)
	case "comment":
		return runComment(args[1:], out)
	case "rules":
		return runRules(args[1:], out)
	case "version":
		return runVersion(out)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func runCheck(args []string, out io.Writer) error {
	specVersion := ""
	strict := false
	markdown := false
	online := false
	var path string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--spec":
			if i+1 >= len(args) {
				return fmt.Errorf("--spec needs a specification version")
			}
			i++
			specVersion = args[i]
		case "--strict":
			strict = true
		case "--markdown":
			markdown = true
		case "--online":
			online = true
		default:
			path = args[i]
		}
	}
	if path == "" {
		return fmt.Errorf("check needs a path to a codecheck.yml")
	}

	context, err := check.FromFile(path)
	if err != nil {
		return err
	}
	if online {
		context = context.WithServices(check.Online())
	}
	report, err := check.Run(context, specVersion, strict)
	if err != nil {
		return err
	}

	if markdown {
		fmt.Fprint(out, report.Markdown())
	} else {
		fmt.Fprint(out, report.Text())
	}
	if !report.OK() {
		return fmt.Errorf("%s", report.FailureMessage())
	}
	return nil
}

// runComment shows what the bot would reply to a comment, which is how the
// command handling is exercised while there is no webhook.
func runComment(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("comment needs a file, or - for stdin")
	}

	var raw []byte
	var err error
	if args[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(args[0])
	}
	if err != nil {
		return err
	}

	parsed, addressed := command.Parse(string(raw))
	if !addressed {
		fmt.Fprintln(out, "not addressed to the bot, no reply")
		return nil
	}

	switch parsed.Name {
	case command.Check:
		path := "codecheck.yml"
		if len(parsed.Args) > 0 && strings.HasSuffix(parsed.Args[0], ".yml") {
			path = parsed.Args[0]
		}
		context, err := check.FromFile(path)
		if err != nil {
			return err
		}
		report, err := check.Run(context, "", false)
		if err != nil {
			return err
		}
		fmt.Fprint(out, report.Markdown())
		return nil
	default:
		fmt.Fprintf(out, "I do not know the command %q. Try `%s commands`.\n",
			parsed.Raw, command.Bot)
		return nil
	}
}

func runRules(args []string, out io.Writer) error {
	specVersion := rules.Newest()
	for i := 0; i < len(args); i++ {
		if args[i] == "--spec" && i+1 < len(args) {
			i++
			specVersion = args[i]
		}
	}

	catalogue, err := rules.For(specVersion)
	if err != nil {
		return err
	}
	for _, rule := range catalogue {
		status := " "
		if _, ok := check.Checks[rule.ID]; ok {
			status = "x"
		}
		fmt.Fprintf(out, "[%s] %s %-32s %-7s %s\n", status, rule.ID, rule.Name,
			rule.Severity, rule.Description)
	}
	fmt.Fprintf(out, "\n%d rules for specification %s, [x] = checked by this bot\n",
		len(catalogue), specVersion)
	return nil
}

func runVersion(out io.Writer) error {
	fmt.Fprintf(out, "chekhov %s\n", version)

	provenance, err := rules.Provenance()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "rules from register commit %s, retrieved %s\n",
		short(provenance.Commit()), provenance.Retrieved)
	for _, file := range provenance.Files {
		fmt.Fprintf(out, "  %s: %d rules for specification %s\n",
			file.Name, file.Rules, file.SpecVersion)
	}
	return nil
}

func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
