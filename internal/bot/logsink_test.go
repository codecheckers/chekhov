package bot

import (
	"context"
	"errors"
	"io"
	stdlog "log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/logbook"
)

// logRepository is the made-up log repository of these tests.
const logRepository = "codecheckers/made-up-log"

// fakeLog is the log repository: what was opened, commented, and whether
// GitHub says it is private.
type fakeLog struct {
	mu       sync.Mutex
	private  bool
	askErr   error
	asked    int
	issues   []github.Issue
	bodies   map[int][]string
	labels   map[int][]string
	comments int
	// hold, when set, keeps every write waiting until it is closed.
	hold chan struct{}
}

func newFakeLog() *fakeLog {
	return &fakeLog{private: true, bodies: map[int][]string{}, labels: map[int][]string{}}
}

func (f *fakeLog) Private(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.private, f.askErr
}

func (f *fakeLog) OpenLabelledIssues(_ context.Context, repository, label string) ([]github.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []github.Issue
	for _, issue := range f.issues {
		if command.HasLabel(issue.Labels, label) && !issue.Closed {
			found = append(found, issue)
		}
	}
	return found, nil
}

func (f *fakeLog) CreateIssue(_ context.Context, repository, title, body string, labels []string) (int, error) {
	if f.hold != nil {
		<-f.hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	number := len(f.issues) + 1
	f.issues = append(f.issues, github.Issue{Number: number, Title: title, Labels: labels})
	f.bodies[number] = append(f.bodies[number], body)
	f.labels[number] = labels
	return number, nil
}

func (f *fakeLog) Comment(_ context.Context, repository string, issue int, body string) (int64, error) {
	if f.hold != nil {
		<-f.hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments++
	f.bodies[issue] = append(f.bodies[issue], body)
	return int64(f.comments), nil
}

func (f *fakeLog) opened() []github.Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]github.Issue(nil), f.issues...)
}

// logged is a logger that tees into a buffer and a sink, as Serve wires it.
func logged(t *testing.T, writer logWriter, environment string) (*slog.Logger, *fatalSink, *logbook.Buffer) {
	t.Helper()
	buffer := logbook.NewBuffer()
	tee := logbook.NewHandler(slog.NewTextHandler(io.Discard, nil), buffer)
	deployment := command.Deployment{Version: "9.9.9", Environment: environment, Register: "codecheckers/made-up-register"}
	sink := newFatalSink(logRepository, deployment, writer, buffer, slog.New(slog.DiscardHandler))
	tee.Attach(sink)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sink.Close(ctx)
	})
	return slog.New(tee), sink, buffer
}

func drain(t *testing.T, sink *fatalSink) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sink.Close(ctx)
}

// An error opens one issue, labelled for its deployment and `fatal`, with the
// records before it about the same issue; the same error again is a comment
// on it; a different error is an issue of its own.
func TestAnErrorOpensOneIssueAndRepeatsComment(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")

	logger.Info("answered", "issue", 12, "author", "made-up-editor")
	logger.Info("answered", "issue", 99)
	for range 3 {
		logger.Error("the answer could not be posted", "issue", 12, "error", "502 Bad Gateway")
	}
	logger.Error("the help page could not be rendered", "error", "template")
	drain(t, sink)

	issues := log.opened()
	if len(issues) != 2 {
		t.Fatalf("opened %d issues, want 2: %+v", len(issues), issues)
	}
	first := issues[0]
	if strings.Join(log.labels[first.Number], ",") != "development,fatal" {
		t.Errorf("labels %v, want development and fatal", log.labels[first.Number])
	}
	if !strings.HasPrefix(first.Title, "the answer could not be posted [") {
		t.Errorf("title %q", first.Title)
	}
	bodies := log.bodies[first.Number]
	if len(bodies) != 3 {
		t.Fatalf("%d posts on the first issue, want the issue and two comments", len(bodies))
	}
	if !strings.Contains(bodies[0], "made-up-editor") || strings.Contains(bodies[0], "issue=99") {
		t.Errorf("the report should carry issue 12's records and not issue 99's:\n%s", bodies[0])
	}
	if !strings.Contains(bodies[2], "the 3rd time") {
		t.Errorf("the second repeat does not count:\n%s", bodies[2])
	}
	if counts := sink.Counts(); counts.Posted != 4 || counts.Dropped != 0 || counts.Failed != 0 {
		t.Errorf("counts %+v", counts)
	}
}

