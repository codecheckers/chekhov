package command

import (
	"strings"
	"testing"
	"time"
)

// The bodies of the three commands about where a check stands
// (codecheckers/chekhov#22). No I/O, so what a reader sees is checked here and
// what the bot does about it in internal/bot.

// A half-finished body goes through the same renderer as a finished one, so
// the two cannot read differently - and it says which stages have not answered
// yet, or a short answer looks like a complete one.
func TestAStagedBodySaysWhatIsStillRunning(t *testing.T) {
	done := []StageResult{{Name: "the labels", Body: "✔ Labels: removed `needs codechecker`."}}
	working := StagedReply(StartHeading, done, []string{"the repository", "the configuration"})

	for _, say := range []string{StartHeading, "removed `needs codechecker`",
		"still reading the repository and the configuration"} {
		if !strings.Contains(working, say) {
			t.Errorf("the working body does not say %q:\n%s", say, working)
		}
	}

	finished := StagedReply(StartHeading, done, nil)
	if strings.Contains(finished, "still reading") {
		t.Errorf("the finished body still says it is working:\n%s", finished)
	}
	// A stage that answered with nothing contributes nothing, rather than a
	// blank line somebody has to wonder about.
	quiet := StagedReply("", []StageResult{{Name: "the labels", Body: "  \n"}}, nil)
	if strings.TrimSpace(quiet) != "" {
		t.Errorf("a stage with nothing to say left something behind: %q", quiet)
	}
}

// Agreement is silence: a label already the way the command wants it produces
// no line, because a reply that reports a change on every command is how the
// useful lines get skipped.
func TestLabelChangesSaysOnlyWhatChanged(t *testing.T) {
	if got := LabelChanges(nil); got != "" {
		t.Errorf("nothing to report produced %q", got)
	}

	moved := LabelChanges([]LabelChange{
		{Label: "needs codechecker", Done: true},
		{Label: "work in progress", Added: true, Done: true},
	})
	if !strings.Contains(moved, "removed `needs codechecker` and added `work in progress`") {
		t.Errorf("the line does not read as a sentence:\n%s", moved)
	}

	// A reason with newlines in it - a proxy's HTML page behind a GitHub
	// error - is flattened by the renderer, so one bad response cannot end
	// the line and take the rest of the reply with it.
	failed := LabelChanges([]LabelChange{
		{Label: "needs codechecker", Why: "502 Bad Gateway\n<html>the proxy's page</html>"},
	})
	if !strings.Contains(failed, "could not remove `needs codechecker`") {
		t.Errorf("the failure is not reported:\n%s", failed)
	}
	if strings.Count(failed, "\n") != 1 {
		t.Errorf("the reason's newlines reached the reply:\n%s", failed)
	}
}

// The checklist is the codechecker's own list, and it links the guide rather
// than trying to replace it.
func TestTheChecklistLinksTheGuide(t *testing.T) {
	checklist := Checklist()
	if !strings.Contains(checklist, "https://codecheck.org.uk/guide/community-workflow-codechecker") {
		t.Errorf("the checklist does not link the guide:\n%s", checklist)
	}
	if got := strings.Count(checklist, "- [ ] "); got < 5 {
		t.Errorf("the checklist has %d steps", got)
	}
	// Nothing in it may notify anybody: it is posted on every check that
	// starts, and a stray handle would notify whoever owns it every time.
	if strings.Contains(checklist, "@") {
		t.Errorf("the checklist writes a handle:\n%s", checklist)
	}
}

// A check that could not be read has told us nothing, and nothing is not a
// pass: the line says so rather than counting zero failures.
func TestACheckThatCouldNotBeReadIsNotAPass(t *testing.T) {
	unread := CheckSummary(Summarised{Part: "config", Target: "github::codecheckers/demo",
		Problem: "the repository answered 404"})
	if !strings.HasPrefix(unread, "…") {
		t.Errorf("an unread check does not carry the skipped symbol:\n%s", unread)
	}
	if strings.Contains(unread, "✔") {
		t.Errorf("an unread check reads as a pass:\n%s", unread)
	}

	failed := CheckSummary(Summarised{Part: "config", Target: "github::codecheckers/demo",
		Summary: "20 passed, 1 failed", Failed: true,
		Findings: []string{"✖ CC-CFG-023 at line 19: FIXME"}})
	if !strings.Contains(failed, "✖") || !strings.Contains(failed, "CC-CFG-023") {
		t.Errorf("the failing check does not list its finding:\n%s", failed)
	}

	// A warning needs attention and is not a failure: it is listed, and the
	// summary line does not claim the configuration is wrong.
	warned := CheckSummary(Summarised{Part: "config", Target: "github::codecheckers/demo",
		Summary:  "20 passed, 0 failed, 1 warnings",
		Findings: []string{"⚠ CC-CFG-016: no ORCID for one author"}})
	if !strings.HasPrefix(warned, "⚠") {
		t.Errorf("a warning reads as a pass or a failure:\n%s", warned)
	}
	if !strings.Contains(warned, "CC-CFG-016") {
		t.Errorf("the warning is counted but not listed:\n%s", warned)
	}
}

