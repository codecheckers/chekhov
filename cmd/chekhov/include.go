package main

import (
	stdcontext "context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/codecheckers/chekhov/config"
	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/registration"
)

// `chekhov include check` is `@chekhovbot include check` from the command
// line: the same registration.Check on a row read from an issue, a list file
// or a line typed out, so that an editor can try a row - or every row of a
// list - without posting anything (codecheckers/chekhov#49).

const includeUsage = `chekhov include check [--online] [--as <handle>] <input>

The input is one of:
  an issue URL      https://github.com/owner/repo/issues/N - its first post,
                    or on a registration the bot opened, the newest row of the
                    person its signed record names; reading it implies --online
  a CSV file        every row, the list's header skipped, each checked as its
                    own registrant and not against the lists, which it may be
  a CSV line        one row, typed as the argument
  -                 a row, rows, or an issue body, on stdin

--as names the registrant, who otherwise is the issue's author or the row's
own handle. A single row is compared with the lists - an issue's with its own
repository's - and several rows are not. Without --online, ORCID and the
lists are reported as not checked, never as fine. The team notes the bot adds
are not given: reading a team needs the bot's own token. Exits non-zero when
a registration needs something.
`

// toCheck is one registration to check: where it was read from, the text its
// row is in, whose it is, and the lists to compare it with - none for a row
// read from a list. unread says why there is nothing to check.
type toCheck struct {
	where, body, author string
	lists               []string
	unread              string
}

func runInclude(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("include takes check:\n\n%s", includeUsage)
	}
	as, online, input := "", false, ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--as":
			if i+1 >= len(args) {
				return fmt.Errorf("--as needs a GitHub handle")
			}
			i++
			as = strings.TrimPrefix(args[i], "@")
		case "--online":
			online = true
		default:
			if input != "" {
				return fmt.Errorf("include check takes one input, and has %q and %q - quote a CSV line "+
					"that has spaces in it\n\n%s", input, args[i], includeUsage)
			}
			input = args[i]
		}
	}
	if input == "" {
		return fmt.Errorf("include check needs an input:\n\n%s", includeUsage)
	}
	settings, err := config.Current()
	if err != nil {
		return err
	}
	// An issue is on GitHub, so reading one is online, as a repository is
	// for `check`.
	var services *check.Services
	if _, _, isIssue := check.IssueURL(input); online || isIssue {
		services = check.Online()
	}
	ctx := stdcontext.Background()
	registrations, err := readRegistrations(ctx, services, settings.BotUser(), settings.ListFiles(), input, as)
	if err != nil {
		return err
	}
	return checkRegistrations(ctx, services, registrations, out)
}

// readRegistrations reads what an input holds: one registration from an
// issue, a body or a line, one per row from a CSV file.
//
// lists are the settings' lists. A single row is compared with them, and a
// row from an issue with the same files in the issue's own repository: an
// issue on codecheckers/codecheckers is about its lists, whatever this
// checkout's settings name. Several rows are a list, and are not.
func readRegistrations(ctx stdcontext.Context, services *check.Services, bot string, lists []string,
	input, as string) ([]toCheck, error) {
	if repository, number, ok := check.IssueURL(input); ok {
		found, err := fromIssue(ctx, services, bot, repository, number)
		if err != nil {
			return nil, err
		}
		found.lists = make([]string, len(lists))
		for i, list := range lists {
			found.lists[i] = services.RawFile(repository, path.Base(list))
		}
		if as != "" {
			found.author = as
		}
		return []toCheck{found}, nil
	}

	text, where, compared := input, "the line", lists
	info, statErr := os.Stat(input)
	switch {
	case input == "-":
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		text, where = string(raw), "stdin"
	case statErr == nil && !info.IsDir():
		raw, err := os.ReadFile(input)
		if err != nil {
			return nil, err
		}
		text, where, compared = string(raw), input, nil
	case !strings.Contains(input, ","):
		// No row has fewer than eight commas, so this was meant as a file.
		return nil, fmt.Errorf("%s is not a file, an issue URL, or a CSV line", input)
	}
	// Markdown with the row in a csv block is a registration issue's body.
	if strings.Contains(text, "```") {
		return []toCheck{{where: where, body: text, author: as, lists: compared}}, nil
	}
	rows, err := registration.Rows(text)
	if err != nil {
		return nil, fmt.Errorf("reading %s as CSV: %w", where, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s holds no row", where)
	}
	if len(rows) > 1 {
		compared = nil
	}
	registrations := make([]toCheck, len(rows))
	for i, row := range rows {
		registrations[i] = toCheck{where: where, body: registration.Body(row), author: as, lists: compared}
		if len(rows) > 1 {
			registrations[i].where = fmt.Sprintf("%s, row %d", where, i+1)
		}
	}
	return registrations, nil
}

// fromIssue is the registration an issue holds: its author's first post, or
// on one the bot opened, the newest comment with a row by the person the
// bot's signed record names - read as the bot reads it, so that the answer
// is the one the bot would give.
func fromIssue(ctx stdcontext.Context, services *check.Services, bot, repository string, number int) (toCheck, error) {
	where := fmt.Sprintf("%s#%d", repository, number)
	issue, comments, err := services.Issue(ctx, repository, number)
	if err != nil {
		return toCheck{}, fmt.Errorf("reading %s: %w", where, err)
	}
	if !strings.EqualFold(issue.Author, bot) {
		return toCheck{where: where, body: issue.Body, author: issue.Author}, nil
	}

	found := toCheck{where: where}
	verifier, err := people.NewSigner("", recordKeys...)
	if err != nil {
		return toCheck{}, err
	}
	for _, comment := range comments {
		if !strings.EqualFold(comment.Author, bot) {
			continue
		}
		record, err := followup.Parse(comment.Body, where, verifier)
		if err != nil || record.Kind != followup.KindRegistration {
			continue
		}
		if found.author != "" && !strings.EqualFold(found.author, record.Subject) {
			found.unread = "this issue carries records for two different people, so I read neither"
			return found, nil
		}
		found.author = record.Subject
	}
	if found.author == "" {
		found.unread = "I opened this issue, and it carries no record of whom it is for that I can trust"
		return found, nil
	}
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if !strings.EqualFold(comment.Author, found.author) {
			continue
		}
		if row, _ := registration.Parse(comment.Body); row != (registration.Row{}) {
			found.body = comment.Body
			break
		}
	}
	return found, nil
}

