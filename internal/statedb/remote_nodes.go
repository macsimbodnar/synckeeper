package statedb

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
}

// UpsertRemoteNode inserts or replaces a cached node.
func (d *DB) UpsertRemoteNode(n RemoteNode) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`insert into remote_nodes
		(file_id, parent_id, name, mime_type, md5, size, version, trashed)
		values (?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(file_id) do update set parent_id = excluded.parent_id,
			name = excluded.name, mime_type = excluded.mime_type, md5 = excluded.md5,
			size = excluded.size, version = excluded.version, trashed = excluded.trashed`,
		n.FileID, n.ParentID, n.Name, n.MimeType, n.MD5, n.Size, n.Version, boolToInt(n.Trashed))
	return err
}

// DeleteRemoteNode drops a cached node (file permanently removed).
func (d *DB) DeleteRemoteNode(fileID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`delete from remote_nodes where file_id = ?`, fileID)
	return err
}

// ClearRemoteNodes empties the cache (before a forced full walk).
func (d *DB) ClearRemoteNodes() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`delete from remote_nodes`)
	return err
}

// AllRemoteNodes returns the whole cache.
func (d *DB) AllRemoteNodes() ([]RemoteNode, error) {
	rows, err := d.sql.Query(`select file_id, parent_id, name, mime_type, md5, size, version, trashed
		from remote_nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemoteNode
	for rows.Next() {
		var n RemoteNode
		var trashed int
		if err := rows.Scan(&n.FileID, &n.ParentID, &n.Name, &n.MimeType, &n.MD5, &n.Size, &n.Version, &trashed); err != nil {
			return nil, err
		}
		n.Trashed = trashed != 0
		out = append(out, n)
	}
	return out, rows.Err()
}
