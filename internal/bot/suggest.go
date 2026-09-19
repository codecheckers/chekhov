package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/suggest"
)

// Finding a codechecker: where the lists are, and who on them fits this check.
//
// See codecheckers/chekhov#24. The ranking itself is internal/suggest; what is
// here is the reading of the world it ranks on, and the refusals for what this
// deployment cannot reach.

// Issues is the part of the reply path that reads the register's issues: what
// a check is about when nobody said, and who is already busy. A Poster that
// cannot read them - the test recorder, or the command line preview - simply
// does not, and the reply says which exclusions it could not apply.
type Issues interface {
	Issue(ctx context.Context, repository string, issue int) (github.Issue, error)
	OpenIssues(ctx context.Context, repository string) ([]github.Issue, error)
}

// The reply path a deployment uses reads them, which a type assertion would
// otherwise only discover at runtime, as a missing exclusion.
var _ Issues = (*github.Client)(nil)

// codecheckers says where the lists are, and how long each one is.
func (s *Server) codecheckers(services *check.Services) string {
	configured := s.Settings.CodecheckerLists()
	lists := make([]command.CodecheckerList, 0, len(configured))
	for _, url := range configured {
		// The links are worth having offline; the count is the only part of
		// the answer that needs the network.
		list := command.CodecheckerList{Page: suggest.PageOf(url), Count: -1}
		if services.Enabled() {
			rows, err := services.CSV(url)
			if err != nil {
				list.Problem = err.Error()
			} else {
				list.Count = len(rows)
			}
		}
		lists = append(lists, list)
	}
	return command.CodecheckerListsReply(lists)
}

// suggestCodecheckers proposes who could check this paper.
func (s *Server) suggestCodecheckers(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) string {
	hintWords, err := command.ParseSuggestion(parsed.Args)
	if err != nil {
		return fmt.Sprintf("%s\n", err)
	}
	if !services.Enabled() {
		return "Not online, so I cannot read the codechecker lists.\n"
	}

	codecheckers, unread := suggest.Load(services, s.Settings)
	if len(codecheckers) == 0 {
		return "No codechecker list could be read.\n"
	}

	text := command.BodyText(event.Body)
	hints := suggest.Hints(hintWords, text)
	// With nothing named and nothing pasted, the issue itself is the check.
	if onlyEmptyText(hints) {
		hints = append(hints, s.issueAsHints(ctx, event)...)
	}

	vocabulary := suggest.VocabularyOf(codecheckers)
	evidence := suggest.Gather(services, vocabulary, hints)
	if evidence.Empty() {
		// What could not be read is the answer when there is no other: saying
		// "nothing to go on" about a DOI that simply would not load sends the
		// editor looking for a better command rather than for the outage.
		if len(evidence.Unread) > 0 {
			_, notChecked := s.exclusions(ctx, event)
			return command.SuggestionsReply(command.Suggestions{
				Read:       evidence.Sources,
				Unread:     append(evidence.Unread, listsUnread(unread)...),
				NotChecked: notChecked,
				Considered: len(codecheckers),
			})
		}
		return fmt.Sprintf("Nothing to match on. Please provide a repository, work DOI or "+
			"certificate, or paste the abstract under the command:\n\n"+
			"```\n%s suggest codecheckers github::owner/repo\n```\n", command.Bot)
	}

	excluded, notChecked := s.exclusions(ctx, event)
	ranking := suggest.Rank(codecheckers, evidence, excluded, thisCheck(event))

	// What the suggestions share with the check is the useful half of what was
	// gathered: OpenAlex names thirty subjects for a paper, and the two that
	// somebody declares are what decided the answer. With nobody to suggest,
	// what was looked for is all there is to report.
	languages, fields := ranking.SharedLanguages, ranking.SharedFields
	if len(ranking.Suggested) == 0 {
		languages, fields = evidence.Languages, evidence.Fields
	}

	reply := command.Suggestions{
		Read:            evidence.Sources,
		Unread:          append(evidence.Unread, listsUnread(unread)...),
		Languages:       languages,
		Fields:          fields,
		NotChecked:      notChecked,
		Matched:         ranking.Matched,
		Considered:      len(codecheckers),
		Undistinguished: ranking.Undistinguished,
	}
	for _, candidate := range ranking.Suggested {
		reply.Candidates = append(reply.Candidates, command.Candidate{
			Handle: candidate.Handle, Name: candidate.Name, Why: candidate.Why(),
		})
	}
	for _, out := range ranking.LeftOut {
		reply.LeftOut = append(reply.LeftOut, command.LeftOut{Handle: out.Handle, Why: out.Why})
	}
	return command.SuggestionsReply(reply)
}

