// Package auth implements the OAuth desktop loopback flow against Google
// and persists the resulting token with 0600 permissions.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const driveScope = "https://www.googleapis.com/auth/drive"

// endpoint and openBrowserFn are test seams: R23 drives the whole loopback
// flow against a fake token server and captures the URL a browser would get.
var (
	endpoint      = google.Endpoint
	openBrowserFn = openBrowser
)

// ErrNoCredentials is returned when neither a credentials.json nor the
// embedded client id/secret provide OAuth client credentials.
var ErrNoCredentials = errors.New(
	"no OAuth client credentials: drop a credentials.json in the config dir, fill internal/auth/credentials.go, or build with -ldflags -X overrides")

// oauthConfig builds the OAuth config from the resolved client (a BYO
// credentials.json in configDir wins over the embedded default; spec §9).
func oauthConfig(configDir string) (*oauth2.Config, error) {
	id, secret, _, err := resolveClient(configDir)
	if err != nil {
		return nil, err
	}
	if id == "" || secret == "" {
		return nil, ErrNoCredentials
	}
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     endpoint,
		Scopes:       []string{driveScope},
	}, nil
}

// TokenSource returns a self-refreshing, self-persisting token source backed
// by <configDir>/token.json. Fails if no token is stored yet (run `init`).
func TokenSource(ctx context.Context, configDir string) (oauth2.TokenSource, error) {
	cfg, err := oauthConfig(configDir)
	if err != nil {
		return nil, err
	}
	path := TokenPath(configDir)
	tok, err := LoadToken(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no stored token at %s: run `synckeeper init` first", path)
		}
		return nil, err
	}
	return &persistingSource{
		path: path,
		src:  cfg.TokenSource(ctx, tok),
		last: tok.AccessToken,
	}, nil
}

// Login runs the interactive loopback flow and persists the token. Returns
// the token source for immediate use.
func Login(ctx context.Context, configDir string) (oauth2.TokenSource, error) {
	cfg, err := oauthConfig(configDir)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start loopback listener: %w", err)
	}
	defer listener.Close()
	cfg.RedirectURL = fmt.Sprintf("http://%s/callback", listener.Addr().String())

	state, err := randomState()
	if err != nil {
		return nil, err
	}
	// PKCE (spec §9, R23): the client secret is public by design, so an
	// intercepted authorization code must be useless without the verifier
	// that never leaves this process.
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier))

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			results <- result{err: errors.New("oauth state mismatch")}
		case q.Get("error") != "":
			fmt.Fprintf(w, "Authorization failed: %s. You can close this tab.", q.Get("error"))
			results <- result{err: fmt.Errorf("authorization denied: %s", q.Get("error"))}
		default:
			fmt.Fprint(w, "Synckeeper is authorized. You can close this tab.")
			results <- result{code: q.Get("code")}
		}
	})}
	go server.Serve(listener)
	defer server.Close()

	fmt.Fprintf(os.Stderr, "Open this URL to authorize Synckeeper:\n\n  %s\n\n", authURL)
	openBrowserFn(authURL)

	var res result
	select {
	case res = <-results:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timed out waiting for OAuth authorization")
	}
	if res.err != nil {
		return nil, res.err
	}

	tok, err := cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	path := TokenPath(configDir)
	if err := SaveToken(path, tok); err != nil {
		return nil, fmt.Errorf("save token: %w", err)
	}
	return &persistingSource{path: path, src: cfg.TokenSource(ctx, tok), last: tok.AccessToken}, nil
}

func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// openBrowser makes a best-effort attempt to open the URL; the URL is always
// printed as a fallback.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
