package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// OAuth client credentials for the personal Google Cloud "Desktop app"
// client. A desktop-app client secret is not truly secret (it ships in the
// binary), but this repo must stay private anyway.
//
// This is the shipping default (spec §9). A user who wants their own Drive API
// quota drops a credentials.json in the config dir; it takes precedence — see
// resolveClient. Override the embedded default at build time with:
//
//	go build -ldflags "-X github.com/macsimbodnar/synckeeper/internal/auth.ClientID=... \
//	                   -X github.com/macsimbodnar/synckeeper/internal/auth.ClientSecret=..."
var (
	ClientID     = "REDACTED_CLIENT_ID"
	ClientSecret = "REDACTED_CLIENT_SECRET"
)

// CredentialsFile is the optional bring-your-own OAuth client the user drops
// in the config dir to use their own Google Cloud project's quota. It is the
// exact JSON the Cloud Console downloads for a "Desktop app" OAuth client; its
// id/secret take precedence over the embedded default (spec §9).
const CredentialsFile = "credentials.json"

// CredentialSource names where the active OAuth client came from.
type CredentialSource string

const (
	CredentialEmbedded CredentialSource = "embedded default (author's client)"
	CredentialBYOFile  CredentialSource = "credentials.json in the config dir"
)

// resolveClient returns the OAuth client id/secret and where they came from:
// a credentials.json in the config dir wins over the embedded default.
func resolveClient(configDir string) (id, secret string, src CredentialSource, err error) {
	if configDir != "" {
		p := filepath.Join(configDir, CredentialsFile)
		raw, rerr := os.ReadFile(p)
		switch {
		case rerr == nil:
			id, secret, err = parseClientJSON(raw)
			if err != nil {
				return "", "", "", fmt.Errorf("parse %s: %w", p, err)
			}
			return id, secret, CredentialBYOFile, nil
		case !os.IsNotExist(rerr):
			return "", "", "", fmt.Errorf("read %s: %w", p, rerr)
		}
	}
	return ClientID, ClientSecret, CredentialEmbedded, nil
}

// CredentialInfo reports which OAuth client `account` would use and its (non-
// secret) client id, without exposing the client secret.
func CredentialInfo(configDir string) (src CredentialSource, clientID string, err error) {
	id, _, src, err := resolveClient(configDir)
	if err != nil {
		return "", "", err
	}
	return src, id, nil
}

// parseClientJSON reads a Google OAuth client JSON — the "installed" (desktop)
// or "web" block the Cloud Console downloads — or a flat
// {"client_id":…,"client_secret":…} object.
func parseClientJSON(raw []byte) (id, secret string, err error) {
	var doc struct {
		Installed *clientBlock `json:"installed"`
		Web       *clientBlock `json:"web"`
		clientBlock
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", "", err
	}
	b := doc.Installed
	if b == nil {
		b = doc.Web
	}
	if b == nil {
		b = &doc.clientBlock // flat form
	}
	if b.ClientID == "" || b.ClientSecret == "" {
		return "", "", errors.New(`missing "client_id"/"client_secret" (expected a Google OAuth "Desktop app" client JSON)`)
	}
	return b.ClientID, b.ClientSecret, nil
}

type clientBlock struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}