// thisCheck is what a tied shortlist is ordered by: an editor asking twice
// about one check gets the same order, and two checks that match the same
// fifty people do not both get the same five. On the command line there is no
// issue to be this check, so the comment stands in for one.
func thisCheck(event mention) string {
	if event.Issue > 0 {
		return fmt.Sprintf("%s#%d", event.Repository, event.Issue)
	}
	return event.Body
}

// onlyEmptyText reports that the editor named nothing at all.
func onlyEmptyText(hints []suggest.Hint) bool {
	for _, hint := range hints {
		if hint.Kind != "text" || strings.TrimSpace(hint.Value) != "" {
			return false
		}
	}
	return true
}

// issueAsHints reads the checks issue as the evidence: its title and its first
// comment are where the paper and its repository are named.
func (s *Server) issueAsHints(ctx context.Context, event mention) []suggest.Hint {
	if event.Issue <= 0 {
		return nil
	}
	reader, ok := s.Replies.(Issues)
	if !ok {
		return nil
	}
	issue, err := reader.Issue(ctx, event.Repository, event.Issue)
	if err != nil {
		s.Logger.Warn("the checks issue could not be read for a suggestion",
			"issue", event.Issue, "error", err)
		return nil
	}
	return []suggest.Hint{{Kind: "text", Value: issue.Title + "\n" + issue.Body,
		From: fmt.Sprintf("this check's issue, #%d", event.Issue)}}
}

// exclusions is who may not be suggested, and what could not be asked.
//
// Each part fails softly and says so: an editor is better served by a list
// with a caveat than by a refusal, as long as the caveat is in the reply.
func (s *Server) exclusions(ctx context.Context, event mention) (suggest.Excluded, []string) {
	var excluded suggest.Excluded
	var notChecked []string

	switch {
	case event.Issue <= 0 || s.Checks == nil:
		notChecked = append(notChecked,
			"No check here, so authors and current assignees are not excluded.")
	default:
		reading, err := s.Checks.Read(ctx, event.Repository, event.Issue)
		// A record somebody edited names nobody, exactly as it grants nobody a
		// role in checkRoles: whoever edited it could have deleted the author
		// line of the paper they wrote, and being suggested to check it is the
		// prize for doing so.
		if err != nil || reading.Tampered != nil {
			notChecked = append(notChecked,
				"Roles record not trusted, so authors are not excluded: "+
					errors.Join(err, reading.Tampered).Error())
			break
		}
		holders := reading.Record.Holders()
		excluded.Authors = holders.Authors
		excluded.OnThisCheck = nonEmpty(holders.AssignedCodechecker, holders.HandlingEditor)
	}

	reader, ok := s.Replies.(Issues)
	if !ok {
		notChecked = append(notChecked,
			"Open issues not read here, so I do not know who is busy.")
		return excluded, notChecked
	}
	issues, err := reader.OpenIssues(ctx, s.Settings.TargetRepository())
	if err != nil {
		notChecked = append(notChecked,
			"Could not read the open issues, so I do not know who is busy: "+err.Error())
		return excluded, notChecked
	}
	excluded.OpenChecks = map[string]int{}
	for _, issue := range issues {
		if issue.Number == event.Issue {
			// Being assigned to the check somebody is being sought for is not
			// being busy elsewhere.
			continue
		}
		for _, assignee := range issue.Assignees {
			excluded.OpenChecks[command.Handle(assignee)]++
		}
	}
	return excluded, notChecked
}

func listsUnread(lists []string) []string {
	unread := make([]string, 0, len(lists))
	for _, list := range lists {
		unread = append(unread, "the list at "+list)
	}
	return unread
}

func nonEmpty(handles ...string) []string {
	var kept []string
	for _, handle := range handles {
		if strings.TrimSpace(handle) != "" {
			kept = append(kept, handle)
		}
	}
	return kept
}
