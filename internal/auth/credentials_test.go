package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveClientEmbeddedByDefault(t *testing.T) {
	dir := t.TempDir()
	id, secret, src, err := resolveClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src != CredentialEmbedded {
		t.Errorf("source = %q, want embedded", src)
	}
	if id != ClientID || secret != ClientSecret {
		t.Errorf("resolved id/secret do not match the embedded default")
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
