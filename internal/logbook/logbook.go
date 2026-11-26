// Package logbook keeps what the bot logs, so that somebody without access to
// the platform can be shown it - and never on the register (#73).
//
// Every record still goes to stderr, which is the platform's log and the one
// copy nothing here can lose. Beside it, a Handler keeps the most recent
// records in memory, for `@chekhovbot log`, and hands every record at Error
// and above to a Sink, which reports it in the private log repository. The
// memory is the process's own: a deploy empties it, the same bargain as the
// rest of a bot with no database.
//
// Nothing in this package posts anything. It renders, and it redacts, and the
// rendering is the only way a record leaves it, so that the rule about what a
// record may show is in one place.
package logbook

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kept is how many records the buffer holds: a busy day of the register is a
// few hundred, and a record is a few hundred bytes, so this is under a
// megabyte however long the lines.
const Kept = 2000

// A Record is one line of the log, as the buffer keeps it: the attributes
// flattened to strings once, because a value logged by reference - an error,
// a struct - could change after the line was written.
type Record struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   []Attr
	// PC is where it was logged, resolved by Source only when somebody asks:
	// every line of the process passes through here, and few are ever read.
	PC uintptr
}

// An Attr is one key and its value, as text.
type Attr struct{ Key, Value string }

// Value is the value of the first attribute named key, and whether there was
// one.
func (r Record) Value(key string) (string, bool) {
	for _, attr := range r.Attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return "", false
}

// Issue is the issue number the record is about, 0 when it names none.
func (r Record) Issue() int {
	value, _ := r.Value("issue")
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0
	}
	return number
}

// A Sink is told of every record at Error and above. Offer must return at
// once whatever the sink does with it: it is called on the goroutine that
// logged, which may be the one answering somebody.
type Sink interface {
	Offer(Record)
}

// Buffer is the records most recently logged, oldest first, Kept at most.
type Buffer struct {
	mu      sync.Mutex
	records []Record
	next    int // where the next record goes
	held    int // how many there are, Kept at most
}

// NewBuffer is an empty buffer.
func NewBuffer() *Buffer { return &Buffer{records: make([]Record, Kept)} }

func (b *Buffer) add(record Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records[b.next] = record
	b.next = (b.next + 1) % len(b.records)
	b.held = min(b.held+1, len(b.records))
}

// Select returns the records since a time that keep accepts, oldest first, at
// most limit of them - the newest, when there are more. A zero since is the
// whole buffer. Under the lock every log line needs, so it stops at the first
// record older than since rather than reading the rest.
func (b *Buffer) Select(since time.Time, keep func(Record) bool, limit int) []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	var picked []Record
	// Newest first, so that the limit keeps the newest; turned round below.
	for i := 1; i <= b.held; i++ {
		at := (b.next - i + len(b.records)) % len(b.records)
		if b.records[at].Time.Before(since) {
			break
		}
		if keep(b.records[at]) {
			picked = append(picked, b.records[at])
			if limit > 0 && len(picked) == limit {
				break
			}
		}
	}
	slices.Reverse(picked)
	return picked
}

// Handler writes every record to next, keeps it in the buffer, and offers it
// to the sink when it is at Error or above. A nil buffer or sink is skipped.
type Handler struct {
	next   slog.Handler
	buffer *Buffer
	sink   *sinkSlot
	attrs  []Attr
	group  string
}

// sinkSlot lets the sink be attached after the handler is installed: the
// handler goes in first thing, so that nothing is logged around it, and the
// sink needs the token and the settings, which come later. Records at Error
// logged before then are held in the slot, a few at most, and offered once
// the sink is there.
type sinkSlot struct {
	mu      sync.Mutex
	sink    Sink
	waiting []Record
}

// waitingAtMost is how many Error records are held for a sink not yet
// attached: a startup that fails fails a few times, not a thousand.
const waitingAtMost = 16

// NewHandler tees next into the buffer and, once one is attached, the sink.
func NewHandler(next slog.Handler, buffer *Buffer) *Handler {
	return &Handler{next: next, buffer: buffer, sink: &sinkSlot{}}
}

// Attach sets the sink, and offers it what was logged at Error before it was
// there.
func (h *Handler) Attach(sink Sink) {
	h.sink.mu.Lock()
	waiting := h.sink.waiting
	h.sink.sink, h.sink.waiting = sink, nil
	h.sink.mu.Unlock()
	for _, record := range waiting {
		sink.Offer(record)
	}
}

func (s *sinkSlot) offer(record Record) {
	s.mu.Lock()
	sink := s.sink
	if sink == nil {
		if len(s.waiting) < waitingAtMost {
			s.waiting = append(s.waiting, record)
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	sink.Offer(record)
}

// Enabled is next's answer: the buffer keeps what stderr shows.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle writes the record on, and then keeps and offers it. What next
// answers is returned, and a failure to keep cannot fail the log line.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.next.Handle(ctx, r)
	attrs := make([]Attr, len(h.attrs), len(h.attrs)+r.NumAttrs())
	copy(attrs, h.attrs)
	record := Record{Time: r.Time.UTC(), Level: r.Level, Message: r.Message, Attrs: attrs, PC: r.PC}
	r.Attrs(func(attr slog.Attr) bool {
		record.Attrs = appendAttr(record.Attrs, h.group, attr)
		return true
	})
	if h.buffer != nil {
		h.buffer.add(record)
	}
	if r.Level >= slog.LevelError {
		h.sink.offer(record)
	}
	return err
}

// WithAttrs keeps the attributes for every record of the new handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.next = h.next.WithAttrs(attrs)
	next.attrs = slices.Clone(h.attrs)
	for _, attr := range attrs {
		next.attrs = appendAttr(next.attrs, h.group, attr)
	}
	return &next
}

// WithGroup prefixes the keys of the new handler's attributes.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.next = h.next.WithGroup(name)
	next.group = joinKey(h.group, name)
	return &next
}

func appendAttr(attrs []Attr, group string, attr slog.Attr) []Attr {
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		for _, inner := range value.Group() {
			attrs = appendAttr(attrs, joinKey(group, attr.Key), inner)
		}
		return attrs
	}
	if attr.Key == "" {
		return attrs
	}
	return append(attrs, Attr{Key: joinKey(group, attr.Key), Value: value.String()})
}

func joinKey(group, key string) string {
	if group == "" {
		return key
	}
	return group + "." + key
}

// Source is the function and line a record came from, without the module's
// path - "bot.(*Server).act:315" - for telling one error from another of the
// same words; empty when the runtime could not say.
func (r Record) Source() string {
	if r.PC == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
	if frame.Function == "" {
		return ""
	}
	name := frame.Function
	if at := strings.LastIndex(name, "/"); at >= 0 {
		name = name[at+1:]
	}
	return fmt.Sprintf("%s:%d", name, frame.Line)
}
