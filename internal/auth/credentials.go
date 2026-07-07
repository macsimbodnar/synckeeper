package auth

// OAuth client credentials for the personal Google Cloud "Desktop app"
// client. A desktop-app client secret is not truly secret (it ships in the
// binary), but this repo must stay private anyway.
//
// Fill these in after creating the OAuth client (see docs/phase-0.md
// prerequisites), or override at build time:
//
//	go build -ldflags "-X github.com/macsimbodnar/synckeeper/internal/auth.ClientID=... \
//	                   -X github.com/macsimbodnar/synckeeper/internal/auth.ClientSecret=..."
var (
	ClientID     = "REDACTED_CLIENT_ID"
	ClientSecret = "REDACTED_CLIENT_SECRET"
)