// Nothing is written to a repository GitHub does not call private, and the
// question is asked once.
func TestAPublicLogRepositoryIsNeverWritten(t *testing.T) {
	log := newFakeLog()
	log.private = false
	logger, sink, _ := logged(t, log, "production")
	logger.Error("one")
	logger.Error("two")
	drain(t, sink)

	if len(log.opened()) != 0 || log.comments != 0 {
		t.Fatalf("wrote to a public repository: %+v", log.opened())
	}
	if log.asked != 1 || !sink.Counts().Refused {
		t.Errorf("asked %d times, counts %+v; want one question and refused", log.asked, sink.Counts())
	}
}

// A question that fails is asked again, and nothing is written meanwhile.
func TestAnUnansweredPrivacyQuestionWritesNothing(t *testing.T) {
	log := newFakeLog()
	log.askErr = errors.New("503")
	logger, sink, _ := logged(t, log, "production")
	logger.Error("one")
	logger.Error("two")
	drain(t, sink)
	if len(log.opened()) != 0 || log.asked != 2 {
		t.Errorf("opened %d, asked %d; want nothing opened and the question asked twice", len(log.opened()), log.asked)
	}
}

// A deployment takes its own label's issue and never the other's, though
// both have the same fingerprint.
func TestADeploymentKeepsToItsLabel(t *testing.T) {
	log := newFakeLog()
	record := logbook.Record{Message: "the answer could not be posted"}
	print := fingerprint(record)
	log.issues = []github.Issue{{Number: 1, Title: "x [" + print + "]", Labels: []string{"production", "fatal"}}}

	logger, sink, _ := logged(t, log, "live-test")
	logger.Error("the answer could not be posted")
	drain(t, sink)

	issues := log.opened()
	if len(issues) != 2 || len(log.bodies[1]) != 0 {
		t.Fatalf("a development deployment wrote to production's issue: %+v %v", issues, log.bodies)
	}
	if !command.HasLabel(issues[1].Labels, "development") {
		t.Errorf("a live test is development: %v", issues[1].Labels)
	}
}

// At most sinkNewPerHour new issues in an hour; the rest are counted.
func TestTheSinkOpensFewIssuesAnHour(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	for i := range sinkNewPerHour + 5 {
		logger.Error("different", "n", i, "what", strings.Repeat("x", i))
		// Different words, so different fingerprints.
		logger.Error("different " + strings.Repeat("y", i+1))
	}
	drain(t, sink)
	if got := len(log.opened()); got != sinkNewPerHour {
		t.Errorf("opened %d, want %d", got, sinkNewPerHour)
	}
	if sink.Counts().Limited == 0 {
		t.Errorf("counts %+v, want some limited", sink.Counts())
	}
}

// Logging never waits on GitHub: with every write held, a burst returns at
// once, the queue keeps what fits and the rest is counted as dropped.
func TestABurstNeverBlocks(t *testing.T) {
	log := newFakeLog()
	log.hold = make(chan struct{})
	logger, sink, _ := logged(t, log, "development")

	started := time.Now()
	for i := range 200 {
		logger.Error("burst", "n", i)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("200 errors took %s to log", took)
	}
	counts := sink.Counts()
	if counts.Dropped < 200-sinkQueue-1 || counts.Queued > sinkQueue+1 {
		t.Errorf("counts %+v, want at most the queue taken and the rest dropped", counts)
	}
	close(log.hold)
	drain(t, sink)
	if got := len(log.opened()); got != 1 {
		t.Errorf("the one fingerprint opened %d issues", got)
	}
}

// Close gives up at its deadline rather than holding the process.
func TestCloseKeepsToItsDeadline(t *testing.T) {
	log := newFakeLog()
	log.hold = make(chan struct{})
	defer close(log.hold)
	logger, sink, _ := logged(t, log, "development")
	logger.Error("held")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	sink.Close(ctx)
	if took := time.Since(started); took > time.Second {
		t.Errorf("Close took %s past a 50ms deadline", took)
	}
	logger.Error("after close")
	if sink.Counts().Dropped == 0 {
		t.Error("a record after Close was not counted as dropped")
	}
}

// A panic's report carries its stack, and its fingerprint is where it
// panicked rather than where every panic is logged.
func TestAPanicIsReportedWithItsStack(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "production")
	server, _ := testServer(t)
	server.Logger = logger
	func() {
		defer server.recoverBackground("A made-up panic.")
		panic("made up")
	}()
	drain(t, sink)

	issues := log.opened()
	if len(issues) != 1 {
		t.Fatalf("opened %+v", issues)
	}
	body := log.bodies[1][0]
	for _, want := range []string{"made up", "stack:", "logsink_test.go", "production"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report is missing %q:\n%s", want, body)
		}
	}
	if !strings.HasPrefix(issues[0].Title, "panicked: A made-up panic.") {
		t.Errorf("title %q", issues[0].Title)
	}
}

