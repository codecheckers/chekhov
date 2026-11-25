package command

import "fmt"

// What an assignment says about the team it puts somebody in (#47). The bot
// changes the membership in internal/bot; the words are here, with every
// other reply's.

// TeamAdded says the person was put in the team.
func TeamAdded(handle, organisation, team string) string {
	return fmt.Sprintf("✔ I have put %s in `%s/%s`.", mention(handle), organisation, team)
}

// TeamPending says the person was invited through the team and has not
// accepted, so is not in it.
func TeamPending(handle, organisation, team string) string {
	return fmt.Sprintf("⚠ %s has been invited to `%s/%s` and has not accepted yet, "+
		"so they are not in it.", mention(handle), organisation, team)
}

// TeamMaintainer says the person was in the team already, as a maintainer.
func TeamMaintainer(handle, organisation, team string) string {
	return fmt.Sprintf("ℹ %s is already in `%s/%s`, as a maintainer - "+
		"which I did not set and will not change.", mention(handle), organisation, team)
}

// TeamFailed says the person could not be put in the team, and why.
func TeamFailed(organisation, team, why string) string {
	return NotDone(fmt.Sprintf("put them in `%s/%s`", organisation, team), why)
}

// JoinedTeamStill says, again, that somebody who joined the organisation is
// not in the team yet: the note is what the team came to tonight. Said every
// three days at most, so it names who it is about rather than leaning on the
// reply it follows (#69). It promises no retry: a pending membership is the
// person's to accept, and only a refused add is tried again.
func JoinedTeamStill(handle, note string) string {
	return fmt.Sprintf("%s is in the organisation, and not in the team yet.\n\n%s\n\n"+
		"I put them in it as soon as I can, and say so here when it is done.\n", mention(handle), note)
}
