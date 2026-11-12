package reconcile

// S031 round 2: a seeded random sweep of Plan's inputs — small trees where
// files are renamed, edited, deleted and created on both sides, flat and
// under a folder Drive renames (on disk too, or not yet). Every plan must
// pass both run-time nets (ValidateTransferStage, ValidateLocalOverwrites —
// a refused plan is refused every cycle), and a model of the executor runs it
// with no failure, each single and each pair of failed actions, each crash
// point, and each move's commit lost after its rename: no local file's bytes
// may end up nowhere — not on Drive, not on disk, not uploaded, not in the
// bin. S035 adds the folder merge's start state (Drive renamed D to N, N and
// D both here). Adapted from the independent check's harness; it found the round-2
// regression (a pinned download onto a released file) and the pre-existing
// download-onto-an-occupant overlap. S036 adds the wide shape — two folder
// levels, held rows, local renames over synced files (pfGenWide) — and one
// more check on the clean run: an update_remote uploads the file that sat at
// its row's path or at Drive's path, folder renames aside (pfCanon). S020
// deletes a folder here in the folder and wide shapes (pfDropLocal): the outer
// or the inner one, renamed on Drive or not. S038 adds the casefold shape: the
// folder shape on a disk that folds case (the model's disk is keyed by the
// fold), with folder and file names respelled in case here or on Drive.
//
// Default budget keeps the package fast; SYNCKEEPER_PLANFUZZ_RUNS=100000 is
// the deep run (per shape: flat, folder and wide).

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPlanFuzz(t *testing.T) {
	runs := 5000
	if v := os.Getenv("SYNCKEEPER_PLANFUZZ_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runs = n
		}
	}
	for _, shape := range []struct {
		name string
		gen  func(*rand.Rand) Input
	}{{"flat", pfGenFlat}, {"folder", pfGenFolder}, {"wide", pfGenWide}, {"casefold", pfGenCaseFold}} {
		t.Run(shape.name, func(t *testing.T) {
			failures := 0
			for seed := 0; seed < runs && failures < 3; seed++ {
				in := shape.gen(rand.New(rand.NewSource(int64(seed))))
				in.Machine, in.Now = "test_box", testNow
				if why := pfCheck(in); why != "" {
					failures++
					plan, _ := Plan(in)
					t.Errorf("%s seed %d: %s\n%s", shape.name, seed, why, pfDump(in, plan))
				}
			}
		})
	}
}

// pfCheck returns why the input's plan is wrong, or "".
func pfCheck(in Input) string {
	plan, _ := Plan(in)
	if err := ValidateTransferStage(plan); err != nil {
		return err.Error()
	}
	if err := ValidateLocalOverwrites(in, plan); err != nil {
		return err.Error()
	}
	if why := pfMints(in, plan); why != "" {
		return why
	}
	kept := map[string]bool{}
	for _, b := range in.Base {
		if b.MD5 == b.DriveMD5 {
			kept[b.MD5] = true
		}
	}
	for _, r := range in.Remote {
		kept[r.MD5] = true
	}
	check := func(what string, fail, commitFail map[int]bool, crashAfter int) string {
		disk, saved, bad := pfRun(in, plan, fail, commitFail, crashAfter)
		for _, f := range disk {
			saved[f.md5] = true
		}
		for _, p := range slices.Sorted(maps.Keys(in.Local)) {
			if l := in.Local[p]; !l.IsDir && !kept[l.MD5] && !saved[l.MD5] {
				return fmt.Sprintf("%s (fail %v, commit lost %v, crash after %d): %s's bytes %s lost", what, fail, commitFail, crashAfter, p, l.MD5)
			}
		}
		if what == "clean" && bad != "" {
			return bad
		}
		return ""
	}
	if why := check("clean", nil, nil, -1); why != "" {
		return why
	}
	for i := range plan {
		for _, why := range []string{
			check("one failure", map[int]bool{i: true}, nil, -1),
			check("crash", nil, nil, i),
		} {
			if why != "" {
				return why
			}
		}
		if plan[i].Type == MoveLocal {
			if why := check("commit lost", nil, map[int]bool{i: true}, -1); why != "" {
				return why
			}
		}
		for j := i + 1; j < len(plan); j++ {
			if why := check("two failures", map[int]bool{i: true, j: true}, nil, -1); why != "" {
				return why
			}
			if plan[i].Type == MoveLocal || plan[j].Type == MoveLocal {
				if why := check("two commits lost", nil, map[int]bool{i: true, j: true}, -1); why != "" {
					return why
				}
			}
		}
	}
	return ""
}

