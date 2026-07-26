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

	// CaseFold/NormFold mirror the sync dir's probed name folding (spec §5).
	// When set, new-local vs new-remote matching also compares names under
	// the fold, so fold-equal siblings fire the ordinary §4.2 adopt/conflict
	// instead of blind-uploading a duplicate (C2, R19).
	CaseFold bool
	NormFold bool

	// ShadowedRemote holds file ids that exist on Drive but were collapsed
	// out of the remote snapshot (a duplicate or fold-colliding sibling
	// lost "first by id"). A baseline row whose id is shadowed is held
	// harmless — surfaced as a skip, never read as remote-deleted (C2b).
	ShadowedRemote map[string]bool
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

	// ProtectedBy names the moves-stage action (a ConflictBackup or a local
	// move, by its source RelPath) that must have succeeded for this action
	// to act on the right file. The executor refuses the action if that
	// predecessor failed — durability invariant 7: destruction (and
	// recording, which overwrites the baseline's truth) never outruns
	// protection.
	ProtectedBy string

	// Subtree is what a collapsed directory delete stands for (W13-T2): the
	// descendant deletes it absorbed, each carrying the stat the scan pinned,
	// so the executor can verify at execution time that the directory still
	// holds exactly the content the plan reasoned about — and nothing else —
	// before moving the whole thing to the trash. Empty on every other action,
	// including a directory delete that absorbed nothing.
	Subtree []SubtreeEntry

	// SubtreeFiles is the number of FILES a collapsed delete covers (the file
	// entries of Subtree). It keeps counting honest after the collapse: the
	// mass-delete guard counts content, not containers (spec §6, R10), and one
	// action now stands for many files — reporting says "1145 files", not "1".
	SubtreeFiles int

	// Downloads: the local state the plan assumes will be at RelPath when
	// the atomic replace happens (after moves and backups have run). The
	// executor re-stats the target immediately before the rename and
	// refuses the download on any drift — a local write racing the cycle
	// must win it (spec §7 overwrite guard). Zero values mean "absent".
	LocalExists  bool
	LocalSize    int64
	LocalMtimeNS int64
}

// SubtreeEntry is one covered descendant of a collapsed directory delete:
// its rel_path plus, for a file, the size/mtime the scan observed. The
// executor re-stats against those before the directory moves, so an edit
// landing between scan and execution still wins the cycle (§4.2 "edit beats
// delete", R13) even though its own action was absorbed.
type SubtreeEntry struct {
	RelPath string
	IsDir   bool
	FileID  string // the baseline row this entry retires
	Size    int64
	MtimeNS int64
}

// Skip is a reported, non-fatal exclusion (invalid name, type clash, ...).
// FileID is set when the skipped name shadows a live remote id (a duplicate
// or fold-colliding sibling): reconcile holds baseline rows for those ids
// harmless instead of reading them as remote-deleted (C2b, R19).
type Skip struct {
	RelPath string
	Reason  string
	FileID  string
}
