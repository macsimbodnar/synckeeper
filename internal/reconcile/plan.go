package reconcile

import (
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/conflicts"
	"github.com/macsimbodnar/synckeeper/internal/names"
)

type remoteRef struct {
	path string
	item RemoteItem
}

// localWinsName decides who keeps the plain name in a both-new conflict.
// Only the init merge asks (PreferNewer, spec §11); everywhere else the answer
// is "Drive", which needs no clock. An unknown remote stamp — a mirror row
// written before schema v5, or a Drive reply without modifiedTime — falls back
// to that same clock-free rule rather than guessing, which is also why the
// migration needs no backfill. A tie goes to Drive for the same reason.
func (in Input) localWinsName(loc LocalItem, r RemoteItem) bool {
	return in.PreferNewer && r.ModifiedNS > 0 && loc.MtimeNS > r.ModifiedNS
}

// holds indexes Input.UnscannedLocal for the path holds (S013): paths on
// disk the scan did not read, where nothing may be decided or written.
// Matching honours the sync dir's name folding, like every other comparison
// across trees — on a case-folding disk "Photos/x.jpg" lands inside "photos".
type holds struct {
	why   map[string]string // unscanned path → the scan's reason
	byKey map[string]string // folded unscanned path → unscanned path
	keys  []string          // folded unscanned paths, sorted
	fold  func(string) string
}

func newHolds(in Input, fold func(string) string) *holds {
	h := &holds{why: in.UnscannedLocal, byKey: map[string]string{}, fold: fold}
	for _, u := range slices.Sorted(maps.Keys(in.UnscannedLocal)) {
		k := fold(u)
		h.byKey[k] = u
		h.keys = append(h.keys, k)
	}
	slices.Sort(h.keys)
	return h
}

func (h *holds) none() bool { return len(h.byKey) == 0 }

// at returns the unscanned path at or above p, if any.
func (h *holds) at(p string) (string, bool) {
	if h.none() || p == "" {
		return "", false
	}
	for q := h.fold(p); ; {
		if u, ok := h.byKey[q]; ok {
			return u, true
		}
		i := strings.LastIndexByte(q, '/')
		if i < 0 {
			return "", false
		}
		q = q[:i]
	}
}

// atAny is at over several paths, the first match winning.
func (h *holds) atAny(ps ...string) (string, bool) {
	for _, p := range ps {
		if u, ok := h.at(p); ok {
			return u, true
		}
	}
	return "", false
}

// under returns an unscanned path strictly below dir, if any.
func (h *holds) under(dir string) (string, bool) {
	prefix := h.fold(dir) + "/"
	for _, k := range h.keys {
		if strings.HasPrefix(k, prefix) {
			return h.byKey[k], true
		}
	}
	return "", false
}

// reason is the skip reason for p, held because of the unscanned path u.
func (h *holds) reason(p, u string) string {
	if p == u {
		return "the scan does not read it (" + h.why[u] + "); leaving both sides alone"
	}
	return "under " + u + ", which the scan does not read (" + h.why[u] + "); leaving both sides alone"
}

// pinRemote is what a remote mutation expects Drive to still hold (spec §7
// remote write gate): the snapshot entry at remotePath, which is the path of
// the item on Drive, so its last segment is the Drive name.
func pinRemote(remotePath string, r RemoteItem) *RemoteExpect {
	return &RemoteExpect{ParentID: r.ParentID, Name: path.Base(remotePath), IsDir: r.IsDir, MD5: r.MD5}
}

