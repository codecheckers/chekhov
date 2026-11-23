package command

import (
	"slices"
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
			name:      "a sign-off is a command",
			comment:   "@chekhovbot Goodbye!",
			addressed: true,
			want:      Goodbye,
		},
		{
			name:      "and so are its aliases",
			comment:   "@chekhovbot bye",
			addressed: true,
			want:      Goodbye,
		},
		{
			name:      "farewell, in the punctuation of the sentence",
			comment:   "@chekhovbot farewell.",
			addressed: true,
			want:      Goodbye,
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

// The last word of a command is the end of a sentence to whoever wrote it,
// and its full stop is not part of the role (register#244).
func TestTheSentencesPunctuationIsNotAnArgument(t *testing.T) {
	cases := map[string][]string{
		"@chekhovbot assign @a-codechecker as assigned codechecker.": {"@a-codechecker", "as", "assigned", "codechecker"},
		"@chekhovbot assign @a-codechecker as codechecker!":          {"@a-codechecker", "as", "codechecker"},
		"@chekhovbot remind me in 2 weeks .":                         {"me", "in", "2", "weeks"},
		"@chekhovbot certificate set 2024-001;":                      {"2024-001"},
		// A venue's full stop is its own.
		"@chekhovbot venue set journal J. Stat. Softw.": {"journal", "J.", "Stat.", "Softw."},
		"@chekhovbot register journal J. Stat. Softw.":  {"journal", "J.", "Stat.", "Softw."},
	}
	for comment, want := range cases {
		parsed, addressed := Parse(comment)
		if !addressed {
			t.Errorf("%q is not addressed", comment)
			continue
		}
		if strings.Join(parsed.Args, "|") != strings.Join(want, "|") {
			t.Errorf("%q: args %q, want %q", comment, parsed.Args, want)
		}
	}
	role, err := AssignableRole("assigned codechecker")
	if err != nil || role != RoleAssignedCodechecker {
		t.Errorf("the role after the full stop is gone: %q (%v)", role, err)
	}
}

// Every line that begins with the mention is a command of its own, with the
// lines under it (register#244).
func TestSplit(t *testing.T) {
	cases := []struct {
		name    string
		comment string
		want    []string
	}{
		{
			name:    "one command, with a note under it",
			comment: "@chekhovbot remind me in 2 weeks\nthe bundle is late",
			want:    []string{"@chekhovbot remind me in 2 weeks\nthe bundle is late"},
		},
		{
			name: "two commands, the second with a note",
			comment: "@chekhovbot assign @a-codechecker as codechecker.\n\n" +
				"@ChekhovBot remind me in 2 weeks\nthe bundle is late",
			want: []string{"@chekhovbot assign @a-codechecker as codechecker.\n",
				"@ChekhovBot remind me in 2 weeks\nthe bundle is late"},
		},
		{
			name:    "the blank lines before the first command are its own",
			comment: "\r\n@chekhovbot hello\r\n@chekhovbot version",
			want:    []string{"\r\n@chekhovbot hello\r", "@chekhovbot version"},
		},
		{
			name:    "a longer fence quotes a shorter one",
			comment: "@chekhovbot commands\n````\n```\n@chekhovbot certificate next\n```\n````",
			want:    []string{"@chekhovbot commands\n````\n```\n@chekhovbot certificate next\n```\n````"},
		},
		{
			name:    "and an indented line is code",
			comment: "@chekhovbot commands\n    @chekhovbot remove @a-made-up-author as author",
			want:    []string{"@chekhovbot commands\n    @chekhovbot remove @a-made-up-author as author"},
		},
		{
			name:    "a comment that does not begin with one has none",
			comment: "thanks!\n@chekhovbot check",
		},
		{
			name:    "a command in a code fence is an example",
			comment: "@chekhovbot commands\n```\n@chekhovbot check\n```",
			want:    []string{"@chekhovbot commands\n```\n@chekhovbot check\n```"},
		},
		{
			name:    "and a mention inside a line is conversation",
			comment: "@chekhovbot hello\nthis is what @chekhovbot does",
			want:    []string{"@chekhovbot hello\nthis is what @chekhovbot does"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var got []string
			for _, parsed := range Split(testCase.comment) {
				got = append(got, parsed.Body)
			}
			if !slices.Equal(got, testCase.want) {
				t.Errorf("Split = %q, want %q", got, testCase.want)
			}
		})
	}
}

// A comment may give at most MostCommands, and the note names the rest.
func TestCapped(t *testing.T) {
	comment := strings.Repeat("@chekhovbot hello\n", MostCommands) + "@chekhovbot version\n@chekhovbot roles"
	run, note := Capped(Split(comment))
	if len(run) != MostCommands {
		t.Errorf("%d run, want %d", len(run), MostCommands)
	}
	for _, want := range []string{"`@chekhovbot version`", "`@chekhovbot roles`"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not name %s: %s", want, note)
		}
	}
	if _, note := Capped(Split("@chekhovbot hello")); note != "" {
		t.Errorf("a note for one command: %s", note)
	}
}
