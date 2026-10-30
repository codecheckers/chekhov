package command

import (
	"fmt"
	"strings"
	"time"
)

// The register row a check would get, as `check register` shows it (#21).

// RegisterHeading is the first line of the staged reply to `check register`.
const RegisterHeading = "The register row for this check"

// A RowField is one column of the row: its value and where that came from, or
// why there is none and how to give one.
type RowField struct {
	Value string
	// From says where the value came from, as the end of "from ...": "the
	// record", "the label `check-nl`".
	From string
	// Missing says why there is no value, and what would give it one.
	Missing string
}

// A Row is the register row as far as it could be built.
type Row struct {
	Certificate, Repository, Type, Venue, Issue RowField
}

// Complete reports whether every column has a value.
func (r Row) Complete() bool {
	for _, field := range r.fields() {
		if field.Value == "" {
			return false
		}
	}
	return true
}

func (r Row) fields() []RowField {
	return []RowField{r.Certificate, r.Repository, r.Type, r.Venue, r.Issue}
}

// RowTable shows the row, each column with where it came from, so a value
// read off a label is seen to be one before it is in a pull request.
func RowTable(row Row) string {
	var table strings.Builder
	table.WriteString("| Column | Value | From |\n|---|---|---|\n")
	for i, field := range row.fields() {
		value, from := "—", field.Missing
		if field.Value != "" {
			value, from = Code(field.Value), field.From
		}
		// A repository with a sub-directory is written with a pipe, which
		// would end the cell, code span or not.
		fmt.Fprintf(&table, "| %s | %s | %s |\n", rowColumns[i], escapePipes(value), escapePipes(from))
	}
	return table.String()
}

// rowColumns are the columns of register.csv a row fills, in Row's order.
var rowColumns = []string{"Certificate", "Repository", "Type", "Venue", "Issue"}

// RowIncomplete is the reply when a column could not be filled: the row as
// far as it goes, and nothing run, because rules judging half a row would
// report the missing half as though it were the finding.
func RowIncomplete(row Row) string {
	return fmt.Sprintf("%s\n%s\n\nI have run no rules: a row with a column missing is not one to judge.\n",
		RegisterHeading, RowTable(row))
}

// CertificateMismatch is the stage that refuses to judge a row whose
// certificate is not the one the configuration carries: the register would
// name one identifier and point at a `codecheck.yml` that says another.
func CertificateMismatch(configured, reserved string) string {
	return fmt.Sprintf("\n… The `codecheck.yml` says certificate %s, and this check reserved %s, "+
		"so I have not run the register rules: put the reserved identifier in the configuration, "+
		"or `%s certificate set` the one it carries.\n",
		certificateOrNone(configured), Code(reserved), Bot)
}

// RowSet is the reply to `repository set` and `venue set`: what the record now
// says of the row, and that the rest is still read from the issue.
func RowSet(holders Holders, by string) string {
	return fmt.Sprintf("Recorded for the register row, as %s asked.\n%s\n"+
		"A column not set here is read from the issue when the row is built: "+
		"`%s check register` shows it.\n", mention(by), RowLine(holders), Bot)
}

// NoPullRequest is the last stage of `register` when an earlier one stopped
// it.
func NoPullRequest() string {
	return "\n… I have opened no pull request: the row needs what is above put right first. " +
		"A warning does not stop it; an error, or a configuration I could not read or that carries " +
		"another certificate, does.\n"
}

// PullRequestTitle is the pull request's title and the commit's subject.
func PullRequestTitle(certificate string) string {
	return "add " + certificate
}

// CommitMessage is the commit's, which closes the checks issue when the pull
// request is merged.
func CommitMessage(certificate string, issue int) string {
	return fmt.Sprintf("%s, closes #%d", PullRequestTitle(certificate), issue)
}

// PullRequestBody is what the pull request says: the row, the checks issue it
// closes, and who asked - in a code span, so that nobody is notified.
func PullRequestBody(row Row, issue int, by string) string {
	return fmt.Sprintf("Adds the register row for #%d, as %s asked with `%s register`.\n\n%s\n"+
		"An editor reviews and merges it; I merge nothing. Merging closes #%d.\n",
		issue, mention(by), Bot, RowTable(row), issue)
}

// PullRequestOpened is the reply once the pull request is there, and the
// record of who asked for it. updated says it was there already, unchanged
// that it already proposed exactly this and nothing was written.
func PullRequestOpened(number int, url string, updated, unchanged bool, by string, when time.Time) string {
	did := "Opened the pull request adding this row"
	switch {
	case unchanged:
		did = "The pull request already proposes this row, so I have changed nothing"
	case updated:
		did = "Brought up to date the pull request adding this row"
	}
	return fmt.Sprintf("\n✔ %s: #%d, %s.\n", did, number, url) + entry("asked", by, when)
}

// PullRequestFailed is the reply when GitHub would not take a write: what it
// said is in the log, which is no place for a thread.
func PullRequestFailed() string {
	return "\n… I could not open the pull request: GitHub refused or failed a request on the way, " +
		"and what it said is in my log. Ask me again; if it fails twice, it needs somebody who can read the log.\n"
}

// AlreadyRegistered is the refusal for a certificate the register file has a
// row for already, found on the default branch at the moment of writing.
func AlreadyRegistered(certificate string) string {
	return fmt.Sprintf("\n… `register.csv` already has a row for %s, so I have opened no pull request. "+
		"`%s check certificate %s` runs the rules on it.\n", Code(certificate), Bot, certificate)
}

// BranchNotOurs is the refusal when the branch the bot would write has
// somebody else's commits on it: an editor's fix that a reset would throw
// away.
func BranchNotOurs(certificate string) string {
	return fmt.Sprintf("\n… The branch `register-%s` has commits that are not mine, so I have left it "+
		"alone and opened no pull request: finish or delete that branch, and ask me again.\n", certificate)
}
