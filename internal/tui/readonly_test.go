package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// bannedCalls are the calls that would make the dashboard something other than
// a viewer. `statedb.Open` migrates the schema and takes a writable handle,
// which a read-only command must never do while the daemon holds the instance
// lock (spec §14, R5); the os mutators and MoveToTrash would make a *view*
// capable of changing the user's files.
//
// Structural, in the spirit of the executor's local-write gate: a reviewer can
// forget, the parser cannot.
var bannedCalls = map[string][]string{
	"statedb": {"Open"},
	"os": {
		"Create", "OpenFile", "WriteFile", "Truncate", "Remove", "RemoveAll",
		"Rename", "Mkdir", "MkdirAll", "Chmod", "Chtimes", "Link", "Symlink",
	},
	"trash": {"MoveToTrash"},
}

func TestDashboardIsReadOnlyByConstruction(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
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
			if !ok {
				return true
			}
			for _, banned := range bannedCalls[pkg.Name] {
				if sel.Sel.Name == banned {
					t.Errorf("%s: %s.%s is banned in the dashboard — it renders state, it never changes it",
						fset.Position(call.Pos()), pkg.Name, banned)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source files checked — the guard would pass vacuously")
	}
}

// TestGuardCatchesAPlantedViolation proves the guard above can fail, so a green
// run means something. It plants a banned call in a temporary file in this
// directory and re-runs the same inspection.
func TestGuardCatchesAPlantedViolation(t *testing.T) {
	planted := "zz_planted_violation.go"
	src := "package tui\n\nimport \"os\"\n\nfunc plantedViolation() { os.Remove(\"/tmp/x\") }\n"
	if err := os.WriteFile(planted, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(planted) })

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, planted, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
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
		if !ok {
			return true
		}
		for _, banned := range bannedCalls[pkg.Name] {
			if sel.Sel.Name == banned {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Error("the read-only guard did not catch a planted os.Remove")
	}
}
