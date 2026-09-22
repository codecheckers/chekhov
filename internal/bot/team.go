package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// Who may hold a role, and which team they belong in.
//
// The division of labour GitHub imposes: only an organisation owner can invite
// somebody from outside into a team, and a team maintainer can add somebody
// who is already in the organisation. The bot is the second and must never be
// the first - an owner reaches membership everywhere, billing and repository
// deletion, whatever the code chooses to call. So the owners invite, and the
// bot does the teams. See codecheckers/chekhov#47.

// Organisation is the part of the reply path that reads and changes team
// membership. A reply path that cannot - the command line preview, the test
// recorder - simply does not, and the reply says so rather than pretending.
//
// There is no method here that removes anybody, and none that sets a role: see
// internal/github/team.go.
type Organisation interface {
	InOrganisation(ctx context.Context, organisation, handle string) (bool, error)
	Owners(ctx context.Context, organisation string) ([]string, error)
	TeamMembership(ctx context.Context, organisation, team, handle string) (active, pending bool, err error)
	AddToTeam(ctx context.Context, organisation, team, handle string) (login string, err error)
}

// The reply path a deployment uses can do all four, which a type assertion
// would otherwise only discover at runtime - as a command refusing with a
// message that sends an editor to audit a token that is perfectly fine.
var _ Organisation = (*github.Client)(nil)

// Membership is what the organisation says about somebody: whether they are in
// it, and whether the question could be answered at all.
//
// The two are separate on purpose. Not a member and could not tell lead to
// different places: the first refuses the role and asks the owners, and the
// second records the role and touches no team - because the write that would
// follow is a PUT on a team membership, and GitHub turns that into an
// organisation invitation for somebody who is not a member. Acting on a
// guess there would be the one thing this bot must not do.
func (s *Server) membership(ctx context.Context, handle string) (inside, known bool) {
	organisation := s.Settings.TeamOrganisation()
	reader, ok := s.Replies.(Organisation)
	if !ok || organisation == "" {
		return false, false
	}
	inside, err := reader.InOrganisation(ctx, organisation, handle)
	if err != nil {
		s.Logger.Warn("could not read whether somebody is in the organisation",
			"handle", handle, "organisation", organisation, "error", err)
		return false, false
	}
	return inside, true
}

// outsideReply asks the owners to invite somebody, for a caller that has
// established they are not in the organisation.
func (s *Server) outsideReply(ctx context.Context, event mention, handle string,
	role command.Role, chosen string) string {
	organisation := s.Settings.TeamOrganisation()
	reader, ok := s.Replies.(Organisation)
	if !ok || organisation == "" {
		return ""
	}

	request := command.OutsideRequest{Handle: handle, Role: role, Organisation: organisation}
	// Only a role that puts somebody in a team can promise one. An author is
	// not a codechecker, and the handling editor is an editor already.
	if role.Checks() {
		request.Team = s.teamFor(ctx, event, chosen)
	}
	// A deployment may name who to ask instead of reading the organisation's
	// owners. That reply is the one place the bot writes a plain @mention, so
	// a development deployment asks whoever is testing it rather than
	// notifying three people who did not ask to be in a test.
	if configured := s.Settings.OwnersOverride(); len(configured) > 0 {
		request.Owners, request.Configured = configured, true
		return command.OutsideReply(request)
	}
	owners, err := reader.Owners(ctx, organisation)
	if err != nil {
		request.Unreadable = err.Error()
	} else {
		request.Owners = owners
	}
	return command.OutsideReply(request)
}

// institutionLabel is what the register puts on a check an institution's
// arrangement covers. The name is the register's, so it belongs in the
// settings eventually - #44 moves the label names there; until then it is
// written once, here, and used for nothing but reading a label. Which team an
// institutional check uses is a named setting, not this string matched against
// team names.
const institutionLabel = "institution"

