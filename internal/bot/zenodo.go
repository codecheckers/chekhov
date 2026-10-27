package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
)

// checkZenodo is `check zenodo` (#26): the certificate's record, or its
// draft, against the community's curation policy and this check's
// codecheck.yml. It reads and never writes, and needs no Zenodo token of the
// bot's own: a draft is read with the share link somebody gave the command.
func (s *Server) checkZenodo(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) answered {
	// The record the command names, read before anything else: a word that
	// is not one is said so, rather than audited as something else.
	var target check.ZenodoTarget
	if len(parsed.Args) > 0 {
		var refusal string
		// A share link is never quoted back, whatever else is wrong with it.
		if target, refusal = zenodoTarget(parsed.Args[0], withoutQuery(parsed.Args[0]), ""); refusal != "" {
			return said(refusal)
		}
	}

	// The codecheck.yml of this check, from where `check ready` finds the
	// repository.
	issue, issueErr := s.issue(ctx, event)
	repository := s.checkRepository(ctx, event, whatTheBotKnows{issue: issue, labelsRead: issueErr == nil}, services)
	var audit command.Audit
	var configuration check.Context
	if repository.Value == "" {
		audit.NoConfiguration = "this check names no repository"
	} else if read, err := s.read(repository.Value, services); err != nil {
		audit.NoConfiguration = fmt.Sprintf("I could not read it from %s: %s", command.Code(repository.Value), err)
	} else {
		configuration = read
		audit.Configuration, audit.From = repository.Value, repository.From
	}

	if len(parsed.Args) == 0 {
		report := strings.TrimSpace(configuration.Config.Report)
		if report == "" {
			why := "this check's `codecheck.yml` has no `report`"
			if audit.NoConfiguration != "" {
				why = "there is no `codecheck.yml` to take it from: " + audit.NoConfiguration
			}
			return said(command.NoRecord(why))
		}
		var refusal string
		if target, refusal = zenodoTarget(report, report, "the `report` of this check's `codecheck.yml`, "); refusal != "" {
			return said(refusal)
		}
	}

	result := check.AuditZenodo(ctx, target, configuration, services)
	audit.Record, audit.Draft, audit.Shared, audit.Problem = result.Record, result.Draft, result.Shared, result.Problem
	audit.RulesSummary = result.RulesSummary
	for _, item := range result.Policy {
		audit.Policy = append(audit.Policy, command.AuditItem{What: item.What, Status: string(item.Status), Detail: item.Detail})
	}
	for _, rule := range result.Rules {
		if check.Reported(rule.Outcome) {
			audit.Rules = append(audit.Rules, command.RuleLine{Symbol: check.Symbol(rule.Outcome),
				ID: rule.Rule.ID, Name: rule.Rule.Name, Finding: rule.Detail})
		}
	}
	return said(command.AuditReply(audit))
}

// zenodoTarget reads a record from a word, or says why it cannot: shown is
// the word as the reply may quote it, and about what the word was, for a
// reply about one the command did not name.
func zenodoTarget(word, shown, about string) (check.ZenodoTarget, string) {
	target, err := check.ParseZenodoTarget(word)
	var elsewhere *check.OutOfScopeError
	switch {
	case errors.As(err, &elsewhere):
		return target, command.RecordOutOfScope(shown, elsewhere.Platform)
	case err != nil:
		return target, command.UnreadableRecord(shown, about+err.Error())
	}
	return target, ""
}

// withoutQuery is a link with its query taken off, which is where a share
// link carries its token.
func withoutQuery(word string) string {
	before, _, _ := strings.Cut(word, "?")
	return before
}
