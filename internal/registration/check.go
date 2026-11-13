package registration

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
)

// RegistrationList is the file a registration adds a row to. The other lists -
// institutional, AGILE - are of people who codecheck for an institution or a
// conference, and somebody on one of them may well register as a volunteer
// too; the codecheckers repository says so in its own CLAUDE.md.
const RegistrationList = "codecheckers.csv"

// World is what a registration is checked against. Functions rather than
// clients, so that a test says what each answers without a server.
type World struct {
	// Author is who opened the issue, which GitHub vouches for: the row's
	// handle is compared with it, and its spelling is the one kept.
	Author string
	// ORCID reads a public ORCID record.
	ORCID func(ctx context.Context, orcid string) (check.ORCIDProfile, error)
	// Team is the codecheckers team, organisation/slug, and InTeam whether
	// somebody is in it. A registrant already in the team is told so; it
	// changes nothing else.
	Team   string
	InTeam func(ctx context.Context, login string) bool
	// Institutional is the institutional codecheckers' team, and
	// InInstitutional whether somebody is in it. Being there as well is
	// possible - somebody checks for an institution and on the open list -
	// so it is a warning for an editor to look at, never a refusal.
	Institutional   string
	InInstitutional func(ctx context.Context, login string) bool
	// Lists reads the codechecker lists. Asked only once a row was found:
	// a first attempt often has none, and three downloads would answer
	// nothing then.
	Lists func() []List
}

// A List is one codechecker list, as read.
type List struct {
	URL string
	check.Table
}

func (l List) name() string { return path.Base(l.URL) }

// Holds reports whether the list has a row for a login, its @ and case aside.
func (l List) Holds(handle string) bool {
	return slices.ContainsFunc(l.Rows, func(row map[string]string) bool { return sameLogin(row, handle) })
}

// nameOf is how a row of a list names somebody: its name, or its handle when
// the name is empty.
func nameOf(row map[string]string) string {
	if name := strings.TrimSpace(row["name"]); name != "" {
		return name
	}
	return strings.TrimSpace(row["handle"])
}

// sameLogin reports whether a row of a list is somebody's, by handle.
func sameLogin(row map[string]string, handle string) bool {
	return strings.EqualFold(login(row["handle"]), login(handle))
}

// Report is what a registration still needs.
type Report struct {
	// Row is the registration as it would be listed, the zero Row when the
	// issue holds none.
	Row      Row
	Findings []Finding
	// InTeam says the registrant is in the codecheckers team already, so
	// being listed is all that is left.
	InTeam bool
}

// Complete reports whether a row was found and nothing holds it up.
func (r Report) Complete() bool { return r.Row != (Row{}) && len(r.Blocking()) == 0 }

// Listed reports whether the registrant is on the registration list already.
func (r Report) Listed() bool {
	return slices.ContainsFunc(r.Findings, func(f Finding) bool { return f.listed })
}

// Includable reports whether `include` may go on: a row, and nothing holding
// it up but being listed already. An `include` whose pull request has been
// merged is that, and the organisation's half may still be open.
func (r Report) Includable() bool {
	return r.Row != (Row{}) && !slices.ContainsFunc(r.Blocking(), func(f Finding) bool { return !f.listed })
}

// Blocking is the findings that hold the registration up.
func (r Report) Blocking() []Finding {
	var blocking []Finding
	for _, finding := range r.Findings {
		if finding.Blocks() {
			blocking = append(blocking, finding)
		}
	}
	return blocking
}

// Check reads a registration issue and checks the row against the world.
func Check(ctx context.Context, body string, world World) Report {
	row, findings := Parse(body)
	report := Report{Row: row, Findings: findings}
	if row == (Row{}) {
		return report
	}

	if author := login(world.Author); strings.EqualFold(row.Login(), author) {
		report.Row.Handle = "@" + author
	} else {
		// Nothing more is looked up: whether somebody else is in the team or
		// on a list is not this issue's business, and a reply that said so
		// would answer for anybody whose handle a stranger typed.
		report.Findings = append(report.Findings, failed("The row names %s, and this issue was "+
			"opened by %s. A registration is for the person who opens it: please put your own "+
			"handle in the row.", code(row.Handle), code("@"+author)))
		return report
	}

	report.Findings = append(report.Findings, orcid(ctx, report.Row, world)...)
	if world.Lists != nil {
		report.Findings = append(report.Findings, listed(report.Row, world.Lists())...)
	}
	if world.Institutional != "" && world.InInstitutional != nil && world.InInstitutional(ctx, report.Row.Login()) {
		report.Findings = append(report.Findings, warning("You are in %s already, the institutional "+
			"codecheckers' team. Being in both is possible; an editor will check that both are meant.",
			code(world.Institutional)))
	}
	if world.InTeam != nil && world.InTeam(ctx, report.Row.Login()) {
		report.InTeam = true
		report.Findings = append(report.Findings, finding(Note,
			"You are in %s already, so being listed is what is left.", code(world.Team)))
	}
	return report
}

