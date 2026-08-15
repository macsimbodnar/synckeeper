package statedb

import (
	"database/sql"
	"errors"
)

// activityCap bounds the activity ring: the oldest rows beyond this many are
// dropped on each append so a long-running daemon's table stays small.
const activityCap = 500

// DaemonStatus is the singleton runtime snapshot the watch daemon records so
// read-only commands can report on it without an IPC channel. Running plus a
// fresh LastHeartbeatAt means alive; Running with a stale heartbeat means the
// daemon died without a clean shutdown.
type DaemonStatus struct {
	Running         bool
	PID             int
	StartedAt       int64
	LastHeartbeatAt int64
	Mode            string
	Paused          bool
	LastSyncAt      int64
	LastCycleJSON   string
	LastError       string
	NextPollAt      int64
	GuardBlocked    bool
	GuardReason     string

	// LastErrorAuth marks LastError as Google refusing the refresh token —
	// revoked, or expired, which an unverified "testing" OAuth client does
	// about weekly. Retrying never clears it; only `synckeeper login` does,
	// so every view says so instead of showing raw OAuth JSON (W19).
	LastErrorAuth bool
}

// CycleSummary is the JSON stored in DaemonStatus.LastCycleJSON.
type CycleSummary struct {
	Actions    int   `json:"actions"`
	Executed   int   `json:"executed"`
	Failed     int   `json:"failed"`
	DurationMS int64 `json:"duration_ms"`
}

// Activity is one row of the recent-actions ring. Source is the direction of
// the change: "local" (a local change pushed to Drive), "remote" (a remote
// change pulled down), "conflict", or "" (errors / pre-v4 rows).
type Activity struct {
	ID      int64
	TS      int64
	Kind    string
	RelPath string
	Detail  string
	Source  string
}

// SetDaemonStatus upserts the singleton status row.
func (d *DB) SetDaemonStatus(s DaemonStatus) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`insert into daemon_status
		(id, running, pid, started_at, last_heartbeat_at, mode, paused,
		 last_sync_at, last_cycle_json, last_error, next_poll_at, guard_blocked, guard_reason,
		 last_error_auth)
		values (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			running = excluded.running, pid = excluded.pid, started_at = excluded.started_at,
			last_heartbeat_at = excluded.last_heartbeat_at, mode = excluded.mode, paused = excluded.paused,
			last_sync_at = excluded.last_sync_at, last_cycle_json = excluded.last_cycle_json,
			last_error = excluded.last_error, next_poll_at = excluded.next_poll_at,
			guard_blocked = excluded.guard_blocked, guard_reason = excluded.guard_reason,
			last_error_auth = excluded.last_error_auth`,
		boolToInt(s.Running), s.PID, s.StartedAt, s.LastHeartbeatAt, s.Mode, boolToInt(s.Paused),
		s.LastSyncAt, s.LastCycleJSON, s.LastError, s.NextPollAt, boolToInt(s.GuardBlocked), s.GuardReason,
		boolToInt(s.LastErrorAuth))
	return err
}

// GetDaemonStatus returns the singleton status, or ErrNotFound if the daemon
// has never recorded itself.
func (d *DB) GetDaemonStatus() (DaemonStatus, error) {
	var s DaemonStatus
	var running, paused, guard, authErr int
	err := d.sql.QueryRow(`select running, pid, started_at, last_heartbeat_at, mode, paused,
		last_sync_at, last_cycle_json, last_error, next_poll_at, guard_blocked, guard_reason,
		last_error_auth
		from daemon_status where id = 1`).Scan(
		&running, &s.PID, &s.StartedAt, &s.LastHeartbeatAt, &s.Mode, &paused,
		&s.LastSyncAt, &s.LastCycleJSON, &s.LastError, &s.NextPollAt, &guard, &s.GuardReason,
		&authErr)
	if errors.Is(err, sql.ErrNoRows) {
		return DaemonStatus{}, ErrNotFound
	}
	if err != nil {
		return DaemonStatus{}, err
	}
	s.Running, s.Paused, s.GuardBlocked, s.LastErrorAuth = running != 0, paused != 0, guard != 0, authErr != 0
	return s, nil
}

// AppendActivity records one action and trims the ring to activityCap rows.
func (d *DB) AppendActivity(a Activity) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.sql.Exec(`insert into activity (ts, kind, rel_path, detail, source) values (?, ?, ?, ?, ?)`,
		a.TS, a.Kind, a.RelPath, a.Detail, a.Source); err != nil {
		return err
	}
	_, err := d.sql.Exec(`delete from activity where id <= (select max(id) from activity) - ?`, activityCap)
	return err
}

// RecentActivity returns up to limit most-recent activity rows, newest first.
func (d *DB) RecentActivity(limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.sql.Query(`select id, ts, kind, rel_path, detail, source from activity
		order by id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.TS, &a.Kind, &a.RelPath, &a.Detail, &a.Source); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
