package config

import (
	"strings"
	"testing"
)

// The shipped settings must name the testing register. Development and testing
// run against it and never against the production register, and this is the
// one file that decides which.
func TestShippedSettingsNameTheTestingRegister(t *testing.T) {
	t.Setenv("CHEKHOV_TARGET_REPO", "")

	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.TargetRepository(); got != "codecheckers/testing-dev-register" {
		t.Errorf("target repository = %q, want the testing register", got)
	}
	if strings.Contains(settings.TargetRepository(), "codecheckers/register") {
		t.Error("the development settings point at the production register")
	}
}

func TestEnvironmentOverridesTheDefault(t *testing.T) {
	t.Setenv("CHEKHOV_TARGET_REPO", "codecheckers/another-test-register")
	t.Setenv("CHEKHOV_BOT_GH_USER", "someonebot")

	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.TargetRepository(); got != "codecheckers/another-test-register" {
		t.Errorf("target repository = %q, want the one from the environment", got)
	}
	if got := settings.BotUser(); got != "someonebot" {
		t.Errorf("bot user = %q, want the one from the environment", got)
	}
}

// Running against production begins with writing a settings file, not with
// setting a variable.
func TestUnknownEnvironmentSaysSo(t *testing.T) {
	if _, err := Load("production"); err == nil {
		t.Fatal("production settings do not exist, so loading them must fail")
	}
}

func TestSettingsNameTeamsRatherThanPeople(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	if settings.TeamOrganisation() == "" {
		t.Error("no organisation to read the teams from")
	}
	if settings.EditorsTeam() == "" {
		t.Error("no editors team, so no one could run an editor command")
	}
	// Membership belongs to the organisation. A list of handles here is a
	// second copy that goes stale the day somebody joins.
	for _, team := range settings.Teams() {
		if strings.HasPrefix(team, "@") || strings.Contains(team, " ") {
			t.Errorf("%q looks like a person rather than a team", team)
		}
	}
}

// A development toot is a direct message, so that it cannot reach the public
// or - with its mentions defused - notify anyone.
func TestDevelopmentAnnouncesDirectOnly(t *testing.T) {
	t.Setenv("CHEKHOV_TARGET_REPO", "")

	settings := mustLoad(t)
	if visibility := settings.Mastodon().Visibility; visibility != "direct" {
		t.Errorf("visibility = %q, want direct", visibility)
	}
	if settings.Mastodon().Follow {
		t.Error("development follows accounts and curates collections, which is a public act on a real account")
	}
	for _, source := range append([]string{settings.Chekhov.Env.Certificates}, settings.CodecheckerLists()...) {
		if !strings.Contains(source, "testing-dev-register") {
			t.Errorf("development reads %s, which is not the testing register", source)
		}
	}
	if len(settings.CodecheckerLists()) == 0 {
		t.Error("no codechecker lists")
	}
	if got := settings.CertificateURL("1970-001"); got != "https://codecheck.org.uk/testing-dev-register/1970-001/" {
		t.Errorf("certificate URL = %q", got)
	}
}

// live-test exists only to flip mastodon.follow on for a deliberate, brief
// run against real fediverse test data; everything else about it must match
// development, especially which register it reads.
func TestLiveTestSettingsMatchDevelopmentExceptFollow(t *testing.T) {
	t.Setenv("CHEKHOV_TARGET_REPO", "")

	settings, err := Load("live-test")
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.TargetRepository(); got != "codecheckers/testing-dev-register" {
		t.Errorf("target repository = %q, want the testing register", got)
	}
	if !settings.Mastodon().Follow {
		t.Error("live-test settings do not enable follow, so they serve no purpose over development")
	}
	if visibility := settings.Mastodon().Visibility; visibility != "direct" {
		t.Errorf("visibility = %q, want direct - live-test is not where announce's own behaviour changes", visibility)
	}
}

