package bot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/registration"
)

// Registrations the bot opens (#49).
//
// Somebody who has joined the organisation is asked for their row on an issue
// of the lists: when an `assign` asked the owners to invite them and they
// accept, and when an editor asks with `include open`. GitHub lets only an
// issue's author edit its first post, and the bot is that author, so the row
// comes in the registrant's comment; a signed record in the bot's first
// comment says whose comments those are, because the privacy rule of a
// registration - checked against the person it is for, and nobody else - must
// not rest on a title a collaborator can edit.

// Opener is the part of the lists' reply path that opens a registration, and
// reads the ones there are.
type Opener interface {
	CreateIssue(ctx context.Context, repository, title, body string, labels []string) (int, error)
	LabelledIssues(ctx context.Context, repository, label string) ([]github.Issue, error)
}

var _ Opener = (*github.Client)(nil)

// rowAskedAfter is how long a registration waits for the row before its
// registrant is reminded, once.
const rowAskedAfter = 7 * 24 * time.Hour

// listsReader is the lists' reply path as a reader of their issues.
func (s *Server) listsReader() (Reader, error) {
	reader, ok := s.Lists.(Reader)
	if !ok {
		return nil, errors.New("this deployment cannot read the issues of the lists")
	}
	return reader, nil
}

// checkRegistration checks the registration an event is on, or says why it
// could not be read; inComment says the row was read from a comment.
func (s *Server) checkRegistration(ctx context.Context, event mention) (report registration.Report,
	inComment bool, unread string) {
	who, text, inComment, err := s.registrantOf(ctx, event)
	if err != nil {
		return registration.Report{}, inComment, command.RegistrationUnread(whyNot(err))
	}
	return registration.Check(ctx, text, s.registrationWorld(who)), inComment, ""
}

// registrantOf is who a registration is for and the text its row is in: the
// issue's author and first post - or, on one the bot opened, the person its
// signed record names and their latest comment with a row. The one place
// that knows a registration's subject is not always its author.
func (s *Server) registrantOf(ctx context.Context, event mention) (who, text string, inComment bool, err error) {
	if !strings.EqualFold(event.IssueAuthor, s.Settings.BotUser()) {
		return event.IssueAuthor, event.IssueBody, false, nil
	}
	reader, err := s.listsReader()
	if err != nil {
		return "", "", true, err
	}
	thread := s.threadOf(ctx, reader, event)
	subject, err := registrantIn(thread)
	if err != nil {
		return "", "", true, err
	}
	return subject, latestRow(thread.comments, subject), true, nil
}

// registrantIn is whom a registration the bot opened is for, by its signed
// record: one person, or an error saying why not - the comments could not be
// read, no record holds, or two name different people, which is not for the
// bot to choose between when the choice is whose comments it trusts.
func registrantIn(thread issueThread) (string, error) {
	subject := ""
	for _, record := range thread.newest {
		if record.Kind != followup.KindRegistration {
			continue
		}
		if subject != "" && !strings.EqualFold(subject, record.Subject) {
			return "", errors.New("this issue carries records for two different people, so I read neither")
		}
		subject = record.Subject
	}
	switch {
	case subject != "":
		return subject, nil
	case len(thread.unread) > 0:
		return "", errors.New(strings.Join(thread.unread, "; "))
	}
	return "", errors.New("I opened this issue, and it carries no record of whom it is for that I can trust")
}

// latestRow is the newest comment of the registrant's that holds a row, empty
// when none does - which the check reports as no row.
func latestRow(comments []github.Comment, subject string) string {
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if !strings.EqualFold(comment.Author, subject) {
			continue
		}
		if row, _ := registration.Parse(comment.Body); row != (registration.Row{}) {
			return comment.Body
		}
	}
	return ""
}

