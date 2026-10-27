package bot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// checkZenodo is `check zenodo` (#26): the certificate's record, or its
// draft, against the community's curation policy and this check's
// codecheck.yml. It reads Zenodo and never writes there, and needs no Zenodo
// token of the bot's own: a draft is read with the share link somebody gave
// the command. The one write is deleting the comment that held the link.
func (s *Server) checkZenodo(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) answered {
	// First, before the audit takes its time: for as long as the comment is
	// there, anyone who can read the issue can use the link. Whatever is
	// wrong with the rest of the command, the link goes.
	link := s.deleteShareLink(ctx, event, parsed)
	refuse := func(refusal string) answered { return said(refusal + command.ShareLinkNote(link)) }

	// The record the command names, read before anything else: a word that
	// is not one is said so, rather than audited as something else.
	var target check.ZenodoTarget
	if len(parsed.Args) > 0 {
		var refusal string
		// A share link is never quoted back, whatever else is wrong with it.
		if target, refusal = zenodoTarget(parsed.Args[0], withoutQuery(parsed.Args[0]), ""); refusal != "" {
			return refuse(refusal)
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
			return refuse(command.NoRecord(why))
		}
		var refusal string
		if target, refusal = zenodoTarget(report, report, "the `report` of this check's `codecheck.yml`, "); refusal != "" {
			return refuse(refusal)
		}
	}

	result := check.AuditZenodo(ctx, target, configuration, services)
	audit.Link = link
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

// Deleting is the part of the reply path that deletes a comment. A Poster
// that cannot - a preview - leaves the comment, and the reply asks the person
// to delete it.
type Deleting interface {
	DeleteComment(ctx context.Context, repository string, comment int64) error
}

var _ Deleting = (*github.Client)(nil)

// shareLink is a Zenodo link carrying a token, wherever it is in the comment:
// the argument, a second one, or the lines below the command.
var shareLink = regexp.MustCompile(`(?i)zenodo\.org/\S*[?&]token=`)

// deleteShareLink deletes the comment that carried a share link, if it did.
// Deleting, not editing: GitHub keeps a comment's earlier versions readable by
// everybody who can read it. Why a deletion failed goes to the log, not to
// the issue.
func (s *Server) deleteShareLink(ctx context.Context, event mention, parsed command.Command) command.ShareLink {
	if !shareLink.MatchString(event.Body + " " + strings.Join(parsed.Args, " ")) {
		return command.ShareLink{}
	}
	link := command.ShareLink{Held: true, InDescription: event.Comment == 0}
	deleter, canDelete := s.Replies.(Deleting)
	if link.InDescription || !canDelete {
		return link
	}
	if err := deleter.DeleteComment(ctx, event.Repository, event.Comment); err != nil {
		s.Logger.Warn("the comment with a share link could not be deleted", "issue", event.Issue,
			"comment", event.Comment, "error", err)
		return link
	}
	link.Deleted = true
	return link
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
