package bot

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// issues is the register's issues, closed ones included, and what the bot did
// to them.
type issues struct {
	*issueReader
	labelled []string
	titled   []string
}

func (r *issues) AllIssues(context.Context, string) ([]github.Issue, error) { return r.issues, nil }

func (r *issues) AddLabel(_ context.Context, _ string, number int, label string) error {
	r.labelled = append(r.labelled, fmt.Sprintf("#%d %s", number, label))
	for i := range r.issues {
		if r.issues[i].Number == number {
			r.issues[i].Labels = append(r.issues[i].Labels, label)
		}
	}
	return nil
}

func (r *issues) SetTitle(_ context.Context, _ string, number int, title string) error {
	r.titled = append(r.titled, fmt.Sprintf("#%d %s", number, title))
	for i := range r.issues {
		if r.issues[i].Number == number {
			r.issues[i].Title = title
		}
	}
	return nil
}

// year is this year's identifier with the given number: the commands work in
// the year they are asked in.
func year(number int) string { return fmt.Sprintf("%d-%03d", time.Now().UTC().Year(), number) }

// registered wires a server to a register.csv holding the year's first three
// identifiers, and to issues: #1 is the check the commands are asked on,
// #10 holds an identifier by its title and label, #11 is closed and holds one
// all the same, #12 mentions one without the label, and #13 is labelled with
// nothing in its title.
func registered(t *testing.T) (*Server, *issues, *testserver.Server) {
	t.Helper()
	server, replies := testServer(t)
	stub := testserver.New(t)
	stub.Text("/raw/"+server.Settings.TargetRepository()+"/HEAD/register.csv",
		"Certificate,Repository,Type,Venue,Issue\n"+
			year(1)+",github::codecheckers/demo,journal,GigaScience,2\n"+
			year(2)+",github::codecheckers/demo,journal,GigaScience,3\n"+
			year(3)+",github::codecheckers/demo,journal,GigaScience,4\n")
	server.Services = &check.Services{Access: check.Access{
		HTTP:       stub.Client(),
		Register:   server.Settings.TargetRepository(),
		RawContent: stub.URL + "/raw",
	}}
	register := &issues{issueReader: &issueReader{recorder: replies, issues: []github.Issue{
		{Number: 1, Title: "Baetzel"},
		{Number: 10, Title: "Dewi et al | " + year(5), Labels: []string{"id assigned"}},
		{Number: 11, Title: "Abandoned | " + year(6), Labels: []string{"ID Assigned"}, Closed: true},
		{Number: 12, Title: "Tidying up after " + year(9)},
		{Number: 13, Title: "Ruining Dong, Daniel Cameron", Labels: []string{"id assigned"}},
	}}}
	server.Replies = register
	return server, register, stub
}

