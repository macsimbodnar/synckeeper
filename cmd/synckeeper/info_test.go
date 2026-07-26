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
		trashDest:     "freedesktop trash at /home/max/.local/share/Trash",
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
		"system bin   freedesktop trash at /home/max/.local/share/Trash",
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
	// W13-T5: where a deletion arriving from Drive ends up.
	if paths["system_bin"] != "freedesktop trash at /home/max/.local/share/Trash" {
		t.Errorf("paths.system_bin = %v, want the trash destination", paths["system_bin"])
	}
}

// W7-L6: `info` reports the credentials.json mode the same way it reports
// token.json's, and flags a file other local users can read — the exact state
// a hand-created config dir leaves behind (umask 0664).
func TestPrintInfoCredentialsPerms(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*infoView)
		want, avoid []string
	}{
		{
			name:   "absent",
			mutate: func(v *infoView) { v.credExists = false },
			want:   []string{"credentials  /cfg/credentials.json (absent — required, see below)"},
			avoid:  []string{"chmod 600"},
		},
		{
			name:   "owner only",
			mutate: func(v *infoView) { v.credExists, v.credMode, v.credLoose = true, 0o600, false },
			want:   []string{"credentials  /cfg/credentials.json (present, 0600)"},
			avoid:  []string{"chmod 600", "readable by others"},
		},
		{
			name:   "group and world readable",
			mutate: func(v *infoView) { v.credExists, v.credMode, v.credLoose = true, 0o664, true },
			want:   []string{"(present, 0664 — readable by others", "chmod 600 /cfg/credentials.json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := sampleInfoView()
			tc.mutate(&v)
			var b bytes.Buffer
			printInfoHuman(&b, v)
			out := b.String()
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, a := range tc.avoid {
				if strings.Contains(out, a) {
					t.Errorf("unexpected %q in:\n%s", a, out)
				}
			}
		})
	}
}

// The JSON view carries the same facts for scripts and a future UI.
func TestPrintInfoJSONCredentialsPerms(t *testing.T) {
	v := sampleInfoView()
	v.credExists, v.credMode, v.credLoose = true, 0o664, true
	var b bytes.Buffer
	if err := printInfoJSON(&b, v); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["credentials_mode"] != "0664" {
		t.Errorf("credentials_mode = %v, want 0664", out["credentials_mode"])
	}
	if out["credentials_loose"] != true {
		t.Errorf("credentials_loose = %v, want true", out["credentials_loose"])
	}
}
