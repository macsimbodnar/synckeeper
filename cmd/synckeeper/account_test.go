package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
)

// W5.2: `account` names the signed-in Google account from one about.get, and
// stays useful offline — a failed call prints a note, never fails the command.
func TestPrintAccountIdentity(t *testing.T) {
	tests := []struct {
		name  string
		about driveclient.About
		err   error
		want  string // substring that must appear
	}{
		{
			name:  "email and display name",
			about: driveclient.About{Email: "max@example.com", DisplayName: "Max B"},
			want:  "google account: Max B <max@example.com>",
		},
		{
			name:  "email only",
			about: driveclient.About{Email: "max@example.com"},
			want:  "google account: max@example.com",
		},
		{
			name: "offline is graceful",
			err:  errors.New("dial tcp: no route to host"),
			want: "google account: unavailable (dial tcp: no route to host)",
		},
		{
			name:  "no email returned",
			about: driveclient.About{},
			want:  "google account: (Drive returned no account email)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			printAccountIdentity(&b, tt.about, tt.err)
			if got := b.String(); !strings.Contains(got, tt.want) {
				t.Errorf("printAccountIdentity() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

// The display-name branch must not fire when only a name (no email) comes
// back — an email-less identity is the "no email" case, not a "<>" line.
func TestPrintAccountIdentityNameWithoutEmail(t *testing.T) {
	var b strings.Builder
	printAccountIdentity(&b, driveclient.About{DisplayName: "Max B"}, nil)
	got := b.String()
	if strings.Contains(got, "<>") {
		t.Errorf("rendered an empty <> address: %q", got)
	}
	if !strings.Contains(got, "no account email") {
		t.Errorf("want the no-email note, got %q", got)
	}
}
