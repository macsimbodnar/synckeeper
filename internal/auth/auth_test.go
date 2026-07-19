package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// R23 (C6, spec §9): the loopback flow carries S256 PKCE. The client secret
// is public by design, so an intercepted authorization code must be useless
// without the in-process verifier: the auth URL promises a code_challenge,
// and the exchange proves possession with the matching code_verifier. The
// whole flow runs against a fake token endpoint; the "browser" is the test.
func TestR23LoginUsesPKCE(t *testing.T) {
	dir := t.TempDir()
	creds := `{"client_id":"cid","client_secret":"csecret"}`
	if err := os.WriteFile(filepath.Join(dir, CredentialsFile), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token endpoint: %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer",
			"refresh_token": "rt", "expires_in": 3600,
		})
	}))
	defer tokenSrv.Close()

	oldEndpoint, oldOpen := endpoint, openBrowserFn
	t.Cleanup(func() { endpoint, openBrowserFn = oldEndpoint, oldOpen })
	endpoint = oauth2.Endpoint{AuthURL: "http://auth.invalid/auth", TokenURL: tokenSrv.URL + "/token"}
	urlCh := make(chan string, 1)
	openBrowserFn = func(u string) { urlCh <- u }

	done := make(chan error, 1)
	go func() {
		_, err := Login(context.Background(), dir)
		done <- err
	}()

	var authURL string
	select {
	case authURL = <-urlCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Login never produced an auth URL")
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	challenge := q.Get("code_challenge")
	if challenge == "" {
		t.Fatal("auth URL carries no code_challenge — PKCE missing")
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}

	// Play the browser: hit the loopback callback with the state and a code.
	cb := q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) + "&code=authcode123"
	resp, err := http.Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Login failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Login never completed")
	}

	// The exchange proved possession: its verifier hashes to the promised
	// challenge (RFC 7636 S256: base64url(sha256(verifier)), no padding).
	verifier := gotForm.Get("code_verifier")
	if verifier == "" {
		t.Fatal("token exchange carried no code_verifier")
	}
	sum := sha256.Sum256([]byte(verifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != challenge {
		t.Errorf("verifier does not match the challenge: S256(verifier) = %q, challenge = %q", got, challenge)
	}
	if _, err := os.Stat(TokenPath(dir)); err != nil {
		t.Errorf("token not persisted: %v", err)
	}
}
