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

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/bot"
	"github.com/codecheckers/chekhov/internal/build"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/rules"
)

var usage = fmt.Sprintf(`chekhov - the CODECHECK register bot

Usage:
  chekhov check [options] <path to codecheck.yml, a repository, or a certificate>
  chekhov check [options] [<part>|repository] <target>
  chekhov comment [--as <handle>] [--online] <path to a comment file, or - for stdin>
  chekhov serve [--addr :8080]
  chekhov rules [--spec <version>]
  chekhov rules --check [--markdown]
  chekhov version
  chekhov record-key

Options: --spec <version>, --strict, --markdown, --online

A part of the catalogue is one of: %s.

The target is a path, or a repository the way register.csv names one:
github::org/repo, github::org/repo|sub/dir, gitlab::group/project, osf::<id>,
zenodo::<id>. The platform may be left off: owner/repo is read as GitHub
(chchck/... as GitLab), five characters as OSF, digits as Zenodo, and a
certificate identifier like 2020-001 is looked up in the register. Reading a
repository implies --online.

Naming a part of the catalogue checks only that part, which is what the bot's
"@chekhovbot check bundle" asks for. "check repository" is not a part: it
describes the repository under check - languages, licence, size, whether there
is a codecheck.yml - rather than judging one, and nothing in it passes or
fails.

"rules --check" asks the register whether the bundled rules are still its
current ones, and exits non-zero when they are behind - and when it could not
ask, because a check that cannot run must not report agreement. It is what the
weekly workflow runs.

The check command exits non-zero when a rule failed as an error. Without
--online it reads nothing but the file and its bundle, and the rules that need
Crossref, ORCID, Zenodo, the GitHub API or the register report that they were
not checked.
`, strings.Join(check.Parts(), ", "))

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
	case "serve":
		return runServe(args[1:], out)
	case "rules":
		return runRules(args[1:], out)
	case "version":
		return runVersion(out)
	case "record-key":
		return runRecordKey(out)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

// describeRepository answers "check repository", which reports what the
// repository is rather than whether its configuration is valid - so it has no
// verdict and cannot exit non-zero.
func describeRepository(target string, services *check.Services, markdown bool, out io.Writer) error {
	description, err := check.DescribeRepository(target, true, services)
	if err != nil {
		return err
	}
	if markdown {
		fmt.Fprint(out, description.Markdown())
	} else {
		fmt.Fprint(out, description.Text())
	}
	return nil
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
		case check.IsWord(argument):
			part = argument
		case target != "":
			return fmt.Errorf("check takes one target, but got %q and %q", target, argument)
		default:
			target = argument
		}
	}
	if target == "" {
		return fmt.Errorf("check needs a path to a codecheck.yml, a repository, or a certificate identifier")
	}

	// Whether the target is a path or a repository, and whether that needs the
	// services, is check's to decide.
	services := check.ServicesFor(target, online)
	if part == check.AboutRepository {
		return describeRepository(target, services, markdown, out)
	}

	context, err := check.Load(target, true, services)
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

// runComment shows what the bot would reply to a comment, which is how the
// command handling is exercised while there is no webhook.
func runComment(args []string, out io.Writer) error {
	// The author is the local user as far as roles go, which is who is asking,
	// unless --as says otherwise: an editor previewing an editor's command.
	author, online, source := os.Getenv("USER"), false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--as":
			if i+1 >= len(args) {
				return fmt.Errorf("--as needs a GitHub handle")
			}
			i++
			author = strings.TrimPrefix(args[i], "@")
		case "--online":
			online = true
		default:
			source = args[i]
		}
	}
	if source == "" {
		return fmt.Errorf("comment needs a file, or - for stdin")
	}

	var raw []byte
	var err error
	if source == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(source)
	}
	if err != nil {
		return err
	}

	settings, err := config.Current()
	if err != nil {
		return err
	}

	// The same answer the deployed bot would post. Nothing is posted from the
	// command line, to GitHub or to Mastodon.
	var services *check.Services
	if online {
		services = check.Online()
	}
	reply, addressed := bot.Preview(settings, services, author, string(raw))
	if !addressed {
		fmt.Fprintln(out, "not addressed to the bot, no reply")
		return nil
	}
	fmt.Fprint(out, reply)
	return nil
}

func runRules(args []string, out io.Writer) error {
	specVersion := rules.Newest()
	drift, markdown := false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--spec" && i+1 < len(args):
			i++
			specVersion = args[i]
		case args[i] == "--check":
			drift = true
		case args[i] == "--markdown":
			// Only --check renders; on a plain listing this is a no-op.
			markdown = true
		}
	}
	if drift {
		return runRulesCheck(markdown, out)
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

// runRulesCheck reports whether the bundled rules are the register's current
// ones, and is what the weekly workflow runs (chekhov#43).
//
// Not being able to ask is an error as much as being behind is: a scheduled
// job that goes green when it could not do its one job is worse than no job,
// and the two are told apart by what it prints.
func runRulesCheck(markdown bool, out io.Writer) error {
	drift, err := check.RulesDrift(check.Online())
	if err != nil {
		return err
	}
	if markdown {
		fmt.Fprint(out, drift.Markdown())
	} else {
		fmt.Fprint(out, drift.Text())
	}
	// Behind first, and for the same reason the report puts it first: proof
	// outranks an unmade comparison, and a run that has both should say the
	// thing that can be acted on.
	switch {
	case drift.Behind():
		// What to do about it is the line Text has just printed; the error
		// only has to make the exit status say why.
		return fmt.Errorf("the bundled rules are behind %s", drift.Register)
	case !drift.Known():
		return fmt.Errorf("could not check the rules against %s: %s", drift.Register, drift.Why())
	}
	return nil
}

// runRecordKey makes the key the bot signs a check's roles with.
//
// Printed rather than written anywhere: the private half goes into the
// deployment's configuration, and the public half into the register, so that
// anybody can verify a roles record without asking the bot. See
// docs/record-key.md.
func runRecordKey(out io.Writer) error {
	signer, err := people.GenerateSigner()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "CHEKHOV_RECORD_KEY=%s\n", signer.PrivateKey())
	fmt.Fprintf(out, "public key: %s\n", signer.PublicKey())
	fmt.Fprint(out, "\nThe first line is a secret: it goes in the deployment's configuration, "+
		"and nowhere else.\nThe public key is for publishing, so that a roles record can be "+
		"verified by anyone.\n")
	return nil
}

func runVersion(out io.Writer) error {
	fmt.Fprintf(out, "chekhov %s\n", build.Version)
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
