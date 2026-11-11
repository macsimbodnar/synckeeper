package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/doctor"
)

// S026: held rows print as their own section, each with the scan's reason,
// and never under "rows whose local file is missing".
func TestS026DoctorPrintsHeldRows(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	printReport(&doctor.Report{
		TrackedItems: 3,
		MissingLocal: []string{"gone.txt"},
		HeldLocal: []doctor.HeldPath{
			{RelPath: "dir", Reason: "symlink; not followed"},
			{RelPath: "dir/b.txt", Reason: "under dir (symlink; not followed)"},
		},
	}, false)
	w.Close()
	os.Stdout = stdout
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	for _, want := range []string{
		"rows whose local file is missing (1):\n  gone.txt\n",
		"rows on disk but not read by the scan (held) (2):\n" +
			"  dir: symlink; not followed\n" +
			"  dir/b.txt: under dir (symlink; not followed)\n" +
			"  sync leaves these alone on both sides until each is a readable plain file or folder again.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