// remoteStepsAside renames the losing Drive file to the conflict name, in
// place, before anything is uploaded. It is what keeps the local winner's
// upload from minting a SECOND item under one name in one Drive folder — the
// shape W17 was about — and it preserves the loser on the Drive side without a
// re-upload, since its bytes are already there. Its local copy arrives as the
// matching Download.
func remoteStepsAside(remotePath string, r RemoteItem, in Input) Action {
	return Action{Type: MoveRemote, RelPath: remotePath,
		NewRelPath: conflicts.Path(remotePath, in.Machine, in.Now),
		FileID:     r.FileID, MD5: r.MD5, Size: r.Size, RemoteWant: pinRemote(remotePath, r)}
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
	// keptOnDrive: bytes Drive holds now or held at the last sync — §4.5 rule
	// O1's "kept". The only local files a pinned download may replace (S031
	// round 2); any other file at a download's target is kept first.
	onDrive := map[string]bool{}
	for _, b := range in.Base {
		if !b.IsDir && b.MD5 != "" && b.MD5 == b.DriveMD5 {
			onDrive[b.MD5] = true
		}
	}
	for _, r := range in.Remote {
		if !r.IsDir && r.MD5 != "" {
			onDrive[r.MD5] = true
		}
	}
	keptOnDrive := func(md5 string) bool { return onDrive[md5] }
	// newRemoteAt: a remote item at path p that is not a baseline item —
	// i.e. brand-new remote content, possibly replacing a baseline path.
	newRemoteAt := func(p string) (RemoteItem, bool) {
		r, ok := in.Remote[p]
		if !ok || baseIDs[r.FileID] {
			return RemoteItem{}, false
		}
		return r, true
	}
	// Fold-keyed remote index (C2, R19): when the local FS folds names, a
	// new local path and a new remote path whose names differ only under
	// the fold are ONE path on disk — they must meet in the §4.2 table
	// instead of blind-uploading a duplicate. Keyed per path segment; nil
	// when the FS doesn't fold. Snapshot has already collapsed fold-equal
	// remote siblings, so the index is collision-free.
	folds := in.CaseFold || in.NormFold
	foldPath := func(p string) string {
		segs := strings.Split(p, "/")
		for i, s := range segs {
			segs[i] = names.FoldKey(s, in.CaseFold, in.NormFold)
		}
		return strings.Join(segs, "/")
	}
	hold := newHolds(in, foldPath)
	// S038: on a folding disk the scan is met under the names the plan knows
	// its folders by (casefold.go); caseUp are folders renamed in case here,
	// caseFixes case-only renames of folders adopted at Drive's spelling.
	var fv foldView
	if folds {
		in, fv = foldCanon(in, remByID, baseIDs, hold, foldPath)
	}
	var foldRemote map[string]string
	if folds {
		foldRemote = make(map[string]string, len(in.Remote))
		for p := range in.Remote {
			foldRemote[foldPath(p)] = p
		}
	}
	// claimed marks new-remote paths already resolved by an earlier pass
	// (adopted, conflicted, or replacing baseline content in place), so
	// pass 3 does not download them a second time.
	claimed := map[string]bool{}
	// newRemoteAtFold: newRemoteAt through the fold — a brand-new remote
	// item whose path fold-matches p but differs from it in byte form.
	newRemoteAtFold := func(p string) (string, RemoteItem, bool) {
		if !folds {
			return "", RemoteItem{}, false
		}
		rp, ok := foldRemote[foldPath(p)]
		if !ok || rp == p {
			return "", RemoteItem{}, false
		}
		r, ok := newRemoteAt(rp)
		if !ok {
			return "", RemoteItem{}, false
		}
		return rp, r, true
	}
	// backedUp marks a local path whose untracked content a move reclaimed:
	// it has been routed to a conflict copy, so pass 2 must not also upload it
	// as a plain new file.
	backedUp := map[string]bool{}
	// resolvedLocal marks a new local path pass 1 has already resolved as a
	// baseline file that sits at the remote's post-move location — pass 2 must
	// not upload it as a plain new file (R25).
	resolvedLocal := map[string]bool{}

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
			if _, held := hold.at(p); held {
				continue // S013: the folder is a symlink (or under one); pass 4 holds it
			}
			if _, held := hold.at(ra.path); held {
				continue // S013: moving it here would write through a symlink; pass 4 holds it
			}
			remoteDirMoves = append(remoteDirMoves, dirMove{from: p, to: ra.path})
		}
	}
	// A remote folder move onto a folder that already sits at the destination
	// here and that no live row claims — untracked, or its own row's Drive
	// item gone (another machine deleted that folder, then renamed this one
	// onto its name) — while the source is still here too (S035): the folder
	// rename cannot run — refused every cycle, never converging — so it is
	// carried out as a merge. Each row under the source meets §4.3 on its own
	// (its file moved into the destination, or found there already: R25);
	// each row of the folder that was there meets §4.2 at its own path
	// (unchanged → binned, edited → resurrected into the arriving folder); the
	// folder's row is re-homed at the destination by a commit, which also
	// drops the gone folder's row. The local folder left at the source is new
	// when it still holds something (created on Drive, its untracked content
	// uploaded into it), else removed here. Reached by two machines renaming
	// one folder differently, the files then coming down into Drive's name,
	// and a new folder made under the old name; by a new folder made here
	// under the name Drive renamed to; by a folder deleted on Drive and
	// another renamed onto its name.
	var mergedDirMoves []dirMove
	mergedSrc := map[string]bool{}
	{
		// A folder row whose Drive item is gone: the one deleted on Drive.
		// Shadowed or unreadable is not gone (the row is held).
		goneDir := func(q string) bool {
			b, ok := in.Base[q]
			if !ok || !b.IsDir || in.ShadowedRemote[b.FileID] || in.UnreadableLocal[b.FileID] {
				return false
			}
			_, live := remByID[b.FileID]
			return !live
		}
		keep := remoteDirMoves[:0]
		for _, m := range remoteDirMoves {
			src, srcOK := in.Local[m.from]
			dst, dstOK := in.Local[m.to]
			_, dstTracked := in.Base[m.to]
			if srcOK && src.IsDir && dstOK && dst.IsDir && (!dstTracked || goneDir(m.to)) {
				mergedDirMoves = append(mergedDirMoves, m)
				mergedSrc[m.from] = true
				continue
			}
			keep = append(keep, m)
		}
		remoteDirMoves = keep
	}
	// Every lookup here honours the disk's name folding: on a folding disk
	// "e" is at "E".
	var foldLocal, foldBase map[string]string
	if folds {
		foldLocal = make(map[string]string, len(in.Local))
		for q := range in.Local {
			foldLocal[foldPath(q)] = q
		}
		foldBase = make(map[string]string, len(in.Base))
		for q := range in.Base {
			foldBase[foldPath(q)] = q
		}
	}
	// S038: a folder Drive moved or renamed onto a name that a different
	// entry here holds in another case or accents, its own folder still here:
	// the rename cannot land (one name on this disk; the gate refused it every
	// cycle). Held as a name conflict (DEC-011, DEC-013): the folder, its rows,
	// the entry here and Drive's new items under the name are left alone and
	// reported until the user renames one. A folder Drive moves out of the way
	// itself this cycle is not a holder (its move runs; S039's order).
	foldHeld := map[string]string{} // held move's source → the entry here holding its name
	if folds && len(remoteDirMoves) > 0 {
		movingAway := map[string]bool{}
		for _, m := range remoteDirMoves {
			movingAway[m.from] = true
		}
		// nowAt: where the disk holds Drive's path q before the folder moves run.
		nowAt := func(q string) string {
			best, out := -1, q
			for _, m := range remoteDirMoves {
				if strings.HasPrefix(q, m.to+"/") && len(m.to) > best {
					best, out = len(m.to), m.from+q[len(m.to):]
				}
			}
			return out
		}
		byDepth := slices.Clone(remoteDirMoves)
		slices.SortFunc(byDepth, func(a, b dirMove) int {
			if c := strings.Count(a.from, "/") - strings.Count(b.from, "/"); c != 0 {
				return c
			}
			return strings.Compare(a.from, b.from)
		})
		keep := remoteDirMoves[:0:0]
		for _, m := range byDepth {
			inherited := false
			for d := range foldHeld {
				if strings.HasPrefix(m.from, d+"/") {
					inherited = true
				}
			}
			if inherited {
				continue // inside a held folder: waits with it
			}
			if src, ok := in.Local[m.from]; ok && src.IsDir {
				t := path.Join(nowAt(path.Dir(m.to)), path.Base(m.to))
				if path.Dir(m.to) == "." {
					t = m.to
				}
				if _, exact := in.Local[t]; !exact {
					if lq, ok := foldLocal[foldPath(t)]; ok && lq != m.from && !movingAway[lq] {
						foldHeld[m.from] = lq
						continue
					}
				}
			}
			keep = append(keep, m)
		}
		remoteDirMoves = keep
	}
	rewriteRD := mkRewrite(remoteDirMoves)
	// A remote-driven move the disk already shows: the source is gone locally
	// and a folder sits at the destination — the user made the same rename, or
	// a crashed MoveLocal did (R16). Its MoveLocal still runs, as a DB commit;
	// until then the baseline's children live locally under the destination.
	// Reading them at their baseline paths found nothing, so pass 1 restored
	// them with a download while pass 2 uploaded the same files as new (S002).
	//
	// Both ends are looked up where they sit on disk now (§4.2), outer moves
	// first: under a moved ancestor the disk has not followed yet, the
	// destination is still under the ancestor's old name — Drive renamed D to
	// N and D/E to N/F, this disk only E to F: the move is done, at D/F (S036;
	// was: looked up at N/F only, the rows under it restored by downloads at
	// N/F while D/F's files went up as new at the same paths — §4.5 refused the
	// plan every cycle). Its `to` is then that disk path.
	var doneDirMoves []dirMove
	{
		byDepth := slices.Clone(remoteDirMoves)
		slices.SortFunc(byDepth, func(a, b dirMove) int {
			if c := strings.Count(a.from, "/") - strings.Count(b.from, "/"); c != 0 {
				return c
			}
			return strings.Compare(a.from, b.from)
		})
		// outerFrom: the baseline path whose item lands at q once the remote
		// folder moves strictly above q run.
		outerFrom := func(q string) string {
			best, out := -1, q
			for _, m := range remoteDirMoves {
				if strings.HasPrefix(q, m.to+"/") && len(m.to) > best {
					best, out = len(m.to), m.from+q[len(m.to):]
				}
			}
			return out
		}
		for _, m := range byDepth {
			soFar := mkRewrite(doneDirMoves)
			if _, here := in.Local[m.from]; here {
				continue
			}
			if _, here := in.Local[rewriteRD(m.from)]; here {
				continue
			}
			if _, here := in.Local[soFar(m.from)]; here {
				continue
			}
			if _, tracked := in.Base[m.to]; tracked {
				continue
			}
			if at := soFar(outerFrom(m.to)); at != m.to {
				// A folder with a row of its own there is not the renamed one.
				_, tracked := in.Base[at]
				if l, ok := in.Local[at]; ok && l.IsDir && !tracked {
					doneDirMoves = append(doneDirMoves, dirMove{from: m.from, to: at})
					continue
				}
			}
			if l, ok := in.Local[m.to]; ok && l.IsDir {
				doneDirMoves = append(doneDirMoves, m)
			}
		}
	}
	rewriteDone := mkRewrite(doneDirMoves)
	// The done move is read from names alone: D gone, a folder at N. That is
	// enough to commit the folder's row, never to let N's files stand for D's.
	// A file at N stands for D's file only on evidence, by §16.7's standard
	// for a local folder rename: its bytes are the baseline's or Drive's, or —
	// for an edit — some non-empty file of the folder still carries its
	// baseline bytes (a paired child). An empty file proves nothing: every new
	// package has an empty __init__.py, every placeholder folder a .gitkeep.
	// Without evidence the two are strangers that share a name (D deleted here,
	// an unrelated N made or renamed into place), and pass 1 never lets one
	// stand for the other.
	doneEvidenced := map[string]bool{}
	for q, b := range in.Base {
		if b.IsDir || b.Size <= 0 {
			continue
		}
		for _, m := range doneDirMoves {
			if !strings.HasPrefix(q, m.from+"/") {
				continue
			}
			if l, ok := in.Local[rewriteDone(q)]; ok && !l.IsDir && l.MD5 == b.MD5 && l.Size == b.Size {
				doneEvidenced[m.from] = true
			}
		}
	}
	doneMoveOf := func(p string) (dirMove, bool) {
		var best dirMove
		found := false
		for _, m := range doneDirMoves {
			if strings.HasPrefix(p, m.from+"/") && (!found || len(m.from) > len(best.from)) {
				best, found = m, true
			}
		}
		return best, found
	}
	// S038: a renamed folder whose new name Drive holds in another case or
	// accents (an item other than itself) is not paired as a rename: the
	// reparent would put that name beside it. foldBlocked holds it instead.
	var driveFoldTaken func(n, id string) (string, bool)
	if folds {
		driveFoldTaken = func(n, id string) (string, bool) {
			rp, ok := foldRemote[foldPath(n)]
			if !ok || rp == n || in.Remote[rp].FileID == id {
				return "", false
			}
			return rp, true
		}
	}
	localDirMoves, foldBlocked := detectLocalDirMoves(in, remByID, baseIDs, rewriteRD, rewriteDone, hold, driveFoldTaken)

	// A folder deleted here that Drive renamed or moved (S020, DEC-006): the
	// rename beats the deletion, as §4.2's "deleted | moved → download" does
	// for a file. The folder is gone from every place the disk could hold it —
	// its own path, under its moved ancestors, under the renames the disk
	// already shows; a file there is not the folder — so there is nothing to
	// rename: it comes back at Drive's path (MkdirLocal, which re-homes its
	// row) and its files are restored there by pass 1's own rows, each
	// "deleted here | moved on Drive". A folder whose new path its moved
	// ancestor alone explains (a merge's included) was not renamed itself, so
	// its deletion stands (§4.3: the descendant's change is the ancestor's) —
	// unless that ancestor comes back, carrying it. Was: a MoveLocal of a
	// folder that is not here (failed), its files trashed one by one, the
	// folder trashed at its new name — the rename discarded and Drive's bin
	// holding the pieces.
	//
	// localNamed: the local entry at q — exact, or under the fold.
	localNamed := func(q string) (string, LocalItem, bool) {
		if l, ok := in.Local[q]; ok {
			return q, l, true
		}
		if !folds {
			return "", LocalItem{}, false
		}
		if lq, ok := foldLocal[foldPath(q)]; ok {
			return lq, in.Local[lq], true
		}
		return "", LocalItem{}, false
	}
	baseNamed := func(q string) (string, bool) {
		if _, ok := in.Base[q]; ok {
			return q, true
		}
		if !folds {
			return "", false
		}
		if bq, ok := foldBase[foldPath(q)]; ok {
			return bq, true
		}
		return "", false
	}
	// restoreHeld: a restore that cannot run, left alone and reported with
	// its rows (S020): why, the local path in its way, the Drive path whose
	// new items wait with it; on a name clash (DEC-013) also the two names,
	// which type every skip of the hold as SkipNameConflict (S030).
	type restoreHold struct{ why, local, drive, clashDrive, clashLocal string }
	holdSkip := func(rel, why string, h restoreHold) Skip {
		s := Skip{RelPath: rel, Reason: why}
		if h.clashDrive != "" {
			s.Kind, s.DrivePath, s.LocalPath = SkipNameConflict, h.clashDrive, h.clashLocal
		}
		return s
	}
	restoreHeld := map[string]restoreHold{}
	// S038's name-conflict holds of a folder move, either direction: the same
	// hold as a restore that cannot run.
	for from, lq := range foldHeld {
		to := remByID[in.Base[from].FileID].path
		s := nameConflict(from, to, lq, "moved in Drive to "+to)
		restoreHeld[from] = restoreHold{why: s.Reason, local: lq, drive: to, clashDrive: to, clashLocal: lq}
	}
	for _, m := range foldBlocked {
		s := nameConflict(m.from, m.sibling, m.to, "renamed here to "+m.to+", not renamed in Drive")
		restoreHeld[m.from] = restoreHold{why: s.Reason, local: m.to, drive: m.sibling, clashDrive: m.sibling, clashLocal: m.to}
	}
	restoredDir := map[string]bool{}
	// explainedAt: where a folder lands by its moved ancestors alone, a
	// merge's included.
	explainedAt := mkRewrite(remoteDirMoves, mergedDirMoves)
	heldLocalPath := map[string]bool{}        // local paths a held restore leaves alone, content included
	heldDrivePath := map[string]restoreHold{} // Drive paths whose new items wait with it
	{
		doneFrom := map[string]bool{}
		for _, m := range doneDirMoves {
			doneFrom[m.from] = true
		}
		whereNow := mkRewrite(localDirMoves, doneDirMoves)
		rewriteLDNow := mkRewrite(localDirMoves)
		byDepth := slices.Clone(remoteDirMoves)
		slices.SortFunc(byDepth, func(a, b dirMove) int {
			if c := strings.Count(a.from, "/") - strings.Count(b.from, "/"); c != 0 {
				return c
			}
			return strings.Compare(a.from, b.from)
		})
		// sameFolder: a fold-equal folder at the target holding some non-empty
		// file of the folder with its synced bytes (§4.3's evidence) — this
		// machine's own rename, its name differing in case: not deleted here.
		sameFolder := func(m dirMove, at string) bool {
			for q, b := range in.Base {
				rest, ok := strings.CutPrefix(q, m.from+"/")
				if !ok || b.IsDir || b.Size <= 0 {
					continue
				}
				if _, l, ok := localNamed(at + "/" + rest); ok && !l.IsDir && l.MD5 == b.MD5 && l.Size == b.Size {
					return true
				}
			}
			return false
		}
		for _, m := range byDepth {
			if doneFrom[m.from] || in.UnreadableLocal[in.Base[m.from].FileID] {
				continue // the disk shows the rename; or not gone, only unread (W18-G)
			}
			here := false
			for _, q := range []string{m.from, rewriteRD(m.from), rewriteDone(m.from), whereNow(m.from)} {
				if _, l, ok := localNamed(q); ok && l.IsDir {
					here = true
				}
			}
			if here {
				continue
			}
			carried := false
			for d := path.Dir(m.from); d != "." && d != "/"; d = path.Dir(d) {
				if restoredDir[d] {
					carried = true
					break
				}
			}
			if !carried && explainedAt(m.from) == m.to {
				continue
			}
			if lq, l, ok := localNamed(rewriteLDNow(m.to)); ok && l.IsDir && lq != rewriteLDNow(m.to) && sameFolder(m, lq) {
				continue // renamed here too, the case aside: as before S020
			}
			restoredDir[m.from] = true
		}
		for _, m := range byDepth {
			if !restoredDir[m.from] {
				continue
			}
			inherited := false
			for d := path.Dir(m.from); d != "." && d != "/"; d = path.Dir(d) {
				if h, ok := restoreHeld[d]; ok {
					restoreHeld[m.from] = h
					inherited = true
					break
				}
			}
			if inherited {
				continue
			}
			t := rewriteLDNow(m.to)
			// What sits here at the target or above it: a file → a type clash
			// (§4.2); a folder whose name differs only by the fold → the same
			// path on this disk under another name, which a folder rename
			// here cannot reconcile yet (§5's directory arm) — both reported
			// and left alone, nothing created beside them.
			for q := t; q != "." && q != "/"; q = path.Dir(q) {
				lq, l, ok := localNamed(q)
				switch {
				case !ok:
					continue
				case !l.IsDir:
					restoreHeld[m.from] = restoreHold{why: "type clash: deleted here and moved in Drive to " + t +
						", where the file " + lq + " sits here; not synced", local: lq, drive: q}
				case lq != q:
					restoreHeld[m.from] = restoreHold{why: "name clash: deleted here and moved in Drive to " + t +
						", which this disk holds as " + lq + " (names differing only in case or accents); not synced",
						local: lq, drive: q, clashDrive: q, clashLocal: lq}
				default:
					continue
				}
				break
			}
			if _, ok := restoreHeld[m.from]; ok {
				continue
			}
			// A row with another live Drive item at the target — Drive holds
			// two folders under one name (§5: one shadowed) — is never
			// replaced; the restore waits with it.
			// Its Drive item gone → replaced (§4.3's arriving folder); moved
			// elsewhere → it leaves the path first (its own move).
			if bq, ok := baseNamed(t); ok && bq != m.from {
				id := in.Base[bq].FileID
				r, onDrive := remByID[id]
				if in.ShadowedRemote[id] || in.UnreadableLocal[id] || onDrive && foldPath(r.path) == foldPath(m.to) {
					restoreHeld[m.from] = restoreHold{why: "deleted here and moved in Drive to " + t +
						", where Drive holds another folder of that name; leaving both alone", drive: t}
					continue
				}
			}
			// A local entry under the target whose name differs only by the fold
			// from Drive's item there: one path on this disk, two names.
			for _, rp := range slices.Sorted(maps.Keys(in.Remote)) {
				if !strings.HasPrefix(rp, m.to+"/") {
					continue
				}
				if lq, _, ok := localNamed(rewriteLDNow(rp)); ok && lq != rewriteLDNow(rp) {
					restoreHeld[m.from] = restoreHold{why: "name clash: deleted here and moved in Drive to " + t +
						", where " + lq + " sits here under another case of " + rp + "; not synced",
						local: lq, drive: t, clashDrive: rewriteLDNow(rp), clashLocal: lq}
					break
				}
			}
		}
		if len(restoredDir) > 0 {
			keep := remoteDirMoves[:0:0]
			for _, m := range remoteDirMoves {
				if !restoredDir[m.from] {
					keep = append(keep, m)
				}
			}
			remoteDirMoves = keep
		}
		for d, h := range restoreHeld {
			delete(restoredDir, d)
			if h.local != "" {
				heldLocalPath[h.local] = true
			}
			heldDrivePath[h.drive] = h
		}
	}
	// restoreHoldOf: the held restore that holds base path p — a folder that
	// cannot come back, or a row in it (S020).
	restoreHoldOf := func(p string) (restoreHold, bool) {
		for d, h := range restoreHeld {
			if p == d || strings.HasPrefix(p, d+"/") {
				return h, true
			}
		}
		return restoreHold{}, false
	}
	heldDriveUnder := func(q string) (restoreHold, bool) {
		for d, h := range heldDrivePath {
			if strings.HasPrefix(q, d+"/") {
				return h, true
			}
		}
		return restoreHold{}, false
	}
	underAny := func(q string, set map[string]bool) bool {
		for c := range set {
			if q == c || strings.HasPrefix(q, c+"/") {
				return true
			}
		}
		return false
	}
	// A folder rename the disk already shows read without evidence: the two
	// are strangers (D deleted here, an unrelated N made or renamed into
	// place, S002), so D's files missing here were deleted here while Drive
	// moved them — restored into N, the same rule as above (S020). With
	// evidence the rename is this machine's own and a file missing from N
	// was deleted after it: binned (§4.2).
	strangerDone := func(p string) bool {
		m, ok := doneMoveOf(p)
		return ok && !doneEvidenced[m.from]
	}

	// rewrite maps a baseline path to its post-plan LOCAL location, applying
	// the longest strictly-containing moved ancestor from either detector.
	rewrite := mkRewrite(remoteDirMoves, localDirMoves)
	// rewriteLD applies only the local-driven moves. It serves the two sides
	// a local-driven rename leaves behind (the side that hasn't moved yet):
	// where the user's rename already put a baseline row's local file, and
	// where a current remote path will sit after the planned reparent.
	rewriteLD := mkRewrite(localDirMoves)
	// rewriteLocal maps a baseline path to where its item sits on disk NOW:
	// the user's renames (local-driven) and the remote renames the disk
	// already shows. Every lookup of a baseline row in in.Local goes through
	// it; rewriteLD stays the remote side's map, since the done moves are
	// already on Drive too.
	rewriteLocal := mkRewrite(localDirMoves, doneDirMoves)
	localMoveDstOf := map[string]string{}
	for _, m := range slices.Concat(localDirMoves, doneDirMoves) {
		localMoveDstOf[m.from] = m.to
	}
	// invMoves maps a path back through moves — exact destination included —
	// to the baseline path that explains it.
	invMoves := func(q string, lists ...[]dirMove) string {
		best := -1
		out := q
		for _, list := range lists {
			for _, m := range list {
				switch {
				case q == m.to && len(m.to) > best:
					best = len(m.to)
					out = m.from
				case strings.HasPrefix(q, m.to+"/") && len(m.to) > best:
					best = len(m.to)
					out = m.from + q[len(m.to):]
				}
			}
		}
		return out
	}
	// invLD: the Drive path whose item lands at q once the planned reparents
	// run — the inverse of rewriteLD. A lookup on Drive for a path the plan
	// will write goes through it (S002).
	invLD := func(q string) string { return invMoves(q, localDirMoves) }
	// invLocal: the baseline path that explains the local path q.
	invLocal := func(q string) string { return invMoves(q, localDirMoves, doneDirMoves) }
	// localAtPost: where the local item sits now that lands at q once the
	// planned folder moves run — through the remote-driven moves not yet on
	// disk, then forward to the disk's current layout.
	localAtPost := func(q string) string { return rewriteLocal(invMoves(q, remoteDirMoves)) }

	for _, m := range remoteDirMoves {
		// The dir's own move, expressed from wherever its moved ancestors
		// (if any) already put it.
		from := rewrite(m.from)
		if _, l, here := localNamed(from); (!here || !l.IsDir) && explainedAt(m.from) == m.to {
			// Gone here inside a merged folder (S035): not moved by itself,
			// its deletion stands (§4.2, S020); there is nothing to move.
			continue
		}
		if from != m.to {
			moves = append(moves, Action{Type: MoveLocal, RelPath: from, NewRelPath: m.to, IsDir: true})
		}
	}
	for _, m := range mergedDirMoves {
		// The merge's commit: the folder's row re-homed where Drive holds it
		// (a directory Record writes the DB row only).
		b := in.Base[m.from]
		transfers = append(transfers, Action{Type: Record, RelPath: m.to, FileID: b.FileID,
			IsDir: true, Version: remByID[b.FileID].item.Version})
	}
	for _, m := range localDirMoves {
		// One reparent carries the whole subtree; the per-file moves it
		// explains are never emitted (pass 1 sees the children at their
		// post-rename paths and finds nothing to do). Its row is where an
		// outer one of these moves put it when it runs (S038: a folder renamed
		// inside one renamed in case).
		id := in.Base[m.from].FileID
		ra := remByID[id] // detectLocalDirMoves pairs only dirs the remote holds at m.from
		moves = append(moves, Action{Type: MoveRemote, RelPath: rewriteLD(m.from), NewRelPath: m.to,
			FileID: id, IsDir: true, RemoteWant: pinRemote(ra.path, ra.item)})
	}
	// S038: a folder adopted at Drive's spelling is renamed to it here.
	moves = append(moves, fv.fixes...)

	// Local move pairing candidates: baseline files deleted locally whose
	// remote is unchanged (safe to reinterpret as the source of a move).
	type moveSrc struct {
		path   string
		b      BaseItem
		remote remoteRef // what the remote mutation (move or trash) pins
	}
	pairKey := func(md5 string, size int64) string { return md5 + "|" + itoa(size) }
	moveSrcs := map[string][]moveSrc{}
	var trashCandidates []moveSrc

	// S013: baseline rows held because their path is on disk but unread — a
	// symlink or non-regular file at or above where the row lives, or where
	// Drive now puts it. Decided once, before the passes: pass 4 must know
	// which Drive items and local paths belong to held rows before it removes
	// a folder that contains them.
	//
	// S014 adds two unreadable cases the baseline path cannot show. A row
	// whose local location, after a detected local folder rename, lies under
	// an unreadable directory (W18-G's UnreadableLocal matches baseline paths
	// only). And a file row gone locally whose size AND mtime equal those of
	// an unreadable file with no baseline row of its own: `mv` keeps both, so
	// that is the file renamed and then unreadable — identity by stat, never
	// by name. A coincidental match (same size, same mtime to the
	// nanosecond) holds that one row until the unreadable file is readable,
	// gone, or changed.
	held := map[string]string{} // base path → skip reason
	heldIDs := map[string]bool{}
	var heldLocal []string // where held rows' files sit locally, sorted
	for id := range in.UnreadableLocal {
		heldIDs[id] = true // W18-G rows: their Drive items are protected too
	}
	for q, b := range in.Base {
		if _, ok := restoreHoldOf(q); ok {
			heldIDs[b.FileID] = true // S020: a restore that waits; no folder trash takes them
		}
	}
	if !hold.none() || len(in.UnreadableDirs) > 0 {
		localAt := func(q string) string {
			if to, ok := localMoveDstOf[q]; ok {
				return to
			}
			return rewriteLocal(q)
		}
		lockedDir := map[string]string{} // folded unreadable dir → dir
		for d := range in.UnreadableDirs {
			lockedDir[foldPath(d)] = d
		}
		underLocked := func(q string) (string, bool) {
			for k := path.Dir(foldPath(q)); k != "." && k != ""; k = path.Dir(k) {
				if d, ok := lockedDir[k]; ok {
					return d, true
				}
			}
			return "", false
		}
		type stat struct{ size, mtime int64 }
		renamedTo := map[stat]string{} // unreadable untracked file, by stat
		for _, u := range slices.Sorted(maps.Keys(in.UnreadableFiles)) {
			if _, tracked := in.Base[invLocal(u)]; tracked {
				continue // has its own row: held at its path, renames nothing
			}
			st := in.UnreadableFiles[u]
			if _, dup := renamedTo[stat{st.Size, st.MtimeNS}]; !dup {
				renamedTo[stat{st.Size, st.MtimeNS}] = u
			}
		}
		for p, b := range in.Base {
			remNow := ""
			if ra, ok := remByID[b.FileID]; ok {
				remNow = rewriteLD(ra.path)
			}
			why := ""
			if u, ok := hold.atAny(p, localAt(p), rewrite(p), remNow); ok {
				why = hold.reason(p, u)
			} else if d, ok := underLocked(localAt(p)); ok && localAt(p) != p {
				why = "inside " + d + ", a directory that could not be read (after a local folder rename); leaving both sides alone"
			} else if _, here := in.Local[localAt(p)]; !here && !b.IsDir && b.Size >= 0 {
				if u, ok := renamedTo[stat{b.Size, b.MtimeNS}]; ok {
					why = "gone here, but " + u + " has its exact size and mtime and could not be read — likely this file renamed; leaving both sides alone"
				}
			}
			if why == "" {
				continue
			}
			held[p] = why
			heldIDs[b.FileID] = true
			if _, ok := in.Local[localAt(p)]; ok {
				heldLocal = append(heldLocal, localAt(p))
			}
		}
		slices.Sort(heldLocal)
	}

	// S030 (DEC-011): on a disk that folds case or normalization, a place can
	// be held here under another byte form of its name — README.txt is
	// Readme.txt. A download or move to README.txt meets that file: the gate
	// refuses it every cycle (§7, "a file appeared after the scan"). When the
	// file there is not the incoming item itself, nothing is planned onto the
	// place: the row or Drive item waits, the file here stays, and the two are
	// reported as one name conflict naming both. Likewise no upload or Drive
	// rename puts a name on Drive that differs only by the fold from one Drive
	// keeps there. Released by itself once the user renames one of the two.
	//
	// foldHolder: the local entry holding q's place under another byte form.
	foldHolder := func(q string) (string, bool) {
		if !folds {
			return "", false
		}
		if _, exact := in.Local[q]; exact {
			return "", false
		}
		lq, ok := foldLocal[foldPath(q)]
		return lq, ok && lq != q
	}
	// foldHolderUp: foldHolder for q, or for the nearest folder above it that
	// the disk holds under another byte form (S038): what lands at q lands in
	// that entry. at is the level found.
	foldHolderUp := func(q string) (lq, at string, ok bool) {
		if !folds {
			return "", "", false
		}
		for i := 0; i <= len(q); i++ {
			if i < len(q) && q[i] != '/' {
				continue
			}
			a := q[:i]
			if _, exact := in.Local[a]; exact {
				continue
			}
			if h, ok := foldHolder(a); ok {
				return h, a, true
			}
			return "", "", false
		}
		return "", "", false
	}
	// prefixLanding: the prefix of q (a path the plan writes) that lands at
	// the local path at once the folder moves run — the Drive-side name of a
	// folder foldHolderUp found held here (S038).
	prefixLanding := func(q, at string, post func(string) string) string {
		for i := 0; i <= len(q); i++ {
			if (i == len(q) || q[i] == '/') && post(q[:i]) == at {
				return q[:i]
			}
		}
		return q
	}
	// ownCase: a row whose file is here under another case of its name — its
	// path gone, a fold-equal file no row claims in its place. That is the
	// row's own file, renamed in case here (a folding disk holds one of the
	// two names): never a name conflict. ownedBy is the inverse.
	ownCase, ownedBy := map[string]string{}, map[string]string{}
	if folds {
		// Content beats the name: a file holding the bytes of another row
		// whose file is gone here is that row's file renamed (pass 2 pairs
		// it), not this row's.
		goneBy := map[string]int{} // content key → rows whose file is gone here
		for q, b := range in.Base {
			if _, here := in.Local[rewriteLocal(q)]; !here && !b.IsDir {
				goneBy[pairKey(b.MD5, b.Size)]++
			}
		}
		for _, p := range slices.Sorted(maps.Keys(in.Base)) {
			b := in.Base[p]
			if b.IsDir || in.ShadowedRemote[b.FileID] || in.UnreadableLocal[b.FileID] {
				continue
			}
			if _, h := held[p]; h {
				continue
			}
			lq, ok := foldHolder(rewriteLocal(p))
			if !ok || in.Local[lq].IsDir {
				continue
			}
			if _, tracked := in.Base[invLocal(lq)]; tracked {
				continue
			}
			if _, taken := ownedBy[lq]; taken {
				continue
			}
			if l := in.Local[lq]; l.MD5 != b.MD5 && goneBy[pairKey(l.MD5, l.Size)] > 0 {
				continue
			}
			ownCase[p], ownedBy[lq] = lq, p
		}
	}
	const (
		holderStays  = iota // there this cycle and after: a name conflict
		holderOwn           // the incoming item's own file, its name in another case: no conflict
		holderLeaves        // its own row moves it away in the moves stage
		holderBinned        // its Drive item gone and it unchanged: binned this cycle (deletes stage)
	)
	// holderFate: what becomes of lq, the fold holder of a place item id is
	// due at. Only a row this plan certainly takes away is not in the way.
	holderFate := func(lq, id string) int {
		oq := invLocal(lq)
		if q, ok := ownedBy[lq]; ok {
			oq = q // a row's own file under another case: judged as that row's
		}
		ob, tracked := in.Base[oq]
		l := in.Local[lq]
		if !tracked || ob.IsDir || l.IsDir {
			return holderStays
		}
		if ob.FileID == id {
			return holderOwn
		}
		if _, h := held[oq]; h || in.ShadowedRemote[ob.FileID] || in.UnreadableLocal[ob.FileID] {
			return holderStays
		}
		if _, h := restoreHoldOf(oq); h {
			return holderStays
		}
		ora, live := remByID[ob.FileID]
		switch {
		case !live && l.MD5 == ob.MD5:
			return holderBinned
		case live && rewriteLD(ora.path) != rewrite(oq):
			return holderLeaves
		}
		return holderStays
	}
	// Rows due at a place a fold holder keeps: held like S013's (nothing
	// moved, downloaded, binned or forgotten; their Drive items protected).
	clashed := map[string]Skip{} // base path → its name-conflict skip
	if folds {
		for _, p := range slices.Sorted(maps.Keys(in.Base)) {
			b := in.Base[p]
			if b.IsDir || in.ShadowedRemote[b.FileID] || in.UnreadableLocal[b.FileID] {
				continue
			}
			if _, h := held[p]; h {
				continue
			}
			if _, h := restoreHoldOf(p); h {
				continue
			}
			ra, ok := remByID[b.FileID]
			if !ok {
				continue
			}
			pathNow := rewriteLD(ra.path)
			_, here := in.Local[rewriteLocal(p)]
			if pathNow == rewrite(p) && (here || ra.item.MD5 == b.DriveMD5 && !strangerDone(p)) {
				continue // nothing lands: in place, or deleted here and binned on Drive
			}
			lq, at, ok := foldHolderUp(localAtPost(pathNow))
			if !ok {
				continue
			}
			fate := holderFate(lq, b.FileID)
			if at != localAtPost(pathNow) && fate != holderStays {
				fate = holderBinned // a folder above it: waits for the folder (S038)
			}
			why := ""
			switch fate {
			case holderOwn, holderLeaves:
				continue
			case holderBinned:
				why = "due at " + pathNow + " from Drive; waits a cycle: " + lq +
					", its name here in another case or accents, goes to the bin first"
			default:
				what := "moved in Drive to " + pathNow
				if pathNow == rewrite(p) {
					what = "gone from its name here, changed in Drive"
				}
				s := nameConflict(p, prefixLanding(pathNow, at, localAtPost), rewrite(lq), what)
				clashed[p] = s
				why = s.Reason
			}
			held[p] = why
			heldIDs[b.FileID] = true
			if here {
				heldLocal = append(heldLocal, rewriteLocal(p))
			}
		}
		slices.Sort(heldLocal)
	}
	// driveSibling: the Drive item, other than id, that keeps target's place
	// on Drive under another byte form of its name through this plan — an
	// upload or Drive rename to target would put beside it a second name this
	// disk cannot hold apart. driveLeaves: ids this plan takes off their Drive
	// path (pass 1's bin-or-move candidates), not in the way.
	driveLeaves := map[string]bool{}
	// S038: a folder above target counts too — Drive holding it under
	// another spelling, the item would go into that folder or fail for want
	// of its parent.
	// at is the level of target where it is: target, or the folder above it.
	driveSibling := func(target, id string) (dp, at string, ok bool) {
		if !folds {
			return "", "", false
		}
		for i := 0; i <= len(target); i++ {
			if i < len(target) && target[i] != '/' {
				continue
			}
			rt := invLD(target[:i])
			rp, ok := foldRemote[foldPath(rt)]
			if !ok {
				return "", "", false // not on Drive: nothing below it is either
			}
			if rp == rt {
				continue
			}
			if r := in.Remote[rp]; i == len(target) && r.FileID == id || driveLeaves[r.FileID] {
				return "", "", false
			}
			return rewriteLD(rp), target[:i], true
		}
		return "", "", false
	}
	// heldOnDriveUnder: a held row's Drive item sits under dir on Drive —
	// another machine may have moved it there, and the row, being held, never
	// followed. Trashing dir would take it into Drive's bin.
	heldOnDriveUnder := func(dir string) bool {
		if len(heldIDs) == 0 {
			return false
		}
		prefix := dir + "/"
		for p, r := range in.Remote {
			if heldIDs[r.FileID] && strings.HasPrefix(p, prefix) {
				return true
			}
		}
		return false
	}
	// heldLocalUnder: the local mirror — a held row's file is on disk under
	// dir (its Drive item moved into a held path); binning dir would take it.
	heldLocalUnder := func(dir string) bool {
		prefix := dir + "/"
		i, _ := slices.BinarySearch(heldLocal, prefix)
		return i < len(heldLocal) && strings.HasPrefix(heldLocal[i], prefix)
	}

	// takenOver: a baseline file X whose Drive item is gone, at the path where
	// another row's Drive item now sits and where that row's local file is
	// too — the same file moved over X on both sides (S002, cause D). X's own
	// row decides nothing: the local file there is the other row's, met by
	// R25's branch below, and that commit drops X's row (one row per path).
	// Without this, X's row resurrected the file as an upload while the other
	// row downloaded onto it: §4.5 refused the plan every cycle.
	goneFromDrive := func(x string) bool {
		b, ok := in.Base[x]
		if !ok || b.IsDir || in.ShadowedRemote[b.FileID] || in.UnreadableLocal[b.FileID] {
			return false
		}
		if _, isHeld := held[x]; isHeld {
			return false
		}
		_, onDrive := remByID[b.FileID]
		return !onDrive
	}
	takenOver := map[string]bool{}
	for _, q := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[q]
		if b.IsDir || in.ShadowedRemote[b.FileID] || in.UnreadableLocal[b.FileID] {
			continue
		}
		if _, isHeld := held[q]; isHeld {
			continue
		}
		ra, ok := remByID[b.FileID]
		if !ok {
			continue
		}
		if _, here := in.Local[rewriteLocal(q)]; here {
			continue
		}
		pathNow := rewriteLD(ra.path)
		if pathNow == rewrite(q) {
			continue
		}
		lp := localAtPost(pathNow)
		x := invLocal(lp)
		if ln, ok := in.Local[lp]; !ok || ln.IsDir || x == q || !goneFromDrive(x) {
			continue
		}
		if _, tracked := in.Base[pathNow]; tracked && pathNow != x {
			continue
		}
		takenOver[x] = true
	}

	// vacateFirst: occupants of a local move's destination whose own row's
	// action must change because the move takes their path (S002, W2). Applied
	// after pass 1, whichever of the two rows it reached first.
	const (
		vacateBin    = 1 // the row's QuarantineLocal runs in the moves stage, before the move
		vacateCopied = 2 // the row's resurrecting Upload is the conflict copy's; drop its own
	)
	type vacate struct {
		kind int
		at   string // where the occupant sits when the moves stage reaches it
	}
	vacateFirst := map[string]vacate{}
	// forgets: rows of an unrelated pair under a done folder move; they run
	// first in the plan, before the folder's commit (S002).
	var forgets []Action
	// released: a local file a done move would explain that pass 1 found to
	// be new (the row's file sits at Drive's path); pass 2 treats it as new.
	// deferred: local files left alone this cycle (an unrelated pair).
	released, deferred := map[string]bool{}, map[string]bool{}
	// occChecks: local moves whose destination is occupied, by the move's
	// index in moves; resolved after pass 1 (S031).
	type occCheck struct {
		mv  int    // -1: a download lands there, not a move
		at  string // where the occupant sits now
		dst string // the move's destination, where the moves stage meets it
	}
	var occChecks []occCheck
	// rowAt: the baseline file row whose DB row sits at a path once the
	// folder moves have committed — what a file move's commit meets there.
	rowAt := map[string]string{}
	for q, b := range in.Base {
		if !b.IsDir {
			rowAt[rewrite(q)] = q
		}
	}
	// heldRow: a row pass 1 only reports — never moved, backed up, forgotten.
	heldRow := func(q string) bool {
		b, ok := in.Base[q]
		if !ok || b.IsDir {
			return false
		}
		_, h := held[q]
		return h || in.ShadowedRemote[b.FileID] || in.UnreadableLocal[b.FileID]
	}

	// shadowSrcs: shadowed rows offered to pass 2's pairing (S030); reported
	// as before when nothing pairs with them.
	var shadowSrcs []Skip
	// keptHere: local entries a name conflict leaves in place (S038) — a
	// folder holding one is not removed (pass 4).
	var keptHere []string

	// --- Pass 1: baseline files ---------------------------------------
	for _, p := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[p]
		if b.IsDir {
			continue // dirs resolved in pass 4
		}
		if in.ShadowedRemote[b.FileID] {
			// C2b (R19): the id exists on Drive but a duplicate or
			// fold-colliding sibling shadows it out of the snapshot.
			// "Remote absent" would be a lie — hold the row harmless
			// until the Drive-side collision is resolved.
			shadowSkip := Skip{RelPath: p, FileID: b.FileID,
				Reason: "remote copy is shadowed by a duplicate or fold-colliding name in Drive; leaving both sides alone"}
			// S030: its file gone from its name here, its Drive item as
			// synced — a local rename pairs with it like any other (pass 2),
			// and the Drive item follows to the new name, which ends the
			// collision. Unpaired, the row stays held: never binned.
			if sh, ok := in.ShadowedAt[b.FileID]; ok && !sh.Item.IsDir && sh.Item.MD5 == b.DriveMD5 {
				if _, here := in.Local[rewriteLocal(p)]; !here {
					if _, own := ownCase[p]; !own {
						src := moveSrc{path: p, b: b, remote: remoteRef{path: sh.Path, item: sh.Item}}
						moveSrcs[pairKey(b.MD5, b.Size)] = append(moveSrcs[pairKey(b.MD5, b.Size)], src)
						shadowSrcs = append(shadowSrcs, shadowSkip)
						continue
					}
				}
			}
			skips = append(skips, shadowSkip)
			continue
		}
		if in.UnreadableLocal[b.FileID] {
			// W18-G: the local copy is under a directory the scan could not
			// read. "Local absent" would be a lie, and acting on it would
			// trash the Drive copy of a file that is sitting right there.
			skips = append(skips, Skip{RelPath: p,
				Reason: "local copy is inside a directory that could not be read; leaving both sides alone"})
			continue
		}
		if s, ok := clashed[p]; ok {
			skips = append(skips, s) // S030: due where another file here holds its name
			continue
		}
		// Under a local-driven dir rename this row's file, if it survives,
		// already sits at its post-rename location — every row is decided
		// at the path the item will occupy (§4.2), on both sides.
		if why, ok := held[p]; ok {
			// S013: "local absent" would be a lie, and any write here would
			// replace the user's link or land outside the sync dir; the row
			// waits, untouched, for the path to be a plain file again.
			skips = append(skips, Skip{RelPath: p, Reason: why})
			continue
		}
		if takenOver[p] {
			continue // its local file is another row's, moved over it (cause D)
		}
		if h, ok := restoreHoldOf(p); ok {
			skips = append(skips, holdSkip(p, "in a folder that waits: "+h.why, h))
			continue
		}
		localPath := rewriteLocal(p)
		ra, remOK := remByID[b.FileID]
		loc, locOK := in.Local[localPath]
		if locOK && loc.IsDir {
			// A dir now sits where the baseline had a file. Rare type
			// clash; report and leave both sides alone.
			skips = append(skips, Skip{RelPath: p, Reason: "type changed from file to directory; not synced"})
			continue
		}
		expected := rewrite(p) // local path after all planned dir moves execute
		pathNow := expected
		remoteMoved := false
		if remOK {
			// The remote side of a local-driven move hasn't moved yet:
			// judge and emit at its post-reparent path.
			pathNow = rewriteLD(ra.path)
			remoteMoved = pathNow != expected
		}
		// S030: the row's file is here under another case of its name.
		rehome := false
		if lq, own := ownCase[p]; own && path.Base(lq) == path.Base(p) {
			// Only a folder above it is spelled otherwise (S038 round 3: rows of
			// one folder in two spellings, after a crash cut the folder's case
			// rename): the row's file, in place — the folder's own rule renames
			// the folder. Not a file renamed in case here.
			localPath, loc, locOK = lq, in.Local[lq], true
			resolvedLocal[lq] = true
			if remoteMoved && foldPath(pathNow) == foldPath(expected) && path.Base(pathNow) == path.Base(p) {
				// Drive's path is the same place in the folder's other spelling
				// (the row's folder spelling is stale — a crash recovery
				// committed it before the folder's case rename), and Drive's name
				// for the file is the row's: nothing moves; the row is re-homed
				// by id (round 4; was: a move onto the file itself, read as an
				// occupant and backed up, every cycle). Drive renamed the file
				// too (in case) → the ordinary remote move below carries it here,
				// a case-only move_local pinned to the file (round 5; a re-home
				// at Drive's name left the disk at the old one, read next cycle
				// as a case rename here and sent back to Drive).
				expected, remoteMoved, rehome = pathNow, false, true
			}
		} else if own && remOK {
			switch {
			case remoteMoved && pathNow != lq && foldPath(pathNow) == foldPath(lq):
				// Renamed in case on Drive and here, differently: a folding
				// disk holds one of the two; Drive's keeps it, as Drive's
				// name does in a conflict (§4.2) and in §5's fold pair. The
				// file is met where it sits; its move commits by path, so
				// the row is re-homed by id after it.
				localPath, expected, loc, locOK = lq, lq, in.Local[lq], true
				resolvedLocal[lq] = true
				rehome = true
			case !remoteMoved:
				// Renamed in case here, Drive's name unchanged: the rename
				// goes up as the item's own rename (as pairing would make it),
				// then §4.2 meets the file at its new name — Drive changed
				// it: downloaded ("moved | changed"); edited here: uploaded
				// over the id; both, differently: conflict copy. Was, Drive
				// changed: a name conflict (round 1), a refused download
				// every cycle (HEAD); edited here only: a new upload, the old
				// item binned.
				l := in.Local[lq]
				moves = append(moves, Action{Type: MoveRemote, RelPath: p, NewRelPath: lq,
					FileID: b.FileID, MD5: l.MD5, Size: l.Size, RemoteWant: pinRemote(ra.path, ra.item)})
				remoteChanged := ra.item.MD5 != b.DriveMD5
				switch {
				case !remoteChanged && l.MD5 == b.MD5:
				case !remoteChanged:
					// The Drive item carries the new name once the move ran.
					want := pinRemote(lq, ra.item)
					transfers = append(transfers, Action{Type: UpdateRemote, RelPath: lq, FileID: b.FileID,
						RemoteWant: want, ProtectedBy: p})
				case l.MD5 == ra.item.MD5:
					transfers = append(transfers, Action{Type: Record, RelPath: lq, FileID: b.FileID,
						MD5: l.MD5, Size: l.Size, Version: ra.item.Version, ProtectedBy: p,
						LocalExists: true, LocalSize: l.Size, LocalMtimeNS: l.MtimeNS})
				case l.MD5 == b.MD5 && keptOnDrive(l.MD5):
					transfers = append(transfers, Action{Type: Download, RelPath: lq, FileID: b.FileID,
						MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version, ProtectedBy: p,
						LocalExists: true, LocalSize: l.Size, LocalMtimeNS: l.MtimeNS})
				default:
					cp := conflicts.Path(lq, in.Machine, in.Now)
					moves = append(moves, Action{Type: ConflictBackup, RelPath: lq, NewRelPath: cp})
					transfers = append(transfers,
						Action{Type: Upload, RelPath: cp, ProtectedBy: lq},
						Action{Type: Download, RelPath: lq, FileID: b.FileID,
							MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version, ProtectedBy: lq})
				}
				resolvedLocal[lq] = true
				continue
			}
		}
		// The local file may already sit at the remote's new path: the user
		// made the same rename the remote reports, or a crashed MoveLocal left
		// the disk and Drive ahead of this baseline row. The file is alive on
		// both sides at pathNow, not locally deleted — resolve it there.
		// Without this, pass 1 plans a Download to "restore" the remote file at
		// pathNow while pass 2 uploads the "new" local file at the same path,
		// and §4.5 refuses the whole plan every cycle: a permanent wedge (found
		// by the W4 fuzzer, R25).
		// The local file is the one that lands at pathNow once the planned
		// folder moves run: under a folder Drive renamed and the disk has not
		// yet, it still sits at the old folder's path (S002). It must be one no
		// baseline row explains — that one is its own row's file.
		if locOK && remOK && remoteMoved && loc.MD5 != b.MD5 && loc.MD5 != ra.item.MD5 {
			// The file at the row's path holds neither side's bytes, and Drive
			// renamed the file: if the file at Drive's path carries the row's
			// bytes and no row explains it, that is the row's file — moved there
			// too, by the user or by a move whose commit was lost (S031) —
			// met by R25's branch below; the file at the old path is new. Under
			// a remote folder rename the disk already shows, N/rel is D/rel's
			// file by name only (S002). Elsewhere the old path keeps its row
			// until the R25 commit re-homes it; pass 2 meets the new file there
			// all the same. Was (S031): taken for the row's file — an edit, or a
			// true conflict — so a file written at the old name after an
			// interrupted move went up into the row's Drive item, the row's own
			// bytes to a conflict copy; with Drive edited, §4.5 refused every
			// cycle.
			alt := localAtPost(pathNow)
			_, altIsBase := in.Base[invLocal(alt)]
			_, pathNowTracked := in.Base[pathNow]
			if l2, ok := in.Local[alt]; ok && !l2.IsDir && !altIsBase && !pathNowTracked &&
				(l2.MD5 == b.MD5 || l2.MD5 == ra.item.MD5) {
				released[localPath] = true
				locOK = false
			}
		}
		if m, viaDone := doneMoveOf(p); viaDone && locOK && remOK && loc.MD5 != b.MD5 && loc.MD5 != ra.item.MD5 &&
			!doneEvidenced[m.from] {
			// No evidence the two are one file: they are strangers. The row
			// stops claiming Drive's item, before the folder's commit in
			// the same plan, and the files stay untouched this cycle. From
			// then on Drive's item is Drive-only and the local file new:
			// §4.2's both-new rows keep both (a conflict copy), and no
			// interruption can leave a row that reads as a local deletion —
			// none claims Drive's item.
			forgets = append(forgets, Action{Type: Forget, RelPath: p, FileID: b.FileID})
			deferred[localAtPost(pathNow)] = true
			continue
		}
		lp := localAtPost(pathNow)
		over := invLocal(lp) // the baseline row the local file would otherwise be
		_, pathNowIsBase := in.Base[pathNow]
		_, lpIsBase := in.Base[over]
		movedOver := lpIsBase && takenOver[over] && (!pathNowIsBase || pathNow == over)
		if !locOK && remoteMoved && (!pathNowIsBase && !lpIsBase || movedOver) {
			if ln, ok := in.Local[lp]; ok && !ln.IsDir {
				resolvedLocal[lp] = true
				// A double rename is not by itself a conflict: the baseline
				// still says which side changed the content (§4.2, S023). Each
				// transfer below commits its row keyed on the file id, which
				// re-homes it here and drops the old path, as the Record does.
				localChanged := ln.MD5 != b.MD5
				remoteContentChanged := ra.item.MD5 != b.DriveMD5
				// Moved over another file (cause D), the local bytes only say
				// whose they are when they match one side exactly; anything
				// else is an edit of one of the two, no telling which.
				overUntouched := movedOver && ln.MD5 == in.Base[over].MD5 && keptOnDrive(ln.MD5)
				overEdited := movedOver && localChanged && !overUntouched
				switch {
				case ln.MD5 == ra.item.MD5:
					// Same content on both sides: the move is done end to end,
					// only the baseline path is stale. Record re-homes the row
					// (UpsertItem is keyed on the file id, dropping the old path).
					transfers = append(transfers, Action{Type: Record, RelPath: pathNow, FileID: b.FileID,
						MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version,
						LocalExists: true, LocalSize: ln.Size, LocalMtimeNS: ln.MtimeNS})
				case overUntouched:
					// The file moved over is unchanged here and gone from Drive
					// (§4.2: removed); this row's item was deleted here and moved
					// there (§4.2: downloaded) — the download replaces it in
					// place, pinned to the scanned stat (§7).
					transfers = append(transfers, Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
						MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version,
						LocalExists: true, LocalSize: ln.Size, LocalMtimeNS: ln.MtimeNS})
				case localChanged && !remoteContentChanged && !overEdited:
					// Drive only renamed it: a conflict here would give Drive's
					// older bytes the name — after S011, a partial upload of
					// this very file.
					transfers = append(transfers, Action{Type: UpdateRemote, RelPath: pathNow, FileID: b.FileID,
						RemoteWant: pinRemote(ra.path, ra.item)})
				case !localChanged && remoteContentChanged && keptOnDrive(ln.MD5):
					// Only Drive's content changed; the local file is the
					// baseline's, so the download replaces it — pinned to the
					// scanned stat, so an edit landing meanwhile wins (§7).
					// Bytes never confirmed on Drive (a row whose two md5s
					// disagree) are not replaced: the conflict below keeps them.
					transfers = append(transfers, Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
						MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version,
						LocalExists: true, LocalSize: ln.Size, LocalMtimeNS: ln.MtimeNS})
				default:
					// Both sides changed the content, differently: keep the
					// local bytes as a conflict copy, let the remote win the
					// canonical path. Also taken by a row whose two md5s
					// disagree with neither side changed — no side to trust.
					cp := conflicts.Path(pathNow, in.Machine, in.Now)
					moves = append(moves, Action{Type: ConflictBackup, RelPath: pathNow, NewRelPath: cp})
					transfers = append(transfers,
						Action{Type: Upload, RelPath: cp, ProtectedBy: pathNow},
						Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
							MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version, ProtectedBy: pathNow})
				}
				continue
			}
		}
		localChanged := locOK && loc.MD5 != b.MD5
		remoteContentChanged := remOK && ra.item.MD5 != b.DriveMD5
		// A remote move normally becomes a local move — except in a true
		// conflict, where the local content leaves through its conflict
		// backup instead, taken from where the content actually sits
		// (invariant 7): routing it through a move first would make the
		// backup depend on an ordering the plan must never get wrong.
		trueConflict := locOK && remOK && localChanged && remoteContentChanged && loc.MD5 != ra.item.MD5
		// The local file is the baseline's but its bytes were never confirmed
		// on Drive (a row whose two md5s disagree): a download may not replace
		// them (§4.5 rule O1), so Drive's change meets them as a conflict.
		unkeptBase := locOK && remOK && !localChanged && remoteContentChanged && loc.MD5 != ra.item.MD5 &&
			!keptOnDrive(loc.MD5)
		trueConflict = trueConflict || unkeptBase
		// Drive moved the row: something lands at pathNow — this row's file by
		// a local move, or Drive's bytes by a download (a true conflict, or the
		// file deleted here). The path may already be occupied locally. Never
		// let the rename or the download silently clobber it (invariant 7). The
		// occupant is what sits there once the folder moves run: under a folder
		// Drive renamed it is still at the old folder's path (S002). How it is
		// kept is decided after pass 1 (resolveOccupants below), once every row
		// has said what it does with its own file — moves it away, backs it up,
		// releases it, forgets its row — whichever row the sort reached first
		// (S031). Only a row on hold is decided here: it is never moved, backed
		// up or forgotten, and its row keeps the name, which a move's commit
		// needs — so this row waits with it.
		occAt := ""
		if remoteMoved {
			occAt = localAtPost(pathNow)
			_, occupied := in.Local[occAt]
			waits := ""
			if q, ok := rowAt[pathNow]; ok && q != p && heldRow(q) {
				waits = q // its row keeps the name, its file elsewhere or gone
			}
			if occupied {
				if owner := invLocal(occAt); owner != p && heldRow(owner) {
					waits = owner
				}
			}
			if waits != "" {
				skips = append(skips, Skip{RelPath: p,
					Reason: "moved in Drive to " + pathNow + ", which " + waits + " holds, whose sync is on hold; leaving both alone"})
				continue
			}
			if !occupied {
				occAt = ""
			}
		}
		if remoteMoved && locOK && !trueConflict {
			mv := Action{Type: MoveLocal, RelPath: expected, NewRelPath: pathNow, FileID: b.FileID}
			if folds && foldPath(expected) == foldPath(pathNow) {
				// A case-only rename: the "occupant" of the destination is
				// the file itself under the fold, so the gate pins to its own
				// stat (as for §5's fold adopt). Was: refused every cycle.
				mv.LocalExists, mv.LocalSize, mv.LocalMtimeNS = true, loc.Size, loc.MtimeNS
			}
			if occAt != "" {
				occChecks = append(occChecks, occCheck{mv: len(moves), at: occAt, dst: pathNow})
			}
			moves = append(moves, mv)
		} else if occAt != "" {
			// The download expects the path empty (no pin): the occupant is
			// kept the same way, and a keep that fails refuses the download.
			occChecks = append(occChecks, occCheck{mv: -1, at: occAt, dst: pathNow})
		}

		switch {
		case !locOK && !remOK:
			deletes = append(deletes, Action{Type: Forget, RelPath: p, FileID: b.FileID})

		case !locOK: // local deleted, remote exists
			if remoteContentChanged || remoteMoved || strangerDone(p) {
				// Edit (or move) beats delete: restore the remote version.
				transfers = append(transfers, Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
					MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version})
			} else {
				src := moveSrc{path: p, b: b, remote: ra}
				moveSrcs[pairKey(b.MD5, b.Size)] = append(moveSrcs[pairKey(b.MD5, b.Size)], src)
				trashCandidates = append(trashCandidates, src)
				driveLeaves[b.FileID] = true
			}

		case !remOK: // remote deleted, local exists
			// The replacement is looked up where Drive holds it now: under a
			// folder renamed here it is still at the old folder's path until
			// the reparent runs (S002).
			replacedAt := invLD(localPath)
			newRem, replaced := newRemoteAt(replacedAt)
			sibling, sibAt, minted := "", localPath, false
			if localChanged && !replaced {
				sibling, sibAt, minted = driveSibling(localPath, "")
			}
			switch {
			case localChanged && replaced && newRem.MD5 == loc.MD5:
				// Same new content arrived on both sides independently.
				transfers = append(transfers, Action{Type: Record, RelPath: localPath, FileID: newRem.FileID,
					MD5: newRem.MD5, Size: newRem.Size, Version: newRem.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
				claimed[replacedAt] = true
			case localChanged && replaced:
				cp := conflicts.Path(localPath, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: localPath, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp, ProtectedBy: localPath},
					Action{Type: Download, RelPath: localPath, FileID: newRem.FileID,
						MD5: newRem.MD5, Size: newRem.Size, Version: newRem.Version,
						ProtectedBy: localPath})
				claimed[replacedAt] = true
			case minted:
				// S030: the resurrect would put on Drive a second name beside
				// one Drive keeps, differing only in case or accents.
				skips = append(skips, nameConflict(localPath, sibling, sibAt, "edited here, gone from Drive, not uploaded"))
				keptHere = append(keptHere, localPath)
			case localChanged && heldLocalPath[localPath]:
				// A folder deleted here comes back from Drive under this
				// file's name (S020): a type clash (§4.2) — uploading the file
				// would put a file and a folder under one name on Drive. Left
				// alone, reported, until one of the two moves.
				skips = append(skips, Skip{RelPath: localPath,
					Reason: "type clash: edited here, gone from Drive, and Drive now has a folder under this name; not synced"})
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
				if rehome || b.MtimeNS != loc.MtimeNS || b.Size != loc.Size || b.DriveVersion != ra.item.Version {
					act := Action{Type: Record, RelPath: pathNow, FileID: b.FileID,
						MD5: b.MD5, Size: loc.Size, Version: ra.item.Version,
						LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS}
					if remoteMoved {
						act.ProtectedBy = expected // the record acts on the moved file
					}
					transfers = append(transfers, act)
				}
			case localChanged && !remoteContentChanged:
				act := Action{Type: UpdateRemote, RelPath: pathNow, FileID: b.FileID,
					RemoteWant: pinRemote(ra.path, ra.item)}
				if remoteMoved {
					act.ProtectedBy = expected // upload must read the moved file, not a stranger
				}
				transfers = append(transfers, act)
			case !localChanged && remoteContentChanged && !unkeptBase:
				// The download replaces the scanned local file (moved into
				// place first when the remote also renamed it).
				act := Action{Type: Download, RelPath: pathNow, FileID: b.FileID,
					MD5: ra.item.MD5, Size: ra.item.Size, Version: ra.item.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS}
				if remoteMoved {
					// Replaces the moved file only: if the move failed, whatever
					// is at pathNow is not the file the pin names (S031).
					act.ProtectedBy = expected
				}
				transfers = append(transfers, act)
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

	// resolveOccupants: each occupied move destination, now that every row has
	// decided (S031). Rule O1 (§4.5): a local file is never renamed over unless
	// its bytes are kept — and the planner never relies on that, it keeps the
	// occupant itself:
	//   - a row that still claims it and moves or backs it up in this plan:
	//     that action vacates the path first (dependency order), the move
	//     expects the path empty and depends on it;
	//   - a row whose Drive item is gone: unchanged → binned first, edited →
	//     conflict copy and the row forgotten (S002);
	//   - nobody's (untracked, released to pass 2, or its row forgotten as an
	//     unrelated pair's): conflict copy, uploaded, before the move.
	// Was: a tracked occupant with a live Drive item was taken for "safe on
	// Drive" — the move was pinned to its stat and renamed over it, edited or
	// not, and whether its own move ran first was left to the path sort.
	forgotten := map[string]bool{}
	for _, f := range forgets {
		forgotten[f.RelPath] = true
	}
	vacater := map[string]int{} // path → index in moves of the action that empties it
	for i, m := range moves {
		if !m.IsDir && (m.Type == MoveLocal || m.Type == ConflictBackup) {
			vacater[m.RelPath] = i
		}
	}
	for _, c := range occChecks {
		occ := in.Local[c.at]
		owner := invLocal(c.at)
		ob, tracked := in.Base[owner]
		tracked = tracked && !ob.IsDir && !released[c.at] && !forgotten[owner]
		_, ownerOnDrive := remByID[ob.FileID]
		// Its row's Drive item is gone: its bytes are not on Drive, so it is
		// never clobbered. (Held rows waited in pass 1.)
		ownerGone := tracked && !ownerOnDrive
		// dependsOn: the move into the path depends on what empties it. A
		// download needs no protector: it expects the path empty, so its guard
		// refuses it if the occupant is still there.
		dependsOn := func(q string) {
			if c.mv >= 0 {
				moves[c.mv].ProtectedBy = q
			}
		}
		backup := func() {
			cp := conflicts.Path(c.dst, in.Machine, in.Now)
			moves = append(moves, Action{Type: ConflictBackup, RelPath: c.dst, NewRelPath: cp})
			transfers = append(transfers, Action{Type: Upload, RelPath: cp, ProtectedBy: c.dst})
			dependsOn(c.dst)
			backedUp[c.at] = true
		}
		switch {
		case occ.IsDir:
			// A directory occupies a file's destination — a type clash
			// never resolved by clobbering; the executor refuses it
			// (LocalExists stays false), leaving both sides intact.
		case ownerGone && occ.MD5 == ob.MD5:
			// Unchanged here, deleted on Drive: §4.2 bins it. The bin runs
			// before the move instead of after it (see vacateFirst), and
			// the move expects the path empty — a bin that fails leaves
			// the file there, and the move is refused (§7).
			vacateFirst[c.at] = vacate{vacateBin, c.dst}
		case ownerGone:
			// Edited here, its Drive item gone: its only copy lives here.
			// Preserved as a conflict copy before the move vacates the path,
			// and the move refused if that backup fails. The copy's upload is
			// the edit's resurrection (edit beats delete). The row has
			// nothing left: dropped after the backup and before the move, so
			// the move's commit finds the path free in the baseline.
			backup()
			vacateFirst[c.at] = vacate{vacateCopied, c.dst}
			moves = append(moves, Action{Type: Forget, RelPath: owner, FileID: ob.FileID, ProtectedBy: c.dst})
			dependsOn(owner)
		case !tracked:
			// Untracked local-only content, or a file whose row this plan
			// lets go: its only claim is here.
			backup()
		default:
			v, ok := vacater[c.dst]
			switch {
			case ok && moves[v].Type == MoveLocal:
				// Its own move takes it where its row says it goes, first.
				// A vacate that fails — at its rename or at its commit —
				// refuses this move, so no row ever meets another file's bytes
				// at its name.
				dependsOn(c.dst)
			case ok && c.mv < 0:
				// Its row's true conflict backs it up first; the download's
				// commit (keyed on id and path) takes the name in the baseline.
			case ok:
				// Its row's true conflict backs it up first (the copy is
				// uploaded by that row). The row keeps the name in the
				// baseline until its download re-homes it in the transfer
				// stage — after this move's commit, which needs the name free —
				// so it is dropped first; the download re-creates it by id.
				moves = append(moves, Action{Type: Forget, RelPath: owner, FileID: ob.FileID, ProtectedBy: c.dst})
				dependsOn(owner)
			default:
				// A row that claims the file but does nothing with it: keep the
				// bytes like an untracked occupant's.
				backup()
			}
		}
	}

	if len(vacateFirst) > 0 {
		keep := deletes[:0]
		for _, a := range deletes {
			if v := vacateFirst[a.RelPath]; a.Type == QuarantineLocal && !a.IsDir && v.kind == vacateBin {
				// Where the hoisted folder moves have put it by then.
				a.RelPath = v.at
				moves = append(moves, a)
				continue
			}
			keep = append(keep, a)
		}
		deletes = keep
		kept := transfers[:0]
		for _, a := range transfers {
			if a.Type == Upload && a.FileID == "" && vacateFirst[a.RelPath].kind == vacateCopied {
				continue
			}
			kept = append(kept, a)
		}
		transfers = kept
	}

	// emptiedByMoves: every local entry under dir leaves it by a planned move —
	// a file's own move, a folder moved out, or a merge's source inside that
	// the plan empties too. Anything else (an untracked file, an empty new
	// folder) is something new here.
	var emptiedByMoves func(dir string) bool
	emptiedByMoves = func(dir string) bool {
		if _, ok := hold.under(dir); ok {
			// A symlink, non-regular or unreadable file the scan does not
			// read is something here too (S036; was: the removal planned
			// anyway and failed "directory not empty", then a new folder).
			return false
		}
		movedOut := map[string]bool{}
		for _, m := range moves {
			if m.Type == MoveLocal {
				movedOut[m.RelPath] = true
			}
		}
		under := func(q, d string) bool { return strings.HasPrefix(q, d+"/") }
		for q, l := range in.Local {
			if !under(q, dir) || movedOut[q] {
				continue
			}
			carried := false
			for src := range movedOut {
				if under(q, src) {
					carried = true
					break
				}
			}
			if carried {
				continue
			}
			if l.IsDir && mergedSrc[q] && emptiedByMoves(q) {
				continue
			}
			return false // a file or folder that stays: something new here
		}
		return true
	}

	// --- Pass 2: new local paths ---------------------------------------
	pairedSrc := map[string]bool{} // baseline path consumed as a move source
	movedSrc := map[string]bool{}  // ...and moved on Drive to it
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		// A file where a folder deleted here sat (S020): the folder comes
		// back at Drive's path, its row with it; the file is new.
		fileOnRestored := restoredDir[p] && !in.Local[p].IsDir
		if _, inBase := in.Base[p]; inBase && !released[p] && !mergedSrc[p] && !fileOnRestored {
			// A released path is new although its row is still there: the row's
			// file is elsewhere (R25), and its commit re-homes the row by id. Met
			// here like any new file — upload, or §4.2's both-new rows with a new
			// Drive item at the name (S031 round 2: pass 3 pinned that item's
			// download to the new file, and rule O1 refused every cycle). So is a
			// merged folder's source (S035): its row goes to the destination, the
			// folder left here is created on Drive.
			continue
		}
		if deferred[p] {
			continue // an unrelated pair's file: next cycle, as a new file
		}
		if underAny(p, heldLocalPath) {
			continue // where a folder deleted here would come back: reported in pass 4 (S020)
		}
		if q := invLocal(p); q != p && !released[p] {
			if _, inBase := in.Base[q]; inBase {
				continue // explained by a dir move the disk shows; pass 1 handled the row
			}
		}
		if backedUp[p] {
			continue // reclaimed by a move; already preserved as a conflict copy
		}
		if resolvedLocal[p] {
			continue // pass 1 recorded it at the remote's post-move path (R25)
		}
		loc := in.Local[p]
		target := rewrite(p)
		// Every row resolves at the path the item will occupy when the
		// action runs (§4.2, R11): under a remotely-moved ancestor the new
		// local file meets its remote counterpart at the POST-move path —
		// looking up the pre-move path silently skipped the both-new rows
		// and emitted an upload and a download onto one rel_path. The same
		// holds the other way (S002): under a folder renamed here, the Drive
		// item that lands at target is still at the old folder's path.
		rt := invLD(target)
		if loc.IsDir && mergedSrc[p] && emptiedByMoves(p) {
			// A merge's source the plan empties: removed here, not created on
			// Drive (S035). Expected empty — the removal refuses anything else
			// (a racing write keeps it, the next cycle meets it as new). No
			// row: the folder's row is the merge's, re-homed at its
			// destination.
			deletes = append(deletes, Action{Type: QuarantineLocal, RelPath: p, IsDir: true})
			continue
		}
		if loc.IsDir {
			if r, ok := newRemoteAt(rt); ok {
				if r.IsDir {
					transfers = append(transfers, Action{Type: Record, RelPath: target, FileID: r.FileID,
						IsDir: true, Version: r.Version})
				} else {
					// C5 (R22): a new remote FILE where the local side has a
					// new folder — the same type-clash rule pass 1 applies:
					// report, claim, touch nothing on either side.
					skips = append(skips, Skip{RelPath: target,
						Reason: "type clash: folder here, file in Drive; not synced"})
				}
				claimed[rt] = true
			} else if _, taken := in.Remote[rt]; !taken {
				if dp, at, ok := driveSibling(target, ""); ok {
					// S038: Drive holds the name (or a folder above it) in
					// another case or accents: a second spelling there is
					// one this disk cannot hold apart.
					if at == target {
						at = p
					}
					skips = append(skips, nameConflict(target, dp, at, "new folder here, not created in Drive"))
					keptHere = append(keptHere, p)
				} else {
					mkdirs = append(mkdirs, Action{Type: MkdirRemote, RelPath: target, IsDir: true})
				}
			}
			continue
		}
		if r, ok := newRemoteAt(rt); ok && r.IsDir {
			// C5 (R22): a new remote FOLDER where the local side has a new
			// file. Was: a blind upload minted a same-name file beside the
			// folder on Drive while MkdirLocal failed on the file forever.
			skips = append(skips, Skip{RelPath: target,
				Reason: "type clash: file here, folder in Drive; not synced"})
			claimed[rt] = true
			continue
		}
		if r, ok := newRemoteAt(rt); ok && !r.IsDir {
			switch {
			case r.MD5 == loc.MD5:
				// Adopt: identical content already on both sides.
				transfers = append(transfers, Action{Type: Record, RelPath: target, FileID: r.FileID,
					MD5: r.MD5, Size: r.Size, Version: r.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
			case in.localWinsName(loc, r):
				// Init merge, local edited last (W18-E): the local file keeps
				// the plain name and Drive's copy steps aside.
				moves = append(moves, remoteStepsAside(target, r, in))
				transfers = append(transfers,
					Action{Type: Upload, RelPath: target, ProtectedBy: target},
					Action{Type: Download, RelPath: conflicts.Path(target, in.Machine, in.Now),
						FileID: r.FileID, MD5: r.MD5, Size: r.Size, Version: r.Version,
						ProtectedBy: target})
			default:
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
			claimed[rt] = true
			continue
		}
		if drp, r, ok := newRemoteAtFold(rt); ok && !r.IsDir {
			// Fold-equal both-new pair (C2, R19): one path on disk, so the
			// ordinary §4.2 rows fire, remote winning the canonical byte
			// form. Was: a blind Upload minted a case-duplicate on Drive
			// and the snapshot collapse later quarantined the local file.
			rp := rewriteLD(drp) // where the Drive item sits once the reparents run
			switch {
			case r.MD5 == loc.MD5:
				// Adopt via a case-only rename to the remote byte form.
				// The destination "occupant" is the source itself under
				// the fold, so the §7 gate pins to the file's own stat.
				moves = append(moves, Action{Type: MoveLocal, RelPath: target, NewRelPath: rp,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS})
				transfers = append(transfers, Action{Type: Record, RelPath: rp, FileID: r.FileID,
					MD5: r.MD5, Size: r.Size, Version: r.Version,
					LocalExists: true, LocalSize: loc.Size, LocalMtimeNS: loc.MtimeNS,
					ProtectedBy: target})
			case in.localWinsName(loc, r):
				// Init merge, local edited last (W18-E). The local byte form
				// keeps the plain name; Drive's copy steps aside from ITS own
				// path, which is the fold-equal one.
				moves = append(moves, remoteStepsAside(rp, r, in))
				transfers = append(transfers,
					Action{Type: Upload, RelPath: target, ProtectedBy: rp},
					Action{Type: Download, RelPath: conflicts.Path(rp, in.Machine, in.Now),
						FileID: r.FileID, MD5: r.MD5, Size: r.Size, Version: r.Version,
						ProtectedBy: rp})
			default:
				// Both-new conflict: the backup vacates the fold-path, the
				// remote byte form downloads onto it fresh.
				cp := conflicts.Path(target, in.Machine, in.Now)
				moves = append(moves, Action{Type: ConflictBackup, RelPath: target, NewRelPath: cp})
				transfers = append(transfers,
					Action{Type: Upload, RelPath: cp, ProtectedBy: target},
					Action{Type: Download, RelPath: rp, FileID: r.FileID,
						MD5: r.MD5, Size: r.Size, Version: r.Version,
						ProtectedBy: target})
			}
			claimed[drp] = true
			continue
		}
		// Move pairing: a deleted baseline file with identical content
		// becomes a remote move instead of delete + re-upload.
		key := pairKey(loc.MD5, loc.Size)
		srcID := ""
		if srcs := moveSrcs[key]; len(srcs) > 0 {
			srcID = srcs[0].b.FileID
		}
		if dp, at, ok := driveSibling(target, srcID); ok {
			// S030: uploaded, or its row's Drive item renamed here, it would
			// put on Drive a second name beside dp, differing only in case or
			// accents. Held: a paired source is consumed so it is not binned
			// on Drive (its file is this one, renamed), nor with its folder.
			if srcID != "" {
				pairedSrc[moveSrcs[key][0].path] = true
				moveSrcs[key] = moveSrcs[key][1:]
				heldIDs[srcID] = true
			}
			if at == target {
				at = p
			}
			skips = append(skips, nameConflict(target, dp, at, "new here, not uploaded"))
			keptHere = append(keptHere, p)
			continue
		}
		if srcs := moveSrcs[key]; len(srcs) > 0 {
			src := srcs[0]
			moveSrcs[key] = srcs[1:]
			pairedSrc[src.path] = true
			movedSrc[src.path] = true
			moves = append(moves, Action{Type: MoveRemote, RelPath: src.path, NewRelPath: target,
				FileID: src.b.FileID, MD5: loc.MD5, Size: loc.Size,
				RemoteWant: pinRemote(src.remote.path, src.remote.item)})
			continue
		}
		transfers = append(transfers, Action{Type: Upload, RelPath: target})
	}
	// A file trash names the path Drive holds the item at: under a folder
	// Drive renamed, that is in the renamed folder, where a trash of the
	// folder itself absorbs it — one Drive-bin entry (§4.2's collapse; S020:
	// at the old path the files escaped it and went to the bin one by one).
	for _, sk := range shadowSrcs {
		if !movedSrc[sk.RelPath] {
			skips = append(skips, sk)
		}
	}
	for _, src := range trashCandidates {
		if !pairedSrc[src.path] {
			deletes = append(deletes, Action{Type: TrashRemote, RelPath: src.remote.path, FileID: src.b.FileID,
				RemoteWant: pinRemote(src.remote.path, src.remote.item)})
		}
	}

	// A file move's commit renames its row onto the destination, and rows are
	// unique per path (S031). A row still sitting there must have left by then:
	// moved by its own move, binned, or forgotten in the moves stage. One whose
	// file is elsewhere or gone — re-homed by its transfer, dropped by a
	// delete, both later — is forgotten first, the move depending on it; its
	// transfer re-creates the row by id. Was: the move renamed on disk, its
	// commit failed (`UNIQUE constraint failed: items.rel_path`), and the next
	// cycle met the moved bytes under the other row's name — read as an edit
	// of that file (the swap, a chain interrupted between two moves).
	leaves := map[string]bool{}      // rows the moves stage takes off their path
	forgetFirst := map[string]bool{} // rows forgotten below, before a move's commit
	for _, m := range slices.Concat(forgets, moves) {
		switch {
		case m.Type == Forget || m.Type == QuarantineLocal:
			leaves[m.FileID] = true
		case m.Type == MoveRemote && !m.IsDir:
			leaves[m.FileID] = true
		case isFileMove(m) && m.FileID != "":
			leaves[m.FileID] = true
		}
	}
	for i := range moves {
		m := moves[i]
		if !(isFileMove(m) || m.Type == MoveRemote && !m.IsDir) || m.FileID == "" {
			continue // a remote file move commits its row onto the path too
		}
		q, ok := rowAt[m.NewRelPath]
		if !ok || in.Base[q].FileID == m.FileID || leaves[in.Base[q].FileID] {
			continue
		}
		id := in.Base[q].FileID
		moves = append(moves, Action{Type: Forget, RelPath: q, FileID: id, ProtectedBy: m.ProtectedBy})
		moves[i].ProtectedBy = q
		leaves[id] = true
		forgetFirst[id] = true
	}
	if len(forgetFirst) > 0 {
		// A row gone on both sides was forgotten in the deletes stage too: it
		// leaves once, here (S036; was: `forget` twice in one plan).
		keep := deletes[:0]
		for _, a := range deletes {
			if !(a.Type == Forget && !a.IsDir && forgetFirst[a.FileID]) {
				keep = append(keep, a)
			}
		}
		deletes = keep
	}

	// --- Pass 3: new remote items not yet claimed ----------------------
	// Local paths an already-planned move or backup empties before the
	// transfers run. A download onto such a path must expect it ABSENT: the
	// §7 guard means "this exact file must still be here", so pinning the
	// occupant the scan saw refuses the download every cycle once the moves
	// stage has legitimately taken it away. (Found building W18-E, whose
	// conflict shape produces this routinely on the other machine; reachable
	// before it too — rename a file on one machine and give a new file its old
	// name, and the second machine plans exactly this pair.) A vacate that
	// FAILS still refuses the download, since the guard then finds an occupant
	// where it expected none — invariant 7 by the same mechanism.
	vacated := map[string]bool{}
	for _, m := range moves {
		switch m.Type {
		case MoveLocal, ConflictBackup:
			vacated[m.RelPath] = true
		}
	}
	for _, rp := range slices.Sorted(maps.Keys(in.Remote)) {
		r := in.Remote[rp]
		if baseIDs[r.FileID] || claimed[rp] {
			continue
		}
		// A new remote item under a locally-renamed dir materializes at its
		// post-reparent path — never a zombie under the dead source dir.
		target := rewriteLD(rp)
		if u, held := hold.at(target); held {
			// S013: a download or mkdir here would replace the user's symlink
			// or non-regular file, or land outside the sync dir through it.
			skips = append(skips, Skip{RelPath: target, Reason: hold.reason(target, u)})
			continue
		}
		if h, ok := heldDriveUnder(target); ok {
			// Inside a folder deleted here whose restore waits (S020): it
			// waits with the folder.
			skips = append(skips, holdSkip(target, "new in Drive, in a folder that waits: "+h.why, h))
			continue
		}
		if lq, at, ok := foldHolderUp(localAtPost(target)); ok && !vacated[rewrite(lq)] {
			// S030: another entry here holds the place under another case —
			// looked up where it sits on disk now, under this plan's folder
			// moves (was: at the target only — a download into a folder
			// renamed this cycle was refused once by the gate). S038: or a
			// folder above it does — the item would land in that entry.
			fate := holderFate(lq, r.FileID)
			if fate == holderLeaves && r.IsDir {
				fate = holderBinned // a mkdir runs before the moves stage empties it
			}
			if at != localAtPost(target) && fate != holderStays {
				fate = holderBinned // waits for the folder above it
			}
			switch fate {
			case holderOwn, holderLeaves:
			case holderBinned:
				skips = append(skips, Skip{RelPath: target, Reason: "new in Drive; waits a cycle: " + rewrite(lq) +
					", its name here in another case or accents, leaves first"})
				continue
			default:
				skips = append(skips, nameConflict(target, prefixLanding(target, at, localAtPost), rewrite(lq), "new in Drive"))
				continue
			}
		}
		if l, ok := in.Local[target]; ok && l.IsDir != r.IsDir {
			// C5 (R22): the path is occupied by the other type locally —
			// a MkdirLocal would fail on the file, a Download would be
			// refused on the dir, both forever. Report and leave it; if
			// the occupant is resolved by this plan's delete side, the
			// next cycle materializes the remote item cleanly.
			skips = append(skips, Skip{RelPath: target,
				Reason: "type clash: a file on one side, a folder on the other; not synced"})
			continue
		}
		if r.IsDir {
			mkdirs = append(mkdirs, Action{Type: MkdirLocal, RelPath: target, FileID: r.FileID,
				IsDir: true, Version: r.Version})
		} else {
			act := Action{Type: Download, RelPath: target, FileID: r.FileID,
				MD5: r.MD5, Size: r.Size, Version: r.Version}
			// Replaced-in-place: an unchanged local file sits where the new
			// remote item lands; the guard pins the replace to its scanned
			// stat so a mid-cycle edit is never clobbered. Only the file of
			// the row at that path, with its baseline bytes, kept on Drive
			// (S031 round 2): any other file there — new, released, edited —
			// is kept as a conflict copy first, and a held row's waits.
			if l, ok := in.Local[target]; ok && !l.IsDir && !vacated[target] {
				q := invLocal(target)
				ob, tracked := in.Base[q]
				switch {
				case tracked && heldRow(q):
					skips = append(skips, Skip{RelPath: target,
						Reason: "new in Drive at " + target + ", where " + q + "'s file sits, whose sync is on hold; leaving both alone"})
					continue
				case tracked && !ob.IsDir && !released[target] && l.MD5 == ob.MD5 && keptOnDrive(l.MD5):
					act.LocalExists, act.LocalSize, act.LocalMtimeNS = true, l.Size, l.MtimeNS
				default:
					cp := conflicts.Path(target, in.Machine, in.Now)
					moves = append(moves, Action{Type: ConflictBackup, RelPath: target, NewRelPath: cp})
					transfers = append(transfers, Action{Type: Upload, RelPath: cp, ProtectedBy: target})
					act.ProtectedBy = target
				}
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
	keptUnder := func(dir string) bool {
		for _, q := range keptHere {
			if strings.HasPrefix(q, dir+"/") {
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
		if in.ShadowedRemote[b.FileID] {
			// C2b (R19), dir flavor: a shadowed folder id is not a remote
			// deletion; leave the local dir alone and report it.
			skips = append(skips, Skip{RelPath: p, FileID: b.FileID,
				Reason: "remote folder is shadowed by a duplicate or fold-colliding name in Drive; leaving both sides alone"})
			continue
		}
		if in.UnreadableLocal[b.FileID] {
			// W18-G, dir flavor: a folder under an unreadable one is not a
			// local deletion, and trashing it on Drive would take the whole
			// subtree with it (§4.2's collapse).
			skips = append(skips, Skip{RelPath: p,
				Reason: "local folder is inside a directory that could not be read; leaving both sides alone"})
			continue
		}
		// A local-driven move source (or a dir inside one) lives on at its
		// post-rename location; judging it at the baseline path would read
		// the user's rename as a deletion.
		lp := rewriteLocal(p)
		if to, ok := localMoveDstOf[p]; ok {
			lp = to
		}
		_, locOK := in.Local[lp]
		ra, remOK := remByID[b.FileID]
		if why, ok := held[p]; ok {
			// S013, dir flavor: not a local deletion, and trashing the folder
			// on Drive would take the whole subtree with it.
			skips = append(skips, Skip{RelPath: p, Reason: why})
			continue
		}
		if h, ok := restoreHoldOf(p); ok {
			skips = append(skips, holdSkip(p, h.why, h))
			continue
		}
		if restoredDir[p] {
			// Deleted here, renamed or moved on Drive (S020): back at Drive's
			// path. The commit re-homes the row by id — and replaces a row
			// still at that path whose Drive item is gone (§4.3's arriving
			// folder).
			target := rewriteLD(ra.path)
			act := Action{Type: MkdirLocal, RelPath: target, FileID: b.FileID, IsDir: true, Version: ra.item.Version}
			for i, mv := range moves {
				if mv.Type != MoveLocal || !mv.IsDir {
					continue
				}
				if mv.RelPath == target {
					// The row there is alive and moves away first (a swap on
					// Drive): the commit, which replaces any row at its path,
					// waits for that move — never drops a live row.
					act.ProtectedBy = mv.RelPath
				}
				if mv.NewRelPath == p {
					// A folder move commits onto this folder's old path, where
					// its rows still sit (one row per path): they leave first,
					// forgotten; the MkdirLocal and the downloads re-create them
					// by id. Interrupted: nothing claims Drive's items — new to
					// the next cycle, downloaded.
					for _, q := range slices.Sorted(maps.Keys(in.Base)) {
						if q == p || strings.HasPrefix(q, p+"/") {
							forgets = append(forgets, Action{Type: Forget, RelPath: q, FileID: in.Base[q].FileID, IsDir: in.Base[q].IsDir})
						}
					}
					moves[i].ProtectedBy = forgets[len(forgets)-1].RelPath
				}
			}
			mkdirs = append(mkdirs, act)
			continue
		}
		switch {
		case locOK && remOK:
			// Alive on both sides (a dir move was already emitted).
			_ = ra
		case locOK && !remOK:
			if r, taken := in.Remote[p]; taken && r.IsDir && r.FileID != b.FileID {
				// Drive holds another folder under this name: another machine
				// deleted this one and made a new one (S035). Pass 3
				// materializes it and this folder's content lands in it; its
				// commit takes over this row's path. A resurrect here would
				// put a second folder under one name on Drive (§5: one is then
				// shadowed).
				continue
			}
			if dp, at, ok := driveSibling(p, ""); ok && (hasCreateUnder(p) || hasCreateUnder(rewrite(p)) || keptUnder(lp)) {
				// S038: resurrected, it would be a second spelling beside
				// one Drive keeps; it stays here, with what it holds.
				if at == p {
					at = lp
				}
				skips = append(skips, nameConflict(p, dp, at, "deleted in Drive, holds what is kept here; not re-created in Drive"))
			} else if hasCreateUnder(p) || hasCreateUnder(rewrite(p)) {
				// Resurrect the container for surviving content.
				mkdirs = append(mkdirs, Action{Type: MkdirRemote, RelPath: p, IsDir: true})
			} else if keptUnder(lp) {
				skips = append(skips, Skip{RelPath: lp,
					Reason: "deleted in Drive, but holds an entry left alone by a name conflict; not removed"})
			} else if u, ok := hold.under(lp); ok {
				// S013: a symlink or non-regular file the scan did not read
				// still sits inside; removing the folder would take it along.
				skips = append(skips, Skip{RelPath: lp,
					Reason: "deleted in Drive, but holds " + u + ", which the scan does not read (" + hold.why[u] + "); not removed"})
			} else if heldLocalUnder(lp) {
				skips = append(skips, Skip{RelPath: lp,
					Reason: "deleted in Drive, but holds a file whose sync is on hold; not removed"})
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
			if heldOnDriveUnder(ra.path) {
				// S013: remoteAliveUnder counts only items the baseline does
				// not know; a held row's item is known but was never
				// materialized here, so it reads as nothing — and would go
				// to Drive's bin with the folder.
				skips = append(skips, Skip{RelPath: p,
					Reason: "gone locally, but on Drive holds a file whose sync is on hold; not trashed"})
				continue
			}
			if !hasCreateUnder(ra.path) && !hasCreateUnder(rewriteLD(ra.path)) && !remoteAliveUnder(ra.path) {
				deletes = append(deletes, Action{Type: TrashRemote, RelPath: ra.path, FileID: b.FileID, IsDir: true,
					RemoteWant: pinRemote(ra.path, ra.item)})
			}
		default:
			deletes = append(deletes, Action{Type: Forget, RelPath: p, FileID: b.FileID, IsDir: true})
		}
	}

	// S038 round 3: a Drive folder bound to a folder here that the disk spells
	// otherwise (S035's arriving folder at a re-keyed path) — the disk renamed
	// to Drive's spelling first, before the mkdir commits it.
	// Keyed where the folder is once Drive's folder moves have run (round 4:
	// keyed by its pre-move path, the fix missed a folder under a parent Drive
	// renamed, and ran on one Drive's rename had already moved away).
	if len(fv.respelled) > 0 {
		movedAway := map[string]bool{}
		for _, m := range remoteDirMoves {
			movedAway[m.from] = true
		}
		post := map[string]string{} // post-move path → post-move path in the disk's spelling
		for want, disk := range fv.respelled {
			if movedAway[want] {
				continue
			}
			pw := rewrite(want)
			post[pw] = path.Join(path.Dir(pw), path.Base(disk))
		}
		for _, a := range mkdirs {
			if disk, ok := post[a.RelPath]; ok && a.Type == MkdirLocal && disk != a.RelPath {
				moves = append(moves, Action{Type: MoveLocal, RelPath: disk, NewRelPath: a.RelPath, IsDir: true})
			}
		}
	}
	skips = append(skips, fv.upHeld...)

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
	// S002's unrelated pairs: the folder's commit would re-home a forgotten
	// row onto the stranger's file, so it must not run if a forget failed
	// (S031, F3). One ProtectedBy per action: the forgets chain, each on the
	// one before, and the commit depends on the last.
	for i := range forgets {
		if i > 0 {
			forgets[i].ProtectedBy = forgets[i-1].RelPath
		}
	}
	for i, m := range moveLocalDirs {
		for _, f := range forgets {
			if strings.HasPrefix(f.RelPath, m.RelPath+"/") {
				moveLocalDirs[i].ProtectedBy = forgets[len(forgets)-1].RelPath
			}
		}
	}
	// File moves run in dependency order (§4.5, S031), not in path order: a
	// move into a path another action empties runs after it — chains of any
	// length — and a cycle (a swap, a rotation) goes through a temporary name.
	moveFiles, transfers = breakMoveCycles(moveFiles, transfers, in)
	moveFiles = orderMoves(moveFiles)
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
	plan = append(plan, forgets...)
	plan = append(plan, moveLocalDirs...)
	plan = append(plan, mkdirsPre...)
	plan = append(plan, moveRemoteDirs...)
	plan = append(plan, mkdirsUnderMoved...)
	plan = append(plan, moveFiles...)
	plan = append(plan, transfers...)
	plan = append(plan, deletes...)
	// S038: a folder renamed in case here goes up last, deepest first: every
	// action and commit before it works in the rows' spelling, which names
	// the folder on this disk too; its commit then renames the rows. Failing,
	// it is planned again next cycle; nothing depends on it.
	ups := slices.Clone(fv.caseUp)
	slices.SortStableFunc(ups, func(a, b dirMove) int {
		if c := strings.Count(b.from, "/") - strings.Count(a.from, "/"); c != 0 {
			return c
		}
		return strings.Compare(a.from, b.from)
	})
	for _, m := range ups {
		id := in.Base[m.from].FileID
		ra := remByID[id]
		plan = append(plan, Action{Type: MoveRemote, RelPath: m.from, NewRelPath: m.to,
			FileID: id, IsDir: true, RemoteWant: pinRemote(ra.path, ra.item)})
	}
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
func detectLocalDirMoves(in Input, remByID map[string]remoteRef, baseIDs map[string]bool, rewriteRD, rewriteDone func(string) string, hold *holds,
	driveFoldTaken func(n, id string) (string, bool)) ([]dirMove, []foldBlock) {
	// Move-source candidates exactly as pass 1 admits them: baseline files
	// deleted locally whose remote is unchanged (content and position). A
	// file under a remote folder rename the disk already shows is found
	// there (rewriteDone), not deleted.
	type cand struct{ path string }
	srcByKey := map[string][]cand{}
	candidates := 0
	for _, p := range slices.Sorted(maps.Keys(in.Base)) {
		b := in.Base[p]
		if b.IsDir {
			continue
		}
		if _, ok := in.Local[rewriteDone(p)]; ok {
			continue
		}
		if _, held := hold.at(p); held {
			continue // S013: not gone, only unread — a copy elsewhere is not its new home
		}
		if in.UnreadableLocal[b.FileID] {
			continue // W18-G, same reason: under a folder the scan could not read
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
		return nil, nil
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
	var blocked []foldBlock
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
		if driveFoldTaken != nil {
			if sib, taken := driveFoldTaken(n, b.FileID); taken {
				// S038: Drive holds the name in another case or accents; the
				// reparent would put a second spelling beside it.
				blocked = append(blocked, foldBlock{dirMove: dirMove{from: d, to: n}, sibling: sib})
				out = append(out, dirMove{from: d, to: d}) // carries its subtree: no inner pairing
				continue
			}
		}
		out = append(out, dirMove{from: d, to: n})
	}
	keep := out[:0]
	for _, m := range out {
		if m.from != m.to {
			keep = append(keep, m)
		}
	}
	return keep, blocked
}

// foldBlock is a local folder rename that Drive cannot take (S038): its new
// name differs only by the fold from sibling, an item Drive keeps there.
type foldBlock struct {
	dirMove
	sibling string
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

// nameConflict is the reported skip of a name conflict (S030, DEC-011): rel is
// what is not synced, drive and local the two names, what why rel was due.
func nameConflict(rel, drive, local, what string) Skip {
	return Skip{RelPath: rel, Kind: SkipNameConflict, DrivePath: drive, LocalPath: local,
		Reason: "name conflict: " + what + "; " + drive + " in Drive and " + local +
			" here differ only in case or accents, one name on this disk; neither synced onto the other until one is renamed"}
}