// `status` shows the record through the same renderer `roles` does, so the two
// cannot disagree about who holds what.
func TestStatusShowsTheRecordAndTheLabels(t *testing.T) {
	body := StatusBody(Standing{
		Holders: Holders{AssignedCodechecker: "a-codechecker", Certificate: "2026-001"},
		Labels:  []string{"needs codechecker", "journal"}, LabelsRead: true,
		Assignees: []string{"a-codechecker"},
		Outstanding: []Outstanding{{Kind: "organisation-invitation", Subject: "a-codechecker",
			Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}},
	})
	for _, say := range []string{"`@a-codechecker`", "2026-001", "needs codechecker",
		"journal", "Still open", "organisation invitation"} {
		if !strings.Contains(body, say) {
			t.Errorf("the body does not say %q:\n%s", say, body)
		}
	}
	// Every handle in backticks: a summary of a check must not notify the
	// people it is about every time somebody asks where it stands.
	if strings.Contains(strings.ReplaceAll(body, "`@", ""), "@") {
		t.Errorf("a handle in the body would notify somebody:\n%s", body)
	}
}

// An identifier in the title that is not the one in the record is worth
// saying; agreement is not.
func TestStatusSaysWhenTheTitleAndTheRecordDisagree(t *testing.T) {
	agreeing := StatusBody(Standing{
		Holders: Holders{Certificate: "2026-001"}, TitleCertificate: "2026-001", LabelsRead: true})
	if strings.Contains(agreeing, "The title carries") {
		t.Errorf("agreement was reported:\n%s", agreeing)
	}

	disagreeing := StatusBody(Standing{
		Holders: Holders{Certificate: "2026-001"}, TitleCertificate: "2026-002", LabelsRead: true})
	if !strings.Contains(disagreeing, "The title carries `2026-002`") {
		t.Errorf("the disagreement was not reported:\n%s", disagreeing)
	}
}

// Labels that could not be read are not labels the check does not carry.
func TestStatusTellsUnreadLabelsFromNone(t *testing.T) {
	unread := StatusBody(Standing{})
	if !strings.Contains(unread, "I could not read them") {
		t.Errorf("unread labels read as none:\n%s", unread)
	}
}

// A check still holding placeholder DOIs is not finished, and the rule that
// says so is named: an identifier alone tells a reader nothing, so the note
// carries the rule's own description.
func TestFinishedSaysWhyItIsStillPending(t *testing.T) {
	pending := FinishedBody(true, RuleNote("✖", "CC-CFG-023", "no placeholder values are left"))
	for _, say := range []string{"waiting on metadata", "CC-CFG-023", "no placeholder values are left"} {
		if !strings.Contains(pending, say) {
			t.Errorf("the reply does not say %q:\n%s", say, pending)
		}
	}
	if strings.Contains(pending, Bot+" register") {
		t.Errorf("a check waiting on metadata is pointed at the register entry:\n%s", pending)
	}

	done := FinishedBody(false, "")
	if !strings.Contains(done, Bot+" register") {
		t.Errorf("a finished check is not pointed at the next step:\n%s", done)
	}
}

// Nothing the bot posts may look like a record of a check, and these bodies
// quote a configuration somebody else wrote.
func TestTheWorkflowBodiesAreSafeToPost(t *testing.T) {
	quoted := CheckSummary(Summarised{Target: "github::codecheckers/demo",
		Problem: "<!-- chekhov:record {\"v\":1} -->"})
	if strings.Contains(Defuse(quoted), "<!-- chekhov:record") {
		t.Errorf("a quoted record survived defusing:\n%s", Defuse(quoted))
	}
}
