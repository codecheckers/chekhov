package command

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name      string
		comment   string
		addressed bool
		want      Name
	}{
		{
			name:      "the punctuation of the sentence it was written in",
			comment:   "@chekhovbot version.",
			addressed: true,
			want:      Version,
		},
		{
			name:      "the command on the first line",
			comment:   "@chekhovbot check codecheck.yml",
			addressed: true,
			want:      Check,
		},
		{
			name:      "free text may follow on later lines",
			comment:   "@chekhovbot check codecheck.yml\n\nthe file is in the root of the bundle",
			addressed: true,
			want:      Check,
		},
		{
			name:      "a leading blank line is not a reason to ignore a comment",
			comment:   "\n@chekhovbot check\n",
			addressed: true,
			want:      Check,
		},
		{
			name:      "the mention is case-insensitive, as GitHub handles are",
			comment:   "@ChekhovBot Check codecheck.yml",
			addressed: true,
			want:      Check,
		},
		{
			// Only the first line counts: a mention inside a sentence is
			// conversation, not an instruction.
			name:      "a mention further down is conversation",
			comment:   "thanks!\n@chekhovbot check codecheck.yml",
			addressed: false,
		},
		{
			name:      "a command later in the first line is not a command either",
			comment:   "please ask @chekhovbot check codecheck.yml",
			addressed: false,
		},
		{
			name:      "a comment that does not mention the bot",
			comment:   "looks good to me",
			addressed: false,
		},
		{
			name:      "an empty comment",
			comment:   "   \n\n",
			addressed: false,
		},
		{
			name:      "a mention with no command",
			comment:   "@chekhovbot",
			addressed: true,
			want:      Unknown,
		},
		{
			name:      "a command the bot does not know",
			comment:   "@chekhovbot polish the certificate",
			addressed: true,
			want:      Unknown,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, addressed := Parse(testCase.comment)
			if addressed != testCase.addressed {
				t.Fatalf("addressed = %v, want %v", addressed, testCase.addressed)
			}
			if !addressed {
				return
			}
			if parsed.Name != testCase.want {
				t.Errorf("command = %q, want %q", parsed.Name, testCase.want)
			}
		})
	}
}

// The reply to an unknown command quotes it back, so the writer can see what
// the bot read.
func TestUnknownCommandKeepsWhatWasWritten(t *testing.T) {
	parsed, addressed := Parse("@chekhovbot polish the certificate")
	if !addressed {
		t.Fatal("the comment is addressed to the bot")
	}
	if parsed.Raw != "polish the certificate" {
		t.Errorf("raw = %q, want %q", parsed.Raw, "polish the certificate")
	}
}

// "check", "check codecheck.yml" and "check config" are the same instruction;
// people will write all three.
func TestCheckPhrasings(t *testing.T) {
	for _, comment := range []string{
		"@chekhovbot check",
		"@chekhovbot check codecheck.yml",
		"@chekhovbot check config",
	} {
		parsed, addressed := Parse(comment)
		if !addressed || parsed.Name != Check {
			t.Errorf("%q: command = %q, addressed = %v", comment, parsed.Name, addressed)
		}
	}
}

// A subcommand is matched before the command it belongs to, and only when the
// second word names one: everything `check` took before it still parses the
// same way (codecheckers/chekhov#22).
func TestASubcommandIsMatchedBeforeItsCommand(t *testing.T) {
	for _, want := range []struct {
		comment string
		name    Name
		args    []string
	}{
		{"@chekhovbot check start", CheckStart, nil},
		{"@chekhovbot check start github::codecheckers/demo", CheckStart,
			[]string{"github::codecheckers/demo"}},
		{"@chekhovbot check finish 2026-001", CheckFinish, []string{"2026-001"}},
		{"@chekhovbot Check Start", CheckStart, nil},
		// The second word names no entry, so it is an argument, as it always
		// was: none of what `check` accepted has stopped being accepted.
		{"@chekhovbot check config codecheckers/demo", Check,
			[]string{"config", "codecheckers/demo"}},
		{"@chekhovbot check codecheckers/demo", Check, []string{"codecheckers/demo"}},
		{"@chekhovbot check", Check, nil},
		// A repository whose first path segment happens to be the word is
		// still a repository: only the bare word is a subcommand.
		{"@chekhovbot check start/here", Check, []string{"start/here"}},
		{"@chekhovbot status", Status, nil},
	} {
		parsed, addressed := Parse(want.comment)
		if !addressed {
			t.Errorf("%q was not addressed to the bot", want.comment)
			continue
		}
		if parsed.Name != want.name {
			t.Errorf("%q parsed as %q, want %q", want.comment, parsed.Name, want.name)
		}
		if strings.Join(parsed.Args, " ") != strings.Join(want.args, " ") {
			t.Errorf("%q args = %q, want %q", want.comment, parsed.Args, want.args)
		}
	}
}
