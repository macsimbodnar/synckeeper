// Package reconcile is the pure correctness core: given the persisted
// baseline and fresh local/remote snapshots it returns an ordered action
// plan. No filesystem, network, or DB access — fully unit-testable.
package reconcile

import "time"

// BaseItem is the baseline view of one path (from the items table).
type BaseItem struct {
	FileID       string
	IsDir        bool
	Size         int64
	MD5          string // content md5 as last synced
	MtimeNS      int64  // local mtime observed after last sync
	DriveMD5     string // md5 Drive reported at last sync
	DriveVersion int64
}

// LocalItem is the scanner's view of one path.
type LocalItem struct {
	IsDir   bool
	Size    int64
	MtimeNS int64
	MD5     string // empty for dirs; computed or trusted from baseline
}

// RemoteItem is the remote-snapshot view of one path (not trashed, inside
// the tree, deduplicated).
type RemoteItem struct {
	FileID  string
	IsDir   bool
	Size    int64
	MD5     string // empty for dirs
	Version int64
}

// Input bundles the three snapshots plus what conflict naming needs.
type Input struct {
	Base    map[string]BaseItem   // key: rel_path
	Local   map[string]LocalItem  // key: rel_path
	Remote  map[string]RemoteItem // key: rel_path
	Machine string
	Now     time.Time
}

// Type enumerates plan actions.
type Type string

const (
	MkdirLocal      Type = "mkdir_local"      // create local dir for remote folder FileID
	MkdirRemote     Type = "mkdir_remote"     // create remote folder (new id)
	MoveLocal       Type = "move_local"       // rename/move local RelPath -> NewRelPath (remote-driven)
	MoveRemote      Type = "move_remote"      // rename/move remote FileID; RelPath -> NewRelPath (local-driven)
	ConflictBackup  Type = "conflict_backup"  // rename local RelPath -> NewRelPath (conflict copy)
	Upload          Type = "upload"           // create remote file from local RelPath
	UpdateRemote    Type = "update_remote"    // new revision of FileID from local RelPath
	Download        Type = "download"         // fetch FileID to RelPath (atomic replace); MD5/Size expected
	Record          Type = "record"           // baseline row refresh/adopt only, no transfer
	TrashRemote     Type = "trash_remote"     // move FileID to Drive trash, drop row
	QuarantineLocal Type = "quarantine_local" // move local RelPath to quarantine, drop row
	Forget          Type = "forget"           // drop row only (gone on both sides)
)

// Action is one step of the plan. Fields are populated as each Type needs.
type Action struct {
	Type       Type
	RelPath    string
	NewRelPath string // moves and conflict backups
	FileID     string
	IsDir      bool
	Size       int64
	MD5        string // expected content md5 (download verify, record rows)
	Version    int64  // remote version for record rows
}

// Skip is a reported, non-fatal exclusion (invalid name, type clash, ...).
type Skip struct {
	RelPath string
	Reason  string
}
