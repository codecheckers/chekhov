package command

import (
	"strings"
	"testing"
)

// A repository with a sub-directory is written with a pipe, which ends a
// table cell even inside a code span: the live test on testing-dev-register
// #225 showed the row's Repository split in two (#21).
func TestTheRowTableKeepsAPipeInItsCell(t *testing.T) {
	table := RowTable(Row{Repository: RowField{
		Value: "github::codecheckers/testing-dev-register|testdata/register-row", From: "the record"}})
	if !strings.Contains(table, "`github::codecheckers/testing-dev-register\\|testdata/register-row`") {
		t.Errorf("the pipe is not escaped:\n%s", table)
	}
}