// openRegistration opens a registration for somebody, unless they are on the
// list already or have one - theirs, or one the bot opened for them, open or
// closed: never two for one person. What came of it is an item of the reply
// that asked, and whether there is a registration for them now, or no need
// for one. outside says they are not in the organisation yet, which the
// registration tells them (#68). event is what asked: a check, which the
// registration names and whose handling editor it is assigned to, or an
// issue of the lists, which is no check.
func (s *Server) openRegistration(ctx context.Context, login string, outside bool, event mention) (string, bool) {
	lists, file, label := s.Settings.ListsRepository(), registration.RegistrationList, s.Settings.RegistrationLabel()
	opener, ok := s.Lists.(Opener)
	reader, readable := s.Lists.(Reader)
	if lists == "" || !ok || !readable {
		return command.NotFromHere("open a registration"), false
	}
	// Without a key there is no record of whom it is for, and a registration
	// whose row nobody can read is worse than none.
	if !s.signer().Signs() {
		return command.NotFromHere("sign the record of whom a registration is for, so I open"), false
	}
	from := event
	if s.onLists(event) {
		from = mention{}
	}
	switch listed, err := s.onTheList(login, s.Services.Fresh()); {
	case err != nil:
		return command.Item(command.NotDone("tell whether they are on the list already, so I have opened nothing",
			whyNot(err))), false
	case listed:
		return command.RegistrationNotNeeded(login, file), true
	}
	// Every registration carries the label - the template's put it on, and
	// so does the bot - so the labelled issues are all there is to look at.
	issues, err := opener.LabelledIssues(ctx, lists, label)
	if err != nil {
		// Not knowing whether they have one is no reason to open a second.
		s.Logger.Warn("could not read the registrations", "handle", login, "error", err)
		return command.Item(command.NotDone("tell whether they have a registration already, so I have opened none",
			whyNot(err))), false
	}
	title := command.RegistrationTitle(login)
	for _, issue := range issues {
		if strings.EqualFold(issue.Author, login) {
			return command.RegistrationExists(lists, issue.Number, login) + s.registrationAssignee(ctx, from, issue), true
		}
		// By title for the bot's own: what this decides is whether to open
		// one, never whose comments a row is read from, which is the signed
		// record's to say. One whose record did not get posted is given it
		// now, which is how a failed write is mended.
		if strings.EqualFold(issue.Author, s.Settings.BotUser()) && strings.EqualFold(issue.Title, title) {
			exists := command.RegistrationExists(lists, issue.Number, login) + s.registrationAssignee(ctx, from, issue)
			opened := mention{Repository: lists, Issue: issue.Number}
			if _, err := registrantIn(s.threadOf(ctx, reader, opened)); err != nil {
				if note := s.recordRegistrant(ctx, opened, login); note != "" {
					return exists + note, false
				}
			}
			return exists, true
		}
	}

	outsideOf, source := "", ""
	if outside {
		outsideOf = s.Settings.TeamOrganisation()
	}
	if from.Repository != "" {
		source = from.check()
	}
	number, err := opener.CreateIssue(ctx, lists, title,
		command.Defuse(command.RegistrationInvite(login, file, outsideOf, source)), []string{label})
	if err != nil {
		s.Logger.Warn("could not open a registration", "handle", login, "error", err)
		return command.RegistrationNotOpened(login, whyNot(err)), false
	}
	said := command.RegistrationOpened(lists, number, login) + s.registrationAssignee(ctx, from, github.Issue{Number: number})
	opened := mention{Repository: lists, Issue: number}
	if note := s.recordRegistrant(ctx, opened, login); note != "" {
		return said + note, false
	}
	return said, true
}

// registrationAssignee makes the handling editor of the check a registration
// comes from its assignee, so that somebody looks after it, and says what
// came of it; nothing when there is nothing to do. Only a registration that
// is open with nobody assigned: one somebody assigned already is looked
// after, and a closed one is over. The editor is the record's only when it
// verifies, as for the archive reminders. A failure is said, never a reason
// to undo the registration: the assignee is a convenience on top of it.
func (s *Server) registrationAssignee(ctx context.Context, from mention, registration github.Issue) string {
	assigner, ok := s.Lists.(Assigner)
	if from.Repository == "" || !ok || s.Checks == nil || registration.Closed || len(registration.Assignees) > 0 {
		return ""
	}
	lists := s.Settings.ListsRepository()
	reading, err := s.checks(from).Read(ctx, from.Repository, from.Issue)
	switch {
	case err != nil:
		s.Logger.Warn("could not read the handling editor for a registration", "check", from.check(), "error", err)
		return command.RegistrationEditorUnread(lists, registration.Number, whyNot(err))
	case reading.Tampered != nil:
		return command.RegistrationEditorUnread(lists, registration.Number, command.RecordEdited)
	}
	editor := reading.Record.HandlingEditor
	if editor == "" {
		return ""
	}
	why := s.assignOn(ctx, assigner, lists, registration.Number, editor)
	return command.RegistrationAssigned(lists, registration.Number, editor, why == "", why)
}

// recordRegistrant posts the record of whom a registration is for, empty
// when it was posted and what went wrong when not.
func (s *Server) recordRegistrant(ctx context.Context, opened mention, login string) string {
	record := s.followUp(opened, followup.Record{Kind: followup.KindRegistration, Subject: strings.ToLower(login)})
	if _, err := s.post(ctx, opened, answered{body: command.RegistrationFor(login), follow: record}); err != nil {
		s.Logger.Warn("could not record whom a registration is for", "issue", opened.Issue, "error", err)
		return command.Item(command.NotDone("record whom it is for, so its row cannot be read yet; asking "+
			"again mends it", whyNot(err)))
	}
	return ""
}

