package auth

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestIsExpiredGrant covers the shapes the error actually arrives in: the typed
// oauth2 error, that error wrapped by the transport and the Drive client, and
// the flattened string a stored status carries.
func TestIsExpiredGrant(t *testing.T) {
	typed := &oauth2.RetrieveError{
		ErrorCode: "invalid_grant",
		Body:      []byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`),
	}
	bodyOnly := &oauth2.RetrieveError{Body: []byte(`{"error": "invalid_grant"}`)}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed", typed, true},
		{"body only", bodyOnly, true},
		{"wrapped by the transport", &url.Error{Op: "Get", URL: "https://drive", Err: typed}, true},
		{"wrapped by the engine", fmt.Errorf("refresh remote state: %w", typed), true},
		{"flattened text", errors.New(`auth: cannot fetch token: 400 Response: { "error": "invalid_grant" }`), true},
		{"offline", errors.New("dial tcp: lookup www.googleapis.com: no such host"), false},
		{"other oauth failure", &oauth2.RetrieveError{ErrorCode: "invalid_scope"}, false},
	}
	for _, c := range cases {
		if got := IsExpiredGrant(c.err); got != c.want {
			t.Errorf("%s: IsExpiredGrant = %v, want %v", c.name, got, c.want)
		}
	}
}

type stubSource struct {
	tok  *oauth2.Token
	err  error
	call int
}

func (s *stubSource) Token() (*oauth2.Token, error) {
	s.call++
	return s.tok, s.err
}

// TestARefusedTokenIsReloadedFromDisk is the recovery the daemon needs: it
// builds its token source once at startup, so a `synckeeper login` run in
// another terminal used to change nothing until the daemon was restarted — the
// user fixes the credentials and watches the same error keep scrolling.
func TestARefusedTokenIsReloadedFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := TokenPath(dir)
	fresh := &oauth2.Token{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		Expiry:       time.Now().Add(time.Hour), // valid, so no network is needed
	}
	if err := SaveToken(path, fresh); err != nil {
		t.Fatal(err)
	}

	refused := &stubSource{err: &oauth2.RetrieveError{ErrorCode: "invalid_grant"}}
	p := &persistingSource{
		path:        path,
		cfg:         &oauth2.Config{ClientID: "id", ClientSecret: "secret"},
		src:         refused,
		lastRefresh: "revoked-refresh",
	}

	tok, err := p.Token()
	if err != nil {
		t.Fatalf("a refused token was not reloaded from disk: %v", err)
	}
	if tok.AccessToken != "new-access" {
		t.Errorf("token = %q, want the one `login` wrote", tok.AccessToken)
	}
	if p.lastRefresh != "new-refresh" {
		t.Errorf("the source kept the revoked refresh token %q", p.lastRefresh)
	}

	// A second failure with nothing new on disk reports the refusal instead of
	// reloading in a loop.
	p.src = refused
	p.lastRefresh = "new-refresh"
	if _, err := p.Token(); !IsExpiredGrant(err) {
		t.Errorf("unchanged token.json: err = %v, want the original refusal", err)
	}
}

// TestAnUnrelatedFailureIsNotRetried: only a refused *grant* reloads. A network
// error must surface as itself, at once.
func TestAnUnrelatedFailureIsNotRetried(t *testing.T) {
	dir := t.TempDir()
	if err := SaveToken(TokenPath(dir), &oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	offline := &stubSource{err: errors.New("dial tcp: no such host")}
	p := &persistingSource{path: filepath.Join(dir, "token.json"), cfg: &oauth2.Config{}, src: offline, lastRefresh: "r"}

	if _, err := p.Token(); err == nil || IsExpiredGrant(err) {
		t.Fatalf("err = %v, want the network error", err)
	}
	if offline.call != 1 {
		t.Errorf("the source was called %d times, want 1", offline.call)
	}
}
