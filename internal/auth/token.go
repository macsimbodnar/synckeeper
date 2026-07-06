package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// persistingSource wraps a TokenSource and re-saves the token whenever a
// refresh produces a new one, so restarts keep working.
type persistingSource struct {
	path string
	src  oauth2.TokenSource

	mu   sync.Mutex
	last string // access token last persisted
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.src.Token()
	if err != nil {
		return nil, err
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
