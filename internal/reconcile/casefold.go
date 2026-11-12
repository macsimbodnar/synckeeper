package reconcile

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/names"
)

// S038 — folders on a disk that folds case or accents (spec §5). The scan
// reports each folder under the name it is stored under; the baseline and
// Drive may know the same folder under another byte form of that name, and
// every lookup across trees is by exact path. Read as two folders, one name
// on this disk became two on Drive: a row D/x.txt met an empty D (its file is
// at e/x.txt), read as deleted here, while e went up as a new folder beside
// Drive's E — the machines then moved and binned each other's copies.
//
// One rule, S030's for files: on such a disk a folder here at a folder's own
// path under the fold IS that folder. foldCanon re-keys the scan so the
// planner meets it under the name the plan knows it by, and says what the
// difference in spelling means:
//   - the folder's row: the scan is re-keyed to the row's path, so the whole
//     plan works in the rows' spelling (on a folding disk it names the folder
//     too). Drive holding it as synced (same path) → renamed in case HERE: its
//     own rename, synced as one (caseUp) — a move_remote of the folder, ids
//     kept, run LAST in the plan, deepest first, so every other action and
//     commit before it meets rows in the spelling they have. Drive moved or
//     renamed it (or holds it no longer) → Drive's name wins, as for a file
//     renamed in case on both sides: the folder's own move (MoveLocal)
//     renames it on disk.
//   - no row, Drive holding a NEW folder there: the folder flavour of §5's
//     fold adopt (C2) — re-keyed to Drive's spelling, so §4.2's both-new rows
//     meet it, and a case-only MoveLocal renames the disk to it (fixes).
//   - no row, Drive holding there a folder it moved or renamed (D → E) whose
//     path here is gone, and some non-empty file of it sits in this folder with
//     its synced bytes (DEC-013's evidence): this machine renamed it too, in
//     another case — re-keyed to Drive's spelling: the done move (§4.3, S002),
//     whose commit renames the disk to it. Without that evidence it stays a
//     name clash (S020, DEC-013).
//
// Nothing else changes: a folder with no counterpart keeps its name, and a
// file's own name is S030's (ownCase).
type foldView struct {
	caseUp []dirMove // folders renamed in case here (row path → new spelling), to go up last
	fixes  []Action  // case-only MoveLocal of a folder adopted at Drive's spelling
	// respelled: re-keyed folder path → its path in the disk's spelling. A
	// MkdirLocal at such a path binds a Drive folder to the folder here (S035's
	// arriving folder; round 3): the disk is renamed to the spelling first.
	respelled map[string]string
	upHeld    []Skip // case renames here not taken up: Drive holds the new name as another item
}

// shadowedSibling: a Drive item other than id that the snapshot shadows at a
// path equal to to under the fold (S038 round 3).
func shadowedSibling(in Input, to, id string) (string, bool) {
	for _, sh := range in.ShadowedAt {
		if sh.Item.FileID != id && path.Dir(sh.Path) == path.Dir(to) &&
			names.FoldKey(path.Base(sh.Path), in.CaseFold, in.NormFold) == names.FoldKey(path.Base(to), in.CaseFold, in.NormFold) {
			return sh.Path, true
		}
	}
	return "", false
}

