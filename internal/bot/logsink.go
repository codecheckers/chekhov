package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/build"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/logbook"
)

// The private log repository, as the sink for what goes wrong (#73).
//
// Every record at Error - a panic, an answer that could not be posted - is
// reported there as an issue labelled `fatal`, without anybody asking and
// without notifying anybody: editors watch the repository. The same error
// again is a comment on its open issue rather than a new one.
//
// Never blocking is the first rule. Offer is called on the goroutine that
// logged, which may be answering somebody, so it hands the record to a
// buffered channel and returns; a full channel drops the record and counts it.
// One worker writes, through the deployment's one write queue, so that a
// burst of errors waits its turn behind the replies rather than beside them.
//
// Never public is the second. The worker asks GitHub whether the repository is
// private before its first write, and writes nothing if it is not.
//
// And never a loop: the worker's own client logs to stderr only, and its
// failures are Warn, so nothing it does can come back to it as an Error.

// The labels of the log repository. Every issue carries the deployment's own,
// and the fatal ones `fatal` besides.
const (
	labelProduction  = "production"
	labelDevelopment = "development"
	labelFatal       = "fatal"
)

// logLabel is the label of this deployment's issues in the log repository:
// production's own, and development's for everything else - a live test too.
func logLabel(deployment command.Deployment) string {
	if deployment.Environment == "production" {
		return labelProduction
	}
	return labelDevelopment
}

const (
	// sinkQueue is how many records wait for the worker. An error that
	// repeats a thousand times is the same issue, and the ones dropped are
	// counted.
	sinkQueue = 64
	// sinkNewPerHour is how many issues the sink opens in an hour at most,
	// and sinkPostsPerHour how many writes of any kind; beyond them a record
	// is in the platform's log only, and counted.
	sinkNewPerHour   = 10
	sinkPostsPerHour = 30
	// sinkTimeout bounds one report, the queue included.
	sinkTimeout = time.Minute
	// sinkDrain is how long a stopping process waits for the reports still
	// queued: the last part of the twenty seconds a redeploy gives, after
	// Shutdown's (shutdownGrace). Each write waits its turn in the write
	// queue, three seconds apart, so this is a report or two: what is still
	// queued after it is in stderr only.
	sinkDrain = 5 * time.Second
	// contextRecords is how many records before an error its report quotes.
	contextRecords = 20
	// contextWindow is how far back they may be.
	contextWindow = 15 * time.Minute
)

// logWriter is the part of the GitHub client the sink writes with, so that a
// test can watch it.
type logWriter interface {
	Private(ctx context.Context) (bool, error)
	OpenLabelledIssues(ctx context.Context, repository, label string) ([]github.Issue, error)
	CreateIssue(ctx context.Context, repository, title, body string, labels []string) (int, error)
	Comment(ctx context.Context, repository string, issue int, body string) (int64, error)
}

// fatalSink reports Error records in the log repository.
type fatalSink struct {
	repository string
	label      string
	deployment command.Deployment
	writer     logWriter
	buffer     *logbook.Buffer
	// logger is stderr's alone, never the tee: see the top of the file.
	logger *slog.Logger
	now    func() time.Time

	mu     sync.Mutex
	queue  chan logbook.Record
	closed bool
	counts sinkCounts

	done chan struct{}

	// The worker's own, and touched by nobody else.
	checked bool
	private bool
	loaded  bool           // known has been read from the repository
	known   map[string]int // fingerprint -> its open issue
	seen    map[string]int // fingerprint -> times reported by this process
	posts   []time.Time
	opened  []time.Time
}

// sinkCounts is what the sink has done, for GET /nudges: numbers only.
type sinkCounts struct {
	Queued  int `json:"queued"`
	Posted  int `json:"posted"`
	Dropped int `json:"dropped"`
	Limited int `json:"limited"`
	Failed  int `json:"failed"`
	// Refused says the repository was not private, and nothing was written.
	Refused bool `json:"refused"`
}

// newFatalSink starts the worker. Close stops it.
func newFatalSink(repository string, deployment command.Deployment, writer logWriter,
	buffer *logbook.Buffer, logger *slog.Logger) *fatalSink {
	sink := &fatalSink{
		repository: repository,
		label:      logLabel(deployment),
		deployment: deployment,
		writer:     writer,
		buffer:     buffer,
		logger:     logger,
		now:        time.Now,
		queue:      make(chan logbook.Record, sinkQueue),
		done:       make(chan struct{}),
		known:      map[string]int{},
		seen:       map[string]int{},
	}
	go sink.work()
	return sink
}

// Offer queues a record and returns at once.
func (s *fatalSink) Offer(record logbook.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.counts.Dropped++
		return
	}
	select {
	case s.queue <- record:
		s.counts.Queued++
	default:
		s.counts.Dropped++
	}
}

// Close stops taking records and waits, until ctx ends, for the queued ones.
func (s *fatalSink) Close(ctx context.Context) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
	case <-ctx.Done():
	}
}

