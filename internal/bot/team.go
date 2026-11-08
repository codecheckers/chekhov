package bot

import (
	"context"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
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
	TeamMembership(ctx context.Context, organisation, team, handle string) (github.Place, error)
	AddToTeam(ctx context.Context, organisation, team, handle string) (github.Place, error)
	// OrganisationMembership is where somebody stands beyond yes or no: an
	// invitation waiting, and the role - which is how `include` checks the
	// owners' half (#49).
	OrganisationMembership(ctx context.Context, organisation, handle string) (github.Invitation, error)
}

// The reply path a deployment uses can do all of it, which a type assertion
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
// established they are not in the organisation. team is where they will end up
// once they have, empty for a role that puts nobody in one.
func (s *Server) outsideReply(ctx context.Context, handle string,
	role command.Role, team string) string {
	request, ok := s.ownersRequest(ctx, command.OutsideRequest{
		Handle: handle, Role: role, Organisation: s.Settings.TeamOrganisation(), Team: team,
	})
	if !ok {
		return ""
	}
	return command.OutsideReply(request)
}

// ownersRequest fills in who to ask: the organisation's owners, or why they
// could not be read. False when this reply path cannot read the organisation
// at all. Shared by `assign` and `include`, so that both ask the same people
// the same way.
func (s *Server) ownersRequest(ctx context.Context, request command.OutsideRequest) (command.OutsideRequest, bool) {
	reader, ok := s.Replies.(Organisation)
	if !ok || request.Organisation == "" {
		return request, false
	}
	// A deployment may name who to ask instead of reading the organisation's
	// owners. These replies are the one place the bot writes a plain
	// @mention, so a development deployment asks whoever is testing it rather
	// than notifying three people who did not ask to be in a test.
	if configured := s.Settings.OwnersOverride(); len(configured) > 0 {
		request.Owners, request.Configured = configured, true
		return request, true
	}
	owners, err := reader.Owners(ctx, request.Organisation)
	if err != nil {
		request.Unreadable = err.Error()
	} else {
		request.Owners = owners
	}
	return request, true
}

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

	if event.Issue <= 0 {
		return ordinary
	}
	issue, err := s.issue(ctx, event)
	if err != nil {
		s.Logger.Warn("could not read a check's labels, so the ordinary team was used",
			"issue", event.Issue, "error", err)
		return ordinary
	}
	// What the register puts on a check an institution's arrangement covers.
	// The name is the register's, so the settings carry it as they carry the
	// team; and it is asked with the predicate the label rules use, so the
	// team a check sends somebody to and the warning about that same check
	// cannot disagree about whether it is institutional.
	if command.HasLabel(issue.Labels, s.Settings.InstitutionLabel()) {
		return institutional
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
	team := s.destination(ctx, event, role, chosen)
	if team == "" || !inside {
		return ""
	}
	note, _ := s.intoTheTeam(ctx, handle, team)
	return note
}

// destination is the team this role puts somebody in on this check, empty for
// a role that puts them in none.
//
// One place, because the reply to an assignment promises a team, the record it
// leaves behind carries one, and the sweep that finishes the job reads that
// record: the three must not be able to disagree.
func (s *Server) destination(ctx context.Context, event mention, role command.Role, chosen string) string {
	if !role.Checks() {
		return ""
	}
	return s.teamFor(ctx, event, chosen)
}

// outstanding is the record the ask to the owners leaves behind, so that the
// nightly sweep can finish the job once the person has joined.
//
// Nil when there is nothing to chase with: without an issue there is nowhere
// to write it, and without a signer nothing would trust it later.
func (s *Server) outstanding(event mention, handle string, role command.Role, team string) *followup.Record {
	return s.followUp(event, followup.Record{
		Kind: followup.KindOrganisation, Subject: handle, Role: string(role), Team: team})
}

// followUp is a record of something a reply leaves outstanding on this check,
// asked now - or nil when nothing would come back for it: no issue to come
// back to, or no key to sign it with.
func (s *Server) followUp(event mention, record followup.Record) *followup.Record {
	if event.Issue <= 0 || !s.signer().Signs() {
		return nil
	}
	record.Check = event.check()
	record.Asked = stamped(time.Now())
	return &record
}

// stamped is a time as a record writes it.
func stamped(when time.Time) string { return when.UTC().Format(time.RFC3339) }

// withNote puts a note under a reply, when there is one: the team an
// assignment put somebody in, or a label that no longer describes the check.
func withNote(reply, note string) string {
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
//
// in says whether they are in it now, whatever was said: the welcome on the
// lists waits for it, and a note alone cannot tell "added" from "could not".
func (s *Server) intoTheTeam(ctx context.Context, handle, team string) (note string, in bool) {
	organisation := s.Settings.TeamOrganisation()
	changer, ok := s.Replies.(Organisation)
	if !ok || organisation == "" || team == "" {
		return "", false
	}

	// The read is only an optimisation - the write below is idempotent - so a
	// read that failed falls through to it rather than returning nothing,
	// which an editor would read as "the team was handled".
	place, err := changer.TeamMembership(ctx, organisation, team, handle)
	if err != nil {
		s.Logger.Warn("could not read a team membership, so the add was attempted anyway",
			"handle", handle, "team", team, "error", err)
	}
	switch {
	case err == nil && place.Active:
		// In the team already, whatever role they hold there. Nothing to do
		// and nothing to say - and nothing to write, which matters for a
		// maintainer: a PUT asking for `member` would take the role away.
		return "", true
	case err == nil && place.Pending:
		// Somebody an owner invited through the team, who has not accepted.
		// Adding them again would change nothing, and saying nothing would
		// leave the editor believing the team was handled.
		return command.TeamPending(handle, organisation, team), false
	}

	added, err := changer.AddToTeam(ctx, organisation, team, handle)
	if err != nil {
		s.Logger.Warn("could not add somebody to the team", "handle", handle, "team", team, "error", err)
		return command.TeamFailed(organisation, team, whyNot(err)), false
	}
	login := added.Login
	if login == "" {
		login = handle
	}
	if added.Maintains() {
		// An organisation owner is a maintainer of every team they are in,
		// and GitHub answers 404 when this bot reads their membership - so
		// the add above is how it finds out they were there all along. It is
		// not a failure, and the role is not the bot's to change.
		return command.TeamMaintainer(login, organisation, team), true
	}
	return command.TeamAdded(login, organisation, team), true
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
