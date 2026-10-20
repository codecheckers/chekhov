package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
)

// Where a check stands: `check start`, `status` and `check finish`
// (codecheckers/chekhov#22).
//
// An editor keeps the state of a check in the register's labels by hand, and
// they drift the moment a step is forgotten. These three tie the labels to the
// steps - and each of them is a **wrapper**: the repository description, the
// rules and the record of the check all exist already, and what was missing
// was one command that asks for them together at the moment they matter.
//
// Because they wrap those checks they take real time, so each answers through
// a progress (progress.go): the labels move and the reply appears at once, and
// the same comment is rewritten as the slower stages come in.

// Labelling is what these commands need of the reply path: the two writes,
// both already fenced by the settings in internal/github. A path without them
// - the command line preview, the test recorder - moves no label and the reply
// says so.
type Labelling interface {
	AddLabel(ctx context.Context, repository string, issue int, label string) error
	RemoveLabel(ctx context.Context, repository string, issue int, label string) error
}

var _ Labelling = (*github.Client)(nil)

// checkStart begins a check: the labels move, the repository is described, the
// configuration is looked for, and the codechecker gets their checklist.
func (s *Server) checkStart(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) answered {
	about, refusal := s.about(ctx, event, parsed, services, true)
	if refusal != "" {
		return said(refusal)
	}
	if about.reading.Record.AssignedCodechecker == "" {
		return said(command.NoAssignedCodechecker())
	}
	issue, known, target := about.issue, about.labelsRead, about.target

	// The labels first, and on their own: they are what the command is for,
	// they take one request each, and an editor watching the issue sees the
	// check move out of the queue before anything slower is asked.
	first := func(context.Context) string {
		moved := s.moveLabels(ctx, event, issue, known,
			off(s.Settings.NeedsCodecheckerLabel()), on(s.Settings.WorkInProgressLabel()))
		return moved + command.StartedBy("started", event.Author, time.Now())
	}

	stages := []stage{{Name: "the labels", Run: first}}
	if target == "" {
		stages = append(stages, fixed(command.NoTarget("check start", "the repository under check", true)))
	} else {
		stages = append(stages,
			stage{Name: "the repository", Run: s.describing(target, services)},
			stage{Name: "the configuration",
				Run: s.summarising(s.loading(target, services), target, "config")})
	}
	stages = append(stages, fixed(command.Checklist()))

	return (&progress{server: s, event: event, heading: command.StartHeading, stages: stages}).run(ctx)
}

// status says where this check stands.
//
// It changes nothing, which is why it is not `check status`: it reports what
// the bot keeps and what the rules say, and reserving an identifier or moving
// a label is somebody else's command.
func (s *Server) status(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) answered {
	// Reported as it is, edit and all: `status` acts on nothing, and this
	// reply is where an editor finds out the record was edited.
	about, refusal := s.about(ctx, event, parsed, services, false)
	if refusal != "" {
		return said(refusal)
	}
	target := about.target

	stages := []stage{{Name: "the record", Run: func(context.Context) string {
		return command.StatusBody(s.standing(ctx, event, about.reading))
	}}}
	if target == "" {
		stages = append(stages, fixed(command.NoTarget("status", "the configuration", false)))
	} else {
		loaded := s.loading(target, services)
		stages = append(stages,
			stage{Name: "the configuration", Run: s.summarising(loaded, target, "config")},
			stage{Name: "the bundle", Run: s.summarising(loaded, target, "bundle")})
	}

	return (&progress{server: s, event: event, heading: command.StatusHeading, stages: stages}).run(ctx)
}

