package executor

// The enforcement half of the remote-write gate (spec §7, S010): no file in
// this package other than remotewrite.go may call a Drive mutation on an
// existing item — Update, Trash or Move on a Drive client. Every call there
// carries the plan's expected Drive state; a call anywhere else would act
// on whatever Drive holds at that moment. Same shape as gate_test.go.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

var gatedDriveCalls = map[string]bool{"Update": true, "Trash": true, "Move": true}

func TestGateNoDriveMutationOutsideRemotewrite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "remotewrite.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !gatedDriveCalls[sel.Sel.Name] {
				return true
			}
			// The receiver is a Drive client when it is named like one:
			// x.Client, or a bare client parameter.
			switch recv := sel.X.(type) {
			case *ast.SelectorExpr:
				if recv.Sel.Name != "Client" {
					return true
				}
			case *ast.Ident:
				if !strings.EqualFold(recv.Name, "client") {
					return true
				}
			default:
				return true
			}
			t.Errorf("%s: Drive mutation %s outside the remote gate file (spec §7) — route it through remotewrite.go",
				fset.Position(call.Pos()), sel.Sel.Name)
			return true
		})
	}
}
