package names

import (
	"os"
	"path/filepath"
	"testing"
)

// The probe must agree with a direct case-toggled stat on whatever FS the
// test's temp dir lives on (case-insensitive on macOS APFS, sensitive on
// typical Linux) — so the assertion is host-independent.
func TestCaseInsensitiveFS(t *testing.T) {
	dir := t.TempDir()
	got := CaseInsensitiveFS(dir)

	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "PROBE.TXT"))
	want := err == nil

	if got != want {
		t.Errorf("CaseInsensitiveFS = %v, but direct case-toggled stat implies %v", got, want)
	}
}
