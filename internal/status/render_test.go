package status

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// The golden files were captured from the pre-W15 renderer
// (cmd/synckeeper/status.go's printStatusHuman, via a throwaway generator that
// swapped os.Stdout for a pipe) before this package existed. They are the
// characterization net for W15-U1: `status`'s piped output must not move while
// the interactive view is built around it, and this renderer's output had no
// test at all before now (testing.md's 2026-07-14 "CLI render smoke" row named
// none).
//
// Every fixture offset is >= 61s and away from a minute boundary on purpose:
// Dur truncates with int(), so a seconds-bucket value would differ by a second
// between capture and assertion and make the golden flaky.
func fixtures(ref time.Time) map[string]Snapshot {
	base := Snapshot{
		Now:          ref,
		ConfigDir:    "/home/max/.config/synckeeper",
		SyncDir:      "/home/max/Synckeeper",
		DriveFolder:  "Synckeeper",
		MachineName:  "max_mbp",
		RootID:       "root123",
		TokenOK:      true,
		Items:        12481,
		BinAvailable: true,
		BinDest:      "macOS Trash (Finder)",
		Autostart:    service.State{Installed: true, Enabled: true, Running: true},
	}
	activity := []statedb.Activity{
		{TS: ref.Add(-125 * time.Second).Unix(), Kind: "upload", RelPath: "Photos/2026/img_8841.raf", Source: "local"},
		{TS: ref.Add(-125 * time.Second).Unix(), Kind: "download", RelPath: "Notes/todo.md", Source: "remote"},
		{TS: ref.Add(-185 * time.Second).Unix(), Kind: "conflict", RelPath: "Budget.xlsx", Detail: "-> Budget (conflict max_mbp).xlsx", Source: "conflict"},
		{TS: ref.Add(-245 * time.Second).Unix(), Kind: "trash", RelPath: "Archive/old", Detail: "(1117 files)", Source: "remote"},
	}
	running := statedb.DaemonStatus{
		Running:         true,
		PID:             4242,
		StartedAt:       ref.Add(-(3*24*time.Hour + 4*time.Hour + 30*time.Minute)).Unix(),
		LastHeartbeatAt: ref.Add(-65 * time.Second).Unix(),
		Mode:            "watching",
		LastSyncAt:      ref.Add(-125 * time.Second).Unix(),
		LastCycleJSON:   `{"actions":3,"executed":3,"failed":0,"duration_ms":412}`,
		NextPollAt:      ref.Add(125 * time.Second).Unix(),
	}

	out := map[string]Snapshot{}

	neverRun := base
	neverRun.State = StateNeverRun
	neverRun.TokenOK = false
	neverRun.RootID = ""
	neverRun.Items = 0
	neverRun.Autostart = service.State{}
	out["never_run"] = neverRun

	stopped := base
	stopped.State = StateStopped
	stopped.Daemon = statedb.DaemonStatus{LastHeartbeatAt: ref.Add(-3600 * time.Second).Unix()}
	out["stopped"] = stopped

	stale := base
	stale.State = StateStale
	stale.Daemon = statedb.DaemonStatus{Running: true, LastHeartbeatAt: ref.Add(-185 * time.Second).Unix()}
	out["stale"] = stale

	watching := base
	watching.State = StateRunning
	watching.Daemon = running
	watching.Activity = activity
	out["running_watching"] = watching

	polling := watching
	polling.Daemon.Mode = "polling-only"
	out["running_polling_only"] = polling

	paused := watching
	paused.Daemon.Paused = true
	paused.Daemon.Mode = "paused"
	out["paused"] = paused

	guard := watching
	guard.Daemon.GuardBlocked = true
	guard.Daemon.GuardReason = "plan deletes 1117 of 1118 tracked files (over the 25% mass-delete threshold)"
	out["guard_blocked"] = guard

	errored := watching
	errored.Daemon.Mode = "backoff"
	errored.Daemon.LastError = "refresh remote state: Get \"https://www.googleapis.com/drive/v3/changes\": dial tcp: lookup www.googleapis.com: no such host"
	out["last_error"] = errored

	noBin := watching
	noBin.BinAvailable = false
	noBin.BinDest = "no trash implementation for this platform"
	noBin.QFiles, noBin.QBytes = 3, 12345
	noBin.Autostart = service.State{}
	noBin.Activity = nil
	out["no_system_bin"] = noBin

	return out
}

// TestPrintHumanMatchesPreW15Goldens is the byte-identity guarantee: the plain
// view a pipe or a bug report sees is exactly what the pre-dashboard `status`
// printed, for every daemon state.
func TestPrintHumanMatchesPreW15Goldens(t *testing.T) {
	for name, snap := range fixtures(time.Now()) {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
			if err != nil {
				t.Fatalf("missing golden (regenerate per the comment in this file): %v", err)
			}
			var got bytes.Buffer
			PrintHuman(&got, snap)
			if got.String() != string(want) {
				t.Errorf("plain status output changed.\n--- want ---\n%s\n--- got ---\n%s", want, got.String())
			}
		})
	}
}

