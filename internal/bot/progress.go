package bot

import (
	"context"

	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
)

// A reply that is written more than once.
//
// The commands about the state of a check (codecheckers/chekhov#22) wrap the
// checks that already exist, and those take real time: `check finish` runs the
// whole catalogue, which asks OpenAlex, ORCID and Zenodo. GitHub has had its
// 202 long before then - the command already runs in a goroutine - but the
// person who typed it is looking at an issue with nothing on it.
//
// So the reply is posted as soon as the first stage has answered, and the
// **same comment** is rewritten as the rest come in. One comment, not two: a
// reader arriving at the thread a week later finds one finished answer rather
// than a running commentary, and the rule that a reply is the record of what
// was done still holds.
//
// A reply path that cannot edit - the command line preview, the test recorder
// - posts nothing early and answers once at the end, exactly as every other
// command does. That is the ordinary path for everything except a deployment,
// so it has to be the one that needs no special case.

// Editing is the part of the reply path that can rewrite a comment the bot
// posted. The deployment's client can, which a type assertion would otherwise
// only discover at run time.
//
// EditReply rather than Edit: a rewritten reply is still a reply, signed with
// the development footer and cut to fit as Comment writes it. Edit writes a
// body exactly, which is what a record needs and what a reply must not get -
// the footer would vanish on the first rewrite, and a long answer would be
// refused rather than cut.
type Editing interface {
	EditReply(ctx context.Context, repository string, comment int64, body string) error
}

var _ Editing = (*github.Client)(nil)

// A reply is where one command's answer goes: a comment posted the first time
// it is written and rewritten every time after.
//
// One object rather than a flag, because "post or edit?" is then decided once,
// at construction, instead of by each caller looking at whether a comment id
// happens to be zero. Every command has one; only a staged one writes to it
// more than once.
type reply struct {
	server *Server
	event  mention
	// editor is the reply path when it can rewrite, nil when it cannot - the
	// command line preview, a test recorder - and then the reply is simply
	// posted once, with whatever it says at the end.
	editor Editing
	// comment is what has been posted so far, 0 before the first write.
	comment int64
}

// replyTo opens the reply for one command.
func (s *Server) replyTo(event mention) *reply {
	to := &reply{server: s, event: event}
	if editor, ok := s.Replies.(Editing); ok && event.Issue > 0 {
		to.editor = editor
	}
	return to
}

// stageable reports whether writing before the answer is finished is worth it:
// without an editor the early body would sit on the issue unfinished for ever.
func (r *reply) stageable() bool { return r != nil && r.editor != nil }

// write puts a body on the issue: posted the first time, and rewritten in
// place after that.
//
// The body must already have been through Server.bodyOf - defused, with the
// follow-up record in front of it if there is one. This does not do it,
// because the record is prepended *after* defusing and doing it again here
// would mangle the record's own marker; staged returns the bodies that have
// not been through it yet.
func (r *reply) write(ctx context.Context, body string) error {
	if r.comment == 0 {
		comment, err := r.server.Replies.Comment(ctx, r.event.Repository, r.event.Issue, body)
		r.comment = comment
		return err
	}
	return r.editor.EditReply(ctx, r.event.Repository, r.comment, body)
}

// A stage is one piece of a staged reply: what it is reading, and the markdown
// it contributes once it has.
//
// A stage with no Name waits for nothing - a checklist, a refusal the command
// already has in hand. It still contributes to the body, but it is not
// something to say the reply is "still reading", and a write posted only to
// announce one would be a request for nothing.
type stage struct {
	// Name is how the reply names this stage while it is still running - "the
	// repository", "the configuration" - so it reads as part of a sentence.
	// Empty for a stage that does no reading.
	Name string
	Run  func(ctx context.Context) string
}

// progress runs the stages of one reply, writing as it goes.
type progress struct {
	server  *Server
	event   mention
	heading string
	stages  []stage
}

// run answers the command, having written along the way.
//
// The body it returns is the finished one, and the reply it belongs to travels
// with it, so the label note the dispatch appends afterwards lands in the same
// comment. The final write is therefore the dispatch's, as it is for every
// other command - there is one place a reply is finished.
func (p *progress) run(ctx context.Context) answered {
	done := make([]command.StageResult, 0, len(p.stages))
	to := p.server.replyTo(p.event)

	// A command that dies part way through has already told the check it is
	// still reading something. Finishing that sentence is this function's job,
	// because nothing else knows a comment is waiting: act's recover reports
	// the panic, and the nightly sweep has no record to chase because the
	// command never reached the point of writing one. The panic goes on
	// afterwards, so it is still reported exactly as before.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if to.comment != 0 {
			if body, err := p.server.bodyOf(said(
				command.StagedReply(p.heading, done, nil) + command.Abandoned())); err == nil {
				if err := to.write(ctx, body); err != nil {
					p.server.Logger.Error("a staged reply was left unfinished on the check",
						"issue", p.event.Issue, "comment", to.comment, "error", err)
				}
			}
		}
		panic(r)
	}()

	for i, current := range p.stages {
		done = append(done, command.StageResult{Name: current.Name, Body: current.Run(ctx)})

		// Nothing left that waits for anything is nothing to announce: the
		// stages that remain are words the command already has, and a write
		// superseded a microsecond later costs a request against a
		// rate-limited endpoint and tells nobody anything.
		pending := pendingNames(p.stages[i+1:])
		if len(pending) == 0 || !to.stageable() {
			continue
		}
		body, err := p.server.bodyOf(said(command.StagedReply(p.heading, done, pending)))
		if err == nil {
			err = to.write(ctx, body)
		}
		if err != nil {
			// Nothing is lost: the stages carry on and the finished answer is
			// written at the end, which is what would have happened without
			// any of this.
			p.server.Logger.Warn("a staged reply could not be written, so the answer waits for the end",
				"issue", p.event.Issue, "comment", to.comment, "error", err)
		}
	}
	return answered{body: command.StagedReply(p.heading, done, nil), to: to}
}

// pendingNames are the stages still to run that are worth waiting for, for the
// line that says so.
func pendingNames(stages []stage) []string {
	var names []string
	for _, remaining := range stages {
		if remaining.Name != "" {
			names = append(names, remaining.Name)
		}
	}
	return names
}
