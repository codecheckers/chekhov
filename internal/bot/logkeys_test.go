package bot

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryIssueInTheLogNamesItsRepository walks the deployment's packages and
// fails on a log call that names an issue without its repository: the lists
// number their issues too, so an issue alone could be the register's or the
// lists', and `log` and the error reports would give a check another issue's
// records (#73; logbook.Record.About).
func TestEveryIssueInTheLogNamesItsRepository(t *testing.T) {
	files := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(filepath.Join("..", "..", "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !logLevels[selector.Sel.Name] {
				return true
			}
			keys := map[string]bool{}
			for _, arg := range call.Args {
				if literal, ok := arg.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					keys[strings.Trim(literal.Value, `"`)] = true
				}
			}
			if keys["issue"] {
				checked++
				if !keys["repository"] {
					t.Errorf("%s: a log line names an issue and not its repository", files.Position(call.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("only %d log lines name an issue; the walk is looking in the wrong place", checked)
	}
}

// logLevels are the slog methods a record is written with.
var logLevels = map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true}
