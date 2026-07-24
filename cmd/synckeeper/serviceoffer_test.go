package main

import (
	"errors"
	"strings"
	"testing"
)

// W5.1: `init` offers the login service at the end. The offer never fails
// init; precedence is skip > force > interactive prompt > hint.
func TestOfferServiceInstall(t *testing.T) {
	okInstall := func() (string, error) { return "installed OK", nil }

	tests := []struct {
		name        string
		force, skip bool
		interactive bool
		input       string
		wantInstall bool
		wantOut     string // substring
	}{
		{name: "skip beats force", force: true, skip: true, wantInstall: false, wantOut: "service install"},
		{name: "force installs", force: true, wantInstall: true, wantOut: "installed OK"},
		{name: "interactive yes", interactive: true, input: "y\n", wantInstall: true, wantOut: "installed OK"},
		{name: "interactive Y uppercase", interactive: true, input: "Y\n", wantInstall: true, wantOut: "installed OK"},
		{name: "interactive default (empty)", interactive: true, input: "\n", wantInstall: true, wantOut: "installed OK"},
		{name: "interactive no", interactive: true, input: "n\n", wantInstall: false, wantOut: "service install"},
		{name: "non-interactive prints hint", interactive: false, wantInstall: false, wantOut: "service install"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installed := false
			install := func() (string, error) {
				installed = true
				return okInstall()
			}
			var out strings.Builder
			offerServiceInstall(strings.NewReader(tt.input), &out, tt.force, tt.skip, tt.interactive, install)
			if installed != tt.wantInstall {
				t.Errorf("installed = %v, want %v", installed, tt.wantInstall)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output %q missing %q", out.String(), tt.wantOut)
			}
		})
	}
}

// A failed install is surfaced but never propagates: init still succeeds, and
// the user gets the manual hint.
func TestOfferServiceInstallFailureIsSoft(t *testing.T) {
	install := func() (string, error) { return "", errors.New("launchctl exploded") }
	var out strings.Builder
	offerServiceInstall(strings.NewReader(""), &out, true /*force*/, false, false, install)
	got := out.String()
	if !strings.Contains(got, "launchctl exploded") {
		t.Errorf("want the install error surfaced, got %q", got)
	}
	if !strings.Contains(got, "service install") {
		t.Errorf("want the fallback hint after a failed install, got %q", got)
	}
}

func TestPromptYesNo(t *testing.T) {
	tests := []struct {
		input string
		def   bool
		want  bool
	}{
		{"y\n", false, true},
		{"yes\n", false, true},
		{"Y\n", false, true},
		{"n\n", true, false},
		{"no\n", true, false},
		{"\n", true, true},   // empty → default
		{"\n", false, false}, // empty → default
		{"garbage\n", true, true},
		{"", false, false}, // EOF (no newline) → default
		{"  yes  \n", false, true},
	}
	for _, tt := range tests {
		var out strings.Builder
		got := promptYesNo(strings.NewReader(tt.input), &out, "Q?", tt.def)
		if got != tt.want {
			t.Errorf("promptYesNo(%q, def=%v) = %v, want %v", tt.input, tt.def, got, tt.want)
		}
		if !strings.Contains(out.String(), "Q?") {
			t.Errorf("prompt %q did not echo the question", out.String())
		}
	}
}
