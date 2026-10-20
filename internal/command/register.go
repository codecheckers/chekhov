package command

import (
	"fmt"
	"strings"
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
		fmt.Fprintf(&table, "| %s | %s | %s |\n", rowColumns[i], value, from)
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
	return fmt.Sprintf("… The `codecheck.yml` says certificate %s, and this check reserved %s, "+
		"so I have not run the register rules: put the reserved identifier in the configuration, "+
		"or `%s set certificate` the one it carries.\n",
		certificateOrNone(configured), Code(reserved), Bot)
}

// RowSet is the reply to `set repository` and `set venue`: what the record now
// says of the row, and that the rest is still read from the issue.
func RowSet(holders Holders, by string) string {
	return fmt.Sprintf("Recorded for the register row, as `@%s` asked.\n%s\n"+
		"A column not set here is read from the issue when the row is built: "+
		"`%s check register` shows it.\n", by, RowLine(holders), Bot)
}