// The launch-pad's measurement: register.csv alone stops short of what the
// checks in progress hold, and an abandoned check's number is not free.
func TestNextCertificateReadsTheRegisterAndTheIssues(t *testing.T) {
	server, _, _ := registered(t)

	reply := answer(t, server, command.Next, []string{"certificate"}, "")
	for _, want := range []string{
		"The next certificate identifier is `" + year(7) + "`",
		"`" + year(6) + "`, on #11",
		"#12 has `" + year(9) + "` in its title",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

func TestSetCertificateRecordsTitlesAndLabels(t *testing.T) {
	server, register, _ := registered(t)

	reply := answer(t, server, command.Set, []string{"certificate"}, "")
	if !strings.Contains(reply, "`"+year(7)+"` is now the certificate identifier") {
		t.Fatalf("the next identifier was not set:\n%s", reply)
	}
	if want := "#1 Baetzel | " + year(7); len(register.titled) != 1 || register.titled[0] != want {
		t.Errorf("titled %v, want %q", register.titled, want)
	}
	if len(register.labelled) != 1 || register.labelled[0] != "#1 id assigned" {
		t.Errorf("labelled %v", register.labelled)
	}
	if !strings.Contains(reply, "— set by `@nuest`") {
		t.Errorf("the reply is not the record of the change:\n%s", reply)
	}

	reading, err := server.Checks.Read(context.Background(), server.Settings.TargetRepository(), 1)
	if err != nil || reading.Record.Certificate != year(7) || !reading.Signed() {
		t.Errorf("the record holds %+v (%v)", reading, err)
	}

	// Asking again changes nothing, and says so.
	reply = answer(t, server, command.Set, []string{"certificate", year(7)}, "")
	if !strings.Contains(reply, "is already the certificate identifier") || strings.Contains(reply, "— set by") {
		t.Errorf("a repeat was not answered as one:\n%s", reply)
	}
	if len(register.titled) != 1 || len(register.labelled) != 1 {
		t.Errorf("a repeat wrote to the issue again: %v %v", register.titled, register.labelled)
	}
}

func TestChangingTheCertificateSaysWhatItWas(t *testing.T) {
	server, register, _ := registered(t)
	answer(t, server, command.Set, []string{"certificate", year(7)}, "")

	reply := answer(t, server, command.Set, []string{"certificate", year(8)}, "")
	if !strings.Contains(reply, "Replaces `"+year(7)+"`") {
		t.Errorf("the change does not say what it replaced:\n%s", reply)
	}
	if last := register.titled[len(register.titled)-1]; last != "#1 Baetzel | "+year(8) {
		t.Errorf("the title's identifier was not replaced: %q", last)
	}
}

// An identifier an editor put in the title by hand, before the bot kept a
// record, is what a change replaces.
func TestChangingAHandSetCertificateSaysWhatItWas(t *testing.T) {
	server, register, _ := registered(t)
	register.issues[0].Title = "Baetzel | " + year(4)
	register.issues[0].Labels = []string{"id assigned"}

	reply := answer(t, server, command.Set, []string{"certificate", year(7)}, "")
	if !strings.Contains(reply, "Replaces `"+year(4)+"`") {
		t.Errorf("the hand-set identifier is not named:\n%s", reply)
	}
	if len(register.labelled) != 0 {
		t.Errorf("a label the issue has was added again: %v", register.labelled)
	}
}

func TestSetCertificateRefusesATakenIdentifier(t *testing.T) {
	server, register, _ := registered(t)

	for id, want := range map[string]string{
		year(2): "`" + year(2) + "` is taken: it is in `register.csv`.",
		year(6): "`" + year(6) + "` is taken: it is on #11.",
	} {
		reply := answer(t, server, command.Set, []string{"certificate", id}, "")
		if !strings.Contains(reply, want) || !strings.Contains(reply, "CC-REG-001") ||
			!strings.Contains(reply, "does not already exist in register.csv") {
			t.Errorf("%s was not refused with the rule:\n%s", id, reply)
		}
	}
	if len(register.titled)+len(register.labelled) != 0 {
		t.Errorf("a refusal wrote to the issue: %v %v", register.titled, register.labelled)
	}
}

func TestSetCertificateAsksBeforeAJump(t *testing.T) {
	server, register, _ := registered(t)
	id := year(16)

	reply := answer(t, server, command.Set, []string{"certificate", id}, "")
	for _, want := range []string{
		"is 10 past the year's previous identifier, `" + year(6) + "`, on #11",
		"CC-REG-002",
		"`@chekhovbot set certificate " + id + " anyway`",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, reply)
		}
	}
	if len(register.titled) != 0 {
		t.Fatalf("a refused jump retitled the issue: %v", register.titled)
	}

	reply = answer(t, server, command.Set, []string{"certificate", id, "anyway"}, "")
	if !strings.Contains(reply, "is now the certificate identifier") ||
		!strings.Contains(reply, "Confirmed with `anyway`") {
		t.Errorf("a confirmed jump was not set, or not said to be one:\n%s", reply)
	}
}

