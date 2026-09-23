package command

import (
	"strings"
	"testing"
)

// names is what the shipped settings say, so that the tests read like the
// register does.
var labelNames = LabelNames{Institution: "institution", NeedsCodechecker: "needs codechecker"}

// read is a check whose labels and roles were both read, which is the only
// state any rule has anything to say about.
func read(labels []string, codechecker string) CheckState {
	return CheckState{Names: labelNames, Labels: labels, LabelsRead: true,
		AssignedCodechecker: codechecker, RolesRead: true}
}

func TestLabelDisagreements(t *testing.T) {
	institutional := func(state CheckState, institution string, volunteers bool) CheckState {
		state.Institution, state.AlsoVolunteers = institution, volunteers
		return state
	}

	for _, test := range []struct {
		name  string
		state CheckState
		// want is what every disagreement together has to mention, and none
		// is the test that the rules kept quiet.
		want []string
		none bool
	}{{
		name:  "an institutional codechecker on an ordinary check",
		state: institutional(read([]string{"needs codechecker"}, "a-codechecker"), "FAKE University", false),
		want:  []string{"FAKE University", "`institution`", "a-codechecker"},
	}, {
		name:  "no warning when the check is labelled institutional",
		state: institutional(read([]string{"institution"}, "a-codechecker"), "FAKE University", false),
		none:  true,
	}, {
		// A handle on both lists is institutional for this purpose, and the
		// reply says so rather than leaving the editor to wonder.
		name:  "on both lists",
		state: institutional(read(nil, "a-codechecker"), "FAKE University", true),
		want:  []string{"FAKE University", "ordinary list too"},
	}, {
		name:  "on the ordinary list only",
		state: institutional(read(nil, "a-codechecker"), "", true),
		none:  true,
	}, {
		// A list that would not load names nobody, which must read as "could
		// not check" and never as "this is not institutional".
		name:  "the lists could not be read",
		state: read(nil, "a-codechecker"),
		none:  true,
	}, {
		name:  "assigned and still in the queue",
		state: read([]string{"needs codechecker"}, "a-codechecker"),
		want:  []string{"assigned codechecker", "`needs codechecker`"},
	}, {
		name:  "nobody assigned and not in the queue",
		state: read([]string{"work in progress"}, ""),
		want:  []string{"Nobody is the assigned codechecker", "`needs codechecker`"},
	}, {
		// A finished check needs nobody, so the queue has no business with
		// it: a check whose codechecker this bot never recorded would
		// otherwise be told off every time somebody ran `roles` on it.
		name: "nobody assigned, out of the queue, and closed",
		state: func() CheckState {
			state := read([]string{"work in progress"}, "")
			state.Closed = true
			return state
		}(),
		none: true,
	}, {
		name:  "nobody assigned and in the queue",
		state: read([]string{"needs codechecker"}, ""),
		none:  true,
	}, {
		name:  "assigned and out of the queue",
		state: read(nil, "a-codechecker"),
		none:  true,
	}, {
		// Labels that could not be read leave every rule with nothing to
		// compare against, including the ones that fire on a label's absence.
		name:  "the labels could not be read",
		state: CheckState{Names: labelNames, AssignedCodechecker: "a-codechecker", RolesRead: true},
		none:  true,
	}, {
		// So does a roles record that could not be read or could not be
		// trusted: the queue rules both need to know who is assigned.
		name:  "the roles could not be read",
		state: CheckState{Names: labelNames, Labels: []string{"needs codechecker"}, LabelsRead: true},
		none:  true,
	}, {
		// Settings that name no label switch its rules off rather than
		// matching every check that carries none.
		name: "no label names configured",
		state: CheckState{Labels: []string{"needs codechecker"}, LabelsRead: true,
			AssignedCodechecker: "a-codechecker", RolesRead: true, Institution: "FAKE University"},
		none: true,
	}, {
		name:  "case and spacing of a label do not matter",
		state: institutional(read([]string{" Institution "}, "a-codechecker"), "FAKE University", false),
		none:  true,
	}} {
		t.Run(test.name, func(t *testing.T) {
			found := strings.Join(test.state.Disagreements(), "\n")
			if test.none {
				if found != "" {
					t.Fatalf("said something, want silence:\n%s", found)
				}
				return
			}
			for _, want := range test.want {
				if !strings.Contains(found, want) {
					t.Errorf("does not mention %q:\n%s", want, found)
				}
			}
		})
	}
}

// Both queue rules cannot fire at once, and an institutional warning rides in
// the same note rather than a second one.
func TestLabelNoteIsOneNote(t *testing.T) {
	state := read([]string{"needs codechecker"}, "a-codechecker")
	state.Institution = "FAKE University"
	note := LabelNote(state.Disagreements())
	if got := strings.Count(note, "⚠"); got != 1 {
		t.Errorf("%d warnings in one note, want 1:\n%s", got, note)
	}
	if !strings.Contains(note, "I have changed no labels.") {
		t.Errorf("the note does not say that nothing was changed:\n%s", note)
	}
	if strings.Count(note, "\n- ") != 2 {
		t.Errorf("want both disagreements as bullets of the one note:\n%s", note)
	}
}

// Agreement is silence: a reply that ends "and the labels are fine" on every
// command is noise, and noise is how the useful line gets skipped.
func TestLabelNoteSaysNothingWhenTheLabelsAgree(t *testing.T) {
	if note := LabelNote(read([]string{"needs codechecker"}, "").Disagreements()); note != "" {
		t.Errorf("note %q, want none", note)
	}
}

// A warning names a handle, and a handle a reply writes must not notify its
// owner: the note is about a possible mistake, and the person it names has not
// made one. So every handle in it is in backticks, as mentions writes them.
func TestLabelNoteDoesNotNotifyAnybody(t *testing.T) {
	state := read(nil, "a-codechecker")
	state.Institution = "FAKE University"
	note := LabelNote(state.Disagreements())
	if strings.Count(note, "@a-codechecker") != strings.Count(note, "`@a-codechecker`") {
		t.Errorf("a handle outside backticks would notify its owner:\n%s", note)
	}
}

// The commands that watch the labels are the ones whose subject is the state
// of the check. A command whose subject is not - check, rules, version, hello
// - says nothing about them.
func TestOnlyCheckStateCommandsWatchTheLabels(t *testing.T) {
	watching := map[Name]bool{}
	for _, definition := range Definitions() {
		if definition.AboutTheCheck {
			watching[definition.Name] = true
		}
	}
	for _, name := range []Name{Assign, Remove, ListRoles, SuggestCodecheckers} {
		if !watching[name] {
			t.Errorf("`%s` does not watch the labels", name)
		}
	}
	for _, name := range []Name{Check, Rules, Version, Hello, Commands, Announce, Accept} {
		if watching[name] {
			t.Errorf("`%s` watches the labels, and its subject is not the check's state", name)
		}
	}
}