// followRegistration is a registration the bot opened, waiting for its row:
// nothing to do once a row has come - the replies to it and `include` take
// over - and one reminder a week on.
func (s *Server) followRegistration(ctx context.Context, event mention, record followup.Record) (answered, bool, error) {
	reader, err := s.listsReader()
	if err != nil {
		return answered{}, false, err
	}
	listed, err := comments(event, reader).Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		return answered{}, true, err
	}
	if row, replied := latestRowAnswered(listed, record.Subject, s.Settings.BotUser()); row != "" {
		if replied {
			return answered{}, false, nil
		}
		// A row posted without the command line under it: answered here,
		// once - the answer is a comment of the bot's after it.
		report := registration.Check(ctx, row, s.registrationWorld(record.Subject))
		return answered{body: command.RegistrationReply(report, false, true, ""), follow: s.ready(ctx, event, report)}, false, nil
	}
	return s.tellOnce(event, record, rowAskedAfter, command.RowAsked()), true, nil
}

// latestRowAnswered is the registrant's latest row, and whether the bot has
// said anything since - which, for a row, is its answer to it.
func latestRowAnswered(comments []github.Comment, subject, bot string) (string, bool) {
	row := latestRow(comments, subject)
	if row == "" {
		return "", false
	}
	after := false
	for _, comment := range comments {
		switch {
		case comment.Body == row && strings.EqualFold(comment.Author, subject):
			after = true
		case after && strings.EqualFold(comment.Author, bot) && !command.IsTurnedAway(comment.Body):
			return row, true
		}
	}
	return row, false
}

// tellOnce says something once, after a while: an invitation waiting, a row
// not come. Told in a fresh record, which is how the next sweep knows it was
// said; nothing until then, and nothing after.
func (s *Server) tellOnce(event mention, record followup.Record, after time.Duration, body string) answered {
	if record.Told || !record.Older(after, time.Now()) {
		return answered{}
	}
	told := s.followUp(event, record)
	if told != nil {
		told.Told = true
	}
	return answered{body: body, follow: told}
}

// readyAfter is how long a complete registration waits for an editor's
// `include` before the editors are told it is ready.
const readyAfter = 2 * 24 * time.Hour

// ready is the record a complete registration's reply leaves, so that the
// sweep can tell the editors when nobody has run `include` on it; nil for an
// incomplete one, or where nothing would come back for it. One that was said
// already stays said: a registrant checking again does not tell the editors
// again.
func (s *Server) ready(ctx context.Context, event mention, report registration.Report) *followup.Record {
	if !report.Complete() {
		return nil
	}
	record := s.followUp(event, followup.Record{Kind: followup.KindReady, Subject: strings.ToLower(report.Row.Login())})
	if record == nil {
		return nil
	}
	if reader, err := s.listsReader(); err == nil {
		if said, ok := s.threadOf(ctx, reader, event).newestAbout(*record); ok && said.Told {
			record.Told, record.Settled = true, true
		}
	}
	return record
}

// followReady tells the editors, once, about a registration nobody has
// included, by looking: it is still complete, no `include` has left its
// record, no pull request adds the row, and the row is not on the list. Off
// where the queue nudge is off, since it tells the editors as that does. Said
// is settled: nothing is left waiting on the bot.
func (s *Server) followReady(ctx context.Context, event mention, record followup.Record) (answered, bool, error) {
	if s.Settings.QueueAfter() == 0 || !record.Older(readyAfter, time.Now()) {
		return answered{}, true, nil
	}
	reader, err := s.listsReader()
	if err != nil {
		return answered{}, false, err
	}
	thread := s.threadOf(ctx, reader, event)
	if len(thread.unread) > 0 {
		// Already reported by the walk; nothing to decide on until it reads.
		return answered{}, true, nil
	}
	for _, other := range thread.newest {
		if other.Kind == followup.KindInclusion {
			return answered{}, false, nil
		}
	}
	report, _, unread, err := s.recheckRegistration(ctx, event)
	if err != nil {
		return answered{}, true, err
	}
	if unread != "" || !report.Complete() {
		// Edited since, and incomplete now: its own reply says what it needs.
		return answered{}, false, nil
	}
	if pulls, ok := s.Lists.(RowPullRequests); ok {
		if pull, err := pulls.OpenRowPullRequest(ctx, event.Repository, record.Subject); err != nil || pull.Number != 0 {
			return answered{}, err == nil, err
		}
	}
	editors, configured := s.Settings.EditorsToTell()
	told := s.followUp(event, record)
	if told != nil {
		told.Told, told.Settled = true, true
	}
	return answered{body: command.ReadyToInclude(editors, configured, record.Subject), follow: told}, false, nil
}
