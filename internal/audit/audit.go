// Package audit implements the pre-publication secret-scan gate (W5.5-S4):
// self-contained checks — no external tool — that no secret or sensitive
// runtime file has leaked into the git-tracked tree. Run it via `make audit`,
// or as part of `go test ./...` (TestSecretScanGate drives Scan). The one
// intentional embedded OAuth client secret is allowlisted (spec §9; accepted
// as publishable, decisions.md 2026-07-24 "W5.5 item calls").
package audit

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Finding is one problem the gate found.
type Finding struct {
	File   string
	Line   int // 0 when not line-specific
	Rule   string
	Detail string
}

func (f Finding) String() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d [%s] %s", f.File, f.Line, f.Rule, f.Detail)
	}
	return fmt.Sprintf("%s [%s] %s", f.File, f.Rule, f.Detail)
}

// Report is the result of a Scan.
type Report struct{ Findings []Finding }

// Clean reports whether the gate passed.
func (r *Report) Clean() bool { return len(r.Findings) == 0 }

// secretRules are high-signal secret patterns. Deliberately narrow (few false
// positives) rather than exhaustive — the goal is to catch an accidental paste
// of a real credential, not to be a full DLP scanner.
var secretRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"google-oauth-client-secret", regexp.MustCompile(`GOCSPX-[A-Za-z0-9_\-]{10,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{"aws-access-key-id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`)},
	{"private-key-block", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`)},
}

// secretScanAllowlist names tracked files permitted to contain a secret match:
// only this package's own source (which holds the detection patterns and test
// fixtures). No embedded credentials exist anymore — credentials.go is no
// longer exempt, so a secret reappearing there fails the gate too (spec §9).
// A match anywhere else fails the gate.
var secretScanAllowlist = map[string]bool{
	"internal/audit/audit.go":      true,
	"internal/audit/audit_test.go": true,
}

// requiredGitignore are patterns .gitignore must still carry so the runtime
// secret/state files can never be committed.
var requiredGitignore = []string{"token.json", "credentials.json", "client_secret_*.json", "*.db"}

// isSensitiveTracked reports whether a tracked path is a runtime secret/state
// artifact that must never be committed.
func isSensitiveTracked(rel string) bool {
	base := path.Base(rel)
	switch base {
	case "token.json", "credentials.json":
		return true
	}
	if ok, _ := path.Match("client_secret_*.json", base); ok {
		return true
	}
	switch path.Ext(base) {
	case ".db", ".db-wal", ".db-shm":
		return true
	}
	return false
}

// RepoRoot returns the git top-level directory containing dir.
func RepoRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Scan runs all gate checks against the git-tracked tree rooted at root.
func Scan(root string) (*Report, error) {
	rep := &Report{}
	files, err := trackedFiles(root)
	if err != nil {
		return nil, err
	}

	// (A) sensitive runtime files must not be tracked.
	for _, rel := range files {
		if isSensitiveTracked(rel) {
			rep.Findings = append(rep.Findings, Finding{File: rel, Rule: "sensitive-file-tracked",
				Detail: "runtime secret/state file is committed; it must stay gitignored and untracked"})
		}
	}

	// (B) .gitignore must still cover the sensitive patterns.
	rep.Findings = append(rep.Findings, checkGitignore(root)...)

	// (C) secret content scan over tracked text files.
	for _, rel := range files {
		if secretScanAllowlist[rel] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue // deleted-but-tracked or unreadable; not this gate's concern
		}
		rep.Findings = append(rep.Findings, scanBytes(rel, data)...)
	}
	return rep, nil
}

func trackedFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, b := range bytes.Split(out, []byte{0}) {
		if len(b) > 0 {
			files = append(files, string(b))
		}
	}
	return files, nil
}

func checkGitignore(root string) []Finding {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return []Finding{{File: ".gitignore", Rule: "gitignore-missing", Detail: err.Error()}}
	}
	present := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		present[strings.TrimSpace(sc.Text())] = true
	}
	var out []Finding
	for _, pat := range requiredGitignore {
		if !present[pat] {
			out = append(out, Finding{File: ".gitignore", Rule: "gitignore-pattern-missing",
				Detail: fmt.Sprintf("must list %q so that runtime file can never be committed", pat)})
		}
	}
	return out
}

// scanBytes is the pure content scanner: skip binary/huge data, then match
// each secret rule line by line.
func scanBytes(rel string, data []byte) []Finding {
	if len(data) > 5<<20 || bytes.IndexByte(data, 0) >= 0 {
		return nil // too big, or binary
	}
	var out []Finding
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		for _, rule := range secretRules {
			if rule.re.MatchString(text) {
				out = append(out, Finding{File: rel, Line: line, Rule: rule.name,
					Detail: "possible committed secret; remove it, or if intentional add the file to the audit allowlist"})
			}
		}
	}
	return out
}
