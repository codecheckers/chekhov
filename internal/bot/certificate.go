package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/rules"
)

// `next certificate` and `set certificate` (#20).
//
// An identifier is claimed in two places: a row of register.csv once the
// certificate is published, and before that the title of an issue labelled
// `id assigned`. Either alone misses identifiers - register.csv the checks in
// progress, the issues every identifier given out before the label existed -
// so both are read, the way the launch-pad reads the issues and an editor
// reads both.

// Certificates is what the certificate commands need of GitHub beyond a
// comment: every issue, to see which identifiers are claimed, and the two
// writes that put an identifier on this check where people and the
// launch-pad look.
type Certificates interface {
	AllIssues(ctx context.Context, repository string) ([]github.Issue, error)
	AddLabel(ctx context.Context, repository string, issue int, label string) error
	SetTitle(ctx context.Context, repository string, issue int, title string) error
}

var _ Certificates = (*github.Client)(nil)

// nextCertificate says which identifier comes next.
func (s *Server) nextCertificate(ctx context.Context, parsed command.Command, services *check.Services) string {
	if len(parsed.Args) != 1 || !strings.EqualFold(parsed.Args[0], "certificate") {
		return fmt.Sprintf("Usage: `%s next certificate`\n", command.Bot)
	}
	_, claims, issues, err := s.claims(ctx, services)
	if err != nil {
		return err.Error() + "\n"
	}
	label := s.Settings.IDAssignedLabel()
	next, highest, found := check.NextCertificate(time.Now().UTC().Year(), claims)
	return command.NextCertificateReply(next, claimed(highest, found), label, unlabelled(issues, label))
}

// setCertificate reserves an identifier for this check: in the record, in the
// title, and with the label.
func (s *Server) setCertificate(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) string {
	if reply, ok := s.noCheck(event, "a certificate identifier"); !ok {
		return reply
	}
	id, anyway, err := setArguments(parsed.Args)
	if err != nil {
		return fmt.Sprintf("%s\n\nUsage: `%s set certificate [YYYY-NNN] [anyway]`\n", err, command.Bot)
	}

	// From reading the claims to writing this check's, nobody else may
	// reserve: two editors a few seconds apart would otherwise both be handed
	// the same next number. Update's lock covers one issue, and this is about
	// all of them. One deployment answers one register, so an in-process lock
	// is the whole of the problem, as it is for the record.
	s.reserving.Lock()
	defer s.reserving.Unlock()

	writer, claims, issues, err := s.claims(ctx, services)
	if err != nil {
		return err.Error() + "\n"
	}

	// This check's own title does not take its identifier from it: setting
	// the identifier it already has is not a conflict, and a change is
	// measured against everybody else's.
	var this github.Issue
	for _, issue := range issues {
		if issue.Number == event.Issue {
			this = issue
			break
		}
	}
	if this.Number == 0 {
		// Not a check of this register at all - a pull request, or an issue
		// opened since the list was read. The title is about to be rewritten
		// from it, so an empty one is not a title to start from.
		return fmt.Sprintf("I could not find #%d among the issues of the register.\n", event.Issue)
	}
	others := make([]check.Claim, 0, len(claims))
	for _, claim := range claims {
		if claim.Issue != event.Issue {
			others = append(others, claim)
		}
	}

	// What this check holds already: the record, or before the bot kept one,
	// a labelled title. Without an identifier the command keeps it - it is
	// how an editor has the bot record a number given by hand, or put back a
	// label somebody removed - rather than moving the check to a new one.
	reading, err := s.Checks.Read(ctx, event.Repository, event.Issue)
	if err != nil {
		return fmt.Sprintf("I could not read the record of this check: %s\n", err)
	}
	held := reading.Record.Certificate
	label := s.Settings.IDAssignedLabel()
	if titled, ok := check.TitleCertificate(this.Title); ok && held == "" && command.HasLabel(this.Labels, label) {
		held = titled
	}
	if id == "" {
		id = held
	}
	if id == "" {
		id, _, _ = check.NextCertificate(time.Now().UTC().Year(), others)
	}
	if claim, taken := check.CertificateTaken(id, others); taken {
		return command.CertificateTakenReply(*claimed(claim, true), ruleNote("CC-REG-001"))
	}
	set := command.CertificateSet{ID: id, Label: label}
	// The jump is asked about once, when the number is chosen; the one the
	// check already holds is not a new decision.
	if previous, gap, found := check.CertificateJump(id, others); id != held && gap > check.MaxCertificateJump {
		jump := command.JumpSentence(gap, claimed(previous, found))
		if !anyway {
			return command.CertificateJumpReply(id, jump, ruleNote("CC-REG-002"))
		}
		set.Forced = jump
	}

	_, err = s.Checks.Update(ctx, event.Repository, event.Issue,
		func(record people.Record) (people.Record, error) {
			if record.Certificate == id {
				return record, people.ErrUnchanged
			}
			set.Previous, record.Certificate = record.Certificate, id
			return record, nil
		})
	switch {
	case errors.Is(err, people.ErrUnchanged):
		// Nothing to record, but the title and the label are still put right:
		// somebody may have edited one away since.
		set.Unchanged = true
	case errors.Is(err, people.ErrTampered):
		return tamperedReply(err)
	case err != nil:
		return fmt.Sprintf("I could not record the identifier: %s\n", err)
	}
	if set.Previous == "" && !set.Unchanged {
		// Set by hand before the bot kept a record: the title is where it was.
		if titled, ok := check.TitleCertificate(this.Title); ok && titled != id {
			set.Previous = titled
		}
	}

	s.putOnIssue(ctx, writer, event, this, &set)
	return command.CertificateSetReply(set, event.Author, time.Now())
}