type pfFile struct {
	md5         string
	size, mtime int64
	origin      string // the scan path it came from; "" for a download
}

// pfCanon names a path by the baseline names of its folders, so a file is
// recognised at a path whatever folder renames lie between (D/E/f, N/F/f).
// The generators never reuse a folder name, so a name map suffices.
func pfCanon(in Input) func(string) string {
	alias := map[string]string{}
	remByID := map[string]string{}
	for p, r := range in.Remote {
		remByID[r.FileID] = p
	}
	fold := func(q string) string {
		if in.CaseFold {
			return strings.ToLower(q) // the generators' names are ASCII
		}
		return q
	}
	for q, b := range in.Base {
		if rp, ok := remByID[b.FileID]; ok && b.IsDir {
			alias[fold(path.Base(rp))] = path.Base(q)
		}
	}
	return func(q string) string {
		segs := strings.Split(q, "/")
		for i := range len(segs) - 1 {
			if a, ok := alias[fold(segs[i])]; ok {
				segs[i] = a
			}
		}
		return fold(strings.Join(segs, "/"))
	}
}

// pfRun models the executor on the scan's files: the §7 gate (pinned stat or
// absent), ProtectedBy refusals keyed like failedProtectors, and failures —
// fail: the action fails before any effect; commitFail: a move fails after its
// rename; crashAfter: nothing after that index runs (-1: no crash). It
// returns what is on disk, the bytes uploaded or binned, and a Record that
// would state an md5 the file at its path does not hold.
func pfRun(in Input, plan []Action, fail, commitFail map[int]bool, crashAfter int) (map[string]pfFile, map[string]bool, string) {
	// A folding disk holds one entry per name under the fold.
	key := func(p string) string {
		if in.CaseFold {
			return strings.ToLower(p)
		}
		return p
	}
	disk := map[string]pfFile{}
	// Folders on disk (S038 round 4): a folder move whose source is not
	// there fails, unless the destination is already a folder (the executor
	// replays it as a commit, R16).
	dirs := map[string]bool{}
	addDirs := func(p string) {
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			dirs[key(d)] = true
		}
	}
	for p, l := range in.Local {
		if !l.IsDir {
			disk[key(p)] = pfFile{l.MD5, l.Size, l.MtimeNS, p}
		} else {
			dirs[key(p)] = true
		}
		addDirs(p)
	}
	canon := pfCanon(in)
	basePath, drivePath := map[string]string{}, map[string]string{}
	for p, b := range in.Base {
		basePath[b.FileID] = p
	}
	for p, r := range in.Remote {
		drivePath[r.FileID] = p
	}
	saved := map[string]bool{}
	failed := map[string]bool{}
	bad := ""
	dl := int64(900000)
	for i, a := range plan {
		if crashAfter >= 0 && i > crashAfter {
			break
		}
		noteFail := func() {
			switch a.Type {
			case ConflictBackup, MoveLocal, MoveRemote, Forget:
				failed[a.RelPath] = true
			}
		}
		if (a.ProtectedBy != "" && failed[a.ProtectedBy]) || fail[i] {
			noteFail()
			continue
		}
		gate := func(p string, pinned bool, size, mtime int64) bool {
			f, ok := disk[key(p)]
			return !ok || pinned && f.size == size && f.mtime == mtime
		}
		switch a.Type {
		case MoveLocal:
			if a.IsDir {
				if !dirs[key(a.RelPath)] {
					if !dirs[key(a.NewRelPath)] {
						noteFail()
					}
					continue
				}
				for _, k := range slices.Sorted(maps.Keys(dirs)) {
					if k == key(a.RelPath) || strings.HasPrefix(k, key(a.RelPath)+"/") {
						delete(dirs, k)
						dirs[key(a.NewRelPath)+k[len(key(a.RelPath)):]] = true
					}
				}
				addDirs(a.NewRelPath)
				for _, k := range slices.Sorted(maps.Keys(disk)) {
					if rest, ok := strings.CutPrefix(k, key(a.RelPath)+"/"); ok {
						f := disk[k]
						delete(disk, k)
						disk[key(a.NewRelPath)+"/"+rest] = f
					}
				}
			} else {
				f, ok := disk[key(a.RelPath)]
				if !ok || a.RelPath != a.NewRelPath && !gate(a.NewRelPath, a.LocalExists, a.LocalSize, a.LocalMtimeNS) {
					noteFail()
					continue
				}
				delete(disk, key(a.RelPath))
				disk[key(a.NewRelPath)] = f
				addDirs(a.NewRelPath)
			}
			if commitFail[i] {
				noteFail()
			}
		case MkdirLocal, MkdirRemote:
			dirs[key(a.RelPath)] = true
			addDirs(a.RelPath)
		case ConflictBackup:
			f, ok := disk[key(a.RelPath)]
			if !ok || !gate(a.NewRelPath, false, 0, 0) {
				noteFail()
				continue
			}
			delete(disk, key(a.RelPath))
			disk[key(a.NewRelPath)] = f
		case QuarantineLocal:
			if f, ok := disk[key(a.RelPath)]; ok {
				if a.LocalExists && (f.size != a.LocalSize || f.mtime != a.LocalMtimeNS) {
					continue
				}
				saved[f.md5] = true // in the bin
				delete(disk, key(a.RelPath))
			}
		case Download:
			if !gate(a.RelPath, a.LocalExists, a.LocalSize, a.LocalMtimeNS) {
				continue
			}
			dl++
			disk[key(a.RelPath)] = pfFile{a.MD5, a.Size, dl, ""}
			addDirs(a.RelPath)
		case Upload, UpdateRemote:
			f, ok := disk[key(a.RelPath)]
			if !ok {
				continue
			}
			saved[f.md5] = true
			// A new revision of a Drive item is the file of its row — found
			// at the row's path, or where Drive moved the item (S023: a file
			// at Drive's new name is the row's) — never another file moved
			// there by a wrong move.
			if a.Type == UpdateRemote && f.origin != "" && bad == "" {
				if o := canon(f.origin); o != canon(basePath[a.FileID]) && o != canon(drivePath[a.FileID]) {
					bad = fmt.Sprintf("update_remote %s (id %s) uploads the file from %s", a.RelPath, a.FileID, f.origin)
				}
			}
		case Record:
			if f, ok := disk[key(a.RelPath)]; !a.IsDir && ok && f.size == a.LocalSize && f.mtime == a.LocalMtimeNS && f.md5 != a.MD5 && bad == "" {
				bad = fmt.Sprintf("record %s states md5 %s, the file there holds %s", a.RelPath, a.MD5, f.md5)
			}
		}
	}
	return disk, saved, bad
}

