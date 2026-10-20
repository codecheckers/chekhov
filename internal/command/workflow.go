package command

import (
	"fmt"
	"strings"
	"time"
)

// The replies for the three commands about where a check stands: `check
// start`, `status` and `check finish` (codecheckers/chekhov#22).
//
// All three are **wrappers**. The bot could already describe a repository,
// validate a `codecheck.yml` and read the record of a check; what it could not
// do was answer "what is the state of this check" in one command. So each of
// these runs the checks that already exist and assembles their answers, and
// nothing here re-implements a rule or a report.
//
// Because they run those checks they take real time - `check finish` runs the
// whole catalogue, which asks OpenAlex, ORCID and Zenodo - so the reply is
// written more than once: posted as soon as the first stage has answered, and
// rewritten in place as the rest come in. One comment, not two, as a reply to
// a role change already is; somebody arriving at the thread later reads one
// finished answer rather than a running commentary. The writing is
// internal/bot/progress.go; what is here is only the rendering, so that a
// half-finished body and a finished one go through the same function and
// cannot drift.

// A StageResult is what one stage of a staged reply contributed.
type StageResult struct {
	// Name is what the stage was reading, as the line about the ones still
	// running names it: "the repository", "the configuration".
	Name string
	// Body is the markdown it contributed, empty when it had nothing to say -
	// which is not the same as a stage that has not run, and is why the
	// pending ones are passed separately.
	Body string
}

// StagedReply is the comment as it stands: a heading, whatever the stages that
// have answered had to say, and a line naming the ones still running.
//
// One renderer for all three commands, because the three differ in what their
// stages do and not in how an unfinished answer should read. The line about
// what is still running carries the "skipped" symbol, `…`, which is the
// symbol for "no answer yet" everywhere else the bot reports an outcome.
func StagedReply(heading string, done []StageResult, pending []string) string {
	var out strings.Builder
	if heading != "" {
		fmt.Fprintf(&out, "**%s**\n", heading)
	}
	for _, stage := range done {
		if body := strings.TrimSpace(stage.Body); body != "" {
			out.WriteString("\n" + body + "\n")
		}
	}
	if len(pending) > 0 {
		fmt.Fprintf(&out, "\n… still reading %s. This comment is rewritten as each answers.\n",
			list(pending, "and"))
	}
	return out.String()
}

// list joins names the way a sentence does: "a, b and c". The conjunction is
// the caller's, because the same join says "a or b" when it is describing a
// choice rather than a set - see Definition.Who.
func list(names []string, conjunction string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " " + conjunction + " " + names[len(names)-1]
	}
}

// Abandoned is what a staged reply says when the command writing it did not
// finish - a panic, caught and reported elsewhere.
//
// Without it the comment sits on the check for ever saying it is "still
// reading", which is the one thing it must not do: somebody reading the thread
// would wait for an answer that is never coming, and the sweep has no record
// to chase it with because the command never got as far as writing one.
func Abandoned() string {
	return "\n✖ I stopped before I finished this. Nothing here is a verdict; " +
		"please run the command again.\n"
}

// A LabelChange is one label the bot moved on the check, or did not.
//
// Did not is reported rather than hidden, and the command carries on: the
// labels are the register's, a person can put one right in a second, and a
// command that undid its own work over a label would be worse than one that
// says which label is still wrong. See putOnIssue in internal/bot.
type LabelChange struct {
	Label string
	// Added says the label was put on; otherwise it was taken off.
	Added bool
	// Done says it happened. Why says why not when it did not, and is empty
	// when it did.
	Done bool
	Why  string
}

// LabelChanges says what happened to the check's labels, empty when nothing
// was to be done - a check already labelled the way the command would label it
// is not news, and a line saying so on every command is how the useful lines
// get skipped.
func LabelChanges(changes []LabelChange) string {
	var moved, failed []string
	for _, change := range changes {
		verb := "removed"
		if change.Added {
			verb = "added"
		}
		if change.Done {
			moved = append(moved, fmt.Sprintf("%s %s", verb, Code(change.Label)))
			continue
		}
		// Flattened here rather than by the caller: a GitHub error carries
		// the response body, and a proxy's HTML page would otherwise end the
		// line and take the rest of the reply with it. The renderer owns the
		// constraint, so no future caller has to remember it.
		failed = append(failed, fmt.Sprintf("could not %s %s: %s",
			strings.TrimSuffix(verb, "d"), Code(change.Label), oneLine(change.Why)))
	}

	var out strings.Builder
	if len(moved) > 0 {
		fmt.Fprintf(&out, "✔ Labels: %s.\n", list(moved, "and"))
	}
	for _, problem := range failed {
		fmt.Fprintf(&out, "⚠ %s\n", problem)
	}
	return out.String()
}

// StartHeading, StatusHeading and FinishHeading are the first line of each
// staged reply, so the same words appear on the half-finished comment and the
// finished one.
const (
	StartHeading  = "Starting this check"
	StatusHeading = "Where this check stands"
	FinishHeading = "Finishing this check"
)