// checkFinish ends a check: the rules decide the labels, and the register
// entry is what comes next.
//
// It refuses rather than guesses. Whether the check is finished or waiting on
// metadata is `CC-CFG-023`'s answer, and without a configuration to ask it of
// there is no answer - so nothing moves and the reply says why. A rule that
// was skipped is "could not check" and counts the same way.
func (s *Server) checkFinish(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) answered {
	about, refusal := s.about(ctx, event, parsed, services, true)
	if refusal != "" {
		return said(refusal)
	}
	issue, known, target := about.issue, about.labelsRead, about.target
	if target == "" {
		return said(command.NoTarget("check finish", "the configuration", false))
	}

	// The whole catalogue, because this is the moment it matters. It is what
	// takes the time - OpenAlex, ORCID, Zenodo - so it runs *inside* a stage
	// rather than before the progress starts: the comment saying which
	// repository is being read has to be on the issue while that happens, or
	// the staging buys this command, the slowest of the three, nothing.
	var report check.Report
	var reportErr error
	// What the placeholder rule decided, read once: the register stage and the
	// labels stage must agree on it, or #60 comes back.
	var pending, decided bool
	stages := []stage{
		fixed(command.ReadingReply(target)),
		{Name: "the rules", Run: func(context.Context) string {
			report, reportErr = s.reportOn(target, "", services)
			pending, decided = placeholdersLeft(report)
			return command.CheckSummary(summarised("", target, report, reportErr))
		}},
		// What has to be true of the register entry, said on its own because
		// it is the next step. It reads the results in hand rather than
		// asking again: the wide run has happened and has paid for itself.
		fixed2(func() string {
			if reportErr != nil || report.Refused != nil {
				// The rules did not run, so they did not pass: saying the
				// entry is ready would be the one sentence somebody acts on.
				return ""
			}
			findings := registerFailures(report)
			if pending && len(findings) == 0 {
				// Waiting on metadata: a passing line would sit above "not
				// finished", and a failure is still news the codechecker
				// would otherwise meet for the first time after the DOIs.
				return ""
			}
			return command.RegisterFindings(findings)
		}),
		{Name: "the labels", Run: func(context.Context) string {
			if reportErr != nil || report.Refused != nil {
				return "… I could not read the configuration, so I have moved no label: " +
					"a rule that could not be asked is not a rule that passed.\n"
			}
			if !decided {
				return fmt.Sprintf("… `%s` could not be checked, so I have moved no label. "+
					"Run `%s check config %s` to see why.\n",
					placeholderRule, command.Bot, target)
			}
			changes := []labelMove{off(s.Settings.WorkInProgressLabel())}
			if pending {
				changes = append(changes, on(s.Settings.MetadataPendingLabel()))
			} else {
				changes = append(changes, off(s.Settings.MetadataPendingLabel()))
			}
			moved := s.moveLabels(ctx, event, issue, known, changes...)
			return moved + command.StartedBy("finished", event.Author, time.Now()) + "\n" +
				command.FinishedBody(pending, ruleNote(placeholderRule))
		}},
	}
	return (&progress{server: s, event: event, heading: command.FinishHeading, stages: stages}).run(ctx)
}

// whatTheBotKnows is the check as all three commands find it: its labels, its
// record, and what to read a configuration from.
//
// One prologue, because the three asked the same four questions with the same
// three error sentences, and a wording fixed in one of them would have stayed
// wrong in the other two.
type whatTheBotKnows struct {
	issue      github.Issue
	labelsRead bool
	reading    people.Reading
	// target is what to read a `codecheck.yml` from, empty when there is
	// nothing: what each command does about that is its own.
	target string
}

// about reads it, or says why it could not.
//
// A record somebody edited grants nothing, and these commands act on what it
// says: the assigned codechecker in it is what lets `check start` take the
// check out of the queue, and the certificate in it is what `check finish`
// resolves into a repository to run the rules against and then move labels on.
// A repository collaborator can edit a comment of the bot's, leaving the bot
// as its author, which is why the record is signed - so a reading that does
// not verify is refused here, as it is everywhere else the record decides
// something (bot.go, labels.go, suggest.go, certificate.go).
//
// `status` is the exception, and takes the reading as it is: it reports rather
// than acts, and showing what an edited record says - with the provenance
// beside it - is how an editor finds out it was edited at all.
func (s *Server) about(ctx context.Context, event mention, parsed command.Command,
	services *check.Services, trusted bool) (whatTheBotKnows, string) {
	if reply, ok := s.noCheck(event, "the state of a check"); !ok {
		return whatTheBotKnows{}, reply
	}
	reading, err := s.Checks.Read(ctx, event.Repository, event.Issue)
	if err != nil {
		return whatTheBotKnows{}, fmt.Sprintf("I could not read the record of this check: %s\n", err)
	}
	if trusted && reading.Tampered != nil {
		return whatTheBotKnows{}, tamperedReply(reading.Tampered)
	}
	issue, labelsRead := s.labelsOf(ctx, event)
	return whatTheBotKnows{issue: issue, labelsRead: labelsRead, reading: reading,
		target: s.targetFor(parsed.Args, reading.Record, services)}, ""
}

// standing is what `status` can answer without leaving the register.
func (s *Server) standing(ctx context.Context, event mention, reading people.Reading) command.Standing {
	told := command.Standing{Holders: reading.Record.Holders(), Told: provenanceOf(reading)}
	if issue, err := s.issue(ctx, event); err == nil {
		told.Labels, told.LabelsRead, told.Assignees = issue.Labels, true, issue.Assignees
		if titled, ok := check.TitleCertificate(issue.Title); ok {
			told.TitleCertificate = titled
		}
	} else {
		told.Unread = append(told.Unread, fmt.Sprintf("I could not read this check: %s", err))
	}

	reader, ok := s.Replies.(Reader)
	if !ok {
		told.Unread = append(told.Unread,
			"I cannot read this issue's comments here, so I do not know what earlier replies left open.")
		return told
	}
	records, unread := s.outstandingIn(ctx, reader, event)
	told.Unread = append(told.Unread, unread...)
	for _, record := range records {
		told.Outstanding = append(told.Outstanding, command.Outstanding{
			Kind: record.Kind, Subject: record.Subject, Since: record.Raised()})
	}
	return told
}

