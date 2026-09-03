package service

import (
	"strings"
	"testing"
)

func TestLaunchdPlist(t *testing.T) {
	p := launchdPlist("/usr/local/bin/synckeeper", "/tmp/synckeeper.log")
	for _, want := range []string{
		"<string>/usr/local/bin/synckeeper</string>",
		"<string>watch</string>",
		"<key>KeepAlive</key><true/>",
		"<key>RunAtLoad</key><true/>",
		label,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

func TestSystemdUnit(t *testing.T) {
	u := systemdUnit("/home/max/bin/synckeeper", "/home/max/.local/state/synckeeper/synckeeper.log")
	for _, want := range []string{
		"ExecStart=/home/max/bin/synckeeper watch",
		"Restart=on-failure",
		"WantedBy=default.target",
		// W7-L2: daemon output goes to a file we own, not the user journal.
		"StandardOutput=append:/home/max/.local/state/synckeeper/synckeeper.log",
		"StandardError=append:/home/max/.local/state/synckeeper/synckeeper.log",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	// W7-L5: a user unit cannot order against a system target, so the unit
	// carries no After= at all; the restart pair must survive that removal.
	for _, unwanted := range []string{"After=", "network-online.target"} {
		if strings.Contains(u, unwanted) {
			t.Errorf("unit should not contain %q", unwanted)
		}
	}
	if !strings.Contains(u, "RestartSec=10") {
		t.Error("unit missing RestartSec=10")
	}
	// The baked path must be absolute: systemd rejects a relative append: target.
	for _, line := range strings.Split(u, "\n") {
		for _, key := range []string{"StandardOutput=append:", "StandardError=append:"} {
			if v, ok := strings.CutPrefix(line, key); ok && !strings.HasPrefix(v, "/") {
				t.Errorf("%s%s is not an absolute path", key, v)
			}
		}
	}
}

func TestTaskSchedulerXML(t *testing.T) {
	x := taskSchedulerXML(`C:\bin\synckeeper.exe`)
	for _, want := range []string{
		`<Command>C:\bin\synckeeper.exe</Command>`,
		"<Arguments>watch</Arguments>",
		"<LogonTrigger>",
		"<RestartOnFailure>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("task xml missing %q", want)
		}
	}
}