// orcid checks that the ORCID exists, and that the contact reaches somebody.
//
// One function for both, because `see ORCID page` is answered by the same
// record: an ORCID that cannot be read cannot say whether it shows an address.
func orcid(ctx context.Context, row Row, world World) []Finding {
	contact := strings.TrimSpace(row.Contact)
	seeORCID, linked := pointsToORCID(contact)
	var findings []Finding
	if noContact[strings.ToLower(contact)] {
		findings = append(findings, failed("The `contact` column says nothing. We want another way to "+
			"reach you besides GitHub: an email address, a page that carries one, or `"+SeeORCID+
			"` if your ORCID record shows your email to everyone."))
	}
	if !check.ORCIDWellFormed(row.ORCID) || world.ORCID == nil {
		// Parse has said what is wrong with the ORCID.
		return findings
	}
	// The record asked about is the row's, so a contact naming another one
	// would be answered for the wrong person. That is the one thing said
	// about the contact then: whether the row's record shows an email is
	// asked once the contact points to it.
	for _, id := range linked {
		if id != row.ORCID {
			return append(findings, failed("The `contact` column names the ORCID record %s, and the row's "+
				"iD is %s. Please point to your own record in both, or write `"+SeeORCID+"`.",
				code(id), code(row.ORCID)))
		}
	}

	profile, err := world.ORCID(ctx, row.ORCID)
	switch {
	case check.IsNotFound(err):
		findings = append(findings, failed("ORCID has no record %s. Please check the iD in the row.", code(row.ORCID)))
	case err != nil:
		asked := "whether %s exists"
		if seeORCID {
			asked = "about %s, so I cannot tell yet whether it shows your email"
		}
		findings = append(findings, finding(NotChecked, "I could not ask ORCID "+asked+" (%s).", code(row.ORCID), err))
	case seeORCID && len(profile.Emails) == 0:
		text := "Unfortunately I didn't find an email on your ORCID record - can you please check? " +
			"We simply want another way to reach you besides GitHub. Please make your email " +
			"visible to everyone on ORCID, or put an address, or a page that carries one, in " +
			"the `contact` column instead of pointing to ORCID."
		if len(profile.Pages) > 0 {
			text += " Your record links a page of your own, and I cannot read an address off a " +
				"page: if that is where yours is, please name the page in `contact`."
		}
		findings = append(findings, failed("%s", text))
	case seeORCID && profile.Unverified == len(profile.Emails):
		// A warning, not a failure: an address ORCID has not verified usually
		// works, and the registrant can do something about it in a minute.
		findings = append(findings, warning("Your ORCID record shows your email, but ORCID has not "+
			"verified it. This does not hold up your registration; please verify it on ORCID, so "+
			"that we can rely on it to reach you."))
	}
	return findings
}

// noContact are the ways of writing that there is none. Anything else is
// taken as written: an address, a page, or an address spelt out in words -
// the registrant's choice, which the runbook says is never rewritten.
var noContact = map[string]bool{"": true, "na": true, "n/a": true, "none": true, "-": true, "no": true}

// listed says where the registrant is listed already, by handle or ORCID.
// On the list a registration adds to, that holds it up: a second row for
// one person is the drift the annual maintenance has to clean up.
func listed(row Row, lists []List) []Finding {
	var findings []Finding
	for _, list := range lists {
		if list.Err != nil {
			findings = append(findings, finding(NotChecked,
				"I could not read %s to see whether you are on it already (%s).", code(list.name()), list.Err))
			continue
		}
		// The row with their handle if there is one, and only then one with
		// their ORCID: a list that drifted can hold both, and the registrant's
		// own row is the one that says they are listed.
		var match map[string]string
		for _, existing := range list.Rows {
			if sameLogin(existing, row.Handle) {
				match = existing
				break
			}
			if match == nil && row.ORCID != "" && strings.EqualFold(check.ORCIDDigits(existing["ORCID"]), row.ORCID) {
				match = existing
			}
		}
		if existing := match; existing != nil {
			if list.name() == RegistrationList {
				onIt := failed("You are on %s already, as %s. If something "+
					"there has changed, an editor can correct the row.", code(list.name()),
					code(nameOf(existing)))
				// By handle only: the same ORCID under another handle is a
				// row an editor has to look at, not this registration listed.
				onIt.listed = sameLogin(existing, row.Handle)
				findings = append(findings, onIt)
			} else {
				// Somebody can check for an institution and on the open list
				// too, so this holds nothing up: an editor looks at it.
				findings = append(findings, warning("You are on %s already, as %s. Being on more than "+
					"one list is possible; an editor will check that the rows agree.", code(list.name()),
					code(nameOf(existing))))
			}
		}
	}
	return findings
}