func pfDump(in Input, plan []Action) string {
	var sb strings.Builder
	sb.WriteString("Base:")
	for _, p := range slices.Sorted(maps.Keys(in.Base)) {
		fmt.Fprintf(&sb, " %s(%s,%s)", p, in.Base[p].FileID, in.Base[p].MD5)
	}
	sb.WriteString("\nLocal:")
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		l := in.Local[p]
		fmt.Fprintf(&sb, " %s(%s,%d,%d)", p, l.MD5, l.Size, l.MtimeNS)
	}
	sb.WriteString("\nRemote:")
	for _, p := range slices.Sorted(maps.Keys(in.Remote)) {
		fmt.Fprintf(&sb, " %s(%s,%s)", p, in.Remote[p].FileID, in.Remote[p].MD5)
	}
	sb.WriteString("\nPlan:\n")
	for i, a := range plan {
		fmt.Fprintf(&sb, "  %d %s %s", i, a.Type, a.RelPath)
		if a.NewRelPath != "" {
			fmt.Fprintf(&sb, " -> %s", a.NewRelPath)
		}
		if a.FileID != "" {
			fmt.Fprintf(&sb, " id=%s", a.FileID)
		}
		if a.ProtectedBy != "" {
			fmt.Fprintf(&sb, " pb=%s", a.ProtectedBy)
		}
		if a.LocalExists {
			fmt.Fprintf(&sb, " pin=%d/%d", a.LocalSize, a.LocalMtimeNS)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// pfGenFlat: two to four synced files among five names. Drive keeps, deletes,
// renames (possibly onto another's old name) or edits each, and adds new
// items; here each is kept, edited or deleted, some renamed, and new files
// appear — with new bytes, Drive's, or a baseline's.
func pfGenFlat(r *rand.Rand) Input {
	names := []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"}
	in := Input{Base: map[string]BaseItem{}, Local: map[string]LocalItem{}, Remote: map[string]RemoteItem{}}
	perm := r.Perm(len(names))
	for i, n := 0, 2+r.Intn(3); i < n; i++ {
		id, md5 := "I"+strconv.Itoa(i), "B"+strconv.Itoa(i)
		in.Base[names[perm[i]]] = BaseItem{FileID: id, Size: int64(10 + i), MD5: md5, MtimeNS: 1000, DriveMD5: md5, DriveVersion: 1}
	}
	pfDriveAndLocal(r, &in, names, func(s, _ string) string { return s }, "", "")
	return in
}

// pfGenFolder: the same over folder D's files and two top-level names; Drive
// may rename D to N, and this disk may have done it too.
func pfGenFolder(r *rand.Rand) Input {
	slots := []string{"D/a.txt", "D/b.txt", "D/c.txt", "g.txt", "h.txt"}
	in := Input{Base: map[string]BaseItem{"D": {FileID: "FD", IsDir: true, DriveVersion: 1}},
		Local: map[string]LocalItem{}, Remote: map[string]RemoteItem{}}
	perm := r.Perm(len(slots))
	for i, n := 0, 2+r.Intn(3); i < n; i++ {
		id, md5 := "I"+strconv.Itoa(i), "B"+strconv.Itoa(i)
		in.Base[slots[perm[i]]] = BaseItem{FileID: id, Size: int64(10 + i), MD5: md5, MtimeNS: 1000, DriveMD5: md5, DriveVersion: 1}
	}
	driveDir, localDir := "D", "D"
	if r.Intn(2) == 0 {
		driveDir = "N"
		if r.Intn(2) == 0 {
			localDir = "N"
		}
	}
	in.Remote[driveDir] = RemoteItem{FileID: "FD", IsDir: true, Version: 2}
	in.Local[localDir] = LocalItem{IsDir: true}
	in2 := func(s, dir string) string {
		if rest, ok := strings.CutPrefix(s, "D/"); ok {
			return dir + "/" + rest
		}
		return s
	}
	pfDriveAndLocal(r, &in, slots, in2, driveDir, localDir)
	// S035: Drive renamed D to N while a folder N also sits here, untracked —
	// made here, or the files came down into it while D was renamed otherwise
	// — with D still here: the merge (specs §4.3). Sometimes N holds one of
	// D's files already, at Drive's name, or a new file.
	if driveDir == "N" && localDir == "D" && r.Intn(3) == 0 {
		in.Local["N"] = LocalItem{IsDir: true}
		for _, q := range slices.Sorted(maps.Keys(in.Remote)) {
			if rest, ok := strings.CutPrefix(q, "N/"); ok && r.Intn(2) == 0 {
				if _, here := in.Local["N/"+rest]; !here {
					rm := in.Remote[q]
					in.Local["N/"+rest] = LocalItem{MD5: rm.MD5, Size: rm.Size, MtimeNS: 5000}
					break
				}
			}
		}
		if r.Intn(2) == 0 {
			in.Local["N/new.txt"] = LocalItem{MD5: "NN", Size: 77, MtimeNS: 5001}
		}
	}
	// S020: the folder deleted here — while Drive renamed it, or not; with
	// the merge's N above, an unrelated folder at Drive's new name.
	if r.Intn(4) == 0 {
		pfDropLocal(&in, localDir)
	}
	return in
}

// pfMints (S038): on a folding disk, the plan run in full leaves Drive with no
// two names differing only by the fold — none it creates (mkdir, upload, move)
// beside one Drive keeps or one the same plan creates. Drive's side is
// modelled by id: a trash or move acts on the item wherever it is by then.
func pfMints(in Input, plan []Action) string {
	if !in.CaseFold {
		return ""
	}
	drive := map[string]string{} // path → id ("" for an item this plan creates)
	for p, r := range in.Remote {
		drive[p] = r.FileID
	}
	at := func(id string) (string, bool) {
		for q, i := range drive {
			if i == id && id != "" {
				return q, true
			}
		}
		return "", false
	}
	move := func(from, to string) {
		for _, q := range slices.Sorted(maps.Keys(drive)) {
			if q == from || strings.HasPrefix(q, from+"/") {
				id := drive[q]
				delete(drive, q)
				if to != "" {
					drive[to+q[len(from):]] = id
				}
			}
		}
	}
	for _, a := range plan {
		switch a.Type {
		case MkdirRemote, Upload:
			drive[a.RelPath] = ""
		case TrashRemote:
			if q, ok := at(a.FileID); ok {
				move(q, "")
			}
		case MoveRemote:
			if q, ok := at(a.FileID); ok {
				move(q, a.NewRelPath)
			}
		}
	}
	seen := map[string]string{}
	for _, q := range slices.Sorted(maps.Keys(drive)) {
		if o, dup := seen[strings.ToLower(q)]; dup {
			return fmt.Sprintf("Drive left holding %s and %s, one name on a folding disk", o, q)
		}
		seen[strings.ToLower(q)] = q
	}
	return ""
}

// pfGenCaseFold (S038): the folder shape on a folding disk, the folder's name
// respelled in case here (d, n) and/or on Drive (Drive renamed D to d, or N to
// n), sometimes a file's too — the shapes a folding disk meets when another
// machine or the web UI spells a name differently.
func pfGenCaseFold(r *rand.Rand) Input {
	in := pfGenFolder(r)
	in.CaseFold = true
	respell := func(m map[string]LocalItem, rm map[string]RemoteItem, from, to string) {
		for _, q := range slices.Sorted(maps.Keys(m)) {
			if q == from || strings.HasPrefix(q, from+"/") {
				m[to+q[len(from):]] = m[q]
				delete(m, q)
			}
		}
		for _, q := range slices.Sorted(maps.Keys(rm)) {
			if q == from || strings.HasPrefix(q, from+"/") {
				rm[to+q[len(from):]] = rm[q]
				delete(rm, q)
			}
		}
	}
	for _, d := range []string{"D", "N"} {
		if _, ok := in.Local[d]; ok && r.Intn(2) == 0 {
			respell(in.Local, nil, d, strings.ToLower(d))
		}
		if _, ok := in.Remote[d]; ok && r.Intn(3) == 0 {
			respell(nil, in.Remote, d, strings.ToLower(d))
		}
	}
	// A file renamed in case here.
	for _, q := range slices.Sorted(maps.Keys(in.Local)) {
		if l := in.Local[q]; !l.IsDir && r.Intn(8) == 0 {
			if up := path.Join(path.Dir(q), strings.ToUpper(path.Base(q))); up != q {
				in.Local[up] = l
				delete(in.Local, q)
			}
		}
	}
	return in
}

// pfDropLocal deletes dir and everything under it from the local scan.
func pfDropLocal(in *Input, dir string) {
	for _, q := range slices.Sorted(maps.Keys(in.Local)) {
		if q == dir || strings.HasPrefix(q, dir+"/") {
			delete(in.Local, q)
		}
	}
}

// pfDriveAndLocal fills Remote and Local from Base. at(slot, dir) places a
// slot under the folder's name on that side.
func pfDriveAndLocal(r *rand.Rand, in *Input, slots []string, at func(string, string) string, driveDir, localDir string) {
	files := func(m map[string]BaseItem) []string {
		var out []string
		for _, p := range slices.Sorted(maps.Keys(m)) {
			if !m[p].IsDir {
				out = append(out, p)
			}
		}
		return out
	}
	used := map[string]bool{}
	order := files(in.Base)
	r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	for _, p := range order {
		b := in.Base[p]
		if r.Intn(100) < 12 {
			continue // deleted on Drive
		}
		dst := p
		if r.Intn(100) < 55 {
			dst = slots[r.Intn(len(slots))]
		}
		dst = at(dst, driveDir)
		if used[dst] {
			if dst = at(p, driveDir); used[dst] {
				continue
			}
		}
		used[dst] = true
		md5, size := b.MD5, b.Size
		if r.Intn(100) < 25 {
			md5, size = "D"+b.FileID, b.Size+100
		}
		in.Remote[dst] = RemoteItem{FileID: b.FileID, MD5: md5, Size: size, Version: 2}
	}
	for k, s := range slots {
		if s = at(s, driveDir); !used[s] && r.Intn(100) < 13 {
			in.Remote[s] = RemoteItem{FileID: "R" + strconv.Itoa(k), MD5: "RN" + strconv.Itoa(k), Size: int64(200 + k), Version: 1}
			used[s] = true
		}
	}
	lused := map[string]bool{}
	mt := int64(2000)
	for _, p := range files(in.Base) {
		b := in.Base[p]
		lp := at(p, localDir)
		switch x := r.Intn(100); {
		case x < 11:
			continue // deleted here
		case x < 40:
			mt++
			in.Local[lp] = LocalItem{MD5: "L" + b.FileID, Size: b.Size + 50, MtimeNS: mt}
		default:
			in.Local[lp] = LocalItem{MD5: b.MD5, Size: b.Size, MtimeNS: 1000}
		}
		lused[lp] = true
	}
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		if in.Local[p].IsDir || r.Intn(100) >= 10 {
			continue
		}
		if n := at(slots[r.Intn(len(slots))], localDir); !lused[n] {
			in.Local[n] = in.Local[p]
			delete(in.Local, p)
			lused[n], lused[p] = true, false
		}
	}
	for k, s := range slots {
		if s = at(s, localDir); lused[s] || r.Intn(100) > 25 {
			continue
		}
		mt++
		var md5 string
		var size int64
		switch r.Intn(3) {
		case 0:
			md5, size = "N"+strconv.Itoa(k), int64(300+k)
		case 1:
			var rs []RemoteItem
			for _, q := range slices.Sorted(maps.Keys(in.Remote)) {
				if !in.Remote[q].IsDir {
					rs = append(rs, in.Remote[q])
				}
			}
			if len(rs) == 0 {
				continue
			}
			x := rs[r.Intn(len(rs))]
			md5, size = x.MD5, x.Size
		default:
			bs := files(in.Base)
			x := in.Base[bs[r.Intn(len(bs))]]
			md5, size = x.MD5, x.Size
		}
		in.Local[s] = LocalItem{MD5: md5, Size: size, MtimeNS: mt}
		lused[s] = true
	}
}

// pfGenWide (S036, from the independent check's harness): folder D with
// subfolder E. Drive may rename D to N and E to F, and this disk may have
// made each rename too, or not; some rows are held (their Drive item shadowed
// by a duplicate, or their file unreadable here); a local rename may replace
// a synced file; up to five synced files among six slots, edits on both sides.
// It found S036.1 (the inner rename done here, the outer not: 1,014 of 60,000
// inputs refused).
func pfGenWide(r *rand.Rand) Input {
	in := Input{Base: map[string]BaseItem{"D": {FileID: "FD", IsDir: true, DriveVersion: 1},
		"D/E": {FileID: "FE", IsDir: true, DriveVersion: 1}},
		Local: map[string]LocalItem{}, Remote: map[string]RemoteItem{},
		ShadowedRemote: map[string]bool{}, UnreadableLocal: map[string]bool{}}
	slots := []string{"D/E/a.txt", "D/E/b.txt", "D/c.txt", "D/d.txt", "g.txt", "h.txt"}
	perm := r.Perm(len(slots))
	for i, n := 0, 2+r.Intn(4); i < n; i++ {
		id, md5 := "I"+strconv.Itoa(i), "B"+strconv.Itoa(i)
		in.Base[slots[perm[i]]] = BaseItem{FileID: id, Size: int64(10 + i), MD5: md5, MtimeNS: 1000, DriveMD5: md5, DriveVersion: 1}
	}
	dD, dE := "D", "E"
	if r.Intn(2) == 0 {
		dD = "N"
	}
	if r.Intn(2) == 0 {
		dE = "F"
	}
	lD, lE := "D", "E"
	if dD == "N" && r.Intn(2) == 0 {
		lD = "N"
	}
	if dE == "F" && r.Intn(2) == 0 {
		lE = "F"
	}
	in.Remote[dD] = RemoteItem{FileID: "FD", IsDir: true, Version: 2}
	in.Remote[dD+"/"+dE] = RemoteItem{FileID: "FE", IsDir: true, Version: 2}
	in.Local[lD] = LocalItem{IsDir: true}
	in.Local[lD+"/"+lE] = LocalItem{IsDir: true}
	at := func(s, d, e string) string {
		if rest, ok := strings.CutPrefix(s, "D/E/"); ok {
			return d + "/" + e + "/" + rest
		}
		if rest, ok := strings.CutPrefix(s, "D/"); ok {
			return d + "/" + rest
		}
		return s
	}
	files := func() []string {
		var out []string
		for _, p := range slices.Sorted(maps.Keys(in.Base)) {
			if !in.Base[p].IsDir {
				out = append(out, p)
			}
		}
		return out
	}
	used := map[string]bool{}
	for _, p := range files() {
		b := in.Base[p]
		x := r.Intn(100)
		if x < 10 {
			continue // deleted on Drive
		}
		if x < 15 {
			in.ShadowedRemote[b.FileID] = true // a duplicate hides it
			continue
		}
		dst := p
		if r.Intn(100) < 55 {
			dst = slots[r.Intn(len(slots))]
		}
		dst = at(dst, dD, dE)
		if used[dst] {
			if dst = at(p, dD, dE); used[dst] {
				continue
			}
		}
		used[dst] = true
		md5, size := b.MD5, b.Size
		if r.Intn(100) < 25 {
			md5, size = "D"+b.FileID, b.Size+100
		}
		in.Remote[dst] = RemoteItem{FileID: b.FileID, MD5: md5, Size: size, Version: 2}
	}
	for k, s := range slots {
		if s = at(s, dD, dE); !used[s] && r.Intn(100) < 12 {
			in.Remote[s] = RemoteItem{FileID: "R" + strconv.Itoa(k), MD5: "RN" + strconv.Itoa(k), Size: int64(200 + k), Version: 1}
			used[s] = true
		}
	}
	lused := map[string]bool{}
	mt := int64(2000)
	for _, p := range files() {
		b := in.Base[p]
		lp := at(p, lD, lE)
		switch x := r.Intn(100); {
		case x < 10:
			continue // deleted here
		case x < 13:
			in.UnreadableLocal[b.FileID] = true
			continue
		case x < 40:
			mt++
			in.Local[lp] = LocalItem{MD5: "L" + b.FileID, Size: b.Size + 50, MtimeNS: mt}
		default:
			in.Local[lp] = LocalItem{MD5: b.MD5, Size: b.Size, MtimeNS: 1000}
		}
		lused[lp] = true
	}
	for _, p := range slices.Sorted(maps.Keys(in.Local)) {
		if in.Local[p].IsDir || r.Intn(100) >= 15 {
			continue
		}
		n := at(slots[r.Intn(len(slots))], lD, lE)
		if n == p {
			continue
		}
		if !lused[n] || r.Intn(3) == 0 { // may rename over a synced file
			in.Local[n] = in.Local[p]
			delete(in.Local, p)
			lused[n], lused[p] = true, false
		}
	}
	for k, s := range slots {
		if s = at(s, lD, lE); lused[s] || r.Intn(100) > 25 {
			continue
		}
		mt++
		var md5 string
		var size int64
		switch r.Intn(3) {
		case 0:
			md5, size = "N"+strconv.Itoa(k), int64(300+k)
		case 1:
			var rs []RemoteItem
			for _, q := range slices.Sorted(maps.Keys(in.Remote)) {
				if !in.Remote[q].IsDir {
					rs = append(rs, in.Remote[q])
				}
			}
			if len(rs) == 0 {
				continue
			}
			x := rs[r.Intn(len(rs))]
			md5, size = x.MD5, x.Size
		default:
			bs := files()
			x := in.Base[bs[r.Intn(len(bs))]]
			md5, size = x.MD5, x.Size
		}
		in.Local[s] = LocalItem{MD5: md5, Size: size, MtimeNS: mt}
		lused[s] = true
	}
	// S020: the outer or the inner folder deleted here, whichever renames
	// Drive made.
	switch r.Intn(6) {
	case 0:
		pfDropLocal(&in, lD)
	case 1:
		pfDropLocal(&in, lD+"/"+lE)
	}
	return in
}
