package conflicts

import (
	"testing"
	"time"
)

func TestPath(t *testing.T) {
	ts := time.Date(2026, 7, 8, 14, 22, 0, 0, time.UTC)
	cases := []struct{ in, want string }{
		{"notes.md", "notes (conflict max_desktop 2026-07-08_142200).md"},
		{"dir/sub/report.tar.gz", "dir/sub/report.tar (conflict max_desktop 2026-07-08_142200).gz"},
		{"noext", "noext (conflict max_desktop 2026-07-08_142200)"},
		{"dir/.hidden", "dir/ (conflict max_desktop 2026-07-08_142200).hidden"},
	}
	for _, tc := range cases {
		if got := Path(tc.in, "max_desktop", ts); got != tc.want {
			t.Errorf("Path(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
