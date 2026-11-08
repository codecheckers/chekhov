package registration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
)

// The handles here were checked to belong to nobody, see CLAUDE.md. The ORCID
// is Josiah Carberry's, ORCID's own test record.
const (
	registrant = "a-made-up-registrant"
	carberry   = "0000-0002-1825-0097"
	validRow   = `Josiah Carberry,@a-made-up-registrant,0000-0002-1825-0097,see ORCID page,"psychoceramics, cracked pots","R, Python",expired,2026-11;https://orcid.org/0000-0002-1825-0097,@carberry@fediscience.org`
)

// filed is the registration template, from codecheckers/codecheckers, with
// its data line replaced by the row a registrant wrote - the HTML comments,
// and the example row inside them, left as the template has them.
func filed(t *testing.T, row string) string {
	t.Helper()
	template, err := os.ReadFile("testdata/template.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(template), "\n")
	for i, line := range lines {
		if strings.Contains(line, "@GitHub-handle") {
			lines[i] = row
			return strings.Join(lines, "\n")
		}
	}
	t.Fatal("the template has no data line to replace")
	return ""
}

// world answers as a registration in good order would have it, and each test
// changes the one thing it is about.
func world() World {
	return World{
		Author: registrant,
		ORCID: func(context.Context, string) (check.ORCIDProfile, error) {
			return check.ORCIDProfile{Name: "Josiah Carberry", Emails: []string{"made-up@example.invalid"}}, nil
		},
		Team:   "codecheckers/codecheckers",
		InTeam: func(context.Context, string) bool { return false },
		Lists: lists(List{URL: "https://example.invalid/codecheckers.csv", Table: check.Table{Rows: []map[string]string{
			{"name": "Fake Codechecker", "handle": "@fake-codechecker", "ORCID": "0000-0000-0000-0002"},
		}}}),
	}
}

func lists(read ...List) func() []List { return func() []List { return read } }

func TestACompleteRegistrationIsComplete(t *testing.T) {
	report := Check(context.Background(), filed(t, validRow), world())
	if !report.Complete() {
		t.Fatalf("a complete registration is not: %+v", report.Findings)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings on a complete registration: %+v", report.Findings)
	}
	want := Row{Name: "Josiah Carberry", Handle: "@" + registrant, ORCID: carberry, Contact: SeeORCID,
		Fields: "psychoceramics, cracked pots", Languages: "R, Python", ECRUntil: "expired",
		ECRChecked: "2026-11;https://orcid.org/0000-0002-1825-0097", Fediverse: "@carberry@fediscience.org"}
	if report.Row != want {
		t.Errorf("row = %+v\nwant  %+v", report.Row, want)
	}
}

// What registrants write, and what the list writes instead: the runbook's
// table of normalisations.
func TestTheRowIsPutInTheListsConventions(t *testing.T) {
	row, findings := Parse(filed(t, ` Josiah Carberry , a-made-up-registrant, https://orcid.org/0000-0002-1825-0097, see ORCID page, "psychoceramics, , cracked pots, ","R,Python", NA, NA, https://fediscience.org/@carberry`))
	if row == (Row{}) || len(findings) != 0 {
		t.Fatalf("row %+v, findings %+v", row, findings)
	}
	for _, c := range []struct{ what, got, want string }{
		{"name", row.Name, "Josiah Carberry"},
		{"handle", row.Handle, "@" + registrant},
		{"ORCID", row.ORCID, carberry},
		{"fields", row.Fields, "psychoceramics, cracked pots"},
		{"languages", row.Languages, "R,Python"},
		{"fediverse", row.Fediverse, "@carberry@fediscience.org"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
		}
	}
	for _, written := range []string{"@" + registrant, "@@" + registrant, "https://github.com/" + registrant + "/"} {
		if got := handle(written); got != "@"+registrant {
			t.Errorf("handle(%q) = %q", written, got)
		}
	}
}

// Rows filed before the fediverse column existed are read with it empty.
func TestARowWithoutTheFediverseColumnIsRead(t *testing.T) {
	row, findings := Parse(filed(t, strings.TrimSuffix(validRow, ",@carberry@fediscience.org")))
	if row.Name == "" || len(findings) != 0 || row.Fediverse != "" {
		t.Errorf("row %+v, findings %+v", row, findings)
	}
}

// The shapes real registrations have arrived in, and what each is told.
func TestWhatIsWrongWithARowIsSaid(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
	}{
		{"the template left as it is", filed(t, "name,@GitHub-handle,ORCID ID (not url),contact,\"fields\",\"languages\",ecr_until,ecr_checked,fediverse"),
			"still holds the template's line"},
		{"no data at all", "I would like to register.", "no row of data"},
		{"five fields, as in codecheckers#77", filed(t, `Josiah Carberry,@a-made-up-registrant,0000-0002-1825-0097,"psychoceramics","R"`),
			"has 5 fields, and the list has 9"},
		{"two rows", filed(t, validRow+"\n"+validRow), "2 rows of data"},
	} {
		row, findings := Parse(c.body)
		if row != (Row{}) {
			t.Errorf("%s: parsed, want refused", c.name)
		}
		if len(findings) != 1 || !strings.Contains(findings[0].Text, c.want) || !findings[0].Blocks() {
			t.Errorf("%s: findings %+v, want one blocking that says %q", c.name, findings, c.want)
		}
	}

	// The template's line left above the row is not a second row.
	if row, findings := Parse(filed(t, "name,@GitHub-handle,ORCID ID (not url),contact\n"+validRow)); row.Name != "Josiah Carberry" || len(findings) != 0 {
		t.Errorf("the row under the template's line: %+v, %+v", row, findings)
	}

	// The example row in the template's own comment is not a registration.
	if _, findings := Parse(filed(t, "")); len(findings) != 1 || !strings.Contains(findings[0].Text, "no row of data") {
		t.Errorf("the template's example row was read as data: %+v", findings)
	}
}

