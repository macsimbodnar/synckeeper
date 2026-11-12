package reconcile

// S031 round 2: a seeded random sweep of Plan's inputs — small trees where
// files are renamed, edited, deleted and created on both sides, flat and
// under a folder Drive renames (on disk too, or not yet). Every plan must
// pass both run-time nets (ValidateTransferStage, ValidateLocalOverwrites —
// a refused plan is refused every cycle), and a model of the executor runs it
// with no failure, each single and each pair of failed actions, each crash
// point, and each move's commit lost after its rename: no local file's bytes
// may end up nowhere — not on Drive, not on disk, not uploaded, not in the
// bin. Adapted from the independent check's harness; it found the round-2
// regression (a pinned download onto a released file) and the pre-existing
// download-onto-an-occupant overlap.
//
// Default budget keeps the package fast; SYNCKEEPER_PLANFUZZ_RUNS=100000 is
// the deep run (per shape: flat and folder).

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
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
	}{{"flat", pfGenFlat}, {"folder", pfGenFolder}} {
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
}

// pfRun models the executor on the scan's files: the §7 gate (pinned stat or
// absent), ProtectedBy refusals keyed like failedProtectors, and failures —
// fail: the action fails before any effect; commitFail: a move fails after its
// rename; crashAfter: nothing after that index runs (-1: no crash). It
// returns what is on disk, the bytes uploaded or binned, and a Record that
// would state an md5 the file at its path does not hold.
func pfRun(in Input, plan []Action, fail, commitFail map[int]bool, crashAfter int) (map[string]pfFile, map[string]bool, string) {
	disk := map[string]pfFile{}
	for p, l := range in.Local {
		if !l.IsDir {
			disk[p] = pfFile{l.MD5, l.Size, l.MtimeNS}
		}
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
			f, ok := disk[p]
			return !ok || pinned && f.size == size && f.mtime == mtime
		}
		switch a.Type {
		case MoveLocal:
			if a.IsDir {
				for _, k := range slices.Sorted(maps.Keys(disk)) {
					if rest, ok := strings.CutPrefix(k, a.RelPath+"/"); ok {
						disk[a.NewRelPath+"/"+rest] = disk[k]
						delete(disk, k)
					}
				}
			} else {
				f, ok := disk[a.RelPath]
				if !ok || a.RelPath != a.NewRelPath && !gate(a.NewRelPath, a.LocalExists, a.LocalSize, a.LocalMtimeNS) {
					noteFail()
					continue
				}
				delete(disk, a.RelPath)
				disk[a.NewRelPath] = f
			}
			if commitFail[i] {
				noteFail()
			}
		case ConflictBackup:
			f, ok := disk[a.RelPath]
			if !ok || !gate(a.NewRelPath, false, 0, 0) {
				noteFail()
				continue
			}
			delete(disk, a.RelPath)
			disk[a.NewRelPath] = f
		case QuarantineLocal:
			if f, ok := disk[a.RelPath]; ok {
				if a.LocalExists && (f.size != a.LocalSize || f.mtime != a.LocalMtimeNS) {
					continue
				}
				saved[f.md5] = true // in the bin
				delete(disk, a.RelPath)
			}
		case Download:
			if !gate(a.RelPath, a.LocalExists, a.LocalSize, a.LocalMtimeNS) {
				continue
			}
			dl++
			disk[a.RelPath] = pfFile{a.MD5, a.Size, dl}
		case Upload, UpdateRemote:
			if f, ok := disk[a.RelPath]; ok {
				saved[f.md5] = true
			}
		case Record:
			if f, ok := disk[a.RelPath]; !a.IsDir && ok && f.size == a.LocalSize && f.mtime == a.LocalMtimeNS && f.md5 != a.MD5 && bad == "" {
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
	return in
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
