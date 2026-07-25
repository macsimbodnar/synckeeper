package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// OAuth client credentials. Synckeeper ships with NO embedded credentials:
// you must supply your own Google Cloud "Desktop app" OAuth client as a
// credentials.json in the config dir (spec §9, MANUAL §5). resolveClient
// requires it and returns ErrNoCredentials when it's absent.
//
// These vars are empty by default, so no secret ever lives in the source or
// git history. A private build MAY inject a client at build time (optional;
// still nothing hardcoded in source):
//
//	go build -ldflags "-X github.com/macsimbodnar/synckeeper/internal/auth.ClientID=... \
//	                   -X github.com/macsimbodnar/synckeeper/internal/auth.ClientSecret=..."
var (
	ClientID     = ""
	ClientSecret = ""
)

// CredentialsFile is the optional bring-your-own OAuth client the user drops
// in the config dir to use their own Google Cloud project's quota. It is the
// exact JSON the Cloud Console downloads for a "Desktop app" OAuth client; its
// id/secret take precedence over the embedded default (spec §9).
const CredentialsFile = "credentials.json"

// CredentialSource names where the active OAuth client came from.
type CredentialSource string

const (
	CredentialEmbedded CredentialSource = "build-time embedded (-ldflags)"
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
	// No credentials.json. Use build-time-injected creds if present; otherwise
	// there is nothing to authenticate with — the file is required (spec §9).
	if ClientID == "" || ClientSecret == "" {
		return "", "", "", fmt.Errorf("%w\n\n%s", ErrNoCredentials, credentialsHelp(configDir))
	}
	return ClientID, ClientSecret, CredentialEmbedded, nil
}

// credentialsHelp is the end-user guidance shown when no OAuth client is
// configured: how to obtain a Google "Desktop app" client, and exactly where
// to save it. Kept generic (no personal project) and with a docs link.
func credentialsHelp(configDir string) string {
	path := filepath.Join(configDir, CredentialsFile)
	return "Synckeeper needs your own Google OAuth client credentials.\n\n" +
		"How to get them (one time):\n" +
		"  1. Open the Google Cloud console:  https://console.cloud.google.com/\n" +
		"  2. Create a project and enable the Google Drive API.\n" +
		"  3. Create an OAuth client of type \"Desktop app\" and download its JSON.\n" +
		"     Guide: https://developers.google.com/workspace/guides/create-credentials#oauth-client-id\n" +
		"  4. Save that downloaded file as:\n" +
		"       " + path + "\n\n" +
		"Then run `synckeeper login` (or `synckeeper init`)."
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
