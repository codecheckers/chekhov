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

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/bot"
	"github.com/codecheckers/chekhov/internal/build"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/rules"
)

// version and commit are the build, overridden at build time with
// -ldflags "-X main.version=... -X main.commit=...". They are injected rather
// than read from a file so that "@chekhovbot version" is honest about what is
// actually running.
var (
	version = "dev"
	commit  = ""
)

const usage = `chekhov - the CODECHECK register bot

Usage:
  chekhov check [options] <path to codecheck.yml, or a repository spec>
  chekhov check [options] [config|metadata|bundle|references|report|register] <target>
  chekhov comment <path to a comment file, or - for stdin>
  chekhov serve [--addr :8080]
  chekhov rules [--spec <version>]
  chekhov version

Options: --spec <version>, --strict, --markdown, --online

The target is a path, or a repository the way register.csv names one:
github::org/repo, github::org/repo|sub/dir, gitlab::group/project, osf::<id>,
zenodo::<id>. Reading a repository implies --online.

Naming a part of the catalogue checks only that part, which is what the bot's
"@chekhovbot check bundle" asks for.

The check command exits non-zero when a rule failed as an error. Without
--online it reads nothing but the file and its bundle, and the rules that need
Crossref, ORCID, Zenodo, the GitHub API or the register report that they were
not checked.
`

func main() {
	version, commit = build.Stamp(version, commit)
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
	case "serve":
		return runServe(args[1:], out)
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
	part := ""
	var target string

	for i := 0; i < len(args); i++ {
		switch argument := args[i]; {
		case argument == "--spec":
			if i+1 >= len(args) {
				return fmt.Errorf("--spec needs a specification version")
			}
			i++
			specVersion = args[i]
		case argument == "--strict":
			strict = true
		case argument == "--markdown":
			markdown = true
		case argument == "--online":
			online = true
		case check.IsPart(argument) && argument != "":
			part = argument
		case target != "":
			return fmt.Errorf("check takes one target, but got %q and %q", target, argument)
		default:
			target = argument
		}
	}
	if target == "" {
		return fmt.Errorf("check needs a path to a codecheck.yml, or a repository spec")
	}

	context, err := loadTarget(target, online)
	if err != nil {
		return err
	}

	report, err := check.RunPart(context, specVersion, strict, part)
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

// loadTarget reads a codecheck.yml from a path or from a repository.
//
// A repository has to be read before it can be checked, so asking for one is
// asking to go online.
func loadTarget(target string, online bool) (check.Context, error) {
	if check.IsRepositorySpec(target) {
		return check.FromRepository(target, check.Online())
	}
	context, err := check.FromFile(target)
	if err != nil {
		return context, err
	}
	if online {
		context = context.WithServices(check.Online())
	}
	return context, nil
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

	settings, err := config.Current()
	if err != nil {
		return err
	}

	// The same answer the deployed bot would post. The author is the local
	// user as far as roles go, which is who is asking.
	reply, addressed := bot.Preview(settings, version, commit, nil, os.Getenv("USER"), string(raw))
	if !addressed {
		fmt.Fprintln(out, "not addressed to the bot, no reply")
		return nil
	}
	fmt.Fprint(out, reply)
	return nil
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
	if commit != "" {
		fmt.Fprintf(out, "commit %s (https://github.com/codecheckers/chekhov/commit/%s)\n",
			build.Shorten(commit), commit)
	}
	if settings, err := config.Current(); err == nil {
		fmt.Fprintf(out, "working on %s as @%s (%s)\n",
			settings.TargetRepository(), settings.BotUser(), config.Environment())
	}

	provenance, err := rules.Provenance()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "rules from register commit %s, retrieved %s\n",
		build.Shorten(provenance.Commit()), provenance.Retrieved)
	for _, file := range provenance.Files {
		fmt.Fprintf(out, "  %s: %d rules for specification %s\n",
			file.Name, file.Rules, file.SpecVersion)
	}
	return nil
}
