package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/logbook"
)

// `log` and `log sweep` (#73): what the bot logged, put in the private log
// repository for an editor who has to work out what went wrong - and never on
// the register, where the reply is the link alone.
//
// Each check has one standing issue there per deployment, "<register>#N
// (development)", found by its title and the deployment's label; every `log`
// is a comment on it, assigned to whoever asked, which is how they hear of it.
// Assigning notifies through GitHub, the editor who asked - the command's
// author, as CLAUDE.md requires; GitHub keeps an earlier asker subscribed to
// the issue they were assigned, as it does anybody who took part, so they hear
// of later comments on it too. What is about no check has a standing issue of
// its own. The writes go through the sink's client, which
// writes to that repository and nowhere else.

// logRecords is how many records one `log` writes at most, the newest; what
// fits in one comment is the other bound.
const logRecords = 500

// writeLog answers `log` and, with sweep, `log sweep`.
func (s *Server) writeLog(ctx context.Context, event mention, sweep bool) string {
	if s.logSink == nil || s.Log == nil {
		return command.LogUnavailable("this deployment has none")
	}
	writer, repository := s.logSink.writer, s.logSink.repository
	// Asked every time rather than trusted from the sink: this writes what
	// somebody is about to read, and a repository made public since the sink
	// last asked must not get it.
	switch private, err := writer.Private(ctx); {
	case err != nil:
		s.Logger.Warn("could not ask whether the log repository is private", "repository", repository, "error", err)
		return command.LogUnavailable("GitHub could not be asked whether it is private")
	case !private:
		s.Logger.Warn("the log repository is not private, so `log` wrote nothing there", "repository", repository)
		return command.LogUnavailable("it is not private")
	}

	label := logLabel(s.Deployment)
	var title, about string
	var keep func(logbook.Record) bool
	if sweep {
		// Not about a check of the register: about no issue at all, or about
		// one of the lists, where `log` is not answered.
		title, about = "Not about a check ("+label+")", "about no check"
		register := s.Settings.TargetRepository()
		keep = func(r logbook.Record) bool { return r.Issue() == 0 || !r.About(register, r.Issue()) }
	} else {
		// In backticks, as in the header: a reference written out would put
		// a cross-reference from the log repository on the check's timeline.
		title, about = fmt.Sprintf("%s (%s)", event.check(), label), "about `"+event.check()+"`"
		keep = func(r logbook.Record) bool { return r.About(event.Repository, event.Issue) }
	}
	// The buffer keeps the newest records, Kept of them: on a busy day it
	// does not reach back to the start, and the replies say how far it does.
	kept := s.Log.Oldest(s.sweeps.started)
	records := s.Log.Select(time.Time{}, keep, logRecords)
	if len(records) == 0 {
		return command.LogNothing(kept)
	}
	header := fmt.Sprintf("Asked for by `@%s` on `%s`: the newest of what the bot logged %s, "+
		"which it keeps back to %s.\n\n", event.Author, event.check(), about, kept.UTC().Format(time.RFC3339))
	body, written := fitted(header, records)

	// One `log` at a time from here: two at once would each find no standing
	// issue and open one apiece.
	s.logWriting.Lock()
	defer s.logWriting.Unlock()
	issue, err := s.standingIssue(ctx, writer, repository, title, label)
	if err != nil {
		s.Logger.Warn("the log could not be written", "repository", event.Repository, "issue", event.Issue, "error", err)
		return command.LogUnavailable("writing there failed")
	}
	if _, err := writer.Comment(ctx, repository, issue.Number, command.Defuse(body)); err != nil {
		// The issue may have been closed or moved: look again next time.
		delete(s.logIssues, title)
		s.Logger.Warn("the log could not be written", "repository", event.Repository, "issue", event.Issue, "error", err)
		return command.LogUnavailable("writing there failed")
	}
	// Assigned once: a second `log` by the same editor writes no assignee.
	notAssigned := ""
	if !slices.ContainsFunc(issue.Assignees, func(who string) bool { return strings.EqualFold(who, event.Author) }) {
		if notAssigned = s.assignOn(ctx, writer, repository, issue.Number, event.Author); notAssigned == "" {
			issue.Assignees = append(issue.Assignees, event.Author)
			s.logIssues[title] = issue
		}
	}
	return command.LogWritten(repository, issue.Number, written, records[len(records)-written].Time, notAssigned)
}

// standingIssue is this deployment's open issue with this title, opened when
// there is none. Kept once found, under s.logWriting: every open issue of the
// deployment is a paged read, and they only grow.
func (s *Server) standingIssue(ctx context.Context, writer logWriter, repository, title, label string) (github.Issue, error) {
	if issue, ok := s.logIssues[title]; ok {
		return issue, nil
	}
	if s.logIssues == nil {
		s.logIssues = map[string]github.Issue{}
	}
	issues, err := writer.OpenLabelledIssues(ctx, repository, label)
	if err != nil {
		return github.Issue{}, err
	}
	for _, issue := range issues {
		if issue.Title == title && ours(issue, label) {
			s.logIssues[title] = issue
			return issue, nil
		}
	}
	number, err := writer.CreateIssue(ctx, repository, title,
		"What the bot logged, written here by `@chekhovbot log` and nowhere else; one comment per request.\n",
		[]string{label})
	if err != nil {
		return github.Issue{}, err
	}
	issue := github.Issue{Number: number, Title: title, Labels: []string{label}}
	s.logIssues[title] = issue
	return issue, nil
}

// fitted is the comment: the header and as many of the newest records as fit
// in one, and how many that was. Each record is rendered once, newest first;
// one too long for a comment alone - a proxy's page in an error - is cut, as a
// fatal report is.
func fitted(header string, records []logbook.Record) (string, int) {
	room := bodyBudget - len(header)
	var kept []string
	for i := len(records) - 1; i >= 0; i-- {
		rendered := logbook.Render(records[i:i+1], true)
		if len(rendered) > room {
			if len(kept) == 0 {
				kept = append(kept, cutBlock(rendered, room))
			}
			break
		}
		kept, room = append(kept, rendered), room-len(rendered)
	}
	slices.Reverse(kept)
	return header + strings.Join(kept, ""), len(kept)
}
