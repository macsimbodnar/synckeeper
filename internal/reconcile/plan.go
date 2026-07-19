package reconcile

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/conflicts"
)

type remoteRef struct {
	path string
	item RemoteItem
}

// Plan computes the ordered action plan. Output order: local dir moves
// (top-down), mkdirs (top-down), file moves and conflict backups, transfers,
// deletes (bottom-up).
func Plan(in Input) ([]Action, []Skip) {
	var skips []Skip
	var mkdirs, moves, transfers, deletes []Action

	remByID := make(map[string]remoteRef, len(in.Remote))
	for p, r := range in.Remote {
		remByID[r.FileID] = remoteRef{path: p, item: r}
	}
	baseIDs := make(map[string]bool, len(in.Base))
	for _, b := range in.Base {
		baseIDs[b.FileID] = true
	}
	// newRemoteAt: a remote item at path p that is not a baseline item —
	// i.e. brand-new remote content, possibly replacing a baseline path.
	newRemoteAt := func(p string) (RemoteItem, bool) {
		r, ok := in.Remote[p]
		if !ok || baseIDs[r.FileID] {
			return RemoteItem{}, false
		}
		return r, true
	}
	// claimed marks new-remote paths already resolved by an earlier pass
	// (adopted, conflicted, or replacing baseline content in place), so
	// pass 3 does not download them a second time.
	claimed := map[string]bool{}
	// backedUp marks a local path whose untracked content a move reclaimed:
	// it has been routed to a conflict copy, so pass 2 must not also upload it
	// as a plain new file.
	backedUp := map[string]bool{}

	// Directory moves: two detectors, one representation (spec §4.3).
	// Remote-driven: the remote reports the same folder id at a new path —
	// direct identity, the local side follows with one MoveLocal.
	// Local-driven: the folder id is unknown for the new local dir, so the
	// move is collapsed out of the file-pairing evidence — the Drive folder
	// keeps its identity through one MoveRemote reparent.
	var remoteDirMoves []dirMove
	for p, b := range in.Base {
		if !b.IsDir {
			continue
		}
		if ra, ok := remByID[b.FileID]; ok && ra.path != p {
			remoteDirMoves = append(remoteDirMoves, dirMove{from: p, to: ra.path})
		}
	}
	rewriteRD := mkRewrite(remoteDirMoves)
	localDirMoves := detectLocalDirMoves(in, remByID, baseIDs, rewriteRD)

	// rewrite maps a baseline path to its post-plan LOCAL location, applying
	// the longest strictly-containing moved ancestor from either detector.
	rewrite := mkRewrite(remoteDirMoves, localDirMoves)
	// rewriteLD applies only the local-driven moves. It serves the two sides
	// a local-driven rename leaves behind (the side that hasn't moved yet):
	// where the user's rename already put a baseline row's local file, and
	// where a current remote path will sit after the planned reparent.
	rewriteLD := mkRewrite(localDirMoves)
	localMoveDstOf := map[string]string{}
	for _, m := range localDirMoves {
		localMoveDstOf[m.from] = m.to
	}
	// invLD maps a new local path back through the local-driven moves —
	// exact destination included — to the baseline path that explains it.
	invLD := func(q string) string {
		best := -1
		out := q
		for _, m := range localDirMoves {
			switch {
			case q == m.to && len(m.to) > best:
				best = len(m.to)
				out = m.from
			case strings.HasPrefix(q, m.to+"/") && len(m.to) > best:
				best = len(m.to)
				out = m.from + q[len(m.to):]
			}
		}
		return out
	}

	for _, m := range remoteDirMoves {
		// The dir's own move, expressed from wherever its moved ancestors
		// (if any) already put it.
		from := rewrite(m.from)
		if from != m.to {
			moves = append(moves, Action{Type: MoveLocal, RelPath: from, NewRelPath: m.to, IsDir: true})
		}
	}
	for _, m := range localDirMoves {
		// One reparent carries the whole subtree; the per-file moves it
		// explains are never emitted (pass 1 sees the children at their
		// post-rename paths and finds nothing to do).
		moves = append(moves, Action{Type: MoveRemote, RelPath: m.from, NewRelPath: m.to,
			FileID: in.Base[m.from].FileID, IsDir: true})
	}

	// Local move pairing candidates: baseline files deleted locally whose
	// remote is unchanged (safe to reinterpret as the source of a move).
	type moveSrc struct {
		path string
		b    BaseItem
	}
	pairKey := func(md5 string, size int64) string { return md5 + "|" + itoa(size) }
	moveSrcs := map[string][]moveSrc{}
	var trashCandidates []moveSrc

	// --- Pass 1: baseline files ---------------------------------------
	for _, p := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[p]
		if b.IsDir {
			continue // dirs resolved in pass 4
		}
		// Under a local-driven dir rename this row's file, if it survives,
		// already sits at its post-rename location — every row is decided
		// at the path the item will occupy (§4.2), on both sides.
		localPath := rewriteLD(p)
		loc, locOK := in.Local[localPath]
		if locOK && loc.IsDir {
			// A dir now sits where the baseline had a file. Rare type
			// clash; report and leave both sides alone.
			skips = append(skips, Skip{RelPath: p, Reason: "type changed from file to directory; not synced"})
			continue
		}
		ra, remOK := remByID[b.FileID]
		expected := rewrite(p) // local path after all planned dir moves execute
		pathNow := expected
		remoteMoved := false
		if remOK {
			// The remote side of a local-driven move hasn't moved yet:
			// judge and emit at its post-reparent path.
			pathNow = rewriteLD(ra.path)
			remoteMoved = pathNow != expected
		}
		localChanged := locOK && loc.MD5 != b.MD5
		remoteContentChanged := remOK && ra.item.MD5 != b.DriveMD5
		// A remote move normally becomes a local move — except in a true
		// conflict, where the local content leaves through its conflict
		// backup instead, taken from where the content actually sits
		// (invariant 7): routing it through a move first would make the
		// backup depend on an ordering the plan must never get wrong.
		trueConflict := locOK && remOK && localChanged && remoteContentChanged && loc.MD5 != ra.item.MD5
		if remoteMoved && locOK && !trueConflict {
			mv := Action{Type: MoveLocal, RelPath: expected, NewRelPath: pathNow, FileID: b.FileID}
			// The move's destination may already be occupied locally. Never let
			// the rename silently clobber it (invariant 7).
			if occ, occupied := in.Local[pathNow]; occupied && pathNow != expected {
				_, tracked := in.Base[pathNow]
				switch {
				case !occ.IsDir && !tracked:
					// Untracked local-only file: its only copy lives here.
					// Preserve it as a conflict copy before the move vacates the
					// path, and refuse the move if that backup fails.
					cp := conflicts.Path(pathNow, in.Machine, in.Now)
					moves = append(moves, Action{Type: ConflictBackup, RelPath: pathNow, NewRelPath: cp})
					transfers = append(transfers, Action{Type: Upload, RelPath: cp, ProtectedBy: pathNow})
					mv.ProtectedBy = pathNow
					backedUp[pathNow] = true
				case !occ.IsDir && tracked:
					// Tracked content (bytes safe on Drive, e.g. a swap): the
					// executor may clobber it, but must first confirm it is
					// still the file the scan saw so a racing write wins (§7).
					mv.LocalExists, mv.LocalSize, mv.LocalMtimeNS = true, occ.Size, occ.MtimeNS
				default:
					// A directory occupies a file's destination — a type clash
					// never resolved by clobbering; the executor refuses it
					// (LocalExists stays false), leaving both sides intact.
				}
			}
			moves = append(moves, mv)
		}

		switch {
		case !locOK && !remOK:
			deletes = append(deletes, Action{Type: Forget, RelPath: p, FileID: b.FileID})

		case !locOK: // local deleted, remote exists
			if remoteContentChanged || remoteMoved {
				// Edit (or move) beats delete: restore the remote version.
				transfers = append(transfers, Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
					MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version})
			} else {
				src := moveSrc{path: p, b: b}
				moveSrcs[pairKey(b.MD5, b.Size)] = append(moveSrcs[pairKey(b.MD5, b.Size)], src)
				trashCandidates = append(trashCandidates, src)
			}

		case !remOK: // remote deleted, local exists
			newRem, replaced := newRemoteAt(localPath)
			switch {
			case localChanged && replaced && newRem.MD5 == loc.MD5:
				// Same new content arrived on both sides independently.
				transfers = append(transfers, Action{Type: Record, RelPath: localPath, FileID: newRem.FileID,
					MD5: newRem.MD5, Size: newRem.Size, Version: newRem.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
				claimed[localPath] = true
			case localChanged && replaced:
				cp := conflicts.Path(localPath, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: localPath, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp, ProtectedBy: localPath},
					Action{Type: Download, RelPath: localPath, FileID: newRem.FileID,
						MD5: newRem.MD5, Size: newRem.Size, Version: newRem.Version,
						ProtectedBy: localPath})
				claimed[localPath] = true
			case localChanged:
				// Resurrect: edit beats delete; re-upload as a new file.
				transfers = append(transfers, Action{Type: Upload, RelPath: localPath})
			case replaced:
				// Unchanged local will be replaced in place by the new
				// remote item (handled in pass 3); no quarantine.
			default:
				// The quarantine carries the scanned stat (R13/A7): the
				// executor refuses to quarantine a file that drifted since
				// the scan — an edit landing mid-cycle wins the cycle
				// ("edit beats delete", §4.2).
				deletes = append(deletes, Action{Type: QuarantineLocal, RelPath: localPath, FileID: b.FileID,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
			}

		default: // both exist
			switch {
			case !localChanged && !remoteContentChanged:
				if b.MtimeNS != loc.MtimeNS || b.Size != loc.Size || b.DriveVersion != ra.item.Version {
					act := Action{Type: Record, RelPath: pathNow, FileID: b.FileID,
						MD5: b.MD5, Size: loc.Size, Version: ra.item.Version,
						LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS}
					if remoteMoved {
						act.ProtectedBy = expected // the record acts on the moved file
					}
					transfers = append(transfers, act)
				}
			case localChanged && !remoteContentChanged:
				act := Action{Type: UpdateRemote, RelPath: pathNow, FileID: b.FileID}
				if remoteMoved {
					act.ProtectedBy = expected // upload must read the moved file, not a stranger
				}
				transfers = append(transfers, act)
			case !localChanged && remoteContentChanged:
				// The download replaces the scanned local file (moved into
				// place first when the remote also renamed it).
				transfers = append(transfers, Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
					MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
			case loc.MD5 == ra.item.MD5:
				// Both changed to identical content; record, no transfer.
				act := Action{Type: Record, RelPath: pathNow, FileID: b.FileID,
					MD5: loc.MD5, Size: loc.Size, Version: ra.item.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS}
				if remoteMoved {
					act.ProtectedBy = expected
				}
				transfers = append(transfers, act)
			default:
				// True conflict: local becomes the conflicted copy — backed
				// up from its current location — and remote keeps the
				// canonical name; the copy is uploaded too. Upload and
				// download both carry the backup as their protector.
				cp := conflicts.Path(expected, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: expected, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp, ProtectedBy: expected},
					Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
						MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version,
						ProtectedBy: expected})
			}
		}
	}

	// --- Pass 2: new local paths ---------------------------------------
	pairedSrc := map[string]bool{} // baseline path consumed as a move source
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		if _, inBase := in.Base[p]; inBase {
			continue
		}
		if q := invLD(p); q != p {
			if _, inBase := in.Base[q]; inBase {
				continue // explained by a local-driven dir move; pass 1 handled the row
			}
		}
		if backedUp[p] {
			continue // reclaimed by a move; already preserved as a conflict copy
		}
		loc := in.Local[p]
		target := rewrite(p)
		// Every row resolves at the path the item will occupy when the
		// action runs (§4.2, R11): under a remotely-moved ancestor the new
		// local file meets its remote counterpart at the POST-move path —
		// looking up the pre-move path silently skipped the both-new rows
		// and emitted an upload and a download onto one rel_path.
		if loc.IsDir {
			if r, ok := newRemoteAt(target); ok && r.IsDir {
				transfers = append(transfers, Action{Type: Record, RelPath: target, FileID: r.FileID,
					IsDir: true, Version: r.Version})
				claimed[target] = true
			} else if _, taken := in.Remote[target]; !taken {
				mkdirs = append(mkdirs, Action{Type: MkdirRemote, RelPath: target, IsDir: true})
			}
			continue
		}
		if r, ok := newRemoteAt(target); ok && !r.IsDir {
			if r.MD5 == loc.MD5 {
				// Adopt: identical content already on both sides.
				transfers = append(transfers, Action{Type: Record, RelPath: target, FileID: r.FileID,
					MD5: r.MD5, Size: r.Size, Version: r.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
			} else {
				// Both-new conflict: the backup acts at the post-move path,
				// where the ancestor's MoveLocal (hoisted before it) has
				// already put the local content.
				cp := conflicts.Path(target, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: target, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp, ProtectedBy: target},
					Action{Type: Download, RelPath: target, FileID: r.FileID,
						MD5: r.MD5, Size: r.Size, Version: r.Version,
						ProtectedBy: target})
			}
			claimed[target] = true
			continue
		}
		// Move pairing: a deleted baseline file with identical content
		// becomes a remote move instead of delete + re-upload.
		key := pairKey(loc.MD5, loc.Size)
		if srcs := moveSrcs[key]; len(srcs) > 0 {
			src := srcs[0]
			moveSrcs[key] = srcs[1:]
			pairedSrc[src.path] = true
			moves = append(moves, Action{Type: MoveRemote, RelPath: src.path, NewRelPath: target,
				FileID: src.b.FileID, MD5: loc.MD5, Size: loc.Size})
			continue
		}
		transfers = append(transfers, Action{Type: Upload, RelPath: target})
	}
	for _, src := range trashCandidates {
		if !pairedSrc[src.path] {
			deletes = append(deletes, Action{Type: TrashRemote, RelPath: src.path, FileID: src.b.FileID})
		}
	}

	// --- Pass 3: new remote items not yet claimed ----------------------
	for _, rp := range slices.Sorted(maps.Keys(in.Remote)) {
		r := in.Remote[rp]
		if baseIDs[r.FileID] || claimed[rp] {
			continue
		}
		// A new remote item under a locally-renamed dir materializes at its
		// post-reparent path — never a zombie under the dead source dir.
		target := rewriteLD(rp)
		if r.IsDir {
			mkdirs = append(mkdirs, Action{Type: MkdirLocal, RelPath: target, FileID: r.FileID,
				IsDir: true, Version: r.Version})
		} else {
			act := Action{Type: Download, RelPath: target, FileID: r.FileID,
				MD5: r.MD5, Size: r.Size, Version: r.Version}
			// Replaced-in-place: an unchanged local file sits where the new
			// remote item lands; the guard pins the replace to its scanned
			// stat so a mid-cycle edit is never clobbered.
			if l, ok := in.Local[target]; ok && !l.IsDir {
				act.LocalExists, act.LocalSize, act.LocalMtimeNS = true, l.Size, l.MtimeNS
			}
			transfers = append(transfers, act)
		}
	}

	// --- Pass 4: baseline directories ----------------------------------
	// Decide dirs after file actions are known: a dir due for deletion
	// survives (or is resurrected) when anything creates content beneath it.
	creates := map[string]bool{}
	noteCreate := func(p string) { creates[p] = true }
	for _, a := range slices.Concat(mkdirs, moves, transfers) {
		switch a.Type {
		case MkdirLocal, MkdirRemote, Upload, UpdateRemote, Download, Record:
			noteCreate(a.RelPath)
		case MoveRemote, MoveLocal, ConflictBackup:
			noteCreate(a.NewRelPath)
		}
	}
	hasCreateUnder := func(dir string) bool {
		prefix := dir + "/"
		for p := range creates {
			if strings.HasPrefix(p, prefix) {
				return true
			}
		}
		return false
	}
	remoteAliveUnder := func(dir string) bool {
		prefix := dir + "/"
		for p, r := range in.Remote {
			if strings.HasPrefix(p, prefix) && !baseIDs[r.FileID] {
				return true
			}
		}
		return false
	}
	for _, p := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[p]
		if !b.IsDir {
			continue
		}
		// A local-driven move source (or a dir inside one) lives on at its
		// post-rename location; judging it at the baseline path would read
		// the user's rename as a deletion.
		lp := rewriteLD(p)
		if to, ok := localMoveDstOf[p]; ok {
			lp = to
		}
		_, locOK := in.Local[lp]
		ra, remOK := remByID[b.FileID]
		switch {
		case locOK && remOK:
			// Alive on both sides (a dir move was already emitted).
			_ = ra
		case locOK && !remOK:
			if hasCreateUnder(p) || hasCreateUnder(rewrite(p)) {
				// Resurrect the container for surviving content.
				mkdirs = append(mkdirs, Action{Type: MkdirRemote, RelPath: p, IsDir: true})
			} else {
				deletes = append(deletes, Action{Type: QuarantineLocal, RelPath: lp, FileID: b.FileID, IsDir: true})
			}
		case !locOK && remOK:
			// A local dir already sitting at the id's current remote path is
			// the dir alive on both sides — the crashed half of a directory
			// move, renamed on disk with the DB commit lost (R16): the
			// pending MoveLocal replays as a commit, and a trash here would
			// be a remote delete no user caused (invariant 6). A dir absent
			// from BOTH local paths stays a real local deletion and the
			// trash propagates it.
			if l, ok := in.Local[ra.path]; ok && l.IsDir {
				continue
			}
			if !hasCreateUnder(ra.path) && !hasCreateUnder(rewriteLD(ra.path)) && !remoteAliveUnder(ra.path) {
				deletes = append(deletes, Action{Type: TrashRemote, RelPath: ra.path, FileID: b.FileID, IsDir: true})
			}
		default:
			deletes = append(deletes, Action{Type: Forget, RelPath: p, FileID: b.FileID, IsDir: true})
		}
	}

	// --- Ordering -------------------------------------------------------
	depth := func(p string) int { return strings.Count(p, "/") }
	sort.SliceStable(mkdirs, func(i, j int) bool {
		if d1, d2 := depth(mkdirs[i].RelPath), depth(mkdirs[j].RelPath); d1 != d2 {
			return d1 < d2
		}
		return mkdirs[i].RelPath < mkdirs[j].RelPath
	})
	// LOCAL dir moves are hoisted ahead of the mkdirs: a MkdirLocal beneath
	// a moved dir would otherwise scaffold the move's destination and the
	// rename would fail forever (invariant 7: creations order after moves of
	// the paths they touch). Hoisting them is safe — a local dir move
	// depends on nothing the plan creates (the executor makes its
	// destination parents). A local-driven dir move is a MoveRemote — a
	// remote operation touching no local path — and runs *after* the
	// mkdirs like every other MoveRemote, since its destination parent may
	// be a folder a MkdirRemote creates; it runs *before* the file moves,
	// which resolve parents through the reparent's renamed path ids. File
	// moves and conflict backups stay after the mkdirs, because a
	// MoveRemote may target a folder a MkdirRemote has yet to create.
	var moveLocalDirs, moveRemoteDirs, moveFiles []Action
	for _, m := range moves {
		switch {
		case m.IsDir && m.Type == MoveLocal:
			moveLocalDirs = append(moveLocalDirs, m)
		case m.IsDir:
			moveRemoteDirs = append(moveRemoteDirs, m)
		default:
			moveFiles = append(moveFiles, m)
		}
	}
	byDepthThenPath := func(s []Action) func(i, j int) bool {
		return func(i, j int) bool {
			if d1, d2 := depth(s[i].RelPath), depth(s[j].RelPath); d1 != d2 {
				return d1 < d2
			}
			return s[i].RelPath < s[j].RelPath
		}
	}
	sort.SliceStable(moveLocalDirs, byDepthThenPath(moveLocalDirs))
	sort.SliceStable(moveRemoteDirs, byDepthThenPath(moveRemoteDirs))
	// Conflict backups vacate a path before the move that fills it, so they
	// must precede the file moves within the stage (a move may be ProtectedBy a
	// backup that reclaims its destination).
	moveFilesLess := byDepthThenPath(moveFiles)
	sort.SliceStable(moveFiles, func(i, j int) bool {
		if bi, bj := moveFiles[i].Type == ConflictBackup, moveFiles[j].Type == ConflictBackup; bi != bj {
			return bi
		}
		return moveFilesLess(i, j)
	})
	sort.SliceStable(transfers, func(i, j int) bool { return transfers[i].RelPath < transfers[j].RelPath })
	sort.SliceStable(deletes, func(i, j int) bool {
		if d1, d2 := depth(deletes[i].RelPath), depth(deletes[j].RelPath); d1 != d2 {
			return d1 > d2
		}
		return deletes[i].RelPath > deletes[j].RelPath
	})

	// A MkdirRemote under a locally-renamed directory needs the parent id
	// the reparent produces, so it orders after the dir MoveRemotes — the
	// same dependency rule that puts file moves after the mkdirs
	// (invariant 7). Everything else keeps the normal mkdir slot.
	underLocalMoveDst := func(q string) bool {
		for _, m := range localDirMoves {
			if q == m.to || strings.HasPrefix(q, m.to+"/") {
				return true
			}
		}
		return false
	}
	var mkdirsPre, mkdirsUnderMoved []Action
	for _, a := range mkdirs {
		if a.Type == MkdirRemote && underLocalMoveDst(a.RelPath) {
			mkdirsUnderMoved = append(mkdirsUnderMoved, a)
		} else {
			mkdirsPre = append(mkdirsPre, a)
		}
	}

	plan := make([]Action, 0, len(mkdirs)+len(moves)+len(transfers)+len(deletes))
	plan = append(plan, moveLocalDirs...)
	plan = append(plan, mkdirsPre...)
	plan = append(plan, moveRemoteDirs...)
	plan = append(plan, mkdirsUnderMoved...)
	plan = append(plan, moveFiles...)
	plan = append(plan, transfers...)
	plan = append(plan, deletes...)
	sort.Slice(skips, func(i, j int) bool { return skips[i].RelPath < skips[j].RelPath })
	return plan, skips
}