// StartedBy is the line that makes the reply the record of the act: who asked
// for the check to be started, and when. The thread is the history; the labels
// are the current state.
func StartedBy(what, who string, when time.Time) string {
	return strings.TrimSpace(entry(what, who, when)) + "\n"
}

// NoAssignedCodechecker refuses `check start` on a check nobody is doing.
//
// A check is started by the person doing it. Starting one with nobody assigned
// would take it out of the queue that exists to find somebody, which is the
// one way for a check to be lost quietly.
func NoAssignedCodechecker() string {
	return fmt.Sprintf("Nobody is the assigned codechecker of this check, so there is nothing to "+
		"start: taking it out of the queue now would hide it from the people looking for work.\n\n"+
		"```\n%s assign @user as codechecker\n```\n", Bot)
}

// NoTarget says a command has nothing to read a `codecheck.yml` from.
//
// Nothing is guessed out of the issue's prose. The bot uses what it keeps -
// the certificate identifier in the record, looked up in `register.csv` - or
// what an editor names. A label moved on a guess is worse than a label not
// moved.
//
// `changed` is whether the command has already done something by the time it
// says this: `check start` has moved the labels and only the reading is
// missing, `check finish` has done nothing at all because the reading is what
// decides its labels. Saying "I have changed nothing" on the first would be
// untrue, and it is the sentence somebody acts on.
func NoTarget(command, what string, changed bool) string {
	opening := fmt.Sprintf("… I have nothing to read %s from", what)
	if !changed {
		opening += ", so I have changed nothing"
	}
	return fmt.Sprintf("%s. This check's record holds no certificate identifier "+
		"`register.csv` knows, so name the repository:\n\n```\n%s %s github::owner/repo\n```\n",
		opening, Bot, command)
}

// A Summarised is one wrapped check as a staged reply shows it: the part, one
// line of counts, and only the rules that need attention.
type Summarised struct {
	// Part is the piece of the catalogue that ran - "config", "bundle" - or
	// empty for all of it.
	Part string
	// Target is what was read, as a person wrote it.
	Target string
	// Summary is the report's own one-line count.
	Summary string
	// Findings are the lines for the rules that need attention - failures and
	// warnings - already carrying their own symbol, so that the wording and
	// the severity are the catalogue's rather than this package's.
	Findings []string
	// Failed says a rule failed at error severity, which is what decides the
	// symbol on the summary line: a warning is worth listing and is not a
	// configuration that failed.
	Failed bool
	// Problem is why nothing could be read, empty when something was. Never a
	// verdict: a repository that would not answer has told us nothing.
	Problem string
}

// CheckSummary is one wrapped check, as a stage of a staged reply.
func CheckSummary(s Summarised) string {
	what := "`codecheck.yml`"
	if s.Part != "" {
		what = fmt.Sprintf("`codecheck.yml` (%s)", s.Part)
	}
	if s.Problem != "" {
		return fmt.Sprintf("… I could not check %s of %s: %s\n", what, Code(s.Target), s.Problem)
	}

	var out strings.Builder
	symbol := "✔"
	switch {
	case s.Failed:
		symbol = "✖"
	case len(s.Findings) > 0:
		symbol = "⚠"
	}
	fmt.Fprintf(&out, "%s %s of %s: %s.\n", symbol, what, Code(s.Target), s.Summary)
	for _, finding := range s.Findings {
		fmt.Fprintf(&out, "- %s\n", finding)
	}
	return out.String()
}

// ReadingReply is the first thing `check finish` says: which repository it is
// about to run the rules against.
//
// It reads nothing, which is the point - it is on the issue before the slow
// work starts, so somebody who has just typed the command sees that it was
// heard rather than an issue with nothing on it.
func ReadingReply(target string) string {
	return fmt.Sprintf("Running the rules against %s.\n", Code(target))
}

// RegisterFindings is what has to be true of the register entry before the
// pull request adding it is opened: the register rules' findings out of the
// report that has just run.
//
// The passing line names `CC-REG` so it is not read as a verdict on the
// `CC-CFG` rules, and does not claim the entry is ready: this does not know
// whether the check is finished.
func RegisterFindings(findings []string) string {
	if len(findings) == 0 {
		return "✔ The register rules (`CC-REG`) pass.\n"
	}
	var out strings.Builder
	out.WriteString("The register entry needs attention first:\n")
	for _, finding := range findings {
		fmt.Fprintf(&out, "- %s\n", finding)
	}
	return out.String()
}

