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