// What a title says can mention nobody.
func TestATitleMentionsNobody(t *testing.T) {
	got := title(logbook.Record{Message: "panicked", Attrs: []logbook.Attr{{Key: "what", Value: "asked by `@made-up-editor`"}}}, "abcd")
	if strings.Contains(got, "@") {
		t.Errorf("title %q holds an at sign", got)
	}
}

// With a log repository, the public admin issue says only where the details
// are: not the panic value, not the trace. The log repository has both.
func TestWithALogRepositoryTheAdminIssueSaysOnlyWhere(t *testing.T) {
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "99")
	server, replies := testServer(t)
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	server.Logger, server.logSink = logger, sink

	event := mention{Repository: server.Settings.TargetRepository(), Issue: 7, Author: "a-codechecker"}
	panicIn(server, event, "the-made-up-panic-value")
	drain(t, sink)

	report := replies.comments[0]
	if report.Issue != 99 {
		t.Fatalf("first comment on #%d, want the admin issue", report.Issue)
	}
	for _, absent := range []string{"the-made-up-panic-value", "goroutine", ".go:"} {
		if strings.Contains(report.Body, absent) {
			t.Errorf("the public admin issue says %q:\n%s", absent, report.Body)
		}
	}
	if !strings.Contains(report.Body, server.Settings.LogRepository()) {
		t.Errorf("the admin issue does not say where the details are:\n%s", report.Body)
	}
	if bodies := log.bodies[1]; len(bodies) != 1 || !strings.Contains(bodies[0], "the-made-up-panic-value") {
		t.Errorf("the log repository did not get the panic: %v", log.bodies)
	}
}

// A handler that panics is recovered by net/http, which logs it through
// ErrorLog; listen points that at the log, so it reaches the sink.
func TestAPanickingHandlerReachesTheSink(t *testing.T) {
	log := newFakeLog()
	buffer := logbook.NewBuffer()
	tee := logbook.NewHandler(slog.NewTextHandler(io.Discard, nil), buffer)
	deployment := command.Deployment{Version: "9.9.9", Environment: "development"}
	sink := newFatalSink(logRepository, deployment, log, buffer, slog.New(slog.DiscardHandler))
	tee.Attach(sink)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("a made-up handler panic")
	}))
	server.Config.ErrorLog = stdlog.New(httpErrors{slog.New(tee)}, "", 0)
	server.Start()
	defer server.Close()
	if response, err := http.Get(server.URL); err == nil {
		response.Body.Close()
	}
	drain(t, sink)

	if issues := log.opened(); len(issues) != 1 || !strings.Contains(log.bodies[1][0], "a made-up handler panic") {
		t.Errorf("the handler's panic did not reach the log repository: %+v %v", issues, log.bodies)
	}
}

// Two different panics are two issues: the fingerprint is where each
// panicked, past the runtime's own frames.
func TestTwoPanicsAreTwoIssues(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	server, _ := testServer(t)
	server.Logger = logger
	nilMap := func() {
		defer server.recoverBackground("A nil map.")
		var m map[string]int
		m["x"] = 1
	}
	nilPointer := func() {
		defer server.recoverBackground("A nil pointer.")
		var p *mention
		_ = p.Issue
	}
	nilMap()
	nilPointer()
	nilMap()
	drain(t, sink)

	issues := log.opened()
	if len(issues) != 2 {
		t.Fatalf("opened %d issues, want one per panic: %+v", len(issues), issues)
	}
	if len(log.bodies[1]) != 2 {
		t.Errorf("the nil map panicked twice, so its issue should have one comment: %v", log.bodies[1])
	}
}

// A record too long for GitHub is cut to fit rather than never reported.
func TestATooLongReportIsCutToFit(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	page := strings.Repeat("<p>a proxy's page</p>\n", 6000)
	logger.Info("before", "issue", 4, "body", page)
	logger.Error("the answer could not be posted", "issue", 4, "error", page)
	drain(t, sink)
	bodies := log.bodies[1]
	if len(bodies) != 1 || len(bodies[0]) > github.MaxCommentLength-1000 {
		t.Fatalf("the report is %d long, want it under GitHub's limit", len(bodies[0]))
	}
	if !strings.Contains(bodies[0], "(cut)") {
		t.Error("the report does not say it was cut")
	}
}

