package statedb

import (
	"database/sql"
	"strings"
	"time"
)

// Item is one row of the items table: the baseline state of a synced path.
type Item struct {
	DriveFileID  string
	RelPath      string
	IsDir        bool
	Size         int64
	ContentMD5   string
	LocalMtimeNS int64
	DriveMD5     string
	DriveVersion int64
	SyncedAt     int64
}

// Tx runs fn inside a write transaction, serialized with all other writes.
func (d *DB) Tx(fn func(tx *sql.Tx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// AllItems returns every baseline row, ordered by rel_path.
func (d *DB) AllItems() ([]Item, error) {
	rows, err := d.sql.Query(`select drive_file_id, rel_path, is_dir, coalesce(size, 0),
		coalesce(content_md5, ''), coalesce(local_mtime_ns, 0), coalesce(drive_md5, ''),
		coalesce(drive_version, 0), synced_at from items order by rel_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.DriveFileID, &it.RelPath, &it.IsDir, &it.Size,
			&it.ContentMD5, &it.LocalMtimeNS, &it.DriveMD5, &it.DriveVersion, &it.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpsertItem writes an item row inside tx. Any existing rows claiming the
// same drive_file_id or rel_path are removed first, so id changes
// (resurrection) and path changes (moves) cannot violate uniqueness.
func UpsertItem(tx *sql.Tx, it Item) error {
	if it.SyncedAt == 0 {
		it.SyncedAt = time.Now().Unix()
	}
	if _, err := tx.Exec(`delete from items where drive_file_id = ? or rel_path = ?`,
		it.DriveFileID, it.RelPath); err != nil {
		return err
	}
	_, err := tx.Exec(`insert into items
		(drive_file_id, rel_path, is_dir, size, content_md5, local_mtime_ns, drive_md5, drive_version, synced_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.DriveFileID, it.RelPath, boolToInt(it.IsDir), it.Size, it.ContentMD5,
		it.LocalMtimeNS, it.DriveMD5, it.DriveVersion, it.SyncedAt)
	return err
}

// DeleteItemByID removes the row for a drive file id inside tx.
func DeleteItemByID(tx *sql.Tx, driveFileID string) error {
	_, err := tx.Exec(`delete from items where drive_file_id = ?`, driveFileID)
	return err
}

// RenameItemPath moves a row (and, for directories, every descendant row)
// from oldPath to newPath inside tx.
func RenameItemPath(tx *sql.Tx, oldPath, newPath string) error {
	if _, err := tx.Exec(`update items set rel_path = ? where rel_path = ?`, newPath, oldPath); err != nil {
		return err
	}
	prefix := oldPath + "/"
	rows, err := tx.Query(`select drive_file_id, rel_path from items where rel_path like ? escape '\'`,
		escapeLike(prefix)+"%")
	if err != nil {
		return err
	}
	type ren struct{ id, path string }
	var rens []ren
	for rows.Next() {
		var r ren
		if err := rows.Scan(&r.id, &r.path); err != nil {
			rows.Close()
			return err
		}
		rens = append(rens, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range rens {
		if _, err := tx.Exec(`update items set rel_path = ? where drive_file_id = ?`,
			newPath+"/"+strings.TrimPrefix(r.path, prefix), r.id); err != nil {
			return err
		}
	}
	return nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpdateItemDrive refreshes only the Drive-side fields of an item row.
// Deliberately narrow (R18, spec §7): a move's commit must never restate
// the row's local truth — the scanned size/mtime/md5 stay untouched, so an
// edit landing after the scan remains visibly dirty to the next scan.
func UpdateItemDrive(tx *sql.Tx, driveFileID, driveMD5 string, version int64) error {
	_, err := tx.Exec(`update items set drive_md5 = ?, drive_version = ? where drive_file_id = ?`,
		driveMD5, version, driveFileID)
	return err
}
