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
	"strconv"
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
			// Certificates is the URL of a published certificate, with {id}
			// for its identifier.
			Certificates string `yaml:"certificates"`
			// CodecheckerLists are the URLs of the codechecker lists.
			CodecheckerLists []string `yaml:"codechecker_lists"`
			// AdminIssue is where a command's panic is reported, 0 for nowhere.
			AdminIssue IssueNumber `yaml:"admin_issue"`
		} `yaml:"env"`
		Metadata Metadata `yaml:"metadata"`
		Mastodon Mastodon `yaml:"mastodon"`
		Teams    struct {
			// Organisation the teams belong to.
			Organisation string `yaml:"organisation"`
			// Editors may run the editor-only commands, Codecheckers are the
			// people who perform checks. These are GitHub team slugs, not
			// handles: membership is maintained by the organisation, in one
			// place, and the bot reads it with the token's Members: read
			// permission. See docs/github-token.md.
			Editors      string `yaml:"editors"`
			Codecheckers string `yaml:"codecheckers"`
			// Owners replaces the organisation's owners in the reply that
			// asks them to invite somebody. Empty in production, where the
			// organisation is asked; set in development and for a live test,
			// because that reply is the one place the bot writes a plain
			// @mention, and a test must not notify people who did not ask to
			// be in it. See CLAUDE.md -> Committing and publishing.
			Owners []string `yaml:"owners"`
			// Institutional is where a codechecker on a check an
			// institution's arrangement covers belongs. Named rather than
			// found among Managed by the shape of its name: which team is
			// which is not something to infer from a substring.
			Institutional string `yaml:"institutional"`
			// Managed are the teams the bot may add somebody to. An
			// allow-list rather than a single name, because which team a
			// codechecker belongs in follows from the check - an
			// institutional one belongs in the institutional team - and a
			// list that cannot contain the editors team is the property that
			// matters, not a count of one. validate refuses a list that
			// does contain it: adding to `editors` would let the bot hand out
			// the permission its own editor commands are gated on.
			Managed []string `yaml:"managed"`
		} `yaml:"teams"`
	} `yaml:"chekhov"`
}

// IssueNumber is an issue of the target repository, 0 for none.
//
// Written quoted in the settings file and parsed here, because a bare value is
// expanded before the YAML is read: a mistyped "#42" would become a comment,
// and switch off what it was meant to switch on.
type IssueNumber int

// UnmarshalYAML refuses anything but a number that is not negative.
func (n *IssueNumber) UnmarshalYAML(node *yaml.Node) error {
	issue, err := strconv.Atoi(node.Value)
	if err != nil || issue < 0 {
		return fmt.Errorf("line %d: %q is not an issue number; 0 means none", node.Line, node.Value)
	}
	*n = IssueNumber(issue)
	return nil
}

// Metadata is where the bot asks what a paper is: its title, its authors and
// what it is about.
//
// One source at a time, named here rather than decided in the code, because
// which one answers changes what four validation rules compare against.
type Metadata struct {
	// Source is "openalex" or "crossref". OpenAlex is the default: of ten
	// article DOIs taken from the register it answered for nine against
	// Crossref's seven, carried an abstract for nine against four, and knew
	// twenty of thirty-two author ORCIDs against seven of twenty-seven -
	// and Crossref's `subject` field, which said what a paper was about, is
	// empty on every record it returns now.
	Source string `yaml:"source"`
	// Mailto is the address OpenAlex asks for, which puts the bot in its
	// polite pool. A shared project address, never a person's.
	Mailto string `yaml:"mailto"`
}

// MetadataSource is where the bot asks what a paper is.
func (s *Settings) MetadataSource() string { return s.Chekhov.Metadata.Source }

// MetadataMailto is the address sent to OpenAlex for its polite pool.
func (s *Settings) MetadataMailto() string { return s.Chekhov.Metadata.Mailto }

// Mastodon is where the announcements go and what they are composed from.
type Mastodon struct {
	// Instance is the server, https://fediscience.org.
	Instance string `yaml:"instance"`
	// Account is the account the bot posts as, without @ or instance. The
	// token has to belong to it, which the client checks.
	Account string `yaml:"account"`
	// Visibility is the only one the bot posts with: public, unlisted,
	// private or direct.
	Visibility string `yaml:"visibility"`
	// Follow says whether this deployment may follow accounts and curate the
	// Codecheckers/Authors/Venues collections at all. Following is a public,
	// hard to reverse act on a real account - unlike posting, it is not
	// softened by direct visibility - so it defaults to off rather than
	// inheriting "switched on unless configured otherwise".
	Follow bool `yaml:"follow"`
}