// targetFor is what to read a `codecheck.yml` from.
//
// What an editor named, or the repository an editor set for the register row,
// or else the certificate identifier this check has reserved, which
// `register.csv` can turn into a repository. Nothing is read out of the
// issue's prose here: these commands move labels, and a label moved on a
// guess is worse than a label not moved. `check register` does read the
// Repository line, because it moves nothing and shows where each column came
// from.
func (s *Server) targetFor(args []string, record people.Record, services *check.Services) string {
	for _, argument := range args {
		if target := check.TargetIn(argument); target != "" {
			return target
		}
	}
	if record.Repository != "" {
		return record.Repository
	}
	if record.Certificate == "" {
		return ""
	}
	// Only a certificate register.csv knows names a repository; one reserved
	// for a check still in progress has no row yet, and is not a target.
	if _, err := check.ResolveTarget(record.Certificate, services); err != nil {
		return ""
	}
	return record.Certificate
}

// describing is the `check repository` stage: what the codechecker is taking
// on, rather than a verdict on it.
func (s *Server) describing(target string, services *check.Services) func(context.Context) string {
	return func(context.Context) string {
		description, err := check.DescribeRepository(target, s.LocalPaths, services)
		if err != nil {
			return fmt.Sprintf("… I could not look at %s: %s\n", command.Code(target), err)
		}
		return description.Markdown()
	}
}

// summarising is one wrapped part of the catalogue, shown as its counts and
// the rules that need attention - never the whole table, which would bury the
// few lines somebody has to act on.
//
// The configuration is read once per command, not once per part: `status` asks
// for the config rules and then the bundle rules, and loading twice would
// resolve the target, parse the YAML and work out the specification version
// again for the same file. The responses are cached either way, so what this
// saves is the work after them.
func (s *Server) summarising(loaded func() (check.Context, error), target, part string,
) func(context.Context) string {
	return func(context.Context) string {
		context, err := loaded()
		if err != nil {
			return command.CheckSummary(command.Summarised{Part: part, Target: target,
				Problem: err.Error()})
		}
		report, err := check.RunPart(context, "", false, part)
		return command.CheckSummary(summarised(part, target, report, err))
	}
}

// loading reads a configuration once, however many parts ask for it. The error
// is remembered too, so a repository having a bad moment costs one attempt
// rather than one per stage.
func (s *Server) loading(target string, services *check.Services) func() (check.Context, error) {
	return sync.OnceValues(func() (check.Context, error) {
		context, err := s.read(target, services)
		if err != nil {
			return context, fmt.Errorf("I could not read `%s`: %w", target, err)
		}
		return context, nil
	})
}

// summarised is a report as a stage shows it.
func summarised(part, target string, report check.Report, err error) command.Summarised {
	shown := command.Summarised{Part: part, Target: target}
	switch {
	case err != nil:
		shown.Problem = err.Error()
	case report.Refused != nil:
		shown.Problem = report.Refused.Why
	default:
		shown.Summary = report.Summary()
		shown.Failed = len(report.Failures()) > 0
		for _, result := range needingAttention(report) {
			shown.Findings = append(shown.Findings,
				check.Symbol(result.Outcome)+" "+result.Line())
		}
	}
	return shown
}

// registerFailures are the register rules' findings, out of a report that has
// already run. The area is the catalogue's own, so there is no second list
// here of which rules are about the register.
func registerFailures(report check.Report) []string {
	var found []string
	for _, result := range needingAttention(report) {
		if result.Rule.Area == registerArea {
			found = append(found, check.Symbol(result.Outcome)+" "+result.Line())
		}
	}
	return found
}

// needingAttention is every result a reply should list: the failures and the
// warnings.
//
// Report.Failures is errors alone, which is right for "is this valid?" and
// wrong for "what do I have to act on?" - a warning is a rule that needs
// attention, it is counted in the one-line summary, and a reply that showed
// the count without the line would leave somebody looking for it.
func needingAttention(report check.Report) []check.RuleResult {
	var found []check.RuleResult
	for _, result := range report.Results {
		if result.Outcome == check.OutcomeError || result.Outcome == check.OutcomeWarning {
			found = append(found, result)
		}
	}
	return found
}

// registerArea is the rule area about the register's own row and issue, as the
// catalogue names it - the part `check certificate` narrows to.
const registerArea = "register"

