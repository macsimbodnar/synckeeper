package statedb

import (
	"database/sql"
	"errors"
	"strings"
)

// RemoteNode is the cached metadata of one Drive file, maintained by
// internal/remotedelta from the changes feed. The tree snapshot is derived
// by walking parent links from the root folder, so nodes outside the synced
// tree are harmless.
type RemoteNode struct {
	FileID   string
	ParentID string
	Name     string
	MimeType string
	MD5      string
	Size     int64
	Version  int64
	Trashed  bool

	// ModifiedNS is Drive's modifiedTime in unix nanoseconds; 0 means the feed
	// never reported one (a row written before schema v5). Only the init
	// merge's conflict naming reads it (spec §11, W18-E).
	ModifiedNS int64
}

// upsertRemoteNodeSQL is shared by the DB-level and tx-scoped writers so the
// change feed and our own commits produce byte-identical rows.
const upsertRemoteNodeSQL = `insert into remote_nodes
	(file_id, parent_id, name, mime_type, md5, size, version, trashed, modified_ns)
	values (?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(file_id) do update set parent_id = excluded.parent_id,
		name = excluded.name, mime_type = excluded.mime_type, md5 = excluded.md5,
		size = excluded.size, version = excluded.version, trashed = excluded.trashed,
		modified_ns = excluded.modified_ns`

func upsertRemoteNodeArgs(n RemoteNode) []any {
	return []any{n.FileID, n.ParentID, n.Name, n.MimeType, n.MD5, n.Size, n.Version, boolToInt(n.Trashed), n.ModifiedNS}
}

// UpsertRemoteNode inserts or replaces a cached node.
func (d *DB) UpsertRemoteNode(n RemoteNode) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(upsertRemoteNodeSQL, upsertRemoteNodeArgs(n)...)
	return err
}

// UpsertRemoteNodeTx writes a cached node inside tx — the executor's own
// remote-side commits (W16), which must land the mirror row and the baseline
// row together or not at all: a crash between them leaves the baseline
// naming a Drive id the mirror does not know, which reconcile reads as
// "deleted on Drive" (invariant 6).
func UpsertRemoteNodeTx(tx *sql.Tx, n RemoteNode) error {
	_, err := tx.Exec(upsertRemoteNodeSQL, upsertRemoteNodeArgs(n)...)
	return err
}

// DeleteRemoteNode drops a cached node (file permanently removed).
func (d *DB) DeleteRemoteNode(fileID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`delete from remote_nodes where file_id = ?`, fileID)
	return err
}

// DeleteRemoteNodesTx drops cached nodes inside tx — the mirror half of a
// remote trash (W16), committed with the baseline rows it retires. Batched
// like DeleteItemsByID so a collapsed subtree's statement count stays bounded.
func DeleteRemoteNodesTx(tx *sql.Tx, fileIDs []string) error {
	const batch = 400
	for len(fileIDs) > 0 {
		n := min(batch, len(fileIDs))
		args := make([]any, n)
		for i, id := range fileIDs[:n] {
			args[i] = id
		}
		q := `delete from remote_nodes where file_id in (?` + strings.Repeat(",?", n-1) + `)`
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
		fileIDs = fileIDs[n:]
	}
	return nil
}

// ClearRemoteNodes empties the cache (before a forced full walk).
func (d *DB) ClearRemoteNodes() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`delete from remote_nodes`)
	return err
}

// HasRemoteNode reports whether a cached node exists for the file id.
func (d *DB) HasRemoteNode(fileID string) (bool, error) {
	var one int
	err := d.sql.QueryRow(`select 1 from remote_nodes where file_id = ?`, fileID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// AllRemoteNodes returns the whole cache.
func (d *DB) AllRemoteNodes() ([]RemoteNode, error) {
	rows, err := d.sql.Query(`select file_id, parent_id, name, mime_type, md5, size, version, trashed, modified_ns
		from remote_nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemoteNode
	for rows.Next() {
		var n RemoteNode
		var trashed int
		if err := rows.Scan(&n.FileID, &n.ParentID, &n.Name, &n.MimeType, &n.MD5, &n.Size, &n.Version, &trashed, &n.ModifiedNS); err != nil {
			return nil, err
		}
		n.Trashed = trashed != 0
		out = append(out, n)
	}
	return out, rows.Err()
}
