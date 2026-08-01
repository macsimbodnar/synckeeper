// Package statedb owns the SQLite baseline database: schema, migrations,
// and typed accessors. All writes are serialized through a mutex because
// modernc.org/sqlite with WAL tolerates a single writer.
package statedb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Well-known meta keys.
const (
	MetaSchemaVersion = "schema_version"
	MetaPageToken     = "page_token"
	MetaRootFolderID  = "root_folder_id"
	MetaMachineID     = "machine_id"
	// MetaRootFolderName is the Drive folder's name as last observed (W18).
	// The ID is authoritative; this is what the read-only views display, so a
	// folder renamed in the Drive web UI reads truthfully without config.toml
	// ever being rewritten (rewriting it would strip the user's comments).
	// drive.folder_name keeps one job: naming the folder when we must create it.
	MetaRootFolderName = "root_folder_name"
)

// ErrNotFound is returned by Get-style accessors when no row matches.
var ErrNotFound = errors.New("statedb: not found")

// DB wraps the SQLite handle with write serialization.
type DB struct {
	sql *sql.DB
	mu  sync.Mutex // held around every write transaction
}

// migrations[i] upgrades the schema from version i to i+1. The current
// schema version is len(migrations).
var migrations = []string{
	// v0 -> v1: initial schema, per docs/spec.md.
	`
	create table items (
	  drive_file_id   text primary key,
	  rel_path        text not null unique,
	  is_dir          integer not null,
	  size            integer,
	  content_md5     text,
	  local_mtime_ns  integer,
	  drive_md5       text,
	  drive_version   integer,
	  synced_at       integer not null
	);
	create table meta (
	  key   text primary key,
	  value text not null
	);
	create table pending_ops (
	  op_id         integer primary key autoincrement,
	  op_type       text not null,
	  rel_path      text,
	  drive_file_id text,
	  payload       text,
	  state         text not null default 'planned'
	);
	`,
	// v1 -> v2: remote_nodes, the cached Drive metadata fed by changes.list.
	`
	create table remote_nodes (
	  file_id   text primary key,
	  parent_id text not null,
	  name      text not null,
	  mime_type text not null,
	  md5       text not null default '',
	  size      integer not null default 0,
	  version   integer not null default 0,
	  trashed   integer not null default 0
	);
	`,
	// v2 -> v3: daemon runtime state for monitoring (phase 6). daemon_status
	// is a singleton heartbeat the `watch` daemon updates; activity is a
	// capped ring of recent actions read by `status`/`activity`.
	`
	create table daemon_status (
	  id                integer primary key check (id = 1),
	  running           integer not null default 0,
	  pid               integer not null default 0,
	  started_at        integer not null default 0,
	  last_heartbeat_at integer not null default 0,
	  mode              text    not null default '',
	  paused            integer not null default 0,
	  last_sync_at      integer not null default 0,
	  last_cycle_json   text    not null default '',
	  last_error        text    not null default '',
	  next_poll_at      integer not null default 0,
	  guard_blocked     integer not null default 0,
	  guard_reason      text    not null default ''
	);
	create table activity (
	  id       integer primary key autoincrement,
	  ts       integer not null,
	  kind     text    not null,
	  rel_path text    not null default '',
	  detail   text    not null default ''
	);
	`,
	// v3 -> v4: activity.source — direction of the change, "local" (pushed to
	// Drive) or "remote" (pulled down), so activity/status can label events.
	`alter table activity add column source text not null default '';`,
	// v4 -> v5: remote_nodes.modified_ns — Drive's modifiedTime in unix
	// nanoseconds, read only by the init merge's conflict naming (spec §11,
	// W18-E). No backfill: 0 means "not known" and falls back to the clock-free
	// rule, and `init` — the one command that reads the column — force-walks
	// the mirror before reading it, so it populates what it consumes.
	`alter table remote_nodes add column modified_ns integer not null default 0;`,
}

// Path returns the state DB path inside the given config dir.
func Path(configDir string) string {
	return filepath.Join(configDir, "state.db")
}

func dsn(path string) string {
	return path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
}

// Open opens (creating and migrating if needed) the database at path.
func Open(path string) (*DB, error) {
	handle, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	// One connection: modernc/sqlite serializes internally, and a single
	// conn makes the writer mutex sufficient.
	handle.SetMaxOpenConns(1)
	db := &DB{sql: handle}
	if err := db.migrate(); err != nil {
		handle.Close()
		return nil, err
	}
	return db, nil
}

// OpenRead opens an existing database for read-only commands, which run
// WITHOUT the instance lock and therefore must never migrate the schema
// under a live daemon (spec §14). It accepts only an exact schema-version
// match — skew in either direction is refused with guidance — and it never
// creates a missing database.
func OpenRead(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no state db at %s: run `synckeeper init` first", path)
		}
		return nil, err
	}
	handle, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	handle.SetMaxOpenConns(1)
	db := &DB{sql: handle}
	version, err := db.schemaVersion()
	if err != nil {
		handle.Close()
		return nil, err
	}
	switch {
	case version < len(migrations):
		handle.Close()
		return nil, fmt.Errorf("state db schema v%d predates this binary (v%d); run `synckeeper sync` once — or restart the daemon — with this binary to migrate, then retry", version, len(migrations))
	case version > len(migrations):
		handle.Close()
		return nil, fmt.Errorf("state db schema version %d is newer than this binary supports (%d)", version, len(migrations))
	}
	return db, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// schemaVersion reads the recorded schema version; 0 means a fresh
// (pre-migration) database.
func (d *DB) schemaVersion() (int, error) {
	var v string
	err := d.sql.QueryRow(`select value from meta where key = ?`, MetaSchemaVersion).Scan(&v)
	switch {
	case err == nil:
		n, aerr := strconv.Atoi(v)
		if aerr != nil {
			return 0, fmt.Errorf("corrupt schema_version %q: %w", v, aerr)
		}
		return n, nil
	case isMissingTable(err):
		return 0, nil
	default:
		return 0, fmt.Errorf("read schema version: %w", err)
	}
}

func (d *DB) migrate() error {
	version, err := d.schemaVersion()
	if err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("state db schema version %d is newer than this binary supports (%d)", version, len(migrations))
	}
	for ; version < len(migrations); version++ {
		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[version]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration to v%d: %w", version+1, err)
		}
		if _, err := tx.Exec(`insert into meta (key, value) values (?, ?)
			on conflict(key) do update set value = excluded.value`,
			MetaSchemaVersion, strconv.Itoa(version+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record schema version v%d: %w", version+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func isMissingTable(err error) bool {
	return err != nil && (errors.Is(err, sql.ErrNoRows) ||
		// modernc/sqlite reports "no such table: meta" as a plain error string.
		strings.Contains(err.Error(), "no such table"))
}

// GetMeta returns the value for key, or ErrNotFound.
func (d *DB) GetMeta(key string) (string, error) {
	var v string
	err := d.sql.QueryRow(`select value from meta where key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetMeta inserts or updates a meta key.
func (d *DB) SetMeta(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`insert into meta (key, value) values (?, ?)
		on conflict(key) do update set value = excluded.value`, key, value)
	return err
}

// ItemCount returns the number of tracked items.
func (d *DB) ItemCount() (int, error) {
	var n int
	err := d.sql.QueryRow(`select count(*) from items`).Scan(&n)
	return n, err
}

// PendingOpCount returns the number of pending_ops rows not yet done.
func (d *DB) PendingOpCount() (int, error) {
	var n int
	err := d.sql.QueryRow(`select count(*) from pending_ops where state != 'done'`).Scan(&n)
	return n, err
}