// CertificateURL is the published page of one certificate, ending in a slash.
func (s *Settings) CertificateURL(id string) string {
	page := strings.ReplaceAll(s.Chekhov.Env.Certificates, "{id}", id)
	if !strings.HasSuffix(page, "/") {
		page += "/"
	}
	return page
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
	if err := validate(name, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

// validate is what a settings file has to say before the bot will run on it.
//
// Its own function so that each refusal can be tested: the file itself is
// embedded, so a test cannot write one, and these are the rules worth having
// a test for rather than a comment.
func validate(name string, s *Settings) error {
	if s.Chekhov.Env.TargetRepository == "" {
		return fmt.Errorf("%s names no target repository", name)
	}
	switch s.Chekhov.Metadata.Source {
	case "openalex", "crossref":
	case "":
		s.Chekhov.Metadata.Source = "openalex"
	default:
		return fmt.Errorf("%s: metadata source %q is neither openalex nor crossref",
			name, s.Chekhov.Metadata.Source)
	}
	// The editors team is what grants the editor-only commands, and the
	// codecheckers team is a standing role anybody on a check may hold, so
	// naming the same team twice would make every codechecker an editor.
	editors, codecheckers := s.EditorsTeam(), s.CodecheckersTeam()
	if codecheckers != "" && strings.EqualFold(editors, codecheckers) {
		return fmt.Errorf("%s: the editors and codecheckers teams are both %q; "+
			"they must differ, or every codechecker would be an editor", name, codecheckers)
	}
	// The bot may add people to the managed teams, so the editors team must
	// not be one of them: every editor-only command rests on a membership the
	// bot must not be able to grant itself.
	if editors != "" && s.Managed(editors) {
		return fmt.Errorf("%s: the editors team %q is in teams.managed; "+
			"the bot must not be able to add anybody to the team its own "+
			"editor commands are gated on", name, editors)
	}
	// A team the bot is told to put somebody in must be one it is allowed to
	// add to. Without this a settings file with no teams.managed passes, and
	// then the team half of `assign` silently does nothing at all.
	// A slice rather than a map, so that a file with both wrong is refused
	// with the same message every time.
	for _, named := range []struct{ what, team string }{
		{"teams.codecheckers", codecheckers},
		{"teams.institutional", s.InstitutionalTeam()},
	} {
		if named.team != "" && !s.Managed(named.team) {
			return fmt.Errorf("%s: %s is %q, which is not in teams.managed; "+
				"the bot could not put anybody in it", name, named.what, named.team)
		}
	}
	switch s.Chekhov.Mastodon.Visibility {
	case "public", "unlisted", "private", "direct":
	default:
		return fmt.Errorf("%s: mastodon visibility %q is none of public, unlisted, private, direct",
			name, s.Chekhov.Mastodon.Visibility)
	}
	return nil
}

// Current loads the settings for the current environment.
func Current() (*Settings, error) { return Load(Environment()) }

// TargetRepository is the register the bot works on, as owner/repo.
func (s *Settings) TargetRepository() string { return s.Chekhov.Env.TargetRepository }

// BotUser is the account the bot posts as. A comment by this account is never
// acted on, so that a reply cannot trigger a reply.
func (s *Settings) BotUser() string { return s.Chekhov.Env.BotGitHubUser }

// AdminIssue is the issue of the target repository a panic is reported on,
// 0 when there is none.
func (s *Settings) AdminIssue() int { return int(s.Chekhov.Env.AdminIssue) }

// CodecheckerLists are the URLs of the codechecker lists.
func (s *Settings) CodecheckerLists() []string { return s.Chekhov.Env.CodecheckerLists }

// Mastodon is where announcements are posted.
func (s *Settings) Mastodon() Mastodon { return s.Chekhov.Mastodon }

// TeamOrganisation is the organisation the standing roles are read from.
//
// Trimmed, as the team names are: these go into request paths and into the
// guard that keeps `invite` out of the editors team, and a stray space would
// make that comparison pass while the two names mean the same team.
func (s *Settings) TeamOrganisation() string { return strings.TrimSpace(s.Chekhov.Teams.Organisation) }

// EditorsTeam is the team whose members may run the editor-only commands.
func (s *Settings) EditorsTeam() string { return strings.TrimSpace(s.Chekhov.Teams.Editors) }

// CodecheckersTeam is the team of people who perform CODECHECKs, which grants
// the codechecker role.
func (s *Settings) CodecheckersTeam() string { return strings.TrimSpace(s.Chekhov.Teams.Codecheckers) }

// ManagedTeams are the teams the bot may add somebody to, trimmed and in the
// order the settings name them. Never the editors team, which validate
// refuses.
func (s *Settings) ManagedTeams() []string {
	managed := make([]string, 0, len(s.Chekhov.Teams.Managed))
	for _, team := range s.Chekhov.Teams.Managed {
		if team = strings.TrimSpace(team); team != "" {
			managed = append(managed, team)
		}
	}
	return managed
}

// InstitutionalTeam is where a codechecker on an institutional check belongs,
// empty when the settings name none - and then an institutional check is
// treated as an ordinary one.
func (s *Settings) InstitutionalTeam() string {
	return strings.TrimSpace(s.Chekhov.Teams.Institutional)
}

// OwnersOverride are the handles to ask instead of the organisation's owners,
// empty when the organisation itself should be asked.
//
// A leading @ is tolerated, because somebody writing a settings file will
// write one half the time.
func (s *Settings) OwnersOverride() []string {
	configured := make([]string, 0, len(s.Chekhov.Teams.Owners))
	for _, owner := range s.Chekhov.Teams.Owners {
		if owner = strings.TrimPrefix(strings.TrimSpace(owner), "@"); owner != "" {
			configured = append(configured, owner)
		}
	}
	return configured
}

// Managed reports whether a team is one the bot may add somebody to.
func (s *Settings) Managed(team string) bool {
	_, ok := s.ManagedTeam(team)
	return ok
}

// ManagedTeam is the team as the settings spell it, for a name somebody typed.
//
// The spelling matters: the guard in internal/github compares exactly, as a
// guard at a request boundary should, so a team matched here case-insensitively
// has to be handed on in the settings' own spelling or it would be refused
// there instead - the role recorded and the team not changed.
func (s *Settings) ManagedTeam(team string) (string, bool) {
	team = strings.TrimSpace(team)
	for _, allowed := range s.ManagedTeams() {
		if strings.EqualFold(team, allowed) {
			return allowed, true
		}
	}
	return "", false
}

// Teams are the teams the bot reads, in the order a listing should show them.
func (s *Settings) Teams() []string {
	var teams []string
	for _, team := range []string{s.EditorsTeam(), s.CodecheckersTeam()} {
		if team != "" {
			teams = append(teams, team)
		}
	}
	return teams
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
