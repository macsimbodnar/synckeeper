package service

import "testing"

func TestLaunchdRunning(t *testing.T) {
	loaded := `{
	"StandardErrorPath" = "/Users/max/Library/Logs/synckeeper.log";
	"Label" = "com.macsimbodnar.synckeeper";
	"OnDemand" = false;
	"PID" = 4821;
	"Program" = "/usr/local/bin/synckeeper";
}`
	notRunning := `{
	"Label" = "com.macsimbodnar.synckeeper";
	"OnDemand" = false;
	"LastExitStatus" = 0;
}`
	if !launchdRunning(loaded) {
		t.Error("expected running when a PID is present")
	}
	if launchdRunning(notRunning) {
		t.Error("expected not-running when no PID line is present")
	}
}

func TestParseSystemctl(t *testing.T) {
	cases := []struct {
		enabledOut, activeOut  string
		wantEnabled, wantRun   bool
	}{
		{"enabled\n", "active\n", true, true},
		{"disabled\n", "inactive\n", false, false},
		{"enabled\n", "failed\n", true, false},
		{"", "", false, false},
	}
	for _, c := range cases {
		enabled, running := parseSystemctl(c.enabledOut, c.activeOut)
		if enabled != c.wantEnabled || running != c.wantRun {
			t.Errorf("parseSystemctl(%q,%q) = (%v,%v), want (%v,%v)",
				c.enabledOut, c.activeOut, enabled, running, c.wantEnabled, c.wantRun)
		}
	}
}

func TestParseSchtasks(t *testing.T) {
	running := `
Folder: \
HostName:      DESKTOP-1
TaskName:      \Synckeeper
Status:        Running
Logon Mode:    Interactive/Background
`
	ready := `
TaskName:      \Synckeeper
Status:        Ready
`
	if !parseSchtasks(running) {
		t.Error("expected running for Status: Running")
	}
	if parseSchtasks(ready) {
		t.Error("expected not-running for Status: Ready")
	}
}
