package names

import "testing"

func TestIgnored(t *testing.T) {
	patterns := []string{"*.tmp", "~$*", ".DS_Store", "*.swp", ".synckeeper*"}
	cases := []struct {
		name string
		want bool
	}{
		{"file.tmp", true},
		{"~$doc.docx", true},
		{".DS_Store", true},
		{".synckeeper.tmp.abc123", true}, // temp prefix, always ignored
		{"normal.txt", false},
		{"tmp.file", false},
		{"DS_Store", false},
	}
	for _, tc := range cases {
		if got := Ignored(tc.name, patterns); got != tc.want {
			t.Errorf("Ignored(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", "nul\x00byte"} {
		if err := Validate(bad); err == nil {
			t.Errorf("Validate(%q) = nil, want error", bad)
		}
	}
	for _, good := range []string{"a.txt", "with spaces", "üñïçødé", ".hidden"} {
		if err := Validate(good); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", good, err)
		}
	}
}

func TestJoin(t *testing.T) {
	if got := Join("", "a.txt"); got != "a.txt" {
		t.Errorf("Join root = %q", got)
	}
	if got := Join("dir/sub", "a.txt"); got != "dir/sub/a.txt" {
		t.Errorf("Join nested = %q", got)
	}
}
