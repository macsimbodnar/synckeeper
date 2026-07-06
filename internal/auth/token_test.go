package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	tok := &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
	}
	if err := SaveToken(path, tok); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != tok.AccessToken || got.RefreshToken != tok.RefreshToken {
		t.Errorf("round trip mismatch: got %+v", got)
	}
}

func TestSaveTokenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(path, &oauth2.Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token perms = %o, want 600", perm)
	}
}

func TestLoadTokenRejectsLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(path, &oauth2.Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("want error for 0644 token file, got nil")
	}
}

func TestPersistingSourceSavesOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	fresh := &oauth2.Token{AccessToken: "new", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	src := &persistingSource{
		path: path,
		src:  oauth2.StaticTokenSource(fresh),
		last: "old",
	}
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new" {
		t.Errorf("persisted access token = %q, want new", got.AccessToken)
	}
}
