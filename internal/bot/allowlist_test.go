package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// accounted is a reply path that knows which handles are people's accounts.
type accounted struct{ *heard }

func (a accounted) PersonAccount(_ context.Context, handle string) (string, error) {
	if handle == "a-made-up-nobody" {
		return "", github.ErrNotAUser
	}
	return handle, nil
}

// An editor lets somebody ask on one check, and takes it back again.
func TestAnEditorAllowsSomebodyAndTakesItBack(t *testing.T) {
	server, path := gated(t)
	server.Replies = accounted{path}
	hello := asking{by: coauthor}
	do := func(by, body string) string {
		asking{by: by, body: "@chekhovbot " + body}.send(t, server)
		return path.last()
	}

	if answeredHello(t, server, path, hello) {
		t.Fatal("answered before being allowed")
	}
	says(t, do("nuest", "allowlist"), "Nobody has been added")
	says(t, do("nuest", "allowlist add @A-Made-Up-Coauthor"), "✔ `@a-made-up-coauthor` may now ask me",
		"no role", "allowed by `@nuest`")
	if command.Mentions(path.last(), coauthor) {
		t.Errorf("the reply notifies them:\n%s", path.last())
	}
	if !answeredHello(t, server, path, hello) {
		t.Fatalf("not answered once allowed: %s", path.last())
	}
	if answeredHello(t, server, path, asking{by: coauthor, issue: 2}) {
		t.Error("allowed on one check, answered on another")
	}
	says(t, do(coauthor, "allowlist"), "On the allowlist of this check: `@a-made-up-coauthor`")
	says(t, do(coauthor, "roles"), "**Also may ask me here**: `@a-made-up-coauthor`")
	says(t, do(coauthor, "allowlist add "+stranger), "is for editors")
	says(t, do("nuest", "allowlist add "+coauthor), "on the allowlist of this check already")
	says(t, do("nuest", "allowlist remove "+coauthor), "✔ `@a-made-up-coauthor` is off the allowlist",
		"disallowed by `@nuest`")
	says(t, do("nuest", "allowlist remove "+coauthor), "was not on the allowlist")
	// Refused again, and silently: they were told on this issue already.
	before := path.count()
	do(coauthor, "commands")
	if path.count() != before {
		t.Errorf("answered after being taken off: %s", path.last())
	}
}

// Nothing is added that was not asked for in full, for an account there is.
func TestWhatAllowlistRefuses(t *testing.T) {
	server, path := gated(t)
	server.Replies = accounted{path}

	for body, want := range map[string]string{
		"allowlist @" + coauthor:         "takes nothing after it",
		"allowlist add":                  "one person at a time",
		"allowlist add one two":          "one person at a time",
		"allowlist add not_a_handle":     "is not a GitHub handle",
		"allowlist add a-made-up-nobody": "I could not add them to the allowlist",
		"allowlist remove":               "one person at a time",
	} {
		asking{by: "nuest", body: "@chekhovbot " + body}.send(t, server)
		if !strings.Contains(path.last(), want) {
			t.Errorf("%s: the reply does not say %q:\n%s", body, want, path.last())
		}
	}
	reading, err := server.Checks.Read(context.Background(), server.Settings.TargetRepository(), 1)
	if err != nil || len(reading.Record.Allowlist) != 0 {
		t.Errorf("something was added: %v, %v", reading.Record.Allowlist, err)
	}
}

// Letting somebody ask is not their taking part, so it does not let the bot
// notify them afterwards.
func TestAnAllowlistMentionIsNotTakingPart(t *testing.T) {
	for _, body := range []string{"@chekhovbot allowlist add @" + coauthor,
		"@chekhovbot allowlist remove @" + coauthor, "@chekhovbot allowlist @" + coauthor} {
		if mentionedOutsideAsking(body, coauthor) {
			t.Errorf("%q counted as taking part", body)
		}
	}
}
