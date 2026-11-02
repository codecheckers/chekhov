package command

import (
	"strings"
	"testing"
	"time"
)

func TestParseReminder(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want Reminder
	}{
		{[]string{"me", "in", "10", "days"}, Reminder{Handle: "nuest", Days: 10}},
		{[]string{"ME", "1", "day"}, Reminder{Handle: "nuest", Days: 1}},
		{[]string{"@A-Codechecker", "in", "2", "weeks"}, Reminder{Handle: "a-codechecker", Days: 14}},
		{[]string{"@nuest", "in", "1", "Week"}, Reminder{Handle: "nuest", Days: 7}},
	} {
		got, err := ParseReminder(tc.args, "nuest")
		if err != nil || got != tc.want {
			t.Errorf("ParseReminder(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}
}

func TestParseReminderRefuses(t *testing.T) {
	for _, tc := range []struct {
		args []string
		says string
	}{
		{nil, "whom, and when?"},
		{[]string{"me"}, "in how long?"},
		{[]string{"me", "in", "2"}, "in how long?"},
		{[]string{"me", "in", "two", "weeks"}, "not a number"},
		{[]string{"me", "in", "0", "days"}, "not a number"},
		{[]string{"me", "in", "3", "months"}, "days or weeks"},
		{[]string{"me", "in", "53", "weeks"}, "at most 365 days"},
		{[]string{"@not|a|handle", "in", "2", "days"}, "not a GitHub handle"},
	} {
		_, err := ParseReminder(tc.args, "nuest")
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("ParseReminder(%q) = %v, want an error saying %q", tc.args, err, tc.says)
		}
	}
}

func TestReminderNoteIsTheLinesUnderTheCommand(t *testing.T) {
	note, err := ReminderNote("@chekhovbot remind me in 2 days\nask about the data\n", false)
	if err != nil || note != "ask about the data" {
		t.Errorf("ReminderNote = %q, %v", note, err)
	}
	if _, err := ReminderNote("@chekhovbot remind me in 2 days\n"+strings.Repeat("x", MostReminderNote+1), false); err == nil {
		t.Error("a reason longer than a reminder keeps was accepted")
	}
	// The lines under a command that opens an issue are the issue.
	if note, err := ReminderNote("@chekhovbot remind me in 2 days\n"+strings.Repeat("x", 2000), true); note != "" || err != nil {
		t.Errorf("an issue description was read as a reason: %q, %v", note, err)
	}
}

// The person reminded is mentioned so that it notifies; anybody the note
// names is not, because the comment the note came from already did.
func TestAReminderNotifiesOnlyThePersonReminded(t *testing.T) {
	due := ReminderDue("a-codechecker", "nuest", time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
		"ask @a-handling-editor about the data,\n@codecheckers/editors too, mail me@example.org")
	for _, want := range []string{
		"@a-codechecker, here is the reminder `@nuest` asked for on 2 November 2026:",
		"> ask &#64;a-handling-editor about the data,",
		"> &#64;codecheckers/editors too, mail me&#64;example.org",
	} {
		if !strings.Contains(due, want) {
			t.Errorf("the reminder does not say %q:\n%s", want, due)
		}
	}
}

func TestAReminderForOneselfSaysSo(t *testing.T) {
	due := ReminderDue("nuest", "nuest", time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), "")
	if due != "@nuest, here is the reminder you asked for on 2 November 2026.\n" {
		t.Errorf("reminder = %q", due)
	}
}

func TestReminderSetNamesTheDay(t *testing.T) {
	set := ReminderSet(Reminder{Handle: "a-codechecker", Days: 14}, "nuest",
		time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC), "")
	if !strings.Contains(set, "I will remind `@a-codechecker` here on Monday 16 November 2026.") {
		t.Errorf("confirmation = %q", set)
	}
	if strings.Contains(strings.ReplaceAll(set, "`@a-codechecker`", ""), "@") {
		t.Errorf("the confirmation notifies somebody:\n%s", set)
	}
}
