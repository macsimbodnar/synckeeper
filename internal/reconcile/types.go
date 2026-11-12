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

	// ParentID is the Drive folder the mirror holds the item in — what a
	// remote mutation pins as part of its expected state (RemoteExpect).
	ParentID string

	// ModifiedNS is Drive's modifiedTime in unix nanoseconds, 0 when unknown.
	// Read only under PreferNewer.
	ModifiedNS int64
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

	// PreferNewer makes a both-new conflict give the canonical name to the
	// side edited last instead of always to Drive. Set by exactly one caller,
	// `init`'s merge (spec §11, W18-E): joining a machine is a one-off event
	// where the user's own answer to "which of these two is the real one" is
	// the more recent edit. Steady-state §4.2 leaves it false and keeps
	// remote-wins, which is deterministic WITHOUT a clock — every machine
	// agrees on what Drive holds, while newer-wins needs cross-machine clock
	// agreement and a skewed machine would always lose the plain name.
	PreferNewer bool

	// ShadowedRemote holds file ids that exist on Drive but were collapsed
	// out of the remote snapshot (a duplicate or fold-colliding sibling
	// lost "first by id"). A baseline row whose id is shadowed is held
	// harmless — surfaced as a skip, never read as remote-deleted (C2b).
	ShadowedRemote map[string]bool

	// ShadowedAt is, for a shadowed id the snapshot skipped by name (not one
	// under a shadowed folder), where Drive holds it and its mirror entry
	// (S030). A held row whose file was renamed here pairs with it like any
	// local rename: the Drive item follows (MoveRemote), and the name
	// conflict that held it is gone.
	ShadowedAt map[string]ShadowedItem

	// UnreadableLocal is the same idea on the local side (W18-G): baseline
	// file ids that live under a directory the scan could not read. They are
	// absent from Local for a reason that is not deletion, so a plan that
	// believed the snapshot would trash the user's Drive copies of an entire
	// subtree because one folder lost its read permission.
	UnreadableLocal map[string]bool

	// UnscannedLocal is the same idea for a path that IS on disk but that the
	// scan does not read (S013, audit 2026-11-11 F07): a symlink, never
	// followed, a non-regular file, or a file it may not open (S014) —
	// mapped to the scan's reason. Unlike an
	// unreadable directory the path itself is absent from Local, so the hold
	// is by path: nothing at or under it is decided or written. Baseline rows
	// there are reported, never deleted, moved, downloaded or forgotten; no
	// remote item is materialized there (under a symlinked folder that write
	// would land outside the sync dir); and a folder holding one is never
	// removed — nor is a folder whose Drive or local subtree holds a held
	// row's item. When it is a plain file or folder again, the untouched rows
	// meet it in the ordinary three-way table. Matched under CaseFold /
	// NormFold, at the exact path only: a link elsewhere holds nothing else.
	UnscannedLocal map[string]string

	// UnreadableDirs are the local paths of directories the scan could not
	// read (W18-G). Rows whose local location — after a detected local folder
	// rename — lies under one are held, which UnreadableLocal, keyed by the
	// baseline path, cannot see (S014).
	UnreadableDirs map[string]bool

	// UnreadableFiles maps each local file the scan could not open to the
	// stat it saw (S014). A baseline file row gone locally whose size AND
	// mtime equal those of an unreadable file with no baseline row of its own
	// is that file renamed, and is held rather than read as deleted.
	UnreadableFiles map[string]LocalItem
}

// ShadowedItem is a shadowed Drive item: its path on Drive and its entry.
type ShadowedItem struct {
	Path string
	Item RemoteItem
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

	// RemoteWant is the remote counterpart (spec §7 remote write gate, S010):
	// on UpdateRemote, TrashRemote and MoveRemote, the Drive state the plan
	// was built on. The executor hands it to the Drive client, which re-checks
	// it immediately before the mutation and refuses on any drift — another
	// machine's write landing mid-cycle must reach the decision table, not be
	// overwritten. A folder TrashRemote also carries the folder's contents,
	// which the executor checks against Drive's listing first (S021). nil
	// means no expectation: an action journaled before the field existed.
	RemoteWant *RemoteExpect `json:",omitempty"`
}