// What net/http says of a client is a Warn and never reaches the sink; a
// handler's panic is an Error and does.
func TestOnlyAHandlersPanicIsAnError(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	writer := httpErrors{logger}
	writer.Write([]byte("http: URL query contains semicolon, which is no longer a supported separator\n"))
	writer.Write([]byte("http: panic serving 127.0.0.1:1234: boom\n"))
	drain(t, sink)
	issues := log.opened()
	if len(issues) != 1 || !strings.HasPrefix(issues[0].Title, "http: panic serving") {
		t.Errorf("opened %+v, want only the panic", issues)
	}
}

// A carriage return in a value cannot end the block.
func TestACarriageReturnStaysInTheBlock(t *testing.T) {
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	logger.Error("odd", "value", "one\r@made-up-editor\rtwo")
	drain(t, sink)
	for _, line := range strings.FieldsFunc(log.bodies[1][0], func(r rune) bool { return r == '\n' || r == '\r' }) {
		if strings.Contains(line, "made-up-editor") && !strings.HasPrefix(line, "    ") {
			t.Errorf("the handle is outside the block: %q", line)
		}
	}
}

// Once the sink has found the repository public, the admin issue says what it
// said before there was one, and not that the details are somewhere they are
// not.
func TestARefusedSinkLeavesTheAdminIssueAsItWas(t *testing.T) {
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "99")
	server, replies := testServer(t)
	log := newFakeLog()
	log.private = false
	logger, sink, _ := logged(t, log, "production")
	server.Logger, server.logSink = logger, sink
	server.Deployment.Environment = "production"
	logger.Error("first, to find out")
	for waited := time.Duration(0); !sink.Counts().Refused; waited += time.Millisecond {
		if waited > 2*time.Second {
			t.Fatal("the sink never found the repository public")
		}
		time.Sleep(time.Millisecond)
	}

	panicIn(server, mention{Repository: server.Settings.TargetRepository(), Issue: 7, Author: "a-codechecker"}, "a-value")
	report := replies.comments[0].Body
	if strings.Contains(report, server.Settings.LogRepository()) || !strings.Contains(report, "deployment's log") {
		t.Errorf("the admin issue points at a repository that took nothing:\n%s", report)
	}
}

// `panic` is an editor's test of the error reports: an editor's panics, and
// is heard of on the issue, on the admin issue - as a pointer only - and in
// the log repository; anybody else is refused, and nothing panics.
func TestPanicIsAnEditorsTestOfTheReports(t *testing.T) {
	t.Setenv("CHEKHOV_ADMIN_ISSUE", "99")
	server, replies := testServer(t)
	log := newFakeLog()
	logger, sink, _ := logged(t, log, "development")
	server.Logger, server.logSink = logger, sink
	event := mention{Repository: server.Settings.TargetRepository(), Issue: 260}

	event.Author = "a-codechecker"
	if reply := server.answer(context.Background(), event, command.Command{Name: command.Panic}).body; !strings.Contains(reply, "for editors") {
		t.Errorf("a codechecker was not refused: %s", reply)
	}

	event.Author = "nuest"
	server.act(event, command.Command{Name: command.Panic})
	<-server.done
	drain(t, sink)

	if len(replies.comments) != 2 {
		t.Fatalf("%d comments, want the admin issue's and the apology", len(replies.comments))
	}
	admin, apology := replies.comments[0], replies.comments[1]
	if admin.Issue != 99 || !strings.Contains(admin.Body, server.Settings.LogRepository()) || strings.Contains(admin.Body, "a panic asked for") {
		t.Errorf("the admin issue should get the pointer and not the value: #%d %q", admin.Issue, admin.Body)
	}
	if apology.Issue != 260 || apology.Body != command.PanicApology(command.Panic) {
		t.Errorf("the apology on #%d is %q", apology.Issue, apology.Body)
	}
	issues := log.opened()
	if len(issues) != 1 || !command.HasLabel(issues[0].Labels, "fatal") {
		t.Fatalf("opened %+v, want one fatal issue", issues)
	}
	if body := log.bodies[issues[0].Number][0]; !strings.Contains(body, "a panic asked for by an editor on codecheckers/testing-dev-register#260") {
		t.Errorf("the report does not carry the bot's own value:\n%s", body)
	}
}

// A preview of `panic` is the apology, not the end of the command line.
func TestAPreviewOfPanicIsTheApology(t *testing.T) {
	server, _ := testServer(t)
	event := mention{Repository: server.Settings.TargetRepository(), Author: "nuest", Body: "@chekhovbot panic"}
	if got := previewOne(server, event, command.Command{Name: command.Panic}); got != command.PanicApology(command.Panic) {
		t.Errorf("the preview answered %q", got)
	}
}
