package auth

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setEmbedded overrides the (normally empty) build-time creds for one test and
// returns a restore func, so these tests are correct whether or not the binary
// was built with -ldflags-injected credentials.
func setEmbedded(id, secret string) func() {
	oldID, oldSecret := ClientID, ClientSecret
	ClientID, ClientSecret = id, secret
	return func() { ClientID, ClientSecret = oldID, oldSecret }
}

func TestResolveClientRequiresCredentials(t *testing.T) {
	defer setEmbedded("", "")() // no embedded creds
	dir := t.TempDir()          // no credentials.json
	_, _, _, err := resolveClient(dir)
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("want ErrNoCredentials when no file and no embedded creds, got %v", err)
	}
	// The error must be actionable: what to do, where to save the file, a link.
	msg := err.Error()
	for _, want := range []string{
		filepath.Join(dir, CredentialsFile), // exact path to save it
		"console.cloud.google.com",          // where to create it
		"developers.google.com",             // link to the docs
		"Desktop app",                       // the client type
		"Drive API",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("credentials error not actionable — missing %q", want)
		}
	}
	t.Logf("rendered error:\n%s", msg)
}

func TestResolveClientEmbeddedViaLdflags(t *testing.T) {
	defer setEmbedded("inj-id", "inj-secret")()
	dir := t.TempDir()
	id, secret, src, err := resolveClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src != CredentialEmbedded || id != "inj-id" || secret != "inj-secret" {
		t.Errorf("got %q/%q src=%q, want inj-id/inj-secret embedded", id, secret, src)
	}
}

func TestResolveClientBYOFileWins(t *testing.T) {
	dir := t.TempDir()
	const doc = `{"installed":{"client_id":"byo-id.apps.googleusercontent.com","client_secret":"byo-secret","redirect_uris":["http://localhost"]}}`
	if err := os.WriteFile(filepath.Join(dir, CredentialsFile), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	id, secret, src, err := resolveClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src != CredentialBYOFile {
		t.Errorf("source = %q, want BYO file", src)
	}
	if id != "byo-id.apps.googleusercontent.com" || secret != "byo-secret" {
		t.Errorf("resolved id/secret = %q/%q, want the BYO values", id, secret)
	}
}

func TestResolveClientMalformedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CredentialsFile), []byte(`{"installed":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := resolveClient(dir); err == nil {
		t.Error("want an error for a credentials.json missing client_id/secret")
	}
}

func TestParseClientJSONForms(t *testing.T) {
	cases := []struct{ name, doc string }{
		{"installed", `{"installed":{"client_id":"i","client_secret":"s"}}`},
		{"web", `{"web":{"client_id":"i","client_secret":"s"}}`},
		{"flat", `{"client_id":"i","client_secret":"s"}`},
	}
	for _, c := range cases {
		id, secret, err := parseClientJSON([]byte(c.doc))
		if err != nil || id != "i" || secret != "s" {
			t.Errorf("%s: got %q/%q err=%v, want i/s", c.name, id, secret, err)
		}
	}
	if _, _, err := parseClientJSON([]byte(`not json`)); err == nil {
		t.Error("want an error for invalid JSON")
	}
}

// W7-L6: a group/world-readable credentials.json is warned about, never
// refused — it is the user's own downloaded file and refusing it would block
// onboarding. (token.json, which we write ourselves, is still refused outright
// by LoadToken.)
func TestLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		mode      os.FileMode
		wantLoose bool
	}{
		{0o600, false},
		{0o400, false},
		{0o640, true}, // group-readable
		{0o604, true}, // world-readable
		{0o664, true}, // the umask default that started this (W7-L6)
	} {
		p := filepath.Join(dir, fmt.Sprintf("c%04o.json", tc.mode))
		if err := os.WriteFile(p, []byte("{}"), tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, tc.mode); err != nil { // defeat umask
			t.Fatal(err)
		}
		perm, loose := loosePerms(p)
		if loose != tc.wantLoose {
			t.Errorf("loosePerms(%04o) = %v, want %v", tc.mode, loose, tc.wantLoose)
		}
		if perm != tc.mode {
			t.Errorf("loosePerms(%04o) reported perm %04o", tc.mode, perm)
		}
	}
	if _, loose := loosePerms(filepath.Join(dir, "absent.json")); loose {
		t.Error("a missing file must not report loose perms")
	}
}

func TestResolveClientWarnsButAcceptsLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	const doc = `{"installed":{"client_id":"byo-id.apps.googleusercontent.com","client_secret":"byo-secret"}}`
	p := filepath.Join(dir, CredentialsFile)
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	id, secret, src, err := resolveClient(dir)
	if err != nil {
		t.Fatalf("a loose credentials.json must still be usable: %v", err)
	}
	if src != CredentialBYOFile || id != "byo-id.apps.googleusercontent.com" || secret != "byo-secret" {
		t.Errorf("got %q/%q src=%q, want the BYO file's client", id, secret, src)
	}
	out := logs.String()
	for _, want := range []string{p, "0644", "chmod 600"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q; got: %s", want, out)
		}
	}
}

// The good case stays quiet: a 0600 credentials.json must not warn.
func TestResolveClientQuietOnTightPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	const doc = `{"installed":{"client_id":"byo-id","client_secret":"byo-secret"}}`
	if err := os.WriteFile(filepath.Join(dir, CredentialsFile), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(old)

	if _, _, _, err := resolveClient(dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "readable by other local users") {
		t.Errorf("0600 credentials.json must not warn; got: %s", logs.String())
	}
}
