package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
)

// The allowlist of a check: whom an editor let task the bot there who
// otherwise could not. It is part of the signed record of the check, so it is
// kept and checked as the roles are; what it lets somebody do is decided in
// gate.go.

// listAllowed says who is on the allowlist of this check.
func (s *Server) listAllowed(ctx context.Context, event mention, parsed command.Command) string {
	if reply, ok := s.noCheck(event, "the allowlist of a check"); !ok {
		return reply
	}
	if len(parsed.Args) > 0 {
		return command.AllowlistUsage()
	}
	reading, err := s.checks(event).Read(ctx, event.Repository, event.Issue)
	if err != nil {
		return fmt.Sprintf("I could not read the allowlist of this check: %s\n", err)
	}
	return command.AllowlistReply(reading.Record.Holders().Allowlist, s.Settings.TeamOrganisation(),
		event.PullRequest, provenanceOf(reading))
}

// allow puts somebody on the allowlist. Whether they have taken part in the
// issue is not asked, since an editor may let somebody in before they have;
// that there is such an account is, so that a mistyped handle that names
// nobody is caught. One that names somebody else cannot be, which is why the
// reply says the handle.
func (s *Server) allow(ctx context.Context, event mention, parsed command.Command) string {
	handle, err := command.ParseAllowlisted(parsed.Name, parsed.Args)
	if reply, ok := s.recordCommand(event, "the allowlist of a check", err); !ok {
		return reply
	}
	if accounts, ok := s.Replies.(Accounts); ok {
		if _, err := accounts.PersonAccount(ctx, handle); err != nil {
			return command.NotDone("add them to the allowlist", whyNot(err)) + "\n"
		}
	}

	_, err = s.checks(event).Update(ctx, event.Repository, event.Issue, func(record people.Record) (people.Record, error) {
		return record.Allow(handle)
	})
	return recordChanged(err, "the allowlist entry", func(changed bool) string {
		return command.AllowedReply(handle, changed, event.Author, time.Now())
	})
}

// disallow takes somebody off the allowlist.
func (s *Server) disallow(ctx context.Context, event mention, parsed command.Command) string {
	handle, err := command.ParseAllowlisted(parsed.Name, parsed.Args)
	if reply, ok := s.recordCommand(event, "the allowlist of a check", err); !ok {
		return reply
	}

	_, err = s.checks(event).Update(ctx, event.Repository, event.Issue, func(record people.Record) (people.Record, error) {
		return record.Disallow(handle)
	})
	return recordChanged(err, "the removal", func(changed bool) string {
		return command.DisallowedReply(handle, changed, event.PullRequest, event.Author, time.Now())
	})
}
