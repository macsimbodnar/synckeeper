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
	u := systemdUnit("/home/max/bin/synckeeper")
	for _, want := range []string{
		"ExecStart=/home/max/bin/synckeeper watch",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing %q", want)
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
