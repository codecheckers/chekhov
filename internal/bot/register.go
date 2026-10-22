package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
)

// The register row a check would get (#21): `check register` builds it and
// runs the rules on it, `set repository` and `set venue` record what the issue
// does not say.
//
// Each column comes from the first of three places that has it: what the
// command was given, what the record says an editor set, and what the issue
// says - its Repository line and its labels, read as CC-REG-008 reads them, so
// that a row filled in from the labels is a row the rule passes.

// Registering is what `register` needs of the reply path: the pull request
// adding the row. A path without it - the preview - judges the row and opens
// nothing.
type Registering interface {
	ProposeRow(ctx context.Context, repository string, proposal github.RowProposal) (github.RowPullRequest, error)
}

var _ Registering = (*github.Client)(nil)

// registerRow is `check register` and `register`: the row, the rules on it,
// and - for `register`, when no rule failed - the pull request adding it.
func (s *Server) registerRow(ctx context.Context, event mention, parsed command.Command,
	services *check.Services, propose bool) answered {
	// Before 0.14.0 `check register <certificate>` ran the register rules on
	// a row already there; that is `check certificate` now, and a certificate
	// is never a venue.
	for _, arg := range parsed.Args {
		if check.IsCertificateID(arg) {
			return said(fmt.Sprintf("`%s %s` builds this check's row; the rules on a row "+
				"already in `register.csv` are `%s check %s %s`.\n", command.Bot, parsed.Name,
				command.Bot, check.Certificate, arg))
		}
	}
	// The arguments are the row's, not a target: about is asked without them.
	about, refusal := s.about(ctx, event, command.Command{Name: parsed.Name}, services, true)
	if refusal != "" {
		return said(refusal)
	}
	given := rowArguments(parsed.Args)
	if given.venue != "" {
		// A comment is prose, and `check register please` must not register
		// the venue "please": a venue given here is one venues.csv lists, in
		// its spelling. An unlisted one is what `set venue` is for.
		venue, listed, err := services.Venue(given.venue)
		if err == nil && !listed {
			return said(fmt.Sprintf("%s is no venue `venues.csv` lists, so I have not read it as one. "+
				"`%s set venue` records a venue it does not list, which `CC-REG-005` then warns about.\n",
				command.Code(given.venue), command.Bot))
		}
		given.venue = venue
	}
	row := s.rowFor(about, given, event.Issue, services)
	if !row.Complete() {
		return said(command.RowIncomplete(row))
	}

	proposed := check.RegisterRow{
		Certificate: row.Certificate.Value, Repository: row.Repository.Value,
		Type: row.Type.Value, Venue: row.Venue.Value, Issue: row.Issue.Value,
	}
	target := proposed.Repository
	loaded := s.loading(target, services)
	// mismatch is the refusal when the configuration carries another
	// identifier than the one reserved, empty when it carries that one or
	// could not be read - which the configuration stage reports already.
	mismatch := sync.OnceValue(func() string {
		context, err := loaded()
		if err != nil {
			return ""
		}
		if configured := strings.TrimSpace(context.Config.Certificate); configured != proposed.Certificate {
			return command.CertificateMismatch(configured, proposed.Certificate)
		}
		return ""
	})
	// failed is whether a rule stopped the pull request: a configuration that
	// cannot be read, or a rule failing as an error. A warning does not; the
	// editor who merges reads it. The stages run in order, one after another,
	// which is progress's contract and what makes this safe to share.
	failed := false
	judged := func(report check.Report, err error) {
		failed = failed || err != nil || !report.OK()
	}
	stages := []stage{
		fixed(command.RowTable(row)),
		{Name: "the configuration", Run: func(ctx context.Context) string {
			return s.judging(loaded, target, "config", judged)(ctx) + mismatch()
		}},
		{Name: "the register", Run: func(context.Context) string {
			loadedContext, err := loaded()
			if err != nil || mismatch() != "" {
				return ""
			}
			loadedContext.Row = &proposed
			report, err := check.RunPart(loadedContext, "", false, check.Certificate)
			judged(report, err)
			return command.CheckSummary(summarised(check.Certificate, target, report, err))
		}},
	}
	if propose {
		stages = append(stages, stage{Name: "the pull request", Run: func(ctx context.Context) string {
			if failed || mismatch() != "" {
				return command.NoPullRequest()
			}
			return s.proposeRow(ctx, event, row, proposed)
		}})
	}
	return (&progress{server: s, event: event, heading: command.RegisterHeading, stages: stages}).run(ctx)
}