// TestPrintHumanCoversEveryDaemonState guards the goldens themselves: a state
// with no golden would silently pass the loop above.
func TestPrintHumanCoversEveryDaemonState(t *testing.T) {
	seen := map[string]bool{}
	for _, snap := range fixtures(time.Now()) {
		seen[snap.State] = true
	}
	for _, want := range []string{StateRunning, StateStale, StateStopped, StateNeverRun} {
		if !seen[want] {
			t.Errorf("no fixture renders daemon state %q", want)
		}
	}
}

// TestJSONViewShapeIsStable pins the scripting contract: the keys `status
// --json` emitted before W15 must all still be there.
func TestJSONViewShapeIsStable(t *testing.T) {
	snap := fixtures(time.Now())["running_watching"]
	var buf bytes.Buffer
	if err := WriteJSON(&buf, snap); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("status --json is not valid JSON: %v", err)
	}
	for _, key := range []string{
		"daemon", "sync_dir", "drive_folder", "machine_name", "root_folder",
		"token_present", "tracked_items", "pending_ops", "quarantine",
		"system_bin", "autostart", "recent_activity",
	} {
		if _, ok := doc[key]; !ok {
			t.Errorf("status --json lost top-level key %q", key)
		}
	}
	daemon, ok := doc["daemon"].(map[string]any)
	if !ok {
		t.Fatal("daemon is not an object")
	}
	for _, key := range []string{
		"state", "pid", "mode", "paused", "started_at", "last_sync_at",
		"next_poll_at", "guard_blocked", "guard_reason", "last_error", "last_cycle",
	} {
		if _, ok := daemon[key]; !ok {
			t.Errorf("status --json lost daemon.%q", key)
		}
	}
	if n := len(doc["recent_activity"].([]any)); n != 4 {
		t.Errorf("recent_activity has %d rows, want 4", n)
	}
}

func TestDaemonStateClassification(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	fresh := statedb.DaemonStatus{Running: true, StartedAt: now.Add(-time.Hour).Unix(), LastHeartbeatAt: now.Add(-5 * time.Second).Unix()}
	old := statedb.DaemonStatus{Running: true, StartedAt: now.Add(-time.Hour).Unix(), LastHeartbeatAt: now.Add(-10 * time.Minute).Unix()}
	shut := statedb.DaemonStatus{Running: false, StartedAt: now.Add(-time.Hour).Unix(), LastHeartbeatAt: now.Add(-time.Minute).Unix()}
	window := 30 * time.Second

	cases := []struct {
		name      string
		ds        statedb.DaemonStatus
		found     bool
		pingAlive bool
		want      string
	}{
		{"a live socket wins over a stale heartbeat", old, true, true, StateRunning},
		{"fresh heartbeat, no socket", fresh, true, false, StateRunning},
		{"stale heartbeat", old, true, false, StateStale},
		{"clean shutdown", shut, true, false, StateStopped},
		{"no row", statedb.DaemonStatus{}, false, false, StateNeverRun},
		{"row without a start time", statedb.DaemonStatus{Running: true}, true, false, StateNeverRun},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DaemonState(c.ds, c.found, c.pingAlive, now, window); got != c.want {
				t.Errorf("DaemonState = %q, want %q", got, c.want)
			}
		})
	}
	// A zero window means "never stale on heartbeat age alone".
	if got := DaemonState(old, true, false, now, 0); got != StateRunning {
		t.Errorf("with no staleness window: %q, want %q", got, StateRunning)
	}
}

func TestFormatters(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if got := Ago(now, 0); got != "never" {
		t.Errorf("Ago(zero) = %q", got)
	}
	if got := Ago(now, now.Add(-125*time.Second).Unix()); got != "2m ago" {
		t.Errorf("Ago = %q, want 2m ago", got)
	}
	if got := Until(now, 0); got != "unknown" {
		t.Errorf("Until(zero) = %q", got)
	}
	if got := Until(now, now.Add(-time.Second).Unix()); got != "now" {
		t.Errorf("Until(past) = %q, want now", got)
	}
	if got := Until(now, now.Add(125*time.Second).Unix()); got != "in 2m" {
		t.Errorf("Until = %q, want in 2m", got)
	}
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{90 * time.Minute, "1h30m"},
		{50 * time.Hour, "2d2h"},
	} {
		if got := Dur(c.d); got != c.want {
			t.Errorf("Dur(%s) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := DirectionLabel("nonsense"); got != "" {
		t.Errorf("DirectionLabel(unknown) = %q, want empty", got)
	}
	// SystemBinLine has its own named row (M14.4) in systembin_test.go.
	if got := CycleSuffix(""); got != "" {
		t.Errorf("CycleSuffix(empty) = %q", got)
	}
	if got := CycleSuffix("{not json"); got != "" {
		t.Errorf("CycleSuffix(garbage) = %q, want empty", got)
	}
	if got := AutostartText(service.State{}, nil); got == "" || !bytes.Contains([]byte(got), []byte("not installed")) {
		t.Errorf("AutostartText(absent) = %q", got)
	}
}
