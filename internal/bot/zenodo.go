package bot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

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
	// wrong with the rest of the command, a link that may open something goes.
	// In a deployment actEach has dealt with it, for the whole comment.
	var link command.ShareLink
	if event.linkDealt != nil {
		link = *event.linkDealt
	} else {
		link = s.deleteShareLink(ctx, event, parsed)
	}
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

// zenodoAmong is where the first `check zenodo` is among a comment's
// commands, -1 for none.
func zenodoAmong(commands []command.Command) int {
	return slices.IndexFunc(commands, func(parsed command.Command) bool {
		return parsed.Name == command.CheckZenodo
	})
}

// dealWithShareLink deletes the comment that asks `check zenodo` with a share
// link, and says what became of it; nil when the comment asks no such thing.
// event.Body is still the whole comment here. A panic is reported and costs
// this, not the commands; a `check zenodo` among those run then tries again.
func (s *Server) dealWithShareLink(event mention, commands []command.Command) (dealt *command.ShareLink) {
	i := zenodoAmong(commands)
	if i < 0 {
		return nil
	}
	defer s.recoverBackground("Dealing with a share link panicked on " + event.check() + ".")
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	link := s.deleteShareLink(ctx, event, commands[i])
	return &link
}

// Deleting is the part of the reply path that deletes a comment. A Poster
// that cannot - a preview - leaves the comment, and the reply asks the person
// to delete it.
type Deleting interface {
	DeleteComment(ctx context.Context, repository string, comment int64) error
}

var _ Deleting = (*github.Client)(nil)

// shareLinkCheck bounds the asking whether a link points at anything, since
// the comment stays up until it is answered; mostLinksChecked bounds how many
// of one comment are asked about.
const (
	shareLinkCheck   = 3 * time.Second
	mostLinksChecked = 3
)

// wholeToken is a share link's token that is plainly all there is of it: the
// characters a token is made of, a letter or digit at each end, and then the
// end of the link or its next part. What GitHub leaves out of a link - the
// marks of bold or struck-through text, a closing quote - would otherwise be
// asked about as part of the token, Zenodo would say that opens nothing, and
// the link would stay up.
var wholeToken = regexp.MustCompile(`(?i)[?&]token=[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?(?:[&#]|$)`)

// opensNothing says every share link in a comment is one Zenodo shows nothing
// for, so that there is nothing to protect; see check.ShareLinkOpensNothing
// for how far that goes. Anything it cannot be sure of - a link it cannot
// read or a token that may not be whole, more links than it asks about, a
// token outside a link it read, Zenodo not answering, services off - is false.
func (s *Server) opensNothing(ctx context.Context, text string) bool {
	services := s.Services.Fresh()
	if !services.Enabled() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, shareLinkCheck)
	defer cancel()

	targets := map[check.ZenodoTarget]bool{}
	// What a link is set off by in a comment, Markdown's own marks included.
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("<>()[]\"'`", r)
	}) {
		lower := strings.ToLower(word)
		if !strings.Contains(lower, "token=") {
			continue
		}
		// As command.arguments does, so that the link in the comment and the
		// same link as a command's last argument are one.
		word = strings.TrimRight(word, ".,;:!?")
		if strings.Count(lower, "zenodo.org/") != 1 || strings.Count(lower, "token=") != 1 ||
			!wholeToken.MatchString(word) {
			return false
		}
		if !strings.Contains(word, "://") {
			word = "https://" + word
		}
		target, err := check.ParseZenodoTarget(word)
		if err != nil || target.Token == "" {
			return false
		}
		targets[target] = true
	}
	if len(targets) == 0 || len(targets) > mostLinksChecked {
		return false
	}
	for target := range targets {
		if !services.ShareLinkOpensNothing(ctx, target) {
			return false
		}
	}
	return true
}

// deleteShareLink deletes the comment that carried a share link, if it did,
// unless every link in it surely points at nothing. Deleting, not editing:
// GitHub keeps a comment's earlier versions readable by everybody who can read
// it. Why a deletion failed goes to the log, not to the issue.
func (s *Server) deleteShareLink(ctx context.Context, event mention, parsed command.Command) command.ShareLink {
	text := event.CommentBody + " " + event.Body + " " + strings.Join(parsed.Args, " ")
	// Wherever it is in the comment: the argument, a second one, or the lines
	// below the command.
	if !command.HoldsShareLink(text) {
		return command.ShareLink{}
	}
	inDescription := event.Comment == 0
	if s.opensNothing(ctx, text) {
		return command.ShareLink{OpensNothing: true, InDescription: inDescription}
	}
	link := command.ShareLink{Held: true, InDescription: inDescription}
	// Through the path that writes to where the comment is: the register's
	// refuses the lists, and theirs the register.
	deleter, canDelete := s.posterFor(event.Repository).(Deleting)
	if link.InDescription || !canDelete {
		return link
	}
	if err := deleter.DeleteComment(ctx, event.Repository, event.Comment); err != nil {
		s.Logger.Warn("the comment with a share link could not be deleted", "repository", event.Repository, "issue", event.Issue,
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
