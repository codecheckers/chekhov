package logbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type collected struct {
	mu      sync.Mutex
	records []Record
}

func (c *collected) Offer(record Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, record)
}

func (c *collected) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, record := range c.records {
		out = append(out, record.Message)
	}
	return out
}

// Every record still reaches stderr, the buffer keeps it, and only an Error
// is offered to the sink - also those logged before the sink was attached.
func TestTheHandlerTeesAndOffersErrorsOnly(t *testing.T) {
	var stderr bytes.Buffer
	buffer := NewBuffer()
	handler := NewHandler(slog.NewTextHandler(&stderr, nil), buffer)
	logger := slog.New(handler)

	logger.Error("before the sink", "issue", 7)
	logger.Info("answered", "issue", 7, "author", "made-up-editor")
	sink := &collected{}
	handler.Attach(sink)
	logger.With("ref", "abc").WithGroup("g").Warn("a warning", "x", 1)
	logger.Error("after the sink", "error", errors.New("boom"))

	if got := sink.messages(); strings.Join(got, "|") != "before the sink|after the sink" {
		t.Errorf("offered %q, want the two errors in order", got)
	}
	for _, want := range []string{"before the sink", "answered", "a warning", "after the sink"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr.String())
		}
	}
	kept := buffer.Select(time.Time{}, func(Record) bool { return true }, 0)
	if len(kept) != 4 {
		t.Fatalf("kept %d records, want 4", len(kept))
	}
	if kept[1].Issue() != 7 {
		t.Errorf("issue of %+v is %d, want 7", kept[1], kept[1].Issue())
	}
	if ref, _ := kept[2].Value("ref"); ref != "abc" {
		t.Errorf("WithAttrs lost ref: %+v", kept[2])
	}
	if x, ok := kept[2].Value("g.x"); !ok || x != "1" {
		t.Errorf("WithGroup did not prefix the key: %+v", kept[2])
	}
	if !strings.Contains(kept[3].Source(), "logbook.TestTheHandlerTeesAndOffersErrorsOnly") {
		t.Errorf("source is %q, want this test", kept[3].Source())
	}
}

// Only a few errors are held for a sink that never comes.
func TestErrorsWaitingForASinkAreFew(t *testing.T) {
	handler := NewHandler(discard(), nil)
	logger := slog.New(handler)
	for i := range 100 {
		logger.Error("again", "n", i)
	}
	sink := &collected{}
	handler.Attach(sink)
	if got := len(sink.messages()); got != waitingAtMost {
		t.Errorf("%d waiting errors offered, want %d", got, waitingAtMost)
	}
}

// The buffer keeps the newest Kept, oldest first, and a limit keeps the
// newest of what was asked for.
func TestTheBufferKeepsTheNewest(t *testing.T) {
	buffer := NewBuffer()
	handler := NewHandler(discard(), buffer)
	logger := slog.New(handler)
	for i := range Kept + 10 {
		logger.Info("line", "issue", i%3, "n", i)
	}
	all := buffer.Select(time.Time{}, func(Record) bool { return true }, 0)
	if len(all) != Kept {
		t.Fatalf("kept %d, want %d", len(all), Kept)
	}
	if first, _ := all[0].Value("n"); first != "10" {
		t.Errorf("the oldest kept is n=%s, want 10", first)
	}
	two := buffer.Select(time.Time{}, func(r Record) bool { return r.Issue() == 2 }, 3)
	var got []string
	for _, record := range two {
		n, _ := record.Value("n")
		got = append(got, n)
		if record.Issue() != 2 {
			t.Errorf("a record of issue %d among issue 2's", record.Issue())
		}
	}
	if strings.Join(got, ",") != "2003,2006,2009" {
		t.Errorf("the newest three of issue 2 are %v", got)
	}
}

// What a secret looks like never survives, wherever in a record it is.
func TestRedactTakesOutSecrets(t *testing.T) {
	for _, secret := range []string{
		"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ",
		"ghp_0123456789abcdefghijABCDEFGHIJ012345",
		"ghs_0123456789abcdefghijABCDEFGHIJ012345",
		"Bearer abcdefghijklmnop.qrstu",
		"token=s3cr3t-value",
		"api_key: hunter2hunter2",
		"https://zenodo.org/records/123?preview=1&token=eyJhbGciOiJIUzUxMiJ9.eyJpZCI6Ij",
		"https://user:pass@example.org/path",
		strings.Repeat("Q", 60),
	} {
		got := Redact("failed: " + secret + " (end)")
		for _, part := range []string{"github_pat_11", "ghp_0123", "ghs_0123", "abcdefghijklmnop", "s3cr3t",
			"hunter2", "eyJhbGci", "user:pass", strings.Repeat("Q", 41)} {
			if strings.Contains(secret, part) && strings.Contains(got, part) {
				t.Errorf("Redact(%q) = %q, still holding %q", secret, got, part)
			}
		}
		if !strings.HasSuffix(got, " (end)") {
			t.Errorf("Redact(%q) = %q, took more than the secret", secret, got)
		}
	}
	// What is not a secret stays.
	for _, plain := range []string{
		"could not read codecheckers/testing-dev-register#12: 404 Not Found",
		"0123456789abcdef0123456789abcdef01234567",
		"/home/runner/work/chekhov/chekhov/internal/bot/bot.go:315 +0x1a4",
	} {
		if got := Redact(plain); got != plain {
			t.Errorf("Redact(%q) = %q, want it unchanged", plain, got)
		}
	}
}

// A rendered record is an indented block, one record a line, with a hidden
// key's value left out and a trace shown only when asked for.
func TestRenderIsAnIndentedBlock(t *testing.T) {
	buffer := NewBuffer()
	logger := slog.New(NewHandler(discard(), buffer))
	logger.Warn("could not post", "issue", 3, "author", "@made-up-editor", "token", "plain-secret",
		"error", fmt.Errorf("401 for https://api.github.com/x?access_token=abc"))
	logger.Error("panicked", "what", "a command", "stack", "goroutine 1\n\tbot.go:12")
	records := buffer.Select(time.Time{}, func(Record) bool { return true }, 0)

	without := Render(records, false)
	for _, line := range strings.Split(strings.TrimRight(without, "\n"), "\n") {
		if !strings.HasPrefix(line, "    ") {
			t.Errorf("a line that is not indented, so not in the block: %q", line)
		}
	}
	for _, absent := range []string{"plain-secret", "access_token=abc", "goroutine 1"} {
		if strings.Contains(without, absent) {
			t.Errorf("rendered without traces still says %q:\n%s", absent, without)
		}
	}
	for _, present := range []string{"token=[hidden]", "author=@made-up-editor", "issue=3"} {
		if !strings.Contains(without, present) {
			t.Errorf("rendered is missing %q:\n%s", present, without)
		}
	}
	if with := Render(records, true); !strings.Contains(with, "        goroutine 1\n") {
		t.Errorf("with traces, the stack is missing or not indented:\n%s", with)
	}
}

// discard is a handler that takes Info and above and writes nowhere. Not
// slog.DiscardHandler, which is enabled for nothing, so nothing reaches Handle.
func discard() slog.Handler { return slog.NewTextHandler(io.Discard, nil) }
