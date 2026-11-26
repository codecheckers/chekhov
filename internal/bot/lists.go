package bot

import (
	"context"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/registration"
)

// The codechecker lists, the second repository the bot works on (#49).
//
// People register there by opening an issue from a template, and the bot
// answers a registration and nothing else: it reads the row, checks it, and
// says what is still missing. A delivery from the lists takes the same path
// as one from the register - the same guards, the same act and post - and
// parts at answer, because the register's commands mean nothing there and a
// registration means nothing on the register. Writes there go through a
// client of their own, Server.Lists, which names that repository and no
// other, and shares the register client's queue.

// posterFor is the client that writes to a repository, nil for one this
// deployment does not work on - which is the fence refuse holds.
func (s *Server) posterFor(repository string) Poster {
	switch {
	case repository == s.Settings.TargetRepository():
		return s.Replies
	case s.onLists(mention{Repository: repository}):
		return s.Lists
	}
	return nil
}

// onLists reports whether an event is on the lists this deployment works on.
// Compared exactly, as the register is and as the client's own fence is: a
// repository the bot accepted here and its client then refused would be a
// registration answered into nowhere.
func (s *Server) onLists(event mention) bool {
	lists := s.Settings.ListsRepository()
	return lists != "" && s.Lists != nil && event.Repository == lists
}

// worksOn names the repositories this deployment answers on, for a log line.
func (s *Server) worksOn() string {
	if lists := s.Settings.ListsRepository(); lists != "" && s.Lists != nil {
		return s.Settings.TargetRepository() + " and " + lists
	}
	return s.Settings.TargetRepository()
}

// registrationOpened reports whether an event is a registration being opened,
// which is answered without being asked.
func (s *Server) registrationOpened(event mention) bool {
	return event.Comment == 0 && s.onLists(event) && s.isRegistration(event)
}

func (s *Server) isRegistration(event mention) bool {
	return command.HasLabel(event.Labels, s.Settings.RegistrationLabel())
}

// answerOnLists is the reply to one command on the lists. The first answer to
// an issue - the one to its being opened - thanks the registrant for it.
func (s *Server) answerOnLists(ctx context.Context, event mention, parsed command.Command) answered {
	definition, found := command.Lookup(string(parsed.Name))
	switch {
	case !found || !definition.Lists:
		return said(command.OnListsReply(s.Settings.ListsRepository(), s.Settings.TargetRepository()))
	case !definition.AnyListsIssue && !s.isRegistration(event):
		return said(command.NotARegistrationReply(s.Settings.RegistrationLabel()))
	// Only the standing roles, as the lists have no check and so no per-check
	// role; and only for a command not for anybody, so that the answer to
	// every opened registration needs no team read.
	case !definition.Permits(nil) && !definition.Permits(s.standingRoles(ctx, event)):
		return said(definition.Refusal())
	case parsed.Name == command.IncludeOpen:
		return s.invite(ctx, event, parsed, true)
	case parsed.Name == command.Include && len(parsed.Args) > 0:
		return said(command.IncludeTakesNoArguments())
	case parsed.Name == command.Include:
		return s.include(ctx, event)
	}
	report, inComment, unread := s.checkRegistration(ctx, event)
	if unread != "" {
		return said(unread)
	}
	first := event.Comment == 0
	// A registration being opened has no pull request adding its row yet;
	// every later check keeps the one there is in step (#49).
	var step rowStep
	if !first {
		step = s.keepRowInStep(ctx, event, report)
	}
	body := command.RegistrationReply(report, first, inComment, step.line)
	// The survey is asked about in the first answer only, and only on a
	// registration from the template: one the bot opened asks no questions.
	if first && !inComment {
		if reminder := command.SurveyReminder(registration.SurveyUnanswered(event.IssueBody)); reminder != "" {
			body += "\n" + reminder
		}
	}
	return answered{body: body, follow: s.ready(ctx, event, report)}
}

// registrationWorld is what a registration is checked against: ORCID, the
// codecheckers team and the lists as they are now.
func (s *Server) registrationWorld(author string) registration.World {
	services := s.Services.Fresh()
	team, institutional := s.Settings.CodecheckersTeam(), s.Settings.InstitutionalTeam()
	world := registration.World{
		Author: author,
		ORCID:  services.ORCIDProfile,
		Team:   s.Settings.TeamOrganisation() + "/" + team,
		InTeam: func(ctx context.Context, login string) bool { return s.Teams.Has(ctx, team, login) },
		Lists:  func() []registration.List { return registration.ReadLists(services, s.Settings.ListFiles()...) },
	}
	if institutional != "" {
		world.Institutional = s.Settings.TeamOrganisation() + "/" + institutional
		world.InInstitutional = func(ctx context.Context, login string) bool {
			return s.Teams.Has(ctx, institutional, login)
		}
	}
	return world
}
