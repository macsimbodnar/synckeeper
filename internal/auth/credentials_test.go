package auth

import (
	"errors"
	"os"
	"path/filepath"
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