// A panic is reported on an issue of the target repository, and nowhere when
// none is named - which is the default, so a deployment opts in.
func TestTheAdminIssueComesFromTheEnvironment(t *testing.T) {
	if got := mustLoad(t).AdminIssue(); got != 0 {
		t.Errorf("admin issue = %d by default, want 0", got)
	}
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "42")
	if got := mustLoad(t).AdminIssue(); got != 42 {
		t.Errorf("admin issue = %d, want the one from the environment", got)
	}
	// A typo must not quietly turn the reports off.
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "#42")
	if _, err := Load("development"); err == nil {
		t.Error("an admin issue that is not a number was accepted")
	}
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "-1")
	if _, err := Load("development"); err == nil || !strings.Contains(err.Error(), "not an issue number") {
		t.Errorf("error %v, want a negative admin issue refused", err)
	}
}

func mustLoad(t *testing.T) *Settings {
	t.Helper()
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestTheMastodonAccountComesFromTheEnvironment(t *testing.T) {
	t.Setenv("CHEKHOV_MASTODON_ACCOUNT", "codecheck_test")
	t.Setenv("CHEKHOV_MASTODON_INSTANCE", "https://example.social")

	mastodon := mustLoad(t).Mastodon()
	if mastodon.Account != "codecheck_test" || mastodon.Instance != "https://example.social" {
		t.Errorf("account %q on %q, want the ones from the environment", mastodon.Account, mastodon.Instance)
	}
}

// The bot may add people to the codecheckers team and no other, and the guard
// that keeps `invite` from handing out the bot's own permissions compares team
// names. Naming the same team twice would leave that guard holding while
// meaning nothing.
func TestTheEditorsAndCodecheckersTeamsMayNotBeTheSame(t *testing.T) {
	settings := &Settings{}
	settings.Chekhov.Env.TargetRepository = "codecheckers/testing-dev-register"
	settings.Chekhov.Mastodon.Visibility = "direct"
	settings.Chekhov.Teams.Organisation = "codecheckers"
	settings.Chekhov.Teams.Editors = "editors"
	// The same team, written as somebody would write it by accident.
	settings.Chekhov.Teams.Codecheckers = "Editors"

	err := validate("settings-test.yml", settings)
	if err == nil {
		t.Fatal("inviting a codechecker into the editors team was allowed")
	}
	if !strings.Contains(err.Error(), "they must differ") {
		t.Errorf("error %q, want it to say why", err)
	}

	settings.Chekhov.Teams.Codecheckers = "codecheckers"
	settings.Chekhov.Teams.Managed = []string{"codecheckers"}
	if err := validate("settings-test.yml", settings); err != nil {
		t.Errorf("two different teams should be accepted: %v", err)
	}
}

// The bot may add people to the managed teams, so the editors team must not be
// one of them: every editor-only command rests on a membership the bot cannot
// grant itself.
func TestTheEditorsTeamMayNotBeManaged(t *testing.T) {
	settings := &Settings{}
	settings.Chekhov.Env.TargetRepository = "codecheckers/testing-dev-register"
	settings.Chekhov.Mastodon.Visibility = "direct"
	settings.Chekhov.Teams.Organisation = "codecheckers"
	settings.Chekhov.Teams.Editors = "editors"
	settings.Chekhov.Teams.Codecheckers = "codecheckers"
	settings.Chekhov.Teams.Managed = []string{"codecheckers", " Editors "}

	err := validate("settings-test.yml", settings)
	if err == nil {
		t.Fatal("the editors team was allowed into the managed list")
	}
	if !strings.Contains(err.Error(), "editor commands are gated on") {
		t.Errorf("error %q, want it to say why", err)
	}

	settings.Chekhov.Teams.Managed = []string{"codecheckers", "institutional-codecheckers"}
	if err := validate("settings-test.yml", settings); err != nil {
		t.Errorf("a list without the editors team should be accepted: %v", err)
	}
}

// The shipped settings name the two teams a codechecker can belong to, and not
// the editors team.
func TestShippedSettingsManageTheCodecheckerTeams(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	managed := settings.ManagedTeams()
	if len(managed) != 2 || managed[0] != "codecheckers" || managed[1] != "institutional-codecheckers" {
		t.Errorf("managed teams = %v", managed)
	}
	for _, team := range managed {
		if strings.EqualFold(team, settings.EditorsTeam()) {
			t.Errorf("the editors team is managed: %q", team)
		}
	}
}

// A team the bot is told to put somebody in has to be one it may add to.
// Without this a settings file with no teams.managed passes and the team half
// of `assign` silently does nothing.
func TestANamedTeamMustBeManaged(t *testing.T) {
	settings := &Settings{}
	settings.Chekhov.Env.TargetRepository = "codecheckers/testing-dev-register"
	settings.Chekhov.Mastodon.Visibility = "direct"
	settings.Chekhov.Teams.Organisation = "codecheckers"
	settings.Chekhov.Teams.Editors = "editors"
	settings.Chekhov.Teams.Codecheckers = "codecheckers"
	settings.Chekhov.Teams.Institutional = "institutional-codecheckers"
	settings.Chekhov.Labels.Institution = "institution"

	// Nothing managed at all: the commonest way to get this wrong.
	err := validate("settings-test.yml", settings)
	if err == nil || !strings.Contains(err.Error(), "not in teams.managed") {
		t.Fatalf("error %v, want it to refuse a team the bot cannot add to", err)
	}

	// The institutional team left out of the list.
	settings.Chekhov.Teams.Managed = []string{"codecheckers"}
	err = validate("settings-test.yml", settings)
	if err == nil || !strings.Contains(err.Error(), "teams.institutional") {
		t.Fatalf("error %v, want it to name the team that is missing", err)
	}

	settings.Chekhov.Teams.Managed = []string{"codecheckers", "institutional-codecheckers"}
	if err := validate("settings-test.yml", settings); err != nil {
		t.Errorf("both named teams are managed: %v", err)
	}
}

// The shipped settings name the institutional team, and it is managed.
func TestShippedSettingsNameTheInstitutionalTeam(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.InstitutionalTeam(); got != "institutional-codecheckers" {
		t.Errorf("institutional team = %q", got)
	}
	if !settings.Managed(settings.InstitutionalTeam()) {
		t.Error("the institutional team is not in teams.managed")
	}
}

// The label names belong to the register, so the shipped file names them: a
// register that renames `institution` needs a settings change, not a build.
func TestShippedSettingsNameTheRegistersLabels(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.InstitutionLabel(); got != "institution" {
		t.Errorf("institution label = %q", got)
	}
	if got := settings.NeedsCodecheckerLabel(); got != "needs codechecker" {
		t.Errorf("needs codechecker label = %q", got)
	}
	if got := settings.IDAssignedLabel(); got != "id assigned" {
		t.Errorf("id assigned label = %q", got)
	}
}

// The labels the bot may change are few and named: `set certificate` adds
// `id assigned`, and nothing else is added or removed. Both shipped files say
// so, because live-test is development with one switch flipped.
func TestShippedSettingsLetTheBotAddOnlyIDAssigned(t *testing.T) {
	for _, environment := range []string{"development", "live-test"} {
		settings, err := Load(environment)
		if err != nil {
			t.Fatal(err)
		}
		if got := settings.AddableLabels(); len(got) != 1 || got[0] != "id assigned" {
			t.Errorf("%s: addable labels = %q", environment, got)
		}
		if got := settings.RemovableLabels(); len(got) != 0 {
			t.Errorf("%s: removable labels = %q", environment, got)
		}
	}
}

// A label the bot counts reservations by, but may not add, would leave every
// reservation unlabelled and invisible to the next one. The file is refused
// rather than the command failing quietly on every check.
func TestTheIDAssignedLabelMustBeOneTheBotMayAdd(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	settings.Chekhov.Labels.Managed.Add = []string{"id-assigned"}
	if err := validate("settings-test.yml", settings); err == nil ||
		!strings.Contains(err.Error(), "labels.managed.add") {
		t.Errorf("validate = %v, want a refusal naming labels.managed.add", err)
	}

	// Written differently is the same label, as GitHub has it.
	settings.Chekhov.Labels.Managed.Add = []string{"ID Assigned"}
	if err := validate("settings-test.yml", settings); err != nil {
		t.Errorf("validate = %v, want the label accepted", err)
	}
}

// Which team an institutional check leads to is decided by reading the label
// that marks one, so naming the team without the label would route every
// institutional codechecker to the ordinary team in silence.
func TestTheInstitutionalTeamNeedsTheLabelThatFindsIt(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	settings.Chekhov.Labels.Institution = ""
	if err := validate("settings-test.yml", settings); err == nil ||
		!strings.Contains(err.Error(), "labels.institution") {
		t.Errorf("validate = %v, want a refusal naming labels.institution", err)
	}
}