// RemoteExpect is the Drive state a remote mutation expects to find: the
// snapshot entry the plan was built on. Content (MD5) for a file; for a
// folder its identity — name, parent, not trashed — and, on a folder
// TrashRemote, its contents (S021).
type RemoteExpect struct {
	ParentID string
	Name     string
	IsDir    bool
	MD5      string

	// Contents is, on a folder TrashRemote, what the folder may still hold on
	// Drive when the trash runs (S021) — trashing a folder takes its whole
	// subtree. nil means the contents are not checked: any other action, a
	// journal row from before S021, or a hand-built action.
	Contents *RemoteContents `json:",omitempty"`
}

// RemoteContents is a folder trash's expectation of the folder's subtree on
// Drive: the plan's mirror rows under it, minus what the plan's own earlier
// actions trash or move out (PinFolderTrashContents). The gate lists the
// folder and refuses on anything else — a file edited, added, renamed, moved,
// trashed or gone. Empty Items is the normal expectation for a folder emptied
// item by item: then the folder must hold nothing.
type RemoteContents struct {
	Items []RemoteChild
}

// RemoteChild is one item inside a folder on Drive as the mirror holds it:
// what identifies its place (parent, name) and its version (md5 for a file,
// identity alone for a folder). Every mirror row counts, including those the
// snapshot skips (Google-native, ignored, invalid or shadowed names) — they
// go to the bin with the folder too.
type RemoteChild struct {
	FileID   string
	ParentID string
	Name     string
	IsDir    bool
	MD5      string `json:",omitempty"`
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

	// Unreadable marks a local directory the scan could not read (W18-G).
	// Everything under it is missing from the local snapshot for a reason
	// that is not "the user deleted it", so the engine expands this into
	// Input.UnreadableLocal and the planner holds those rows harmless.
	Unreadable bool

	// Unscanned marks a local path that exists but the scan did not read: a
	// symlink or a non-regular file (S013), or a file it may not open (S014).
	// The engine expands it into
	// Input.UnscannedLocal, so a tracked item replaced by one is held, not
	// read as deleted.
	Unscanned bool

	// UnreadableFile marks, beside Unscanned, a file the scan may not open
	// (S014). It only selects the daemon's warning; it is deliberately not
	// Unreadable, whose expansion holds rows under a directory.
	UnreadableFile bool

	// Size and MtimeNS are, for an UnreadableFile, the stat the scan saw
	// (lstat works where open does not). The planner matches them against
	// baseline rows gone locally: that file renamed, then unreadable (S014).
	Size    int64
	MtimeNS int64

	// Duplicate marks the one skip kind that means Drive itself holds two
	// items under one name in one folder (it allows that; a filesystem does
	// not). Unlike every other skip it is not a property of the file — it is
	// a state of the Drive folder that a person has to resolve there, so
	// `doctor` reports it separately (W17). Fold collisions are NOT this:
	// those are two distinct Drive names this filesystem cannot tell apart.
	Duplicate bool

	// Kind types the skip for a UI that renders it on its own terms (S030,
	// DEC-011); "" for every skip without one. Reason is always set.
	Kind SkipKind

	// DrivePath and LocalPath are, on a SkipNameConflict, the two names: the
	// item on Drive's side (there now, or where this plan would put it) and
	// the file or folder on this disk that keeps the place. RelPath is the
	// item the skip is about, which may be a third path (a row whose move
	// waits, a file inside a folder that waits).
	DrivePath string
	LocalPath string
}

// SkipKind classifies a skip; see Skip.Kind.
type SkipKind string

// SkipNameConflict: on a disk that folds case or Unicode normalization (§5),
// two different items want one place under names that differ only in that
// fold — one on Drive, one here. Neither is written over the other, nor
// uploaded beside it as a second name on Drive; both stay as they are, and
// the hold releases by itself once the user renames one of them (DEC-011;
// folders: DEC-013's name clash).
const SkipNameConflict SkipKind = "name_conflict"