// dirMove is one directory relocation, shared by both detectors: from/to
// are baseline-rooted rel_paths, and rewrite() explains descendants by
// their moved ancestor instead of fanning out per-file actions.
type dirMove struct{ from, to string }

// mkRewrite builds a path rewriter over the given move lists, applying the
// longest strictly-containing moved ancestor.
func mkRewrite(lists ...[]dirMove) func(string) string {
	return func(p string) string {
		best := -1
		out := p
		for _, list := range lists {
			for _, m := range list {
				if strings.HasPrefix(p, m.from+"/") && len(m.from) > best {
					best = len(m.from)
					out = m.to + p[len(m.from):]
				}
			}
		}
		return out
	}
}

// detectLocalDirMoves collapses local-driven directory renames out of the
// file-pairing evidence (spec §4.3, W1.8.2). A baseline dir D pairs to a
// new local location N when: D is absent locally; D's remote is unchanged;
// at least one tracked descendant paired as a move; and every surviving
// paired descendant landed under N preserving its subpath relative to D.
// Genuinely deleted descendants don't block (their own TrashRemote covers
// them); a descendant paired elsewhere does — that's a scatter, not a
// rename. The rule is deliberately conservative: a missed pairing costs a
// churned folder id, a wrong one reparents an entire remote subtree the
// plan never reasoned about. Extra guards in the same spirit: N must be a
// brand-new local dir, and its name must be free on the remote side (a
// reparent onto an occupied name would mint a duplicate).
//
// The pairing here is a prediction of the pass-1/pass-2 pairing, evaluated
// before the collapse changes what those passes see; once a dir collapses,
// its children become ordinary post-move rows and never re-enter pairing.
func detectLocalDirMoves(in Input, remByID map[string]remoteRef, baseIDs map[string]bool, rewriteRD func(string) string) []dirMove {
	// Move-source candidates exactly as pass 1 admits them: baseline files
	// deleted locally whose remote is unchanged (content and position).
	type cand struct{ path string }
	srcByKey := map[string][]cand{}
	candidates := 0
	for _, p := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[p]
		if b.IsDir {
			continue
		}
		if _, ok := in.Local[p]; ok {
			continue
		}
		ra, remOK := remByID[b.FileID]
		if !remOK || ra.item.MD5 != b.DriveMD5 || ra.path != rewriteRD(p) {
			continue
		}
		key := b.MD5 + "|" + itoa(b.Size)
		srcByKey[key] = append(srcByKey[key], cand{path: p})
		candidates++
	}
	if candidates == 0 {
		return nil
	}
	// Destinations a remote-driven move will reclaim by backing up the
	// occupant (pass 1's occupant preserve): those files never pair.
	backedUp := map[string]bool{}
	for p, b := range in.Base {
		if b.IsDir {
			continue
		}
		loc, locOK := in.Local[p]
		if !locOK || loc.IsDir {
			continue
		}
		ra, remOK := remByID[b.FileID]
		if !remOK {
			continue
		}
		expected := rewriteRD(p)
		if ra.path == expected {
			continue
		}
		localChanged := loc.MD5 != b.MD5
		remoteContentChanged := ra.item.MD5 != b.DriveMD5
		if localChanged && remoteContentChanged && loc.MD5 != ra.item.MD5 {
			continue // true conflict: no move, no occupant backup
		}
		if occ, occupied := in.Local[ra.path]; occupied && !occ.IsDir {
			if _, tracked := in.Base[ra.path]; !tracked {
				backedUp[ra.path] = true
			}
		}
	}
	// The pairing pass 2 would run: sorted new local files consume matching
	// sources first-come first-served.
	pairs := map[string]string{}
	for _, q := range slices.Sorted(maps.Keys(in.Local)) {
		if _, inBase := in.Base[q]; inBase {
			continue
		}
		if backedUp[q] {
			continue
		}
		l := in.Local[q]
		if l.IsDir {
			continue
		}
		if r, ok := in.Remote[q]; ok && !baseIDs[r.FileID] && !r.IsDir {
			continue // claimed by a brand-new remote item (adopt/conflict)
		}
		key := l.MD5 + "|" + itoa(l.Size)
		if s := srcByKey[key]; len(s) > 0 {
			pairs[s[0].path] = q
			srcByKey[key] = s[1:]
		}
	}

	var out []dirMove
	for _, d := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[d]
		if !b.IsDir {
			continue
		}
		explained := false
		for _, m := range out {
			if strings.HasPrefix(d, m.from+"/") {
				explained = true // an outer collapse already carries this dir
				break
			}
		}
		if explained {
			continue
		}
		if _, ok := in.Local[d]; ok {
			continue // still present locally: not a rename
		}
		ra, remOK := remByID[b.FileID]
		if !remOK || ra.path != d {
			continue // remote moved or deleted it: not local-driven
		}
		n := ""
		consistent := true
		prefix := d + "/"
		for src, dst := range pairs {
			if !strings.HasPrefix(src, prefix) {
				continue
			}
			rel := src[len(prefix):]
			cut := strings.TrimSuffix(dst, "/"+rel)
			if cut == dst {
				consistent = false // landed somewhere not shaped N/<rel>: scatter
				break
			}
			if n == "" {
				n = cut
			} else if n != cut {
				consistent = false // children split across two destinations
				break
			}
		}
		if !consistent || n == "" {
			continue // no surviving paired child → no evidence → delete + create
		}
		if ln, ok := in.Local[n]; !ok || !ln.IsDir {
			continue
		}
		if _, inBase := in.Base[n]; inBase {
			continue // destination already tracked: a merge, not a rename
		}
		if _, taken := in.Remote[n]; taken {
			continue // remote name occupied: a reparent would mint a duplicate
		}
		out = append(out, dirMove{from: d, to: n})
	}
	return out
}

func itoa(n int64) string {
	// small local helper to avoid strconv import noise in pairKey
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