func TestSetCertificateIsForEditors(t *testing.T) {
	server, register, _ := registered(t)

	reply := server.answer(context.Background(), mention{
		Repository: server.Settings.TargetRepository(), Issue: 1, Author: "a-codechecker",
	}, command.Command{Name: command.Set, Args: []string{"certificate"}}).body
	if !strings.Contains(reply, "is for") || len(register.titled) != 0 {
		t.Errorf("a codechecker set an identifier:\n%s", reply)
	}
}

func TestSetCertificateRefusesArgumentsItDoesNotKnow(t *testing.T) {
	server, _, _ := registered(t)
	for _, args := range [][]string{{"venue"}, {"certificate", "26-1"}, {"certificate", year(7), year(8)}} {
		if reply := answer(t, server, command.Set, args, ""); !strings.Contains(reply, "Usage") {
			t.Errorf("%v was not refused:\n%s", args, reply)
		}
	}
}

// Half the claims is a refusal, not a guess: a number proposed from
// register.csv alone is how two checks end up with one.
func TestTheCertificateCommandsRefuseWithoutTheRegister(t *testing.T) {
	server, register, stub := registered(t)
	stub.Status("/raw/"+server.Settings.TargetRepository()+"/HEAD/register.csv", http.StatusNotFound)

	for _, name := range []command.Name{command.Next, command.Set} {
		reply := answer(t, server, name, []string{"certificate"}, "")
		if !strings.Contains(reply, "could not read `register.csv`") {
			t.Errorf("%s did not refuse:\n%s", name, reply)
		}
	}
	if len(register.titled)+len(register.labelled) != 0 {
		t.Errorf("a refusal wrote to the issue: %v %v", register.titled, register.labelled)
	}

	server.Replies = register.issueReader.recorder
	if reply := answer(t, server, command.Next, []string{"certificate"}, ""); !strings.Contains(reply,
		"cannot read the issues") {
		t.Errorf("next answered without the issues:\n%s", reply)
	}
}

// The identifier shares the record with the roles, so writing either keeps
// the other.
func TestTheCertificateAndTheRolesKeepEachOther(t *testing.T) {
	server, _, _ := registered(t)
	answer(t, server, command.Set, []string{"certificate"}, "")
	answer(t, server, command.Assign, []string{"a-codechecker", "as", "codechecker"}, "")

	reading, err := server.Checks.Read(context.Background(), server.Settings.TargetRepository(), 1)
	if err != nil || reading.Record.Certificate != year(7) || reading.Record.AssignedCodechecker != "a-codechecker" {
		t.Errorf("the record holds %+v (%v)", reading.Record, err)
	}
	if !reading.Signed() {
		t.Errorf("the record does not verify: %v", reading.Tampered)
	}
}

// Without an identifier, a check keeps the one it holds: the command is also
// how an editor has the bot record a number given by hand, or put back a
// label somebody removed, and must not move the check to a new number.
func TestSetCertificateWithoutAnIdentifierKeepsTheOneHeld(t *testing.T) {
	server, register, _ := registered(t)
	register.issues[0].Title = "Baetzel | " + year(4)
	register.issues[0].Labels = []string{"id assigned"}

	reply := answer(t, server, command.Set, []string{"certificate"}, "")
	if !strings.Contains(reply, "`"+year(4)+"` is now the certificate identifier") ||
		strings.Contains(reply, "Replaces") {
		t.Fatalf("the hand-set identifier was not kept:\n%s", reply)
	}

	// Somebody removes the label; the record holds the identifier now.
	register.issues[0].Labels = nil
	reply = answer(t, server, command.Set, []string{"certificate"}, "")
	if !strings.Contains(reply, "`"+year(4)+"` is already the certificate identifier") ||
		!strings.Contains(reply, "Label `id assigned` added") {
		t.Errorf("the label was not put back on the identifier held:\n%s", reply)
	}
}