// foldCanon returns in with Local (and UnreadableFiles) keyed by the planner's
// spelling of each folder, and what the differences mean. The input is
// returned unchanged when two scanned paths would land on one key — a disk
// that folds cannot hold such a pair, so the scan is not what this assumes.
func foldCanon(in Input, remByID map[string]remoteRef, baseIDs map[string]bool, hold *holds, foldPath func(string) string) (Input, foldView) {
	fv := foldView{respelled: map[string]string{}}
	baseDir := map[string]string{} // folded path → baseline folder
	ambiguous := map[string]bool{}
	for p, b := range in.Base {
		if !b.IsDir {
			continue
		}
		k := foldPath(p)
		if _, dup := baseDir[k]; dup {
			ambiguous[k] = true
		}
		baseDir[k] = p
	}
	newDir := map[string]string{}  // folded path → new Drive folder
	movedTo := map[string]string{} // folded Drive path → baseline folder Drive moved there
	for rp, r := range in.Remote {
		if !r.IsDir {
			continue
		}
		if !baseIDs[r.FileID] {
			newDir[foldPath(rp)] = rp
		}
	}
	for p, b := range in.Base {
		if ra, ok := remByID[b.FileID]; ok && b.IsDir && ra.path != p {
			movedTo[foldPath(ra.path)] = p
		}
	}
	diskFold := make(map[string]string, len(in.Local)) // folded disk path → disk path
	for q := range in.Local {
		diskFold[foldPath(q)] = q
	}

	var dirs []string
	for q, l := range in.Local {
		if l.IsDir {
			dirs = append(dirs, q)
		}
	}
	slices.SortFunc(dirs, func(a, b string) int {
		if c := strings.Count(a, "/") - strings.Count(b, "/"); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	canonDir := map[string]string{}
	canon := func(q string) string {
		if c, ok := canonDir[q]; ok {
			return c
		}
		i := strings.LastIndexByte(q, '/')
		if i < 0 {
			return q
		}
		if c, ok := canonDir[q[:i]]; ok {
			return c + q[i:]
		}
		return q
	}
	// evidenced: some non-empty file of baseline folder b sits under the disk
	// folder at with its synced bytes and size.
	evidenced := func(b, at string) bool {
		for q, bi := range in.Base {
			rest, ok := strings.CutPrefix(q, b+"/")
			if !ok || bi.IsDir || bi.Size <= 0 {
				continue
			}
			if lq, ok := diskFold[foldPath(at+"/"+rest)]; ok {
				if l := in.Local[lq]; !l.IsDir && l.MD5 == bi.MD5 && l.Size == bi.Size {
					return true
				}
			}
		}
		return false
	}
	for _, q := range dirs {
		parent, name := "", q
		if i := strings.LastIndexByte(q, '/'); i >= 0 {
			parent, name = canon(q[:i]), q[i+1:]
		}
		cand := name
		if parent != "" {
			cand = parent + "/" + name
		}
		canonDir[q] = cand
		k := foldPath(cand)
		dirOf := func(p string) string {
			if d := path.Dir(p); d != "." {
				return d
			}
			return ""
		}
		if b, ok := baseDir[k]; ok && !ambiguous[k] {
			want := b
			if want == cand || dirOf(want) != parent {
				continue
			}
			if _, exact := in.Local[want]; exact {
				continue
			}
			bi := in.Base[b]
			ra, onDrive := remByID[bi.FileID]
			_, heldB := hold.at(b)
			_, heldQ := hold.at(q)
			canonDir[q] = want
			fv.respelled[want] = cand
			if onDrive && ra.path == b && !heldB && !heldQ &&
				!in.ShadowedRemote[bi.FileID] && !in.UnreadableLocal[bi.FileID] {
				// Renamed in case here, Drive's name as synced: its own rename —
				// unless Drive holds the new name as another item it shadows
				// (a fold sibling that lost to this folder, or an exact
				// duplicate): renamed there, Drive would hold that name twice.
				to := path.Join(path.Dir(b), name)
				if sib, taken := shadowedSibling(in, to, bi.FileID); taken {
					fv.upHeld = append(fv.upHeld, nameConflict(b, sib, cand,
						"renamed in case here to "+to+", a name Drive holds as another folder; not renamed in Drive"))
					continue
				}
				fv.caseUp = append(fv.caseUp, dirMove{from: b, to: to})
			}
			continue
		}
		if rp, ok := newDir[k]; ok && rp != cand && dirOf(rp) == parent {
			// Both new, one name here: adopted at Drive's spelling.
			canonDir[q] = rp
			fv.fixes = append(fv.fixes, Action{Type: MoveLocal, RelPath: cand, NewRelPath: rp, IsDir: true})
			continue
		}
		if b, ok := movedTo[k]; ok {
			rp := remByID[in.Base[b].FileID].path
			if rp == cand || dirOf(rp) != parent {
				continue
			}
			if lq, here := diskFold[foldPath(b)]; here && in.Local[lq].IsDir {
				continue // the folder is still here: not this one
			}
			if evidenced(b, q) {
				// Renamed here too, in another case: the done move (§4.3).
				canonDir[q] = rp
			}
		}
	}
	if len(fv.caseUp) == 0 && len(fv.fixes) == 0 {
		changed := false
		for q, c := range canonDir {
			if q != c {
				changed = true
				break
			}
		}
		if !changed {
			return in, fv
		}
	}
	local := make(map[string]LocalItem, len(in.Local))
	for _, q := range slices.Sorted(maps.Keys(in.Local)) {
		c := canon(q)
		if _, dup := local[c]; dup {
			return in, foldView{}
		}
		local[c] = in.Local[q]
	}
	in.Local = local
	if len(in.UnreadableFiles) > 0 {
		uf := make(map[string]LocalItem, len(in.UnreadableFiles))
		for q, l := range in.UnreadableFiles {
			uf[canon(q)] = l
		}
		in.UnreadableFiles = uf
	}
	return in, fv
}