// Checklist is the codechecker's own list of what to do, posted when a check
// starts.
//
// In the bot's words rather than in the settings: it is not a fact about the
// register, it is the community workflow, and a register that wanted a
// different one would want a different bot. The steps are the guide's, short
// enough that somebody reads them here and follows the link for the detail.
func Checklist() string {
	steps := []string{
		"Say hello on this issue, so the author and the editor know you have it.",
		"Fork or create a repository for the check, and check the licensing is clear.",
		"Add a `codecheck` directory, and a `codecheck.yml` describing the check.",
		"Run the workflow from the manuscript, and record what happened - " +
			"document problems, do not fix them.",
		"Write the certificate as a PDF, with the who, the what and the how.",
		"Deposit it on Zenodo, and tell the editor it is done.",
	}
	var out strings.Builder
	out.WriteString("**Your checklist**\n\n")
	for _, step := range steps {
		fmt.Fprintf(&out, "- [ ] %s\n", step)
	}
	out.WriteString("\nThe whole of it is in the " +
		"[codechecker guide](https://codecheck.org.uk/guide/community-workflow-codechecker).\n")
	return out.String()
}

// A Standing is what `status` says about the check from what the bot keeps:
// the record, the labels and what earlier replies left outstanding. No part of
// it needs the network, so it is the stage that always answers.
type Standing struct {
	Holders Holders
	Told    Provenance
	// TitleCertificate is the identifier the issue's title carries, empty
	// when it carries none. Shown only when it disagrees with the record:
	// agreement is not news.
	TitleCertificate string
	// Labels are the check's labels as they are, and LabelsRead says whether
	// they could be read at all.
	Labels     []string
	LabelsRead bool
	// Assignees are the issue's GitHub assignees.
	Assignees []string
	// Outstanding is what earlier replies left for somebody to do, and Unread
	// is what could not be read while looking.
	Outstanding []Outstanding
	Unread      []string
}

// StatusBody is the part of `status` that the bot can always answer.
func StatusBody(s Standing) string {
	var out strings.Builder

	out.WriteString(RolesTable(s.Holders))
	if sentence := s.Told.Sentence(); sentence != "" {
		out.WriteString("\n" + sentence + "\n")
	}
	if s.TitleCertificate != "" && !strings.EqualFold(s.TitleCertificate, s.Holders.Certificate) {
		fmt.Fprintf(&out, "\n⚠ The title carries %s, and the record %s.\n",
			Code(s.TitleCertificate), certificateOrNone(s.Holders.Certificate))
	}

	out.WriteString("\n| | |\n|---|---|\n")
	fmt.Fprintf(&out, "| Assignees | %s |\n", mentions(s.Assignees))
	if s.LabelsRead {
		fmt.Fprintf(&out, "| Labels | %s |\n", CodeList(s.Labels))
	} else {
		out.WriteString("| Labels | I could not read them |\n")
	}

	if len(s.Outstanding) > 0 {
		out.WriteString("\n**Still open**\n\n")
		for _, item := range s.Outstanding {
			fmt.Fprintf(&out, "- %s\n", item.Line())
		}
	}
	for _, problem := range s.Unread {
		fmt.Fprintf(&out, "\n⚠ %s\n", problem)
	}
	return out.String()
}

// certificateOrNone is an identifier as a sentence names it, for the one place
// that has to say a record holds none.
func certificateOrNone(id string) string {
	if id == "" {
		return "none"
	}
	return Code(id)
}

// An Outstanding is one thing an earlier reply left for somebody to do, as
// `status` lists it.
type Outstanding struct {
	// Kind is the follow-up's kind, as the record carries it.
	Kind string
	// Subject is whose it is about, and Since when it was raised - zero when
	// the record does not say.
	Subject string
	Since   time.Time
}

// Line is one outstanding thing, for the list under `status`.
//
// Here rather than in the caller because of the handle: `mention` is the one
// place that writes a handle so that it does not notify, and a summary of a
// check must not notify the people it is about every time somebody asks where
// it stands.
func (o Outstanding) Line() string {
	what := strings.ReplaceAll(o.Kind, "-", " ")
	if o.Since.IsZero() {
		return fmt.Sprintf("%s, about %s", what, mention(o.Subject))
	}
	return fmt.Sprintf("%s, about %s, since %s",
		what, mention(o.Subject), o.Since.UTC().Format("2006-01-02"))
}

// FinishedBody is what `check finish` says once the labels have moved: what
// the rules decided, and what happens next.
//
// The next step is the register entry, and it is named because the thread is
// where somebody finds out what to do and the register pull request is what
// closes the issue. Closing it is deliberately not a command here: the merged
// pull request does that, with `closes #N`.
//
// It is named in words because there is no `register` command yet (#21);
// TestRepliesNameOnlyCommandsThatExist keeps it that way.
func FinishedBody(pending bool, ruleNote string) string {
	var out strings.Builder
	if pending {
		out.WriteString("⚠ The configuration still has placeholder values, so this check is " +
			"not finished: I have marked it as waiting on metadata rather than clearing it.\n")
		if ruleNote != "" {
			out.WriteString("\n" + ruleNote + "\n")
		}
		out.WriteString("\nRun me again once the DOIs are real.\n")
		return out.String()
	}
	out.WriteString("✔ Nothing is left pending. The next step is the register entry: " +
		"a pull request adding the certificate to `register.csv`. " +
		"`" + Bot + " check register` shows the row it would add and runs the rules on it, " +
		"and `" + Bot + " register` opens the pull request.\n")
	return out.String()
}