// Two editors on two checks at once are handed two numbers, not one.
func TestTwoReservationsAtOnceGetTwoNumbers(t *testing.T) {
	server, register, _ := registered(t)
	register.issues = append(register.issues, github.Issue{Number: 2, Title: "Another"})
	server.Checks.Comments = &threads{bot: server.Settings.BotUser(), by: map[int][]people.Comment{}}

	var wg sync.WaitGroup
	for _, issue := range []int{1, 2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.answer(context.Background(), mention{
				Repository: server.Settings.TargetRepository(), Issue: issue, Author: "nuest",
			}, command.Command{Name: command.Set, Args: []string{"certificate"}})
		}()
	}
	wg.Wait()

	if len(register.titled) != 2 {
		t.Fatalf("titled %v", register.titled)
	}
	first, second := strings.Fields(register.titled[0]), strings.Fields(register.titled[1])
	if first[len(first)-1] == second[len(second)-1] {
		t.Errorf("both checks were given the same identifier: %v", register.titled)
	}
}

// threads is the comments of several issues, each its own, for a test where
// two checks are written at once.
type threads struct {
	mu   sync.Mutex
	bot  string
	by   map[int][]people.Comment
	next int64
}

func (c *threads) Comments(_ context.Context, _ string, issue int) ([]people.Comment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]people.Comment(nil), c.by[issue]...), nil
}

func (c *threads) Post(_ context.Context, _ string, issue int, body string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	c.by[issue] = append(c.by[issue], people.Comment{ID: c.next, Author: c.bot, Body: body})
	return c.next, nil
}

func (c *threads) Edit(_ context.Context, _ string, comment int64, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, thread := range c.by {
		for i := range thread {
			if thread[i].ID == comment {
				thread[i].Body = body
			}
		}
	}
	return nil
}

// The identifier shares the record with the roles, so an edited record stops
// `set certificate` as it stops `assign` - and the refusal says so in the
// record's own terms, rather than sending an editor to adopt "roles" to get
// a certificate identifier through.
func TestAnEditedRecordStopsSetCertificate(t *testing.T) {
	server, register, _ := registered(t)
	answer(t, server, command.Set, []string{"certificate", year(7)}, "")
	comments := server.Checks.Comments.(*issueComments)
	comments.comments[0].Body = strings.Replace(comments.comments[0].Body, year(7), year(4), 1)

	reply := answer(t, server, command.Set, []string{"certificate", year(8)}, "")
	for _, want := range []string{"record of this check was edited", "certificate identifier", "accept record"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, reply)
		}
	}
	if last := register.titled[len(register.titled)-1]; strings.HasSuffix(last, year(8)) {
		t.Errorf("an edited record did not stop the title change: %q", last)
	}
}

// The preview answers `next certificate` from the register's issues, and
// cannot reserve anything: its reply path reads the issues and has no way to
// write to one, whatever command asks.
func TestThePreviewReadsTheClaimsAndWritesNothing(t *testing.T) {
	server, register, _ := registered(t)
	server.Replies = readOnly{register}

	if reply := answer(t, server, command.Next, []string{"certificate"}, ""); !strings.Contains(reply,
		"The next certificate identifier is `"+year(7)+"`") {
		t.Errorf("the preview did not answer next certificate:\n%s", reply)
	}
	if reply := answer(t, server, command.Set, []string{"certificate"}, ""); !strings.Contains(reply,
		"I cannot change an issue from here") {
		t.Errorf("the preview did not refuse to reserve:\n%s", reply)
	}
	if len(register.titled)+len(register.labelled) != 0 {
		t.Errorf("the preview wrote to an issue: %v %v", register.titled, register.labelled)
	}

	var replies Poster = readOnly{register}
	if _, err := replies.Comment(context.Background(), "", 1, "hello"); err == nil {
		t.Error("the preview posted a comment")
	}
	for name, writes := range map[string]bool{
		"Reserving":    is[Reserving](replies),
		"Assigner":     is[Assigner](replies),
		"Organisation": is[Organisation](replies),
	} {
		if writes {
			t.Errorf("the preview's reply path is a %s, and could write through it", name)
		}
	}
}

func is[T any](value any) bool { _, ok := value.(T); return ok }
