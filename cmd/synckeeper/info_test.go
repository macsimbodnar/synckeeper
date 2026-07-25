package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sampleInfoView() infoView {
	return infoView{
		version:       "1.2.3",
		initialized:   true,
		configDir:     "/cfg",
		configPath:    "/cfg/config.toml",
		configLoaded:  true,
		statePath:     "/cfg/state.db",
		tokenPath:     "/cfg/token.json",
		tokenPresent:  true,
		tokenMode:     0o600,
		refreshToken:  true,
		credPath:      "/cfg/credentials.json",
		socketPath:    "/cfg/control.sock",
		quarantineDir: "/cfg/quarantine",
		logPath:       "/logs/synckeeper.log",
		syncDir:       "/home/max/Synckeeper",
		driveFolder:   "Synckeeper",
		rootID:        "root123",
		machineName:   "max_mbp",
		machineID:     "ab12cd",
		oauthSource:   "embedded default (author's client)",
		oauthClient:   "984-client.apps.googleusercontent.com",
		pollSecs:      45,
		massThreshold: 0.25,
		retentionDays: 30,
		ignore:        []string{"*.tmp", ".DS_Store"},
		items:         1620,
		pending:       0,
		qFiles:        3,
		qBytes:        12345,
		daemonState:   "running",
	}
}

// TestPrintInfoHuman asserts every important field surfaces in the human view.
func TestPrintInfoHuman(t *testing.T) {
	var b bytes.Buffer
	printInfoHuman(&b, sampleInfoView())
	out := b.String()

	for _, want := range []string{
		"synckeeper 1.2.3",
		"/cfg/config.toml", "/cfg/state.db", "/cfg/token.json", "0600",
		"/cfg/credentials.json", "/cfg/control.sock", "/cfg/quarantine", "/logs/synckeeper.log",
		"/home/max/Synckeeper", `"Synckeeper"`, "root123", "max_mbp", "ab12cd",
		"embedded default (author's client)", "984-client.apps.googleusercontent.com",
		"refresh token present", "poll interval:    45s", "mass-delete:      0.25",
		"quarantine keep:  30d", "*.tmp .DS_Store",
		"tracked items:    1620", "pending ops:      0", "3 files, 12345 bytes", "daemon:           running",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q\n---\n%s", want, out)
		}
	}
}

// TestPrintInfoHumanNotInitialized: before init, the view shows paths + a hint
// and never prints an empty "(id )" suffix.
func TestPrintInfoHumanNotInitialized(t *testing.T) {
	v := infoView{
		version: "dev", configDir: "/cfg", configPath: "/cfg/config.toml",
		statePath: "/cfg/state.db", tokenPath: "/cfg/token.json", credPath: "/cfg/credentials.json",
		socketPath: "/cfg/control.sock", quarantineDir: "/cfg/quarantine",
		syncDir: "/home/max/Synckeeper", driveFolder: "Synckeeper", machineName: "max_mbp",
		oauthSource: "embedded default (author's client)",
		pollSecs:    45, massThreshold: 0.25, retentionDays: 30,
	}
	var b bytes.Buffer
	printInfoHuman(&b, v)
	out := b.String()
	if !strings.Contains(out, "Not initialized — run `synckeeper init`") {
		t.Errorf("expected not-initialized hint; got:\n%s", out)
	}
	if strings.Contains(out, "(id )") {
		t.Errorf("empty id suffix leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "token.json /cfg/token.json (absent)") && !strings.Contains(out, "(absent)") {
		t.Errorf("expected token absent marker:\n%s", out)
	}
}

// TestPrintInfoJSON: the JSON view is valid and carries the nested structure.
func TestPrintInfoJSON(t *testing.T) {
	var b bytes.Buffer
	if err := printInfoJSON(&b, sampleInfoView()); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatalf("info --json is not valid JSON: %v", err)
	}
	paths, ok := m["paths"].(map[string]any)
	if !ok {
		t.Fatalf("missing paths object: %v", m["paths"])
	}
	if paths["state_db"] != "/cfg/state.db" {
		t.Errorf("paths.state_db = %v, want /cfg/state.db", paths["state_db"])
	}
	if m["version"] != "1.2.3" || m["machine_id"] != "ab12cd" {
		t.Errorf("version/machine_id wrong: %v / %v", m["version"], m["machine_id"])
	}
}
