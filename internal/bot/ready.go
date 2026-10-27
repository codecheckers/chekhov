package bot

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
)

// `check ready` (codecheckers/chekhov#28): can this check start? The looking
// is check.Ready's; what is here is finding what to look at, from a check that
// may not have a record yet - the command is for the author too, before an
// editor has done anything.

// checkReady answers whether what an author provides is there.
//
// It acts on nothing, so a record somebody edited is read as it is, as
// `status` reads it; and a deployment with no record to read still answers
// from the command and the issue.
func (s *Server) checkReady(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) answered {
	issue, issueErr := s.issue(ctx, event)
	about := whatTheBotKnows{issue: issue, labelsRead: issueErr == nil}

	// What the command names wins, and a word it cannot read is said so:
	// answering for the record's repository instead would be a verdict on
	// something the author did not ask about.
	named := ""
	for _, argument := range parsed.Args {
		if s.LocalPaths && check.IsLocalPath(argument) {
			named = argument
		} else {
			named = check.TargetIn(argument)
		}
		if named == "" {
			return said(command.UnreadableTarget(argument))
		}
		break
	}
	target := command.RowField{Value: named, From: "the command"}
	if named == "" {
		target = s.checkRepository(ctx, event, about, services)
	}
	if target.Value == "" {
		return said(command.NoReadyTarget(about.labelsRead))
	}

	ready := check.Ready(target.Value, s.LocalPaths, services, paperInIssue(issue.Body))
	shown := command.Readiness{Target: ready.Target, From: target.From, Problem: ready.Problem}
	for _, item := range ready.Items {
		shown.Requirements = append(shown.Requirements, command.Requirement{
			What: item.What, Passed: item.Status == check.StatusPass, Detail: item.Detail, Fix: item.Fix})
	}
	return said(command.ReadyReply(shown))
}

// checkRepository is the repository of this check, where a command that
// names none finds it: the record of the check, then the issue's Repository
// line.
func (s *Server) checkRepository(ctx context.Context, event mention, about whatTheBotKnows,
	services *check.Services) command.RowField {
	recorded := ""
	if s.Checks != nil && event.Issue > 0 {
		if reading, err := s.Checks.Read(ctx, event.Repository, event.Issue); err == nil {
			recorded = s.targetFor(nil, reading.Record, services)
		}
	}
	return firstOf(command.RowField{Value: recorded, From: "the record"}, repositoryLine(about, services))
}

// htmlComment is a comment in an issue body: the templates' instructions,
// whose links are the guide's, not the author's.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// templateLines are an issue body's lines as its author filled the template
// in, the template's own instructions taken out.
func templateLines(body string) []string {
	return strings.Split(htmlComment.ReplaceAllString(body, ""), "\n")
}

// templateField is what a line of the template says after its label, and
// whether the line has one of the labels.
func templateField(line string, labels ...string) (string, bool) {
	line = strings.TrimSpace(line)
	for _, label := range labels {
		if rest, found := strings.CutPrefix(line, label); found {
			return rest, true
		}
	}
	return "", false
}

// paperLines are where the register's issue templates ask for the paper: the
// community template's Workflow line and the Lifecycle Journal's Submission.
var paperLines = []string{"**Workflow**:", "**Submission**:"}

// paperInIssue is the paper an issue links on the line its template asks
// for it, empty when it links none there: the first link that is not a
// repository or a deposit. Other lines are not guessed at - a Dryad or a
// figshare DOI there is data, and a paper found by guessing would make a check
// ready without one.
func paperInIssue(body string) string {
	for _, line := range templateLines(body) {
		rest, ok := templateField(line, paperLines...)
		if !ok {
			continue
		}
		links := check.LinksIn(rest)
		for _, word := range linkWords(rest) {
			if doi := check.DOIIn(word); doi != "" && !strings.Contains(word, "://") {
				links = append(links, "https://doi.org/"+doi)
			}
		}
		for _, link := range links {
			if check.TargetIn(link) == "" && !check.IsDepositDOI(check.DOIIn(link)) {
				return link
			}
		}
	}
	return ""
}

// linkWords are the words of a line a link may be: written bare, in angle
// brackets, or as Markdown, whose text and target are both worth a try.
func linkWords(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("[]()<>", r)
	})
}