// proposeRow opens the pull request, or says why it did not.
func (s *Server) proposeRow(ctx context.Context, event mention, row command.Row, proposed check.RegisterRow) string {
	writer, ok := s.Replies.(Registering)
	if !ok {
		return "… I cannot open a pull request from here: ask me on the checks issue.\n"
	}
	pull, err := writer.ProposeRow(ctx, event.Repository, github.RowProposal{
		Row:     proposed.Cells(),
		Title:   command.PullRequestTitle(proposed.Certificate),
		Message: command.CommitMessage(proposed.Certificate, event.Issue),
		// Defused as every comment is: the row holds what the issue and the
		// record say, and nothing the bot writes may look like a record.
		Body: command.Defuse(command.PullRequestBody(row, event.Issue, event.Author)),
	})
	switch {
	case errors.Is(err, github.ErrAlreadyRegistered):
		return command.AlreadyRegistered(proposed.Certificate)
	case errors.Is(err, github.ErrBranchNotOurs):
		return command.BranchNotOurs(proposed.Certificate)
	case err != nil:
		// The API's own words, URLs and all, are for the log, not the thread.
		s.Logger.Warn("the register pull request could not be opened", "issue", event.Issue, "error", err)
		return command.PullRequestFailed()
	}
	return command.PullRequestOpened(pull.Number, pull.URL, pull.Updated, pull.Unchanged, event.Author, time.Now())
}

// rowFor builds the row as far as it can, and says where each column came
// from or why it is empty.
func (s *Server) rowFor(about whatTheBotKnows, given rowGiven, issue int, services *check.Services) command.Row {
	record := about.reading.Record
	typed, named := fromLabels(about, services)
	return command.Row{
		Certificate: firstOf(
			command.RowField{Value: record.Certificate, From: "the record"},
			command.RowField{Missing: fmt.Sprintf("this check has reserved none: `%s set certificate`", command.Bot)}),
		Repository: firstOf(
			command.RowField{Value: given.repository, From: "given"},
			command.RowField{Value: record.Repository, From: "the record"},
			repositoryLine(about, services)),
		Type: firstOf(
			command.RowField{Value: given.venueType, From: "given"},
			command.RowField{Value: record.Type, From: "the record"},
			typed),
		Venue: firstOf(
			command.RowField{Value: given.venue, From: "given"},
			command.RowField{Value: record.Venue, From: "the record"},
			named),
		Issue: command.RowField{Value: strconv.Itoa(issue), From: "this issue"},
	}
}

// fromLabels is what the issue's labels say of Type and Venue, or why they
// say nothing and how to say it instead.
func fromLabels(about whatTheBotKnows, services *check.Services) (typed, named command.RowField) {
	fix := fmt.Sprintf("; `%s set venue <type> <venue>`, or label the issue", command.Bot)
	if !about.labelsRead {
		why := "I could not read this issue's labels" + fix
		return command.RowField{Missing: why}, command.RowField{Missing: why}
	}
	labelled, err := services.RowLabels(about.issue.Labels)
	switch len(labelled.Types) {
	case 1:
		typed = command.RowField{Value: strings.ToLower(labelled.Types[0]),
			From: "the label " + command.Code(labelled.Types[0])}
	case 0:
		typed.Missing = "the issue carries no type label" + fix
	default:
		typed.Missing = "the issue carries the type labels " + command.CodeList(labelled.Types) + fix
	}
	switch {
	case err != nil:
		named.Missing = fmt.Sprintf("I could not read `venues.csv`: %s", err)
	case len(labelled.Venues) == 1:
		named = command.RowField{Value: labelled.Venues[0], From: "the labels"}
	case len(labelled.Venues) == 0:
		named.Missing = "no label names a venue in `venues.csv`" + fix
	default:
		named.Missing = "the labels name " + command.CodeList(labelled.Venues) + fix
	}
	return typed, named
}

// repositoryLabel is the label of the line both register issue templates ask
// for the repository on.
const repositoryLabel = "**Repository**:"

// repositoryLine is the repository the issue's Repository line names, which
// both register issue templates have. A line naming no repository the bot can
// read is as good as none; an issue the bot could not read is not.
func repositoryLine(about whatTheBotKnows, services *check.Services) command.RowField {
	if !about.labelsRead {
		return command.RowField{Missing: fmt.Sprintf(
			"I could not read this issue: `%s set repository github::owner/repo`", command.Bot)}
	}
	for _, line := range templateLines(about.issue.Body) {
		rest, found := templateField(line, repositoryLabel)
		if !found {
			continue
		}
		for _, word := range linkWords(rest) {
			if spec := repositorySpecOf(word, services); spec != "" {
				return command.RowField{Value: spec, From: "the issue's Repository line"}
			}
		}
	}
	return command.RowField{Missing: fmt.Sprintf(
		"the issue has no Repository line I can read: `%s set repository github::owner/repo`", command.Bot)}
}