// Counts is a copy of what the sink has done.
func (s *fatalSink) Counts() sinkCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts
}

func (s *fatalSink) count(change func(*sinkCounts)) {
	s.mu.Lock()
	change(&s.counts)
	s.mu.Unlock()
}

// work reports the records one at a time until the queue is closed. A panic
// costs the one record, and is written to stderr: reporting it here would be
// reporting a failure of the reporting. That is also why it does not go
// through internal/safe, which logs at Error.
func (s *fatalSink) work() {
	defer close(s.done)
	// reportGuarded recovers each record's report; this is the frame under
	// it, for a panic in the recovering itself. stderr, as everything here.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Warn("the log sink stopped", "panic", fmt.Sprint(r))
		}
	}()
	for record := range s.queue {
		s.reportGuarded(record)
	}
}

func (s *fatalSink) reportGuarded(record logbook.Record) {
	defer func() {
		if r := recover(); r != nil {
			s.count(func(c *sinkCounts) { c.Failed++ })
			s.logger.Warn("the report of an error panicked", "panic", fmt.Sprint(r))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), sinkTimeout)
	defer cancel()
	if err := s.report(ctx, record); err != nil {
		s.count(func(c *sinkCounts) { c.Failed++ })
		s.logger.Warn("an error could not be reported in the log repository",
			"repository", s.repository, "error", err)
	}
}

// report writes one record: a comment on its open issue, or a new issue. The
// limits are asked before anything is read or rendered, so that a storm of
// errors costs a count each and not a report each.
func (s *fatalSink) report(ctx context.Context, record logbook.Record) error {
	if !s.mayWrite(ctx) {
		s.count(func(c *sinkCounts) { c.Dropped++ })
		return nil
	}
	now := s.now()
	s.posts, s.opened = within(s.posts, now), within(s.opened, now)
	if len(s.posts) >= sinkPostsPerHour {
		s.count(func(c *sinkCounts) { c.Limited++ })
		return nil
	}
	if err := s.loadOpen(ctx); err != nil {
		return err
	}

	print := fingerprint(record)
	s.seen[print]++
	if issue, open := s.known[print]; open {
		s.posts = append(s.posts, now)
		header := "Again, the first time since the bot last started.\n\n"
		if s.seen[print] > 1 {
			header = fmt.Sprintf("Again, the %s time since the bot last started.\n\n", ordinal(s.seen[print]))
		}
		if _, err := s.writer.Comment(ctx, s.repository, issue, command.Defuse(header+s.body(record))); err != nil {
			// The issue may have been closed or moved: read them afresh next time.
			s.loaded = false
			return err
		}
		s.count(func(c *sinkCounts) { c.Posted++ })
		return nil
	}

	if len(s.opened) >= sinkNewPerHour {
		s.count(func(c *sinkCounts) { c.Limited++ })
		return nil
	}
	s.posts, s.opened = append(s.posts, now), append(s.opened, now)
	number, err := s.writer.CreateIssue(ctx, s.repository, title(record, print), command.Defuse(s.body(record)),
		[]string{s.label, labelFatal})
	if err != nil {
		return err
	}
	s.known[print] = number
	s.count(func(c *sinkCounts) { c.Posted++ })
	return nil
}

// mayWrite asks GitHub, once, whether the repository is private. A failure to
// ask is asked again with the next record; an answer of "public" is final for
// the process, and said once.
func (s *fatalSink) mayWrite(ctx context.Context) bool {
	if s.checked {
		return s.private
	}
	private, err := s.writer.Private(ctx)
	if err != nil {
		s.logger.Warn("could not ask whether the log repository is private; nothing is written there until it can",
			"repository", s.repository, "error", err)
		return false
	}
	s.checked, s.private = true, private
	if !private {
		s.count(func(c *sinkCounts) { c.Refused = true })
		s.logger.Warn("the log repository is not private, so nothing is written there; see docs/deployment.md",
			"repository", s.repository)
	}
	return private
}

// loadOpen reads, once, which open fatal issues of this deployment there are,
// by the fingerprint in their titles; after that the sink keeps the map
// itself, adding what it opens. Open ones only: a closed issue is a fixed
// error, and a recurrence opens a fresh one.
func (s *fatalSink) loadOpen(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	issues, err := s.writer.OpenLabelledIssues(ctx, s.repository, labelFatal)
	if err != nil {
		return err
	}
	s.known = map[string]int{}
	for _, issue := range issues {
		// One deployment's, never both: a label put on by hand must not have
		// production write into development's issue or the other way round.
		if !command.HasLabel(issue.Labels, s.label) || command.HasLabel(issue.Labels, otherLabel(s.label)) {
			continue
		}
		if open, close := strings.LastIndex(issue.Title, "["), strings.LastIndex(issue.Title, "]"); open >= 0 && close == len(issue.Title)-1 {
			s.known[issue.Title[open+1:close]] = issue.Number
		}
	}
	s.loaded = true
	return nil
}

