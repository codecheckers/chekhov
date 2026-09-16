// Package config reads the bot's settings.
//
// The settings file is the single place that names the target repository. The
// rule behind that is not tidiness: a deployment pointed at
// codecheckers/register by accident would comment on real checks, so there is
// one name to look at, and a test that asserts what the shipped file says.
//
// The schema follows buffy's, so that the responder names and the target
// repository stay recognisable to anyone who knows the Open Journals bots.
// buffy interpolates with ERB; this uses ${VAR:-default}, which is the same
// idea in a form Go can read without running Ruby.
package config

import (
	"embed"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed *.yml
var files embed.FS

// Settings are the bot's configuration, as the file is shaped.
type Settings struct {
	Chekhov struct {
		Env struct {
			BotGitHubUser    string `yaml:"bot_github_user"`
			TargetRepository string `yaml:"target_repository"`
			RegisterFile     string `yaml:"register_file"`
		} `yaml:"env"`
		Teams struct {
			// Editors may run the editor-only commands. Handles rather than a
			// team identifier: reading team membership needs an
			// organisation-wide permission on the bot's token, and the token
			// is deliberately scoped to one repository. See
			// docs/github-token.md.
			Editors []string `yaml:"editors"`
		} `yaml:"teams"`
	} `yaml:"chekhov"`
}

// Environment names which settings file to read, from CHEKHOV_ENV.
func Environment() string {
	if env := strings.TrimSpace(os.Getenv("CHEKHOV_ENV")); env != "" {
		return env
	}
	return "development"
}

// Load reads the settings for an environment, expanding ${VAR:-default} from
// the process environment.
//
// Only the development settings ship with the binary. Running against the
// production register is meant to be a deliberate act, which begins with
// writing config/settings-production.yml, not with setting a variable.
func Load(environment string) (*Settings, error) {
	name := "settings-" + environment + ".yml"
	raw, err := files.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("no settings for environment %q: %s is not part of this build", environment, name)
	}

	var settings Settings
	if err := yaml.Unmarshal([]byte(expand(string(raw))), &settings); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", name, err)
	}
	if settings.Chekhov.Env.TargetRepository == "" {
		return nil, fmt.Errorf("%s names no target repository", name)
	}
	return &settings, nil
}

// Current loads the settings for the current environment.
func Current() (*Settings, error) { return Load(Environment()) }

// TargetRepository is the register the bot works on, as owner/repo.
func (s *Settings) TargetRepository() string { return s.Chekhov.Env.TargetRepository }

// BotUser is the account the bot posts as. A comment by this account is never
// acted on, so that a reply cannot trigger a reply.
func (s *Settings) BotUser() string { return s.Chekhov.Env.BotGitHubUser }

// Editors are the handles allowed to run the editor-only commands.
func (s *Settings) Editors() []string { return s.Chekhov.Teams.Editors }

// IsEditor reports whether a GitHub handle is an editor. GitHub handles are
// case-insensitive, and people write them as they please.
func (s *Settings) IsEditor(login string) bool {
	for _, editor := range s.Chekhov.Teams.Editors {
		if strings.EqualFold(strings.TrimSpace(editor), strings.TrimSpace(login)) {
			return true
		}
	}
	return false
}

// expand replaces ${VAR} and ${VAR:-default} with what the environment says.
//
// An unset variable with no default becomes empty, as a shell would have it,
// and the caller reports the field that is missing rather than a puzzle about
// where the value went.
func expand(text string) string {
	return os.Expand(text, func(reference string) string {
		name, fallback, hasFallback := strings.Cut(reference, ":-")
		if value := os.Getenv(name); value != "" {
			return value
		}
		if hasFallback {
			return fallback
		}
		return ""
	})
}
