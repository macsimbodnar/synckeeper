package executor

// The enforcement half of the local-write gate (spec §7): no file in this
// package other than localwrite.go may call a raw filesystem-mutating
// stdlib function. Helpers without the mechanical test are how R7 shipped
// after W1 was declared done — the discipline lives here, not in review.
// Test files are excluded: they build fixtures; the gate governs production
// mutations.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// bannedOSCalls: the os-package functions that mutate a path. os.MkdirAll /
// os.Mkdir (non-destructive creation) and os.CreateTemp (unique fresh name)
// are deliberately not banned; everything that can overwrite, truncate, or
// remove existing content is.
var bannedOSCalls = map[string]bool{
	"Rename":    true,
	"Remove":    true,
	"RemoveAll": true,
	"Create":    true,
	"OpenFile":  true,
	"WriteFile": true,
	"Truncate":  true,
	"Chtimes":   true,
	"Link":      true,
	"Symlink":   true,
}

func TestGateNoRawFSMutationOutsideLocalwrite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "localwrite.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// Resolve the local name of the "os" import (it could be aliased).
		osName := ""
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "os" {
				continue
			}
			osName = "os"
			if imp.Name != nil {
				osName = imp.Name.Name
			}
		}
		if osName == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != osName || !bannedOSCalls[sel.Sel.Name] {
				return true
			}
			t.Errorf("%s: raw local-path mutation os.%s outside the gate file (spec §7) — route it through localwrite.go",
				fset.Position(call.Pos()), sel.Sel.Name)
			return true
		})
	}
}
