package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/oauth2"
)

// TokenPath returns the token file path inside the config dir.
func TokenPath(configDir string) string {
	return filepath.Join(configDir, "token.json")
}

// LoadToken reads the persisted token, enforcing 0600 permissions on POSIX.
func LoadToken(path string) (*oauth2.Token, error) {
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s has permissions %o; refusing to use a token readable by others (want 0600)", path, info.Mode().Perm())
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &tok, nil
}

// SaveToken writes the token atomically with 0600 permissions.
func SaveToken(path string, tok *oauth2.Token) error {
	raw, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// IsExpiredGrant reports whether err is Google refusing the *refresh* token:
// it was revoked, or it expired — which happens roughly weekly while the
// OAuth client is still an unverified "testing" client in the Google console.
// Only a fresh `synckeeper login` fixes it, so it is worth telling apart from
// every other network failure.
func IsExpiredGrant(err error) bool {
	if err == nil {
		return false
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode == "invalid_grant" {
			return true
		}
		if bytes.Contains(re.Body, []byte("invalid_grant")) {
			return true
		}
	}
	// The transport wraps the retrieve error in url.Error and fmt.Errorf chains
	// on the way up through the Drive client, and older oauth2 releases return
	// a plain error; the marker itself is Google's and stable.
	return strings.Contains(err.Error(), "invalid_grant")
}

// persistingSource wraps a TokenSource and re-saves the token whenever a
// refresh produces a new one, so restarts keep working.
type persistingSource struct {
	path string
	cfg  *oauth2.Config
	ctx  context.Context

	mu          sync.Mutex
	src         oauth2.TokenSource
	last        string // access token last persisted
	lastRefresh string // refresh token the current source holds
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.source().Token()
	if err != nil {
		if !IsExpiredGrant(err) {
			return nil, err
		}
		// The daemon builds this source once, at startup, around the refresh
		// token as it was then. `synckeeper login` in another terminal writes a
		// new one to disk, and without this the daemon would keep refreshing
		// with the dead one until it was restarted — the fix would look like it
		// had not worked (found with W19, 2026-08-15).
		reloaded, rerr := p.reload()
		if rerr != nil || reloaded == nil {
			return nil, err
		}
		tok, err = reloaded.Token()
		if err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.AccessToken != p.last {
		if err := SaveToken(p.path, tok); err != nil {
			return nil, fmt.Errorf("persist refreshed token: %w", err)
		}
		p.last = tok.AccessToken
	}
	return tok, nil
}

func (p *persistingSource) source() oauth2.TokenSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.src
}

// reload re-reads token.json after a refusal and, if it now holds a *different*
// refresh token than the one that was refused, adopts it. It returns nil when
// the file is unchanged or unreadable — there is nothing to retry with, and the
// caller reports the original refusal.
func (p *persistingSource) reload() (oauth2.TokenSource, error) {
	if p.cfg == nil {
		return nil, nil
	}
	tok, err := LoadToken(p.path)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Compared by refresh token, not by access token: the access token is dead
	// either way, and the refresh token is the thing `login` replaces.
	if tok.RefreshToken == "" || tok.RefreshToken == p.lastRefresh {
		return nil, nil
	}
	p.src = p.cfg.TokenSource(p.baseCtx(), tok)
	p.lastRefresh = tok.RefreshToken
	return p.src, nil
}

func (p *persistingSource) baseCtx() context.Context {
	if p.ctx == nil {
		return context.Background()
	}
	return p.ctx
}
