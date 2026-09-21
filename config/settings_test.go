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
	if err := validate("settings-test.yml", settings); err != nil {
		t.Errorf("two different teams should be accepted: %v", err)
	}
}
