package bot

import (
	"context"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/github"
)

// Reminders: `remind me in 10 days`, and the sweep that keeps the promise.
//
// A reminder is a follow-up like the others: the reply that confirms it
// carries a signed record with the day it is due, and the nightly sweep posts
// it on that day and settles the record. Nothing is kept anywhere else, so a
// restart loses nothing, and a closed issue drops its reminders because the
// sweep does not read a closed check for this kind. See
// codecheckers/chekhov#25.

// remind sets a reminder on this check.
func (s *Server) remind(ctx context.Context, event mention, parsed command.Command,
	roles command.Roles) answered {
	if reply, ok := s.noCheck(event, "reminders"); !ok {
		return said(reply)
	}
	reminder, err := command.ParseReminder(parsed.Args, event.Author)
	if err != nil {
		return said(err.Error() + "\n")
	}
	issue, err := s.issue(ctx, event)
	if err == nil && issue.Closed {
		return said(command.ReminderOnAClosedCheck())
	}
	if reminder.Handle != command.Handle(event.Author) {
		if !s.mayRemindOthers(ctx, event, roles) {
			return said(command.RemindOthersRefused())
		}
		if !s.onTheCheck(ctx, event, issue, reminder.Handle) {
			return said(command.ReminderNotOnTheCheck(reminder.Handle))
		}
	}
	if s.waitingReminders(ctx, event) >= command.MostWaitingReminders {
		return said(command.TooManyReminders())
	}
	note, err := command.ReminderNote(event.Body, event.Comment == 0)
	if err != nil {
		return said(err.Error() + "\n")
	}

	due := time.Now().UTC().AddDate(0, 0, reminder.Days)
	record := s.followUp(event, followup.Record{Kind: followup.KindReminder,
		Subject: reminder.Handle, By: command.Handle(event.Author),
		Due: stamped(due), Note: note})
	if record == nil {
		return said(command.ReminderUnkept())
	}
	return answered{body: command.ReminderSet(reminder, event.Author, due, note), follow: record}
}

// mayRemindOthers is whether the author may set a reminder for somebody
// else; see command.RemindOthers. The roles of this check are a read of its
// record, so they are asked for only when the standing ones are not enough.
func (s *Server) mayRemindOthers(ctx context.Context, event mention, roles command.Roles) bool {
	return command.RemindOthers.Permits(roles) ||
		command.RemindOthers.Permits(s.checkRoles(ctx, event, roles))
}

// onTheCheck is whether somebody has anything to do with this check: opened
// it, is assigned to it, holds a role in its record, or has commented on it.
// A reminder notifies the person it names, and the bot notifies only people
// a check has already brought in. An issue that could not be read answers
// from the comments and the record alone.
func (s *Server) onTheCheck(ctx context.Context, event mention, issue github.Issue, handle string) bool {
	if command.Handle(issue.Author) == handle {
		return true
	}
	for _, assignee := range issue.Assignees {
		if command.Handle(assignee) == handle {
			return true
		}
	}
	if s.Checks != nil {
		reading, err := s.checks(event).Read(ctx, event.Repository, event.Issue)
		if err == nil && reading.Tampered == nil && len(reading.Record.RolesOf(handle)) > 0 {
			return true
		}
	}
	reader, ok := s.Replies.(Reader)
	if !ok {
		return false
	}
	written, err := comments(event, reader).Comments(ctx, event.Repository, event.Issue)
	if err != nil {
		return false
	}
	for _, comment := range written {
		if command.Handle(comment.Author) == handle {
			return true
		}
	}
	return false
}

// waitingReminders is how many reminders on this check have not come yet. A
// reply path that cannot list the comments counts none, and the reminder it
// confirms is one the sweep could not read either.
func (s *Server) waitingReminders(ctx context.Context, event mention) int {
	reader, ok := s.Replies.(Reader)
	if !ok {
		return 0
	}
	records, _ := s.outstandingIn(ctx, reader, event)
	waiting := 0
	for _, record := range records {
		if record.Kind == followup.KindReminder {
			waiting++
		}
	}
	return waiting
}

// followReminder posts a reminder on its day, and settles it, so that it is
// said once. Before the day it is waiting, and says nothing.
func (s *Server) followReminder(_ context.Context, _ mention, record followup.Record) (answered, bool, error) {
	now := time.Now()
	if !record.DueBy(now) {
		return answered{}, true, nil
	}
	settled := record
	settled.Settled, settled.Asked = true, stamped(now)
	return answered{
		body:   command.ReminderDue(record.Subject, record.By, record.Raised(), record.Note),
		follow: &settled,
	}, false, nil
}
