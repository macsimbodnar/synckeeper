package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Drive.FolderName != "Synckeeper" {
		t.Errorf("folder_name = %q, want Synckeeper", cfg.Drive.FolderName)
	}
	if cfg.Engine.PollIntervalSecs != 45 {
		t.Errorf("poll_interval_secs = %d, want 45", cfg.Engine.PollIntervalSecs)
	}
	if cfg.Engine.MassDeleteThreshold != 0.25 {
		t.Errorf("mass_delete_threshold = %v, want 0.25", cfg.Engine.MassDeleteThreshold)
	}
	if cfg.Engine.MachineName == "" {
		t.Error("machine_name default is empty")
	}
	if len(cfg.Engine.Ignore) == 0 {
		t.Error("ignore default is empty")
	}
}

func TestLoadOverrides(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[drive]
folder_name = "Stuff"

[engine]
poll_interval_secs = 10
machine_name = "test_box"
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Drive.FolderName != "Stuff" {
		t.Errorf("folder_name = %q, want Stuff", cfg.Drive.FolderName)
	}
	if cfg.Engine.PollIntervalSecs != 10 {
		t.Errorf("poll_interval_secs = %d, want 10", cfg.Engine.PollIntervalSecs)
	}
	if cfg.Engine.MachineName != "test_box" {
		t.Errorf("machine_name = %q, want test_box", cfg.Engine.MachineName)
	}
	// Unset keys keep defaults.
	if cfg.Engine.MassDeleteThreshold != 0.25 {
		t.Errorf("mass_delete_threshold = %v, want default 0.25", cfg.Engine.MassDeleteThreshold)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "[engine]\npoll_secs = 10\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("want unknown key error, got %v", err)
	}
}

func TestLoadValidation(t *testing.T) {
	cases := []struct{ name, body string }{
		{"bad threshold", "[engine]\nmass_delete_threshold = 1.5\n"},
		{"zero poll", "[engine]\npoll_interval_secs = 0\n"},
		{"empty folder", "[drive]\nfolder_name = \"\"\n"},
		{"unsafe machine name", "[engine]\nmachine_name = \"a/b\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.body)
			if _, err := Load(dir); err == nil {
				t.Fatal("want validation error, got nil")
			}
		})
	}
}

func TestSyncDirTildeExpansion(t *testing.T) {
	cfg := Default()
	cfg.Local.SyncDir = "~/SynckeeperTest"
	got, err := cfg.SyncDir()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "SynckeeperTest")
	if got != want {
		t.Errorf("SyncDir() = %q, want %q", got, want)
	}
}

func TestWriteDefaultIdempotent(t *testing.T) {
	dir := t.TempDir()
	path, created, err := WriteDefault(dir)
	if err != nil || !created {
		t.Fatalf("first write: created=%v err=%v", created, err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("load written default: %v", err)
	}
	// Second call must not overwrite.
	if err := os.WriteFile(path, []byte("[drive]\nfolder_name = \"Custom\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, created, err = WriteDefault(dir); err != nil || created {
		t.Fatalf("second write: created=%v err=%v", created, err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Drive.FolderName != "Custom" {
		t.Error("WriteDefault overwrote an existing config")
	}
}
