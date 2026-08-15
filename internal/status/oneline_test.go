package status

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

const multilineError = "Get \"https://www.googleapis.com/drive/v3/changes?pageToken=204212\": " +
	"auth: cannot fetch token: 400\nResponse: {\n  \"error\": \"invalid_grant\",\n  " +
	"\"error_description\": \"Token has been expired or revoked.\"\n}"

func TestOneLine(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"already one line":     "already one line",
		"two\nlines":           "two lines",
		"  padded\n\n  text  ": "padded text",
		"tabs\tand\r\nbreaks":  "tabs and breaks",
	}
	for in, want := range cases {
		if got := OneLine(in); got != want {
			t.Errorf("OneLine(%q) = %q, want %q", in, got, want)
		}
	}
	if got := OneLine(multilineError); strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("OneLine left a break in %q", got)
	}
}

func TestClip(t *testing.T) {
	if got := Clip("short", 10); got != "short" {
		t.Errorf("Clip shortened a string that fits: %q", got)
	}
	if got := Clip("abcdefghij", 5); got != "abcd…" {
		t.Errorf("Clip = %q, want abcd…", got)
	}
	if got := Clip("abc", 0); got != "abc" {
		t.Errorf("Clip with no bound = %q, want abc", got)
	}
}

// TestPlainStatusKeepsOneFactPerLine: `status` piped to a file or a bug report
// is line-oriented — a grep for `last error:` must return the error, not its
// first line, and one activity entry must not become five.
func TestPlainStatusKeepsOneFactPerLine(t *testing.T) {
	now := time.Now()
	snap := fixtures(now)["running_watching"]
	snap.Daemon.LastError = multilineError
	snap.Activity = []statedb.Activity{
		{TS: now.Add(-time.Minute).Unix(), Kind: "error", Detail: multilineError},
	}

	var buf bytes.Buffer
	PrintHuman(&buf, snap)
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if strings.Contains(line, "invalid_grant") && !strings.Contains(line, "cannot fetch token") {
			t.Errorf("a stored error was printed across lines: %q", line)
		}
	}
	if !strings.Contains(buf.String(), "last error:    Get \"https") {
		t.Error("the last error line lost its head")
	}
}

// TestARejectedTokenIsNamedInPlainStatus: `token: present` is true of a
// revoked credential and useless — the file is still there. The plain view
// says what happened and what fixes it.
func TestARejectedTokenIsNamedInPlainStatus(t *testing.T) {
	now := time.Now()
	snap := fixtures(now)["running_watching"]
	snap.Daemon.LastError = multilineError
	snap.Daemon.LastErrorAuth = true
	snap.TokenOK, snap.TokenRejected = true, true

	var buf bytes.Buffer
	PrintHuman(&buf, snap)
	out := buf.String()
	if !strings.Contains(out, "token:         present but REJECTED") {
		t.Errorf("plain status does not name the rejected token:\n%s", out)
	}
	if !strings.Contains(out, "synckeeper login") {
		t.Error("plain status does not name the cure")
	}

	doc := JSONView(snap)
	if doc["token_rejected"] != true {
		t.Error("status --json lost token_rejected")
	}
	if doc["daemon"].(map[string]any)["last_error_auth"] != true {
		t.Error("status --json lost daemon.last_error_auth")
	}
}

// TestFoldedRowsAreCountedInPlainStatus: the plain view says how many times an
// entry repeated, since the copies it stands for are no longer in the ring.
func TestFoldedRowsAreCountedInPlainStatus(t *testing.T) {
	now := time.Now()
	snap := fixtures(now)["running_watching"]
	snap.Activity = []statedb.Activity{
		{TS: now.Add(-time.Minute).Unix(), Kind: "error", Detail: "auth: cannot fetch token", Count: 288},
		{TS: now.Add(-2 * time.Minute).Unix(), Kind: "upload", RelPath: "a.txt", Source: "local", Count: 1},
	}

	var buf bytes.Buffer
	PrintHuman(&buf, snap)
	out := buf.String()
	if !strings.Contains(out, "(×288)") {
		t.Errorf("plain status does not count a folded row:\n%s", out)
	}
	if strings.Contains(out, "(×1)") {
		t.Error("plain status decorated an entry that happened once")
	}
}