// placeholdersLeft says whether CC-CFG-023 found placeholder values, and
// whether it could tell at all. A rule that was skipped has said nothing, and
// nothing is not agreement.
func placeholdersLeft(report check.Report) (pending, decided bool) {
	for _, result := range report.Results {
		if result.Rule.ID != placeholderRule {
			continue
		}
		switch result.Outcome {
		case check.OutcomeError, check.OutcomeWarning:
			return true, true
		case check.OutcomeSkipped, check.OutcomeUnchecked:
			return false, false
		default:
			return false, true
		}
	}
	return false, false
}

// placeholderRule is the rule that decides whether a finished check is
// waiting on metadata: a DOI still written FIXME is not a published record.
const placeholderRule = "CC-CFG-023"

// A labelMove is one label a command would like on or off the check.
type labelMove struct {
	label string
	add   bool
}

func on(label string) labelMove  { return labelMove{label: label, add: true} }
func off(label string) labelMove { return labelMove{label: label} }

// moveLabels puts the labels a command asks for on and takes the others off,
// and says what happened.
//
// A label the check already carries, or already lacks, is not a request: the
// bot makes no request GitHub would answer with nothing, and a reply that
// reported it would be noise on every command. A label the settings do not
// name at all switches the whole question off, which is what an empty name
// means everywhere in the settings.
//
// Whether the bot may change a label is **not** decided here. internal/github
// is the fence, as it is for team membership, and asking the settings first
// would be a second copy of the policy fed by a second list. What comes back
// is a typed error, so the reply can still say why in its own words.
func (s *Server) moveLabels(ctx context.Context, event mention, issue github.Issue, known bool,
	moves ...labelMove) string {
	writer, canWrite := s.Replies.(Labelling)
	var changes []command.LabelChange

	for _, move := range moves {
		label := strings.TrimSpace(move.label)
		if label == "" || (known && command.HasLabel(issue.Labels, label) == move.add) {
			continue
		}
		change := command.LabelChange{Label: label, Added: move.add}
		switch {
		case !canWrite:
			change.Why = "I cannot change a label from here: ask me on the checks issue"
		default:
			change.Why = s.moveLabel(ctx, writer, event, label, move.add)
		}
		if change.Done = change.Why == ""; change.Done {
			// The memo the dispatch judges the labels from has to know: a
			// note built on the labels as they were would warn that the check
			// still carries one this reply has just reported removing.
			event.read.moveLabel(label, move.add)
		}
		changes = append(changes, change)
	}
	return command.LabelChanges(changes)
}

// moveLabel is the one write, and why it did not happen.
func (s *Server) moveLabel(ctx context.Context, writer Labelling, event mention,
	label string, add bool) string {
	write := writer.RemoveLabel
	if add {
		write = writer.AddLabel
	}
	return s.labelFailure(write(ctx, event.Repository, event.Issue, label), event, label, add)
}

// labelFailure is why a label write did not happen, empty when it did.
//
// Shared by every command that moves a label - the workflow commands and
// `set certificate` - so that a refusal reads the same wherever it came from.
// It is the wording that is shared and not the writer: `set certificate` only
// ever adds, and demanding a reply path that can also remove would refuse it
// on a deployment that has no business removing anything.
//
// A failure is reported rather than undone: the labels are the register's, a
// person can put one right in a second, and a command that rolled back its own
// work over a label would be worse than one that says which label is wrong.
func (s *Server) labelFailure(err error, event mention, label string, add bool) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, github.ErrLabelNotAllowed):
		// The settings are a deployment's decision, not a fault, so the
		// reason is said plainly and not logged as a problem.
		return "the settings do not list it under `labels.managed`"
	default:
		s.Logger.Warn("a label could not be changed", "issue", event.Issue,
			"label", label, "add", add, "error", err)
		return err.Error()
	}
}

// labelsOf is the check's current labels, and whether they could be read.
//
// Not being able to read them is not a refusal. Both writes are idempotent -
// GitHub answers an add of a label already there with the same list, and a
// remove of one that is not there with a 404 the client already treats as
// done - so a command that cannot see the labels asks for what it wants and
// is right either way. What it must not do is claim it changed something it
// cannot see, which is why `known` is carried rather than assumed.
func (s *Server) labelsOf(ctx context.Context, event mention) (github.Issue, bool) {
	issue, err := s.issue(ctx, event)
	if err != nil {
		s.Logger.Warn("a check's labels could not be read before moving them",
			"issue", event.Issue, "error", err)
		return github.Issue{}, false
	}
	return issue, true
}

// fixed is a stage with nothing to read: words the command already has. It has
// no name, so the reply never says it is "still reading" something it is
// holding, and no write is posted to announce one.
func fixed(body string) stage {
	return stage{Run: func(context.Context) string { return body }}
}

// fixed2 is the same, for a body that is not known until an earlier stage has
// run: no reading of its own, so nothing waits for it, but it cannot be built
// in advance either.
func fixed2(body func() string) stage {
	return stage{Run: func(context.Context) string { return body() }}
}
