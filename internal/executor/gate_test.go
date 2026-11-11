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
			// Handing a path to the system trash removes it from the sync
			// dir just as surely as os.Rename does (W13), whoever the
			// receiver is — it belongs behind the gate with everything else.
			if sel.Sel.Name == "MoveToTrash" {
				t.Errorf("%s: local-path removal MoveToTrash outside the gate file (spec §7) — route it through localwrite.go",
					fset.Position(call.Pos()))
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

// S013: the symlink net (refuseLinkedPaths) sits at the one place every
// action passes, execute's dispatch, ahead of the switch that runs the
// primitives. Moving or dropping the call fails here, not in the field.
func TestGateExecuteRunsTheLinkCheckFirst(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "executor.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "execute" && fd.Recv != nil {
			fn = fd
		}
	}
	if fn == nil {
		t.Fatal("executor.go has no (*Executor).execute")
	}
	checked := false
	for _, st := range fn.Body.List {
		if _, ok := st.(*ast.SwitchStmt); ok {
			break
		}
		ast.Inspect(st, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "refuseLinkedPaths" {
					checked = true
				}
			}
			return true
		})
	}
	if !checked {
		t.Fatal("execute must call refuseLinkedPaths before dispatching an action (spec §7, S013)")
	}
}

// Every action type is either a local mutation (checked by the symlink net)
// or named here as touching no local path. A new type fails until it picks.
func TestGateClassifiesEveryActionType(t *testing.T) {
	noLocalWrite := map[string]bool{
		"Upload": true, "UpdateRemote": true, "MoveRemote": true, "TrashRemote": true, "Forget": true,
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../reconcile/types.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	local := map[string]bool{}
	for typ := range localMutations {
		local[string(typ)] = true
	}
	n := 0
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Type" {
				continue
			}
			for i, name := range vs.Names {
				n++
				lit := strings.Trim(vs.Values[i].(*ast.BasicLit).Value, `"`)
				if local[lit] == noLocalWrite[name.Name] {
					t.Errorf("action type %s must be exactly one of: in localMutations (localwrite.go), or listed here as writing no local path", name.Name)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("found no reconcile.Type constants; the parse is wrong")
	}
}
