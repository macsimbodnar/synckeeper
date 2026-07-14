package watch

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/config"
	"github.com/macsimbodnar/synckeeper/internal/control"
	"github.com/macsimbodnar/synckeeper/internal/engine"
)

// reloadResult reports the outcome of a live config reload: which changed
// fields still need a restart, or a load/validation error.
type reloadResult struct {
	NeedsRestart []string
	Error        string
}

// listenControl opens the control socket, clearing a stale file left by a
// crashed run and locking the socket to the owner (0600).
func listenControl(path string) (net.Listener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		os.Chmod(path, 0o600)
	}
	return ln, nil
}

// controlHandler dispatches control commands. Mutating commands hand work to
// the single-threaded sync loop via channels (syncNow, reloadCh) so the loop
// stays the sole owner of the engine, ticker, and config.
func (w *Watcher) controlHandler(syncNow chan<- engine.Options, reloadCh chan chan reloadResult, rec *recorder) control.Handler {
	return func(ctx context.Context, req control.Request) control.Response {
		switch req.Cmd {
		case control.CmdPing:
			return okData(map[string]any{"pid": os.Getpid(), "protocol": control.ProtocolVersion})

		case control.CmdSync:
			var args struct {
				ConfirmDeletes bool `json:"confirm_deletes"`
			}
			if len(req.Args) > 0 {
				if err := json.Unmarshal(req.Args, &args); err != nil {
					return control.Response{OK: false, Error: "bad args: " + err.Error()}
				}
			}
			select {
			case syncNow <- engine.Options{ConfirmDeletes: args.ConfirmDeletes}:
				return okData(map[string]any{"triggered": true})
			default:
				return okData(map[string]any{"triggered": false, "note": "a sync is already queued"})
			}

		case control.CmdPause:
			rec.setPaused(true)
			return okData(map[string]any{"paused": true})

		case control.CmdResume:
			rec.setPaused(false)
			select { // refresh promptly rather than waiting for the next tick
			case syncNow <- engine.Options{}:
			default:
			}
			return okData(map[string]any{"paused": false})

		case control.CmdReload:
			respCh := make(chan reloadResult, 1)
			select {
			case reloadCh <- respCh:
			case <-ctx.Done():
				return control.Response{OK: false, Error: "daemon shutting down"}
			}
			res := <-respCh
			if res.Error != "" {
				return control.Response{OK: false, Error: res.Error}
			}
			return okData(map[string]any{"reloaded": true, "needs_restart": res.NeedsRestart})

		default:
			return control.Response{OK: false, Error: "unknown command: " + req.Cmd}
		}
	}
}

func okData(v any) control.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return control.Response{OK: false, Error: "marshal: " + err.Error()}
	}
	return control.Response{OK: true, Data: b}
}

// applyReload re-reads config.toml and hot-swaps the fields that can change
// without a restart (poll interval, ignore globs, thresholds); identity and
// path fields are reported as needing a restart and left untouched. It runs
// on the sync loop, so no cycle is concurrently reading the config.
func (w *Watcher) applyReload(ticker *time.Ticker) reloadResult {
	cfg, err := config.Load(w.ConfigDir)
	if err != nil {
		return reloadResult{Error: err.Error()}
	}
	old := w.Eng.Cfg
	var needsRestart []string
	if cfg.Local.SyncDir != old.Local.SyncDir {
		needsRestart = append(needsRestart, "local.sync_dir")
	}
	if cfg.Drive.FolderName != old.Drive.FolderName {
		needsRestart = append(needsRestart, "drive.folder_name")
	}
	if cfg.Engine.MachineName != old.Engine.MachineName {
		needsRestart = append(needsRestart, "engine.machine_name")
	}

	// Hot fields: safe to apply to the live engine immediately.
	w.Eng.Cfg.Engine.Ignore = cfg.Engine.Ignore
	w.Eng.Cfg.Engine.MassDeleteThreshold = cfg.Engine.MassDeleteThreshold
	w.Eng.Cfg.Engine.QuarantineRetentionDays = cfg.Engine.QuarantineRetentionDays
	w.Eng.Cfg.Engine.FullRescanIntervalSecs = cfg.Engine.FullRescanIntervalSecs
	if cfg.Engine.PollIntervalSecs != old.Engine.PollIntervalSecs {
		w.Poll = time.Duration(cfg.Engine.PollIntervalSecs) * time.Second
		ticker.Reset(w.Poll)
	}
	return reloadResult{NeedsRestart: needsRestart}
}
