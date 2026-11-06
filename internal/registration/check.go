package registration

import (
	"context"
	"path"
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

// Report is what a registration still needs.
type Report struct {
	// Row is the registration as it would be listed, the zero Row when the
	// issue holds none.
	Row      Row
	Findings []Finding
}

// Complete reports whether a row was found and nothing holds it up.
func (r Report) Complete() bool {
	if r.Row == (Row{}) {
		return false
	}
	for _, finding := range r.Findings {
		if finding.Blocks() {
			return false
		}
	}
	return true
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
	if world.InTeam != nil && world.InTeam(ctx, report.Row.Login()) {
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
	seeORCID := strings.EqualFold(contact, SeeORCID)
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
			"the `contact` column instead of `" + SeeORCID + "`."
		if len(profile.Pages) > 0 {
			text += " Your record links a page of your own, and I cannot read an address off a " +
				"page: if that is where yours is, please name the page in `contact`."
		}
		findings = append(findings, failed("%s", text))
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
		for _, existing := range list.Rows {
			same := strings.EqualFold(login(existing["handle"]), row.Login()) ||
				row.ORCID != "" && strings.EqualFold(check.ORCIDDigits(existing["ORCID"]), row.ORCID)
			if !same {
				continue
			}
			if list.name() == RegistrationList {
				findings = append(findings, failed("You are on %s already, as %s. If something "+
					"there has changed, an editor can correct the row.", code(list.name()),
					code(strings.TrimSpace(existing["name"]))))
			} else {
				findings = append(findings, finding(Note,
					"You are on %s already, which is fine: that list is kept separately.", code(list.name())))
			}
			break
		}
	}
	return findings
}