func TestAValueInTheWrongFormIsNamed(t *testing.T) {
	for _, c := range []struct {
		name, from, to, want string
		blocks               bool
	}{
		{"an ORCID with a wrong check digit", carberry + ",", "0000-0002-1825-0098,", "is not an ORCID iD", true},
		{"a month that is not one", ",expired,", ",2029-13,", "`ecr_until`", true},
		{"a check that names no source", "2026-11;https://orcid.org/0000-0002-1825-0097", "2026-11", "`ecr_checked`", true},
		{"a fediverse account with no instance", "@carberry@fediscience.org", "@carberry", "`fediverse`", true},
		{"no languages", `"R, Python"`, `""`, "`languages` column is empty", false},
	} {
		row, findings := Parse(filed(t, strings.Replace(validRow, c.from, c.to, 1)))
		if row == (Row{}) {
			t.Errorf("%s: the row was not read", c.name)
			continue
		}
		if len(findings) != 1 || !strings.Contains(findings[0].Text, c.want) || findings[0].Blocks() != c.blocks {
			t.Errorf("%s: findings %+v, want one that says %q, blocking %v", c.name, findings, c.want, c.blocks)
		}
	}
}

// No email, no registration (codecheckers/chekhov#49): `see ORCID page` holds
// only when the record shows an address, and an ORCID that cannot be asked
// holds the registration up as well.
func TestTheContactMustReachSomebody(t *testing.T) {
	noEmail := world()
	noEmail.ORCID = func(context.Context, string) (check.ORCIDProfile, error) {
		return check.ORCIDProfile{Pages: []string{"https://example.invalid/carberry"}}, nil
	}
	down := world()
	down.ORCID = func(context.Context, string) (check.ORCIDProfile, error) {
		return check.ORCIDProfile{}, errors.New("ORCID answered 503")
	}
	unknown := world()
	unknown.ORCID = func(context.Context, string) (check.ORCIDProfile, error) {
		return check.ORCIDProfile{}, &check.StatusError{Status: 404}
	}

	for _, c := range []struct {
		name  string
		row   string
		world World
		want  []string
	}{
		{"no public email on ORCID", validRow, noEmail, []string{"didn't find an email", "links a page"}},
		{"ORCID not answering", validRow, down, []string{"could not ask ORCID", "503"}},
		{"an ORCID nobody has", validRow, unknown, []string{"ORCID has no record"}},
		{"no contact at all", strings.Replace(validRow, "see ORCID page", "", 1), world(), []string{"`contact` column says nothing"}},
		{"NA as the contact", strings.Replace(validRow, "see ORCID page", "NA", 1), world(), []string{"`contact` column says nothing"}},
	} {
		report := Check(context.Background(), filed(t, c.row), c.world)
		if report.Complete() {
			t.Errorf("%s: the registration is complete", c.name)
		}
		said := texts(report.Findings)
		for _, want := range c.want {
			if !strings.Contains(said, want) {
				t.Errorf("%s: %q, want it to say %q", c.name, said, want)
			}
		}
		if strings.Contains(said, "example.invalid") {
			t.Errorf("%s: a contact or a page was quoted: %q", c.name, said)
		}
	}

	// An address or a page in the contact is the registrant's choice, and
	// ORCID is not asked about it.
	for _, contact := range []string{"made-up at example dot invalid", "https://example.invalid/contact"} {
		report := Check(context.Background(), filed(t, strings.Replace(validRow, "see ORCID page", contact, 1)), noEmail)
		if !report.Complete() {
			t.Errorf("contact %q: %+v", contact, report.Findings)
		}
		if report.Row.Contact != contact {
			t.Errorf("contact %q was rewritten as %q", contact, report.Row.Contact)
		}
	}
}

