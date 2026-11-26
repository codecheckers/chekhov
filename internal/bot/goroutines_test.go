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

// TestEveryGoroutineRecovers walks every package of the deployment and fails
// on a go statement whose function does not defer a recover - directly, or
// through internal/safe, or through one of the bot's own recovering helpers.
func TestEveryGoroutineRecovers(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	packages := map[string][]*ast.File{}
	files := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		packages[filepath.Dir(path)] = append(packages[filepath.Dir(path)], file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) < 5 {
		t.Fatalf("found %d packages under %s; the walk is looking in the wrong place", len(packages), root)
	}

	found := 0
	for dir, parsed := range packages {
		if filepath.Base(dir) == "safe" {
			continue
		}
		declared := map[string]*ast.FuncDecl{}
		for _, file := range parsed {
			for _, decl := range file.Decls {
				if function, ok := decl.(*ast.FuncDecl); ok && function.Body != nil {
					declared[function.Name.Name] = function
				}
			}
		}
		for _, file := range parsed {
			ast.Inspect(file, func(node ast.Node) bool {
				statement, ok := node.(*ast.GoStmt)
				if !ok {
					return true
				}
				found++
				where := files.Position(statement.Pos())
				if body := goroutineBody(statement.Call.Fun, declared); body == nil || !defersRecover(body) {
					t.Errorf("%s: a goroutine that does not recover; start it with safe.Go, "+
						"or defer safe.Recover or one of the bot's recover helpers first", where)
				}
				return true
			})
		}
	}
	if found == 0 {
		t.Fatal("no go statement found at all; the walk is broken")
	}
}

// goroutineBody is the body a go statement runs, when it can be read: a
// function literal, or a function or method of the same package by name.
func goroutineBody(fun ast.Expr, declared map[string]*ast.FuncDecl) *ast.BlockStmt {
	if literal, ok := fun.(*ast.FuncLit); ok {
		return literal.Body
	}
	if decl := declared[calleeName(fun)]; decl != nil {
		return decl.Body
	}
	return nil
}

// calleeName is the name a call is made by: f, or the f of x.f.
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// defersRecover reports whether a body defers, at its top level, a call that
// recovers: a function whose name says it does, or a literal calling recover.
func defersRecover(body *ast.BlockStmt) bool {
	for _, statement := range body.List {
		deferred, ok := statement.(*ast.DeferStmt)
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.ToLower(calleeName(deferred.Call.Fun)), "recover") {
			return true
		}
		switch f := deferred.Call.Fun.(type) {
		case *ast.FuncLit:
			calls := false
			ast.Inspect(f.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recover" {
						calls = true
					}
				}
				return !calls
			})
			if calls {
				return true
			}
		}
	}
	return false
}
