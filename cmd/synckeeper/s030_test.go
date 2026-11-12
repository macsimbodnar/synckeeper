package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/engine"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// S030.12 — a name conflict, as the planner reports it, prints with both
// names in `sync`'s and `init`'s output.
func TestS030NameConflictPrintsBothNames(t *testing.T) {
	_, skips := reconcile.Plan(reconcile.Input{
		Base:           map[string]reconcile.BaseItem{"Readme.txt": {FileID: "B", Size: 1, MD5: "mb", MtimeNS: 1, DriveMD5: "mb", DriveVersion: 1}},
		Local:          map[string]reconcile.LocalItem{"Readme.txt": {Size: 1, MtimeNS: 1, MD5: "mb"}},
		Remote:         map[string]reconcile.RemoteItem{"README.txt": {FileID: "A", Size: 1, MD5: "ma", Version: 1}},
		ShadowedRemote: map[string]bool{"B": true},
		CaseFold:       true,
	})
	res := &engine.Result{Skips: skips}
	for name, show := range map[string]func(){
		"sync": func() { printResult(res, false) },
		"init": func() { printMergeResult(res) },
	} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout := os.Stdout
		os.Stdout = w
		show()
		w.Close()
		os.Stdout = stdout
		raw, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.Contains(l, "skipped: README.txt (name conflict") {
				line = l
			}
		}
		if line == "" || !strings.Contains(line, "Readme.txt") {
			t.Errorf("[%s] no skipped line naming README.txt and Readme.txt:\n%s", name, raw)
		}
	}
}