func TestTheRowIsTheAuthorsOwn(t *testing.T) {
	someoneElse := world()
	someoneElse.Author = "a-made-up-other-person"
	someoneElse.InTeam = func(context.Context, string) bool {
		t.Error("the team was asked about a handle somebody else typed")
		return true
	}
	someoneElse.Lists = func() []List {
		t.Error("the lists were searched for a handle somebody else typed")
		return nil
	}
	report := Check(context.Background(), filed(t, validRow), someoneElse)
	if report.Complete() || !strings.Contains(texts(report.Findings), "opened by `@a-made-up-other-person`") {
		t.Errorf("a row naming somebody else: %+v", report.Findings)
	}

	// The same person, spelt as GitHub spells them.
	differentCase := world()
	differentCase.Author = "A-Made-Up-Registrant"
	report = Check(context.Background(), filed(t, validRow), differentCase)
	if !report.Complete() || report.Row.Handle != "@A-Made-Up-Registrant" {
		t.Errorf("handle %q, findings %+v", report.Row.Handle, report.Findings)
	}
}

func TestSomebodyListedAlreadyIsNotListedTwice(t *testing.T) {
	onTheList := world()
	onTheList.Lists = lists(List{URL: "https://example.invalid/codecheckers.csv", Table: check.Table{Rows: []map[string]string{
		{"name": "J. Carberry", "handle": "@someone-renamed", "ORCID": carberry},
	}}})
	report := Check(context.Background(), filed(t, validRow), onTheList)
	if report.Complete() || !strings.Contains(texts(report.Findings), "on `codecheckers.csv` already") {
		t.Errorf("listed by ORCID: %+v", report.Findings)
	}
	// Under another handle, so not this registration listed: `include` is
	// held up until an editor has looked at that row.
	if report.Listed() || report.Includable() {
		t.Errorf("the same ORCID under another handle counts as listed: %+v", report.Findings)
	}

	// On another list is not a reason to refuse, and is said.
	elsewhere := world()
	elsewhere.InTeam = func(context.Context, string) bool { return true }
	elsewhere.Lists = lists(List{URL: "https://example.invalid/agile-codecheckers.csv",
		Table: check.Table{Rows: []map[string]string{{"name": "Josiah Carberry", "handle": "@" + registrant}}}})
	report = Check(context.Background(), filed(t, validRow), elsewhere)
	said := texts(report.Findings)
	if !report.Complete() || !report.InTeam || !strings.Contains(said, "`agile-codecheckers.csv`") || !strings.Contains(said, "in `codecheckers/codecheckers` already") {
		t.Errorf("on the AGILE list and in the team: %+v", report.Findings)
	}

	unread := world()
	unread.Lists = lists(List{URL: "https://example.invalid/codecheckers.csv",
		Table: check.Table{Err: errors.New("raw.githubusercontent.com answered 500")}})
	if report := Check(context.Background(), filed(t, validRow), unread); report.Complete() {
		t.Error("a list that could not be read let the registration through")
	}
}

func texts(findings []Finding) string {
	var all []string
	for _, finding := range findings {
		all = append(all, string(finding.Outcome)+" "+finding.Text)
	}
	return strings.Join(all, "\n")
}

// The lists are three downloads, and an issue without a row needs none of
// them.
func TestTheListsAreReadOnlyForARow(t *testing.T) {
	asked := world()
	asked.Lists = func() []List {
		t.Error("the lists were read for an issue that holds no row")
		return nil
	}
	if report := Check(context.Background(), "I would like to register.", asked); report.Complete() {
		t.Error("an issue without a row is complete")
	}
}

// A list that drifted holds the same ORCID twice: the registrant's own row,
// by handle, is the one that says they are listed, wherever it is.
func TestTheRegistrantsOwnRowWinsOverAnOlderOneWithTheirORCID(t *testing.T) {
	drifted := world()
	drifted.Lists = lists(List{URL: "https://example.invalid/codecheckers.csv", Table: check.Table{Rows: []map[string]string{
		{"name": "J. Carberry", "handle": "@someone-renamed", "ORCID": carberry},
		{"name": "Josiah Carberry", "handle": "@" + registrant, "ORCID": carberry},
	}}})
	report := Check(context.Background(), filed(t, validRow), drifted)
	if !report.Listed() || !report.Includable() {
		t.Errorf("the registrant's own row was not found: %+v", report.Findings)
	}
}