// repositorySpecOf is a word as register.csv writes a repository, empty when
// the word is not one. A certificate identifier is a target for `check`, but
// not a repository for a row.
func repositorySpecOf(word string, services *check.Services) string {
	target := check.TargetIn(word)
	if target == "" || check.IsCertificateID(target) {
		return ""
	}
	spec, err := check.ResolveTarget(target, services)
	if err != nil {
		return ""
	}
	return spec.String()
}

// rowGiven is what a command said of the row.
type rowGiven struct{ repository, venueType, venue string }

// rowArguments reads `[<repository>] [<type>] [<venue>]`, in any order: a
// word that is a repository is the repository, a Type written as the Type
// column writes it - lowercase - is the type, and the rest, joined, is the
// venue. That needs no quoting: `Journal of Open Source Software` is a venue,
// and `journal JOSS` a type and a venue.
func rowArguments(args []string) rowGiven {
	var given rowGiven
	var venue []string
	for _, arg := range args {
		if given.repository == "" {
			if spec := repositorySpecOf(arg, nil); spec != "" {
				given.repository = spec
				continue
			}
		}
		if given.venueType == "" && check.IsVenueType(arg) && arg == strings.ToLower(arg) {
			given.venueType = arg
			continue
		}
		venue = append(venue, arg)
	}
	given.venue = strings.Join(venue, " ")
	return given
}

// firstOf is the first field that has a value, or else the last, which says
// why there is none.
func firstOf(fields ...command.RowField) command.RowField {
	for _, field := range fields {
		if field.Value != "" {
			return field
		}
	}
	return fields[len(fields)-1]
}

// setRepository records the repository the row names.
func (s *Server) setRepository(ctx context.Context, event mention, parsed command.Command) string {
	if reply, ok := s.noCheck(event, "a register row"); !ok {
		return reply
	}
	spec := ""
	if len(parsed.Args) == 1 {
		spec = repositorySpecOf(parsed.Args[0], nil)
	}
	if spec == "" {
		return fmt.Sprintf("Usage: `%s set repository <repository>`, a repository the way `register.csv` "+
			"names one (`github::owner/repo`), a link to it, or `owner/repo`.\n", command.Bot)
	}
	return s.setRow(ctx, event, func(record *people.Record) bool {
		changed := record.Repository != spec
		record.Repository = spec
		return changed
	})
}

// setVenue records the row's Type and Venue, or the Venue alone.
func (s *Server) setVenue(ctx context.Context, event mention, parsed command.Command,
	services *check.Services) string {
	if reply, ok := s.noCheck(event, "a register row"); !ok {
		return reply
	}
	given := rowArguments(parsed.Args)
	if given.venue == "" || given.repository != "" {
		return fmt.Sprintf("Usage: `%s set venue [<type>] <venue>`, the venue as `venues.csv` names it.\n",
			command.Bot)
	}
	venue, listed, err := services.Venue(given.venue)
	// The type goes with the venue: a venue set alone leaves the type to the
	// labels, rather than to whatever an earlier `set venue` said of another.
	reply := s.setRow(ctx, event, func(record *people.Record) bool {
		changed := record.Venue != venue || record.Type != given.venueType
		record.Venue, record.Type = venue, given.venueType
		return changed
	})
	switch {
	case err != nil:
		reply += fmt.Sprintf("\nI could not read `venues.csv`, so I have recorded %s as written, "+
			"without checking its spelling: %s\n", command.Code(venue), err)
	case !listed:
		reply += fmt.Sprintf("\n%s is not in `venues.csv`, which `CC-REG-005` will warn about "+
			"until it is added there.\n", command.Code(venue))
	}
	return reply
}

// setRow changes the row in the record and says what it now holds. change
// says whether it changed anything, so that setting what is already set
// writes nothing.
func (s *Server) setRow(ctx context.Context, event mention, change func(*people.Record) bool) string {
	record, err := s.Checks.Update(ctx, event.Repository, event.Issue,
		func(record people.Record) (people.Record, error) {
			if !change(&record) {
				return record, people.ErrUnchanged
			}
			return record, nil
		})
	switch {
	case errors.Is(err, people.ErrUnchanged):
		return "The record already says so.\n" + command.RowLine(record.Holders())
	case errors.Is(err, people.ErrTampered):
		return tamperedReply(err)
	case err != nil:
		return fmt.Sprintf("I could not record it: %s\n", err)
	}
	return command.RowSet(record.Holders(), event.Author)
}
