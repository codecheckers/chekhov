package command

import (
	"strings"
	"testing"
)

func TestParseSuggestion(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		hints []string
		error string
	}{
		{name: "the plural, as the command is written", args: []string{"codecheckers"}},
		{name: "the singular, as somebody will write it", args: []string{"codechecker"}},
		{name: "whatever follows is a hint",
			args:  []string{"codecheckers", "owner/repo", "10.1000/fake"},
			hints: []string{"owner/repo", "10.1000/fake"}},
		{name: "suggest alone does not say what", error: "suggest what"},
		{name: "and nothing else can be suggested yet",
			args: []string{"venues"}, error: "I can suggest `codecheckers`"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			hints, err := ParseSuggestion(testCase.args)
			switch {
			case testCase.error != "" && err == nil:
				t.Fatalf("read %v, want an error about %q", hints, testCase.error)
			case testCase.error != "":
				if !strings.Contains(err.Error(), testCase.error) {
					t.Fatalf("error %q, want it to say %q", err, testCase.error)
				}
				return
			case err != nil:
				t.Fatal(err)
			}
			if strings.Join(hints, " ") != strings.Join(testCase.hints, " ") {
				t.Errorf("hints %v, want %v", hints, testCase.hints)
			}
		})
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
		"could not read it just now: answered 404",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

func TestSuggestionsReplyWithNobodyToSuggest(t *testing.T) {
	reply := SuggestionsReply(Suggestions{Considered: 3, Languages: []string{"Fortran"}})
	if !strings.Contains(reply, "nobody to suggest") {
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

func TestParseSuggestionReadsHoweverItIsWritten(t *testing.T) {
	for _, written := range []string{"codecheckers", "Codecheckers", "CODECHECKERS", "codechecker"} {
		if _, err := ParseSuggestion([]string{written}); err != nil {
			t.Errorf("%q was refused: %v", written, err)
		}
	}
}