// putOnIssue writes the identifier into the title and adds the label, where
// either is missing. A failure is reported as not done rather than undoing
// the record, which is what the bot reads; the title and the label are for
// people and the launch-pad, and anybody can put them right.
func (s *Server) putOnIssue(ctx context.Context, writer Certificates, event mention,
	this github.Issue, set *command.CertificateSet) {
	if title := check.TitleWithCertificate(this.Title, set.ID); title != this.Title {
		if err := writer.SetTitle(ctx, event.Repository, event.Issue, title); err != nil {
			s.Logger.Warn("the identifier could not be put in the title", "issue", event.Issue, "error", err)
			set.TitleFailed = true
		} else {
			set.Title = title
		}
	}
	if !command.HasLabel(this.Labels, set.Label) {
		if err := writer.AddLabel(ctx, event.Repository, event.Issue, set.Label); err != nil {
			s.Logger.Warn("the label could not be added", "issue", event.Issue, "error", err)
			set.LabelFailed = true
		} else {
			set.Labelled = true
		}
	}
}

// claims reads every identifier the register and its issues hold, and the
// issues themselves. Either source unread is a refusal, never a guess: an
// identifier proposed from half the claims is how two checks get one.
//
// It returns the reply path as Certificates too, for set to write through: the
// one place that asks whether this deployment can.
func (s *Server) claims(ctx context.Context, services *check.Services) (
	Certificates, []check.Claim, []github.Issue, error) {
	label := s.Settings.IDAssignedLabel()
	if label == "" {
		return nil, nil, nil, errors.New("The settings name no `labels.id_assigned`, so I cannot tell " +
			"which issues hold an identifier.")
	}
	register, ok := s.Replies.(Certificates)
	if !ok {
		return nil, nil, nil, errors.New("I cannot read the issues of the register here, " +
			"and `register.csv` alone does not know the identifiers reserved on checks in progress.")
	}
	claims, err := services.Certificates()
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"I could not read `register.csv`, so I cannot tell which identifiers are taken: %w", err)
	}
	issues, err := register.AllIssues(ctx, s.Settings.TargetRepository())
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"I could not read the issues, so I cannot tell which identifiers are taken: %w", err)
	}
	for _, issue := range issues {
		if !command.HasLabel(issue.Labels, label) {
			continue
		}
		for _, id := range check.TitleCertificates(issue.Title) {
			claims = append(claims, check.Claim{ID: id, Issue: issue.Number})
		}
	}
	return register, claims, issues, nil
}

// unlabelled is the open issues whose titles carry identifiers that were not
// counted, for want of the label.
func unlabelled(issues []github.Issue, label string) []command.Unlabelled {
	var found []command.Unlabelled
	for _, issue := range issues {
		if issue.Closed || command.HasLabel(issue.Labels, label) {
			continue
		}
		if ids := check.TitleCertificates(issue.Title); len(ids) > 0 {
			found = append(found, command.Unlabelled{Issue: issue.Number, IDs: ids})
		}
	}
	return found
}

// setArguments reads `certificate [YYYY-NNN] [anyway]`.
func setArguments(args []string) (id string, anyway bool, err error) {
	if len(args) == 0 || !strings.EqualFold(args[0], "certificate") {
		return "", false, errors.New("Set what? Only a certificate identifier can be set.")
	}
	for _, arg := range args[1:] {
		switch {
		case strings.EqualFold(arg, "anyway") && !anyway:
			anyway = true
		case check.IsCertificateID(arg) && id == "":
			id = arg
		default:
			return "", false, fmt.Errorf("%s is not a certificate identifier of the form YYYY-NNN.",
				command.Code(arg))
		}
	}
	return id, anyway, nil
}

// claimed is a claim as a reply names it, nil when there is none.
func claimed(claim check.Claim, found bool) *command.Held {
	if !found {
		return nil
	}
	return &command.Held{ID: claim.ID, Issue: claim.Issue}
}

// ruleNote names a rule a refusal is about, with the symbol of its severity
// and its description, from the newest catalogue: a severity comes from the
// rule file, never from the code.
func ruleNote(id string) string {
	rule, err := rules.Get(id, rules.Newest())
	if err != nil {
		return command.RuleNote(check.Symbol(check.OutcomeError), id, "")
	}
	return command.RuleNote(check.Symbol(check.Outcome(rule.Severity)), id, rule.Description)
}
