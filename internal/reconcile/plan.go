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

// Plan computes the ordered action plan. Output order: mkdirs (top-down),
// moves (dirs first) and conflict backups, transfers, deletes (bottom-up).
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

	// Remote directory moves, used to explain descendants' path changes so
	// one dir rename does not fan out into per-file moves.
	type dirMove struct{ from, to string }
	var dirMoves []dirMove
	for p, b := range in.Base {
		if !b.IsDir {
			continue
		}
		if ra, ok := remByID[b.FileID]; ok && ra.path != p {
			dirMoves = append(dirMoves, dirMove{from: p, to: ra.path})
		}
	}
	// rewrite maps a pre-move path to its post-move location, applying the
	// longest strictly-containing moved ancestor.
	rewrite := func(p string) string {
		best := -1
		out := p
		for _, m := range dirMoves {
			if strings.HasPrefix(p, m.from+"/") && len(m.from) > best {
				best = len(m.from)
				out = m.to + p[len(m.from):]
			}
		}
		return out
	}
	for _, m := range dirMoves {
		// The dir's own move, expressed from wherever its moved ancestors
		// (if any) already put it.
		from := rewrite(m.from)
		if from != m.to {
			moves = append(moves, Action{Type: MoveLocal, RelPath: from, NewRelPath: m.to, IsDir: true})
		}
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
		loc, locOK := in.Local[p]
		if locOK && loc.IsDir {
			// A dir now sits where the baseline had a file. Rare type
			// clash; report and leave both sides alone.
			skips = append(skips, Skip{RelPath: p, Reason: "type changed from file to directory; not synced"})
			continue
		}
		ra, remOK := remByID[b.FileID]
		expected := rewrite(p) // local path after remote dir moves execute
		pathNow := expected
		remoteMoved := false
		if remOK {
			pathNow = ra.path
			remoteMoved = ra.path != expected
			if remoteMoved && locOK {
				moves = append(moves, Action{Type: MoveLocal, RelPath: expected, NewRelPath: ra.path, FileID: b.FileID})
			}
		}
		localChanged := locOK && loc.MD5 != b.MD5
		remoteContentChanged := remOK && ra.item.MD5 != b.DriveMD5

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
			newRem, replaced := newRemoteAt(p)
			switch {
			case localChanged && replaced && newRem.MD5 == loc.MD5:
				// Same new content arrived on both sides independently.
				transfers = append(transfers, Action{Type: Record, RelPath: p, FileID: newRem.FileID,
					MD5: newRem.MD5, Size: newRem.Size, Version: newRem.Version})
				claimed[p] = true
			case localChanged && replaced:
				cp := conflicts.Path(p, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: p, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp},
					Action{Type: Download, RelPath: p, FileID: newRem.FileID,
						MD5: newRem.MD5, Size: newRem.Size, Version: newRem.Version})
				claimed[p] = true
			case localChanged:
				// Resurrect: edit beats delete; re-upload as a new file.
				transfers = append(transfers, Action{Type: Upload, RelPath: p})
			case replaced:
				// Unchanged local will be replaced in place by the new
				// remote item (handled in pass 3); no quarantine.
			default:
				deletes = append(deletes, Action{Type: QuarantineLocal, RelPath: p, FileID: b.FileID})
			}

		default: // both exist
			switch {
			case !localChanged && !remoteContentChanged:
				if b.MtimeNS != loc.MtimeNS || b.Size != loc.Size || b.DriveVersion != ra.item.Version {
					transfers = append(transfers, Action{Type: Record, RelPath: pathNow, FileID: b.FileID,
						MD5: b.MD5, Size: loc.Size, Version: ra.item.Version})
				}
			case localChanged && !remoteContentChanged:
				transfers = append(transfers, Action{Type: UpdateRemote, RelPath: pathNow, FileID: b.FileID})
			case !localChanged && remoteContentChanged:
				transfers = append(transfers, Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
					MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version})
			case loc.MD5 == ra.item.MD5:
				// Both changed to identical content; record, no transfer.
				transfers = append(transfers, Action{Type: Record, RelPath: pathNow, FileID: b.FileID,
					MD5: loc.MD5, Size: loc.Size, Version: ra.item.Version})
			default:
				// True conflict: local becomes the conflicted copy, remote
				// keeps the canonical name; the copy is uploaded too.
				cp := conflicts.Path(pathNow, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: pathNow, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp},
					Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
						MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version})
			}
		}
	}

	// --- Pass 2: new local paths ---------------------------------------
	pairedSrc := map[string]bool{} // baseline path consumed as a move source
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		if _, inBase := in.Base[p]; inBase {
			continue
		}
		loc := in.Local[p]
		target := rewrite(p)
		if loc.IsDir {
			if r, ok := newRemoteAt(p); ok && r.IsDir {
				transfers = append(transfers, Action{Type: Record, RelPath: p, FileID: r.FileID,
					IsDir: true, Version: r.Version})
				claimed[p] = true
			} else if _, taken := in.Remote[target]; !taken {
				mkdirs = append(mkdirs, Action{Type: MkdirRemote, RelPath: target, IsDir: true})
			}
			continue
		}
		if r, ok := newRemoteAt(p); ok && !r.IsDir {
			if r.MD5 == loc.MD5 {
				// Adopt: identical content already on both sides.
				transfers = append(transfers, Action{Type: Record, RelPath: p, FileID: r.FileID,
					MD5: r.MD5, Size: r.Size, Version: r.Version})
			} else {
				cp := conflicts.Path(p, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: p, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp},
					Action{Type: Download, RelPath: p, FileID: r.FileID,
						MD5: r.MD5, Size: r.Size, Version: r.Version})
			}
			claimed[p] = true
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
		if r.IsDir {
			mkdirs = append(mkdirs, Action{Type: MkdirLocal, RelPath: rp, FileID: r.FileID,
				IsDir: true, Version: r.Version})
		} else {
			transfers = append(transfers, Action{Type: Download, RelPath: rp, FileID: r.FileID,
				MD5: r.MD5, Size: r.Size, Version: r.Version})
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
		_, locOK := in.Local[p]
		ra, remOK := remByID[b.FileID]
		switch {
		case locOK && remOK:
			// Alive on both sides (a remote move was already emitted).
			_ = ra
		case locOK && !remOK:
			if hasCreateUnder(p) || hasCreateUnder(rewrite(p)) {
				// Resurrect the container for surviving content.
				mkdirs = append(mkdirs, Action{Type: MkdirRemote, RelPath: p, IsDir: true})
			} else {
				deletes = append(deletes, Action{Type: QuarantineLocal, RelPath: p, FileID: b.FileID, IsDir: true})
			}
		case !locOK && remOK:
			if !hasCreateUnder(ra.path) && !remoteAliveUnder(ra.path) {
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
	sort.SliceStable(moves, func(i, j int) bool {
		// Dir moves first (top-down), then file moves/backups by path.
		di, dj := moves[i].IsDir, moves[j].IsDir
		if di != dj {
			return di
		}
		if d1, d2 := depth(moves[i].RelPath), depth(moves[j].RelPath); d1 != d2 {
			return d1 < d2
		}
		return moves[i].RelPath < moves[j].RelPath
	})
	sort.SliceStable(transfers, func(i, j int) bool { return transfers[i].RelPath < transfers[j].RelPath })
	sort.SliceStable(deletes, func(i, j int) bool {
		if d1, d2 := depth(deletes[i].RelPath), depth(deletes[j].RelPath); d1 != d2 {
			return d1 > d2
		}
		return deletes[i].RelPath > deletes[j].RelPath
	})

	plan := make([]Action, 0, len(mkdirs)+len(moves)+len(transfers)+len(deletes))
	plan = append(plan, mkdirs...)
	plan = append(plan, moves...)
	plan = append(plan, transfers...)
	plan = append(plan, deletes...)
	sort.Slice(skips, func(i, j int) bool { return skips[i].RelPath < skips[j].RelPath })
	return plan, skips
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
