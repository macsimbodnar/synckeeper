package status

import (
	"encoding/json"
	"io"
)

// JSONView is the `status --json` document. It is a scripting contract — the
// dashboard changed what a human sees, never this shape (W15) — so fields are
// only ever added, never renamed or removed.
func JSONView(s Snapshot) map[string]any {
	daemon := map[string]any{
		"state":         s.State,
		"pid":           s.Daemon.PID,
		"mode":          s.Daemon.Mode,
		"paused":        s.Daemon.Paused,
		"started_at":    s.Daemon.StartedAt,
		"last_sync_at":  s.Daemon.LastSyncAt,
		"next_poll_at":  s.Daemon.NextPollAt,
		"guard_blocked": s.Daemon.GuardBlocked,
		"guard_reason":  s.Daemon.GuardReason,
		"last_error":    s.Daemon.LastError,
	}
	if cs, ok := ParseCycle(s.Daemon.LastCycleJSON); ok {
		daemon["last_cycle"] = cs
	}

	acts := make([]map[string]any, 0, len(s.Activity))
	for _, a := range s.Activity {
		acts = append(acts, map[string]any{
			"ts": a.TS, "kind": a.Kind, "rel_path": a.RelPath, "detail": a.Detail, "source": a.Source,
		})
	}

	return map[string]any{
		"daemon":          daemon,
		"sync_dir":        s.SyncDir,
		"drive_folder":    s.DriveFolder,
		"machine_name":    s.MachineName,
		"root_folder":     s.RootID,
		"token_present":   s.TokenOK,
		"tracked_items":   s.Items,
		"pending_ops":     s.Pending,
		"quarantine":      map[string]any{"files": s.QFiles, "bytes": s.QBytes},
		"system_bin":      map[string]any{"available": s.BinAvailable, "destination": s.BinDest},
		"autostart":       map[string]any{"installed": s.Autostart.Installed, "enabled": s.Autostart.Enabled, "running": s.Autostart.Running},
		"recent_activity": acts,
	}
}

// WriteJSON encodes the JSON view, indented as `status --json` always has been.
func WriteJSON(w io.Writer, s Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(JSONView(s))
}