// teamFor is the team a codechecker on this check belongs in.
//
// An institutional check is covered by an arrangement an institution has made,
// and the people who check under it are kept in their own team and their own
// list - so the team follows from the check rather than from the person. The
// allow-list in the settings says what may be named at all, and config refuses
// one that contains the editors team.
//
// A check whose labels cannot be read gets the ordinary team: the wrong team
// is a thing a person can fix, and refusing to name one would stop the
// assignment over a question nobody asked.
func (s *Server) teamFor(ctx context.Context, event mention, chosen string) string {
	// An editor who named a team knows something the labels do not, in either
	// direction. Checked against the allow-list before it gets here.
	if chosen != "" {
		return chosen
	}
	ordinary, institutional := s.Settings.CodecheckersTeam(), s.Settings.InstitutionalTeam()
	if institutional == "" {
		return ordinary
	}

	reader, ok := s.Replies.(Issues)
	if !ok || event.Issue <= 0 {
		return ordinary
	}
	issue, err := reader.Issue(ctx, event.Repository, event.Issue)
	if err != nil {
		s.Logger.Warn("could not read a check's labels, so the ordinary team was used",
			"issue", event.Issue, "error", err)
		return ordinary
	}
	for _, label := range issue.Labels {
		if strings.EqualFold(label, institutionLabel) {
			return institutional
		}
	}
	return ordinary
}

// teamNote is what to say about the team after an assignment, empty when
// there is nothing to say.
//
// Only for a role that puts somebody in a team, and only when they are known
// to be in the organisation: the write is a PUT on a team membership, which
// GitHub turns into an organisation invitation for anybody else, and inviting
// is the owners' half.
func (s *Server) teamNote(ctx context.Context, event mention, handle string,
	role command.Role, chosen string, inside bool) string {
	if !role.Checks() || !inside {
		return ""
	}
	return s.intoTheTeam(ctx, handle, s.teamFor(ctx, event, chosen))
}

// withTeam puts a note about the team under a reply, when there is one.
func withTeam(reply, note string) string {
	if note == "" {
		return reply
	}
	return reply + "\n" + note + "\n"
}

// intoTheTeam puts a codechecker in the team this check implies, when they are
// not in it already.
//
// The other half of the division of labour: the person is in the organisation,
// so this is a team change, which is the bot's to make. A failure costs the
// membership and not the role - the role is recorded either way, and the reply
// says what was left undone.
func (s *Server) intoTheTeam(ctx context.Context, handle, team string) string {
	organisation := s.Settings.TeamOrganisation()
	changer, ok := s.Replies.(Organisation)
	if !ok || organisation == "" || team == "" {
		return ""
	}

	// The read is only an optimisation - the write below is idempotent - so a
	// read that failed falls through to it rather than returning nothing,
	// which an editor would read as "the team was handled".
	active, pending, err := changer.TeamMembership(ctx, organisation, team, handle)
	if err != nil {
		s.Logger.Warn("could not read a team membership, so the add was attempted anyway",
			"handle", handle, "team", team, "error", err)
	}
	switch {
	case err == nil && active:
		return ""
	case err == nil && pending:
		// Somebody an owner invited through the team, who has not accepted.
		// Adding them again would change nothing, and saying nothing would
		// leave the editor believing the team was handled.
		return fmt.Sprintf("`@%s` has been invited to `%s/%s` and has not accepted yet, "+
			"so they are not in it.", handle, organisation, team)
	}

	login, err := changer.AddToTeam(ctx, organisation, team, handle)
	if err != nil {
		s.Logger.Warn("could not add somebody to the team", "handle", handle, "team", team, "error", err)
		return fmt.Sprintf("I could not put them in `%s/%s`: %s", organisation, team, err)
	}
	if login == "" {
		login = handle
	}
	return fmt.Sprintf("I have put `@%s` in `%s/%s`.", login, organisation, team)
}

// managedTeam reports whether a team is one the settings allow the bot to add
// to. The fence in internal/github refuses anything else anyway; this is so
// that a mistyped team is answered with the list rather than with a refusal
// from the request.
func (s *Server) managedTeam(team string) bool {
	for _, managed := range s.Settings.ManagedTeams() {
		if strings.EqualFold(team, managed) {
			return true
		}
	}
	return false
}
