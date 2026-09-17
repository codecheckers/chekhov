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

func TestEditorsAreMatchedCaseInsensitively(t *testing.T) {
	settings, err := Load("development")
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Editors()) == 0 {
		t.Fatal("the settings list no editors, so no one could run an editor command")
	}
	editor := settings.Editors()[0]
	if !settings.IsEditor(strings.ToUpper(editor)) {
		t.Errorf("%q is an editor, so %q is too", editor, strings.ToUpper(editor))
	}
	if settings.IsEditor("someone-else") {
		t.Error("an unlisted handle is not an editor")
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
		t.Error("development follows accounts and manages lists, which is a public act on a real account")
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
