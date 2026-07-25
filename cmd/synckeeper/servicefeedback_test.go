package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/service"
)

// TestReportServiceStartup: after `service install`, the command confirms the
// daemon came up, or names the likely cause (missing credentials) when it
// didn't — the injected status/cred funcs keep it OS-independent.
func TestReportServiceStartup(t *testing.T) {
	running := func() (service.State, error) { return service.State{Running: true}, nil }
	notRunning := func() (service.State, error) { return service.State{Running: false}, nil }
	statusErr := func() (service.State, error) { return service.State{}, errors.New("launchctl boom") }
	credsOK := func() error { return nil }
	credsMissing := func() error { return errors.New("no OAuth client credentials configured") }

	cases := []struct {
		name    string
		status  func() (service.State, error)
		cred    func() error
		want    string
		notWant string
	}{
		{"running", running, credsOK, "Service is running.", "not running"},
		{"down, creds missing", notRunning, credsMissing, "Likely cause — no OAuth client credentials", ""},
		{"down, creds ok", notRunning, credsOK, "not running", "Likely cause"},
		{"status error", statusErr, credsOK, "Could not check", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b bytes.Buffer
			reportServiceStartup(&b, c.status, c.cred)
			out := b.String()
			if !strings.Contains(out, c.want) {
				t.Errorf("want %q in output, got:\n%s", c.want, out)
			}
			if c.notWant != "" && strings.Contains(out, c.notWant) {
				t.Errorf("did not want %q in output, got:\n%s", c.notWant, out)
			}
		})
	}
}
