package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/engine"
)

// startWatcherWithControl runs a watcher with a control socket at a short
// path (the long $TMPDIR would overflow the sun_path limit) and returns the
// socket path once it answers a ping. cfgDir is where `reload` re-reads
// config.toml; "" for tests that never reload.
func startWatcherWithControl(t *testing.T, m *machine, poll time.Duration, cfgDir string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "skwatch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "c.sock")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &Watcher{Eng: m.eng, Poll: poll, Debounce: 50 * time.Millisecond, ControlSocket: sock, ConfigDir: cfgDir}
	go func() {
		defer close(done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("[%s] watcher: %v", m.name, err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "control socket up", 5*time.Second, func() bool {
		resp, err := control.Call(sock, control.Request{Cmd: control.CmdPing})
		return err == nil && resp.OK
	})
	return sock
}

func call(t *testing.T, sock, cmd string, args any) control.Response {
	t.Helper()
	req := control.Request{Cmd: cmd}
	if args != nil {
		b, _ := json.Marshal(args)
		req.Args = b
	}
	resp, err := control.Call(sock, req)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	if !resp.OK {
		t.Fatalf("%s: not ok: %s", cmd, resp.Error)
	}
	return resp
}

// remoteHasName reports whether a top-level file of that name exists in the
// fake Drive folder.
func remoteHasName(t *testing.T, fake *driveclient.Fake, root, name string) bool {
	t.Helper()
	children, err := fake.List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range children {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Ping, pause (auto-sync suppressed), resume (sync resumes) — the whole flow.
func TestControlPauseSuppressesSyncThenResume(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	sock := startWatcherWithControl(t, a, time.Hour, "")

	call(t, sock, control.CmdPause, nil)
	waitFor(t, "paused recorded", 3*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Paused
	})

	// A local write while paused must NOT be uploaded.
	a.write(t, "while_paused.txt", "should wait")
	time.Sleep(800 * time.Millisecond)
	if remoteHasName(t, fake, root, "while_paused.txt") {
		t.Fatal("file was uploaded while paused")
	}

	call(t, sock, control.CmdResume, nil)
	waitFor(t, "upload after resume", 5*time.Second, func() bool {
		return remoteHasName(t, fake, root, "while_paused.txt")
	})
	waitFor(t, "paused cleared", 3*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && !ds.Paused
	})
}

// A control `sync` runs a cycle even while paused (it's an explicit request).
func TestControlSyncNow(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	a.write(t, "trigger_me.txt", "sync now")
	sock := startWatcherWithControl(t, a, time.Hour, "")

	call(t, sock, control.CmdPause, nil)
	waitFor(t, "paused", 3*time.Second, func() bool {
		ds, err := a.db.GetDaemonStatus()
		return err == nil && ds.Paused
	})

	call(t, sock, control.CmdSync, map[string]bool{"confirm_deletes": false})
	waitFor(t, "upload via sync-now", 5*time.Second, func() bool {
		return remoteHasName(t, fake, root, "trigger_me.txt")
	})
}

// applyReload hot-swaps threshold/poll and reports machine_name as needing a
// restart, leaving the cold field unchanged. Direct call, no goroutines.
func TestApplyReload(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "a", 45, 0.25)

	cfg := config.Default()
	cfg.Engine.MachineName = "a"
	cfg.Engine.PollIntervalSecs = 45
	cfg.Engine.MassDeleteThreshold = 0.25
	w := &Watcher{Eng: &engine.Engine{Cfg: cfg}, Poll: 45 * time.Second, ConfigDir: dir}

	writeConfig(t, dir, "b", 20, 0.5) // machine_name cold; poll+threshold hot
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	res := w.applyReload(ticker)

	if res.Error != "" {
		t.Fatalf("reload error: %s", res.Error)
	}
	if len(res.NeedsRestart) != 1 || res.NeedsRestart[0] != "engine.machine_name" {
		t.Fatalf("needs_restart = %v, want [engine.machine_name]", res.NeedsRestart)
	}
	if w.Eng.Cfg.Engine.MassDeleteThreshold != 0.5 {
		t.Errorf("hot field not applied: threshold = %v", w.Eng.Cfg.Engine.MassDeleteThreshold)
	}
	if w.Poll != 20*time.Second {
		t.Errorf("poll not hot-swapped: %v", w.Poll)
	}
	if w.Eng.Cfg.Engine.MachineName != "a" {
		t.Errorf("cold field applied: machine_name = %q, want unchanged 'a'", w.Eng.Cfg.Engine.MachineName)
	}
}

// R14 (spec §8.3, A3): `reload` while fsnotify events are flowing must be
// race-clean. The event pump reads the ignore globs on every event, so the
// hot swap must publish a snapshot rather than write the config in place.
// This test's job is to generate genuine event load *during* reloads — the
// suite was race-clean before only because nothing reloaded while events
// flowed. Run under -race; the pre-fix code races (write in applyReload on
// the sync loop, read in the pump goroutine).
func TestR14ReloadUnderEventLoadIsRaceClean(t *testing.T) {
	fake, root := newWorld(t)
	a := newMachine(t, "a", fake, root)
	cfgDir := t.TempDir()
	writeConfig(t, cfgDir, "a", 45, 0.25)
	sock := startWatcherWithControl(t, a, time.Hour, cfgDir)

	// Sustained event storm: each write is an fsnotify event the pump
	// filters through the ignore globs. Errors are ignored — the tree is a
	// private tempdir and the load, not the writes, is the point.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			os.WriteFile(filepath.Join(a.dir, fmt.Sprintf("load_%04d.txt", i)),
				[]byte("event load"), 0o644)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	for i := 0; i < 25; i++ {
		call(t, sock, control.CmdReload, nil)
	}
	close(stop)
	wg.Wait()

	// The loop survived the storm: a write after it still syncs.
	a.write(t, "after_reload.txt", "still alive")
	waitFor(t, "sync after reload storm", 10*time.Second, func() bool {
		return remoteHasName(t, fake, root, "after_reload.txt")
	})
}

func writeConfig(t *testing.T, dir, machine string, poll int, threshold float64) {
	t.Helper()
	toml := `
[drive]
folder_name = "Synckeeper"
[local]
sync_dir = "~/Synckeeper"
[engine]
poll_interval_secs = ` + strconv.Itoa(poll) + `
mass_delete_threshold = ` + strconv.FormatFloat(threshold, 'f', -1, 64) + `
machine_name = "` + machine + `"
quarantine_retention_days = 30
ignore = ["*.tmp"]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
}
