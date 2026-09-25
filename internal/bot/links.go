package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
)

// `check links`: which links around a check no longer answer (chekhov#12).

// linksTimeout is how long `check links` may take in all. There is no cap on
// how many links it asks, and each is asked within check's own per-link limit,
// a few at a time; this bounds the whole, so that a certificate with a
// thousand links answers with what it reached rather than never.
const linksTimeout = 20 * time.Minute

// linksReplyMargin is kept back from the run for posting the answer: asking
// links until the last second would leave no time to say what they answered.
const linksReplyMargin = 30 * time.Second

// linksWorthANotice is how many links make the command say that it has
// started. A handful answer in seconds, and a notice replaced a moment later
// is noise in the thread; past this it can be minutes.
const linksWorthANotice = 5

// links collects the links around a check, says it has started when there
// are enough of them to take a while, asks each, and answers - in the same
// comment as the notice, through the reply a staged command writes to, so one
// command leaves one comment. See progress.go.
func (s *Server) links(ctx context.Context, event mention, configuration check.Context, services *check.Services) answered {
	links := check.CollectLinks(ctx, configuration, s.issueSource(ctx, event))

	to := s.replyTo(event)
	if to.stageable() && links.Len() > linksWorthANotice {
		body, err := s.bodyOf(said(links.Started()))
		if err == nil {
			err = to.write(ctx, body)
		}
		if err != nil {
			// Not worth giving up over: the answer is written at the end.
			s.Logger.Warn("the links notice could not be posted", "issue", event.Issue, "error", err)
		}
	}

	probing := ctx
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		probing, cancel = context.WithDeadline(ctx, deadline.Add(-linksReplyMargin))
		defer cancel()
	}
	links.Probe(probing, services)
	return answered{body: links.Markdown(), to: to}
}

// issueSource is the check issue as `check links` reads it. A mention on no
// issue - the command line preview - has none to read, and the answer says
// nothing about one; an issue that cannot be read is named in the answer.
func (s *Server) issueSource(ctx context.Context, event mention) check.LinkSource {
	if event.Issue <= 0 {
		return check.LinkSource{}
	}
	name := fmt.Sprintf("issue #%d", event.Issue)
	issue, err := s.issue(ctx, event)
	if err != nil {
		return check.LinkSource{Name: name, Err: err}
	}
	return check.LinkSource{Name: name, Text: issue.Title + "\n" + issue.Body}
}