// recordKeys are the public keys the bot's records are signed with, as
// docs/record-keys.pub publishes them - the testing register's and the
// register's - so that a record read here is verified as the bot verifies
// it. TestRecordKeysAreThePublishedOnes holds the two together.
var recordKeys = []string{
	"ynujENA0gGdTDk7uIBtT42qn/abwZU3/6233YZrzgig=",
	"gwhsgP7baDiZZKCC6JA5M3zP+gyFOeH/I0CvpuhtSIY=",
}

// checkRegistrations checks each registration and says what it needs, and
// fails when any needs something. Four at a time, each asking ORCID, and
// written out in their order.
func checkRegistrations(ctx stdcontext.Context, services *check.Services, registrations []toCheck, out io.Writer) error {
	answers := make([]string, len(registrations))
	complete := make([]bool, len(registrations))
	var wait sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, found := range registrations {
		wait.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wait.Done() }()
			answers[i], complete[i] = answer(ctx, services, found)
		}()
	}
	wait.Wait()

	incomplete := 0
	for i, text := range answers {
		fmt.Fprint(out, text)
		if !complete[i] {
			incomplete++
		}
	}
	if len(registrations) > 1 {
		fmt.Fprintf(out, "\n%d of %d registrations have everything the list needs\n",
			len(registrations)-incomplete, len(registrations))
	}
	if incomplete > 0 {
		return fmt.Errorf("%d of %d registrations need something", incomplete, len(registrations))
	}
	return nil
}

// answer is what one registration needs, and whether it is complete.
func answer(ctx stdcontext.Context, services *check.Services, found toCheck) (string, bool) {
	if found.unread != "" {
		return fmt.Sprintf("%s: I could not read this registration: %s\n", found.where, found.unread), false
	}
	author := found.author
	if author == "" {
		// Nobody else to vouch for it: the row is its own registrant's.
		row, _ := registration.Parse(found.body)
		author = row.Login()
	}
	world := registration.World{Author: author, ORCID: services.ORCIDProfile}
	if len(found.lists) > 0 {
		world.Lists = func() []registration.List { return registration.ReadLists(services, found.lists...) }
	}
	report := registration.Check(ctx, found.body, world)

	var text strings.Builder
	verdict := "has everything the list needs"
	if !report.Complete() {
		verdict = "needs something"
	}
	who := "nobody"
	if author != "" {
		who = "@" + author
	}
	fmt.Fprintf(&text, "%s, %s: %s\n", found.where, who, verdict)
	for _, finding := range report.Findings {
		fmt.Fprintf(&text, "  %s %s\n", finding.Outcome, finding.Text)
	}
	return text.String(), report.Complete()
}
