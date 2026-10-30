package command

import (
	"strings"
	"testing"
)

// What to suggest is the subject of the command, and the parser reads it: the
// form the command used to have, `suggest codecheckers`, and the singular
// somebody will write, both name the same entry, and whatever follows them is
// a hint.
func TestCodecheckersSuggestIsReadHoweverItIsWritten(t *testing.T) {
	for _, written := range []string{"codecheckers suggest", "Codecheckers Suggest", "suggest codecheckers",
		"suggest Codecheckers", "SUGGEST CODECHECKERS", "suggest codechecker"} {
		parsed, _ := Parse("@chekhovbot " + written + " owner/repo 10.1000/fake")
		if parsed.Name != CodecheckersSuggest {
			t.Errorf("%q parsed as %q", written, parsed.Name)
		}
		if strings.Join(parsed.Args, " ") != "owner/repo 10.1000/fake" {
			t.Errorf("%q: hints %v, want the words after it", written, parsed.Args)
		}
	}
	// `suggest` alone, or with anything that is not codecheckers, is no
	// command, and is answered with the one that is.
	for _, written := range []string{"suggest", "suggest venues"} {
		parsed, _ := Parse("@chekhovbot " + written)
		if parsed.Name != Unknown {
			t.Fatalf("%q parsed as %q, want it unknown", written, parsed.Name)
		}
		if reply := UnknownReply(parsed, Roles{}.With(RoleEditor)); !strings.Contains(reply, "`@chekhovbot codecheckers suggest`") {
			t.Errorf("%q is not pointed at codecheckers suggest:\n%s", written, reply)
		}
	}
}

func TestBodyText(t *testing.T) {
	cases := []struct{ name, comment, want string }{
		{name: "the prose under the command", comment: "@chekhovbot suggest codecheckers\n\nAn abstract.",
			want: "An abstract."},
		{name: "a comment that is only a command", comment: "@chekhovbot suggest codecheckers"},
		{name: "a leading blank line goes with the command line",
			comment: "\n@chekhovbot suggest codecheckers\nAn abstract.\n", want: "An abstract."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := BodyText(testCase.comment); got != testCase.want {
				t.Errorf("BodyText = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestCodecheckerListsReplyLinksRatherThanCopies(t *testing.T) {
	reply := CodecheckerListsReply([]CodecheckerList{
		{Page: "https://example.invalid/codecheckers.csv", Count: 12},
		{Page: "https://example.invalid/institutional.csv", Count: -1},
		{Page: "https://example.invalid/agile.csv", Problem: "answered 404"},
	})
	for _, want := range []string{
		"[codecheckers.csv](https://example.invalid/codecheckers.csv) — 12 entries",
		"[institutional.csv](https://example.invalid/institutional.csv)\n",
		"could not read it: answered 404",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

func TestSuggestionsReplyWithNobodyToSuggest(t *testing.T) {
	reply := SuggestionsReply(Suggestions{Considered: 3, Languages: []string{"Fortran"}})
	if !strings.Contains(reply, "No codechecker to suggest") {
		t.Errorf("reply:\n%s", reply)
	}
	if !strings.Contains(reply, "Matched on languages `Fortran`") {
		t.Errorf("the reply does not say what it matched on:\n%s", reply)
	}
}

func TestSuggestionsReplyProtectsTheTable(t *testing.T) {
	reply := SuggestionsReply(Suggestions{Considered: 1, Candidates: []Candidate{
		{Handle: "a-codechecker", Name: "A | Codechecker @somebody", Why: "R | Python"},
	}})
	if strings.Contains(reply, "\\\\|") {
		t.Errorf("a pipe is escaped twice, which renders as a backslash and a broken row:\n%s", reply)
	}
	if !strings.Contains(reply, `\|`) {
		t.Errorf("a pipe in a name is not escaped, so it ends the row:\n%s", reply)
	}
	if strings.Contains(reply, "@somebody") {
		t.Errorf("a name out of the lists mentions an account:\n%s", reply)
	}
}
