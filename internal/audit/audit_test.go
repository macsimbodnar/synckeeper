package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretScanGate is the pre-publication gate itself (W5.5-S4): the real
// tracked tree must be clean — no committed secret outside the allowlist, no
// tracked runtime secret/state file, .gitignore intact. `make audit` runs it,
// and so does `go test ./...`.
func TestSecretScanGate(t *testing.T) {
	root, err := RepoRoot(".")
	if err != nil {
		t.Skipf("not a git checkout (%v); the secret-scan gate needs git", err)
	}
	rep, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		t.Errorf("secret-scan finding: %s", f)
	}
	if !rep.Clean() {
		t.Fatalf("secret-scan gate: %d finding(s) above — remove the secret/untrack the file, or if a match is intentional add its file to secretScanAllowlist", len(rep.Findings))
	}
}

// TestScanBytesDetects proves the detector fires, so the gate is not vacuously
// green. The fake secret is assembled by concatenation so the literal never
// appears in this file's source.
func TestScanBytesDetects(t *testing.T) {
	planted := `ClientSecret = "GOCSPX-` + strings.Repeat("F", 20) + `"`
	got := scanBytes("x.go", []byte(planted))
	if len(got) != 1 || got[0].Rule != "google-oauth-client-secret" {
		t.Fatalf("planted secret not detected: %v", got)
	}
	if got := scanBytes("x.go", []byte("just a normal line of code\n")); len(got) != 0 {
		t.Errorf("false positive on clean text: %v", got)
	}
	if got := scanBytes("x.bin", []byte{0, 1, 'G', 'O', 'C', 'S', 'P', 'X'}); len(got) != 0 {
		t.Errorf("binary content should be skipped: %v", got)
	}
}

// TestSensitiveTrackedClassifier pins which paths count as must-never-commit.
func TestSensitiveTrackedClassifier(t *testing.T) {
	for _, p := range []string{"token.json", "a/b/credentials.json", "client_secret_123.json", "x/state.db", "state.db-wal"} {
		if !isSensitiveTracked(p) {
			t.Errorf("%q should be flagged sensitive", p)
		}
	}
	for _, p := range []string{"main.go", "README.md", "internal/auth/credentials.go", "docs/status.md"} {
		if isSensitiveTracked(p) {
			t.Errorf("%q should NOT be flagged sensitive", p)
		}
	}
}

// TestRequiredGitignorePresent double-checks the live .gitignore still carries
// the sensitive patterns (the (B) check, surfaced on its own for a clear name).
func TestRequiredGitignorePresent(t *testing.T) {
	root, err := RepoRoot(".")
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pat := range requiredGitignore {
		if !strings.Contains(string(data), pat) {
			t.Errorf(".gitignore missing required pattern %q", pat)
		}
	}
}