// otherLabel is the label of the other deployment.
func otherLabel(label string) string {
	if label == labelProduction {
		return labelDevelopment
	}
	return labelProduction
}

// body is the report: what logged it and when, the records before it about
// the same issue, and the record itself with its stack.
func (s *fatalSink) body(record logbook.Record) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Logged at %s by chekhov %s", record.Time.Format(time.RFC3339), s.deployment.Version)
	if s.deployment.Revision != "" {
		fmt.Fprintf(&out, " (`%s`)", build.Shorten(s.deployment.Revision))
	}
	fmt.Fprintf(&out, ", %s, working on `%s`", s.deployment.Environment, s.deployment.Register)
	if source := record.Source(); source != "" {
		fmt.Fprintf(&out, ", from `%s`", source)
	}
	out.WriteString(".\n\n")
	// GitHub takes no body over its limit, and an error quoting a proxy's
	// page would then never be reported at all: the record is cut to fit,
	// and the records before it are as many as still fit, the newest kept.
	out.WriteString(cutBlock(logbook.Render([]logbook.Record{record}, true), bodyBudget-out.Len()))
	earlier := s.before(record)
	for len(earlier) > 0 {
		rendered := logbook.Render(earlier, false)
		if out.Len()+len(rendered)+100 <= bodyBudget {
			fmt.Fprintf(&out, "\nThe %d records before it", len(earlier))
			if issue := record.Issue(); issue > 0 {
				fmt.Fprintf(&out, " about issue %d", issue)
			}
			out.WriteString(":\n\n" + rendered)
			break
		}
		earlier = earlier[1:]
	}
	return out.String()
}

// bodyBudget is how long a report may be: GitHub's limit, less room for the
// line a repeat starts with and the signature the client appends.
const bodyBudget = github.MaxCommentLength - 2000

// cutBlock cuts a rendered block to at most limit bytes, at a line, and says
// so in the block.
func cutBlock(block string, limit int) string {
	const cut = "    … (cut)\n"
	if len(block) <= limit {
		return block
	}
	kept := block[:max(limit-len(cut), 0)]
	if at := strings.LastIndex(kept, "\n"); at >= 0 {
		kept = kept[:at+1]
	}
	return strings.ToValidUTF8(kept, "") + cut
}

// before is what was logged shortly before a record, about the same issue
// when it names one.
func (s *fatalSink) before(record logbook.Record) []logbook.Record {
	if s.buffer == nil {
		return nil
	}
	issue := record.Issue()
	from := record.Time.Add(-contextWindow)
	return s.buffer.Select(from, func(r logbook.Record) bool {
		return r.Time.Before(record.Time) && (issue == 0 || r.Issue() == issue)
	}, contextRecords)
}

// fingerprint tells one error from another: its words, and the function that
// logged it - or, for a panic, the function that panicked, since every panic
// is logged from the same place. The function and not its line: a line moves
// with every change above it, and the same error would open a new issue after
// each deploy.
func fingerprint(record logbook.Record) string {
	where, _, _ := strings.Cut(record.Source(), ":")
	if stack, ok := record.Value("stack"); ok {
		if frame := panickingFrame(stack); frame != "" {
			where = frame
		}
	}
	sum := sha256.Sum256([]byte(record.Message + "\x00" + where))
	return hex.EncodeToString(sum[:4])
}

// panickingFrame is the function a stack trace says panicked: the first
// function under the runtime's own panic frame that is not the runtime's -
// a nil map or a nil pointer panics through runtime.mapassign or
// runtime.sigpanic first. A function's line in a trace is its name and its
// arguments; the file under it is indented.
func panickingFrame(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "panic(") {
			continue
		}
		for _, frame := range lines[i+1:] {
			if frame == "" || strings.HasPrefix(frame, "\t") || strings.HasPrefix(frame, "runtime.") {
				continue
			}
			if at := strings.LastIndex(frame, "("); at > 0 {
				frame = frame[:at]
			}
			return strings.TrimSpace(frame)
		}
	}
	return ""
}

// title is the issue's title: the record's words, cut, and the fingerprint,
// which is how the next one finds it. A title notifies nobody, but GitHub
// shows it as plain text, so an at sign is left out rather than written as
// the entity a body uses.
func title(record logbook.Record, print string) string {
	words := logbook.OneLine(logbook.Redact(record.Message))
	if what, ok := record.Value("what"); ok {
		words += ": " + logbook.OneLine(logbook.Redact(what))
	}
	if len(words) > 100 {
		words = strings.ToValidUTF8(words[:100], "") + "…"
	}
	return strings.ReplaceAll(words, "@", "") + " [" + print + "]"
}

// within keeps the times of the last hour.
func within(times []time.Time, now time.Time) []time.Time {
	kept := times[:0]
	for _, at := range times {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	return kept
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
