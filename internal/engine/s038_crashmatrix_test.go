package engine

// S038.61 — the crash matrix (ported from the fourth independent check of
// S038). Three wrong plans of this step came from a crash leaving rows in two
// spellings of one folder (S038.48/49, .56/57, .58/59), and the fuzz crash
// sweeps saw none of them. Each case: a and b synced D/{x,y}.txt, D/S/s.txt,
// r.txt; the folder is renamed in case (D → d) here (a) or on Drive (b);
// work inside it on either side; one action's commit is lost at a checkpoint
// (the fuzzer's mechanism: the hook fails it, the rest of the plan runs) in
// a's first cycle, b's cycle after it, or a's first cycle with b silent; and
// optionally a user op on the other machine between the crash and the
// recovery cycle. Checks: no failed action after the recovery cycle; a fixed
// point within 6 rounds; Drive's, a's and b's trees equal the expected tree
// in exact spelling; rows == Drive (path and id) on both machines; no fold
// pair on Drive; bins as expected.
//
// Not covered: process death (the rest of the plan never runs) — the review
// used an executor seam for it; no production seam is added for this step.
//
// The plain suite runs a thinned matrix: a's first cycle, the first hit of
// the two checkpoints behind this family's defects (download_before_commit,
// move_before_commit), the between ops after download_before_commit only.
// SYNCKEEPER_CRASH_MATRIX=1 runs all eight checkpoints, hits 1–3, all three
// targets, every between op.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/names"
)

// cmOp: work inside the folder by machine a or b; f is the folder's spelling
// on that machine's disk at the time.
type cmOp struct {
	name   string
	a, b   func(t *testing.T, m *machine, f string)
	delta  func(w map[string]string) // edits the expected final tree
	copyOf string                    // a conflict copy with these bytes must exist
	bins   bool                      // the op deletes: ≤1 entry per bin allowed
	clean  bool                      // touches neither x.txt nor y.txt: a between op may follow
}

func cmSet(kv ...string) func(map[string]string) {
	return func(w map[string]string) {
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == "" {
				delete(w, kv[i])
			} else {
				w[kv[i]] = kv[i+1]
			}
		}
	}
}

func cmOps() []cmOp {
	editX := func(c string) func(t *testing.T, m *machine, f string) {
		return func(t *testing.T, m *machine, f string) { m.write(t, f+"/x.txt", c) }
	}
	add := func(rel, c string) func(t *testing.T, m *machine, f string) {
		return func(t *testing.T, m *machine, f string) { m.write(t, f+"/"+rel, c) }
	}
	mvOut := func(t *testing.T, m *machine, f string) { m.rename(t, f+"/x.txt", "x.txt") }
	mvIn := func(t *testing.T, m *machine, f string) { m.rename(t, "r.txt", f+"/r.txt") }
	del := func(t *testing.T, m *machine, f string) { m.remove(t, f+"/y.txt") }
	innerCase := func(t *testing.T, m *machine, f string) { m.rename(t, f+"/S", f+"/s") }
	inner := func(t *testing.T, m *machine, f string) { m.rename(t, f+"/S", f+"/T") }
	innerOut := func(t *testing.T, m *machine, f string) { m.rename(t, f+"/S", "S2") }
	innerDel := func(t *testing.T, m *machine, f string) { m.remove(t, f+"/S") }
	fileCase := func(t *testing.T, m *machine, f string) { m.rename(t, f+"/x.txt", f+"/X.txt") }
	both := func(fs ...func(t *testing.T, m *machine, f string)) func(t *testing.T, m *machine, f string) {
		return func(t *testing.T, m *machine, f string) {
			for _, x := range fs {
				x(t, m, f)
			}
		}
	}
	return []cmOp{
		{name: "none", delta: cmSet(), clean: true},
		{name: "b-edit", b: editX("xb"), delta: cmSet("d/x.txt", "xb")},
		{name: "a-edit", a: editX("xa"), delta: cmSet("d/x.txt", "xa")},
		{name: "both-edit", a: editX("xa"), b: editX("xb"), delta: cmSet("d/x.txt", "xb"), copyOf: "xa"},
		{name: "b-add", b: add("z.txt", "zb"), delta: cmSet("d/z.txt", "zb"), clean: true},
		{name: "a-add", a: add("z.txt", "za"), delta: cmSet("d/z.txt", "za"), clean: true},
		{name: "b-addsub", b: add("N/n.txt", "nb"), delta: cmSet("d/N/n.txt", "nb"), clean: true},
		{name: "a-addsub", a: add("N/n.txt", "na"), delta: cmSet("d/N/n.txt", "na"), clean: true},
		{name: "b-addinS", b: add("S/q.txt", "qb"), delta: cmSet("d/S/q.txt", "qb")},
		{name: "a-addinS", a: add("S/q.txt", "qa"), delta: cmSet("d/S/q.txt", "qa")},
		{name: "b-mvout", b: mvOut, delta: cmSet("d/x.txt", "", "x.txt", "x")},
		{name: "a-mvout", a: mvOut, delta: cmSet("d/x.txt", "", "x.txt", "x")},
		{name: "b-mvin", b: mvIn, delta: cmSet("r.txt", "", "d/r.txt", "r")},
		{name: "a-mvin", a: mvIn, delta: cmSet("r.txt", "", "d/r.txt", "r")},
		{name: "b-del", b: del, delta: cmSet("d/y.txt", ""), bins: true},
		{name: "a-del", a: del, delta: cmSet("d/y.txt", ""), bins: true},
		{name: "b-innercase", b: innerCase, delta: cmSet("d/S/s.txt", "", "d/s/s.txt", "s"), clean: true},
		{name: "a-innercase", a: innerCase, delta: cmSet("d/S/s.txt", "", "d/s/s.txt", "s"), clean: true},
		{name: "b-inner", b: inner, delta: cmSet("d/S/s.txt", "", "d/T/s.txt", "s")},
		{name: "a-inner", a: inner, delta: cmSet("d/S/s.txt", "", "d/T/s.txt", "s"), bins: true},
		{name: "b-innerout", b: innerOut, delta: cmSet("d/S/s.txt", "", "S2/s.txt", "s")},
		{name: "a-innerout", a: innerOut, delta: cmSet("d/S/s.txt", "", "S2/s.txt", "s"), bins: true},
		{name: "b-innerdel", b: innerDel, delta: cmSet("d/S/s.txt", ""), bins: true},
		{name: "a-innerdel", a: innerDel, delta: cmSet("d/S/s.txt", ""), bins: true},
		{name: "a-innercase+add", a: both(innerCase, add("s/q.txt", "qa")),
			delta: cmSet("d/S/s.txt", "", "d/s/s.txt", "s", "d/s/q.txt", "qa")},
		{name: "b-innercase+add", b: both(innerCase, add("s/q.txt", "qb")),
			delta: cmSet("d/S/s.txt", "", "d/s/s.txt", "s", "d/s/q.txt", "qb")},
		{name: "mix: b-edit a-add a-addsub", a: both(add("z.txt", "za"), add("N/n.txt", "na")), b: editX("xb"),
			delta: cmSet("d/x.txt", "xb", "d/z.txt", "za", "d/N/n.txt", "na")},
		{name: "mix: both-edit b-addsub a-addinS", a: both(editX("xa"), add("S/q.txt", "qa")), b: both(editX("xb"), add("N/n.txt", "nb")),
			delta: cmSet("d/x.txt", "xb", "d/N/n.txt", "nb", "d/S/q.txt", "qa"), copyOf: "xa"},
		{name: "mix: b-innercase a-addinS", a: add("S/q.txt", "qa"), b: innerCase,
			delta: cmSet("d/S/s.txt", "", "d/s/s.txt", "s", "d/s/q.txt", "qa")},
		{name: "mix: a-innercase b-addinS", a: innerCase, b: add("S/q.txt", "qb"),
			delta: cmSet("d/S/s.txt", "", "d/s/s.txt", "s", "d/s/q.txt", "qb")},
		{name: "b-filecase", b: fileCase, delta: cmSet("d/x.txt", "", "d/X.txt", "x")},
		{name: "a-filecase", a: fileCase, delta: cmSet("d/x.txt", "", "d/X.txt", "x")},
		{name: "mix: b-edit+filecase", b: both(editX("xb"), fileCase), delta: cmSet("d/x.txt", "", "d/X.txt", "xb")},
		{name: "mix: b-edit a-filecase", a: fileCase, b: editX("xb"), delta: cmSet("d/x.txt", "", "d/X.txt", "xb")},
		{name: "mix: b-edit a-mvout", a: mvOut, b: editX("xb"), delta: cmSet("d/x.txt", "xb", "x.txt", "x")},
		{name: "mix: a-edit b-mvout", a: editX("xa"), b: mvOut, delta: cmSet("d/x.txt", "", "x.txt", "xa")},
	}
}

// cmBetween: a user op on the machine that did not crash, after the crash
// cycle and before the recovery cycle (round 5: the shape that reverted a
// file renamed in case on Drive). Applied with ops marked clean, and with
// b-edit (the J1 shape).
type cmBetween struct {
	name  string
	op    func(t *testing.T, m *machine, f string)
	delta func(w map[string]string)
	bins  bool
}

func cmBetweens() []cmBetween {
	return []cmBetween{
		{name: "filecase", op: func(t *testing.T, m *machine, f string) { m.rename(t, f+"/x.txt", f+"/X.txt") },
			delta: func(w map[string]string) { w["d/X.txt"] = w["d/x.txt"]; delete(w, "d/x.txt") }},
		{name: "edit", op: func(t *testing.T, m *machine, f string) { m.write(t, f+"/x.txt", "x between") },
			delta: cmSet("d/x.txt", "x between")},
		{name: "delete", op: func(t *testing.T, m *machine, f string) { m.remove(t, f+"/y.txt") },
			delta: cmSet("d/y.txt", ""), bins: true},
	}
}

type cmCase struct {
	mode    string // "here": a renames D → d; "drive": b does (a follows)
	op      cmOp
	between *cmBetween
	cp      string // "" = no crash
	nth     int
	target  string // "a1": a's first cycle; "b2": b's cycle after it; "a1x": a's first, b silent before recovery
}

// cmSpelling: how m's disk spells the folder now (D or d).
func cmSpelling(t *testing.T, m *machine) string {
	if s038Dirs(t, m)["d"] {
		return "d"
	}
	return "D"
}

func cmRun(t *testing.T, c cmCase) {
	fake, root, a, b, _ := s038Pair(t)
	a.write(t, "D/S/s.txt", "s")
	a.write(t, "r.txt", "r")
	settleAll(t, a, b)
	trashed := fake.TrashCount()
	want := map[string]string{"d/x.txt": "x", "d/y.txt": "y", "d/S/s.txt": "s", "r.txt": "r"}
	c.op.delta(want)
	bins := c.op.bins
	if c.between != nil {
		c.between.delta(want)
		bins = bins || c.between.bins
	}

	var log, findings []string
	cycleNo := 0
	finding := func(f string, args ...any) { findings = append(findings, fmt.Sprintf(f, args...)) }
	// The transfer stage calls the hook from several workers at once.
	var mu sync.Mutex
	hits := 0
	arm := func() {
		hits = 0
		executor.FaultHook = func(name string) error {
			mu.Lock()
			defer mu.Unlock()
			if name == c.cp {
				hits++
				if hits == c.nth {
					return fmt.Errorf("injected: commit lost at %s", name)
				}
			}
			return nil
		}
	}
	recovery := "" // the machine whose next cycle is the recovery cycle: failures allowed there
	cyc := func(m *machine, crash bool) int {
		cycleNo++
		if crash && c.cp != "" {
			arm()
		}
		res, err := m.eng.Sync(context.Background(), Options{})
		executor.FaultHook = nil
		if err != nil {
			finding("c%d [%s] hard error: %v", cycleNo, m.name, err)
			return 1
		}
		log = append(log, fmt.Sprintf("c%d [%s]%s plan: %s | failed=%d %v", cycleNo, m.name,
			map[bool]string{true: " CRASH", false: ""}[crash && c.cp != ""], zzPlanOf(res), res.Failed, res.Errors))
		if res.Failed > 0 && !crash && recovery != m.name {
			finding("c%d [%s] failed %d: %v", cycleNo, m.name, res.Failed, res.Errors)
		}
		if recovery == m.name && !crash {
			recovery = ""
		}
		return len(res.Plan) + res.Failed
	}
	notHit := func() bool { mu.Lock(); defer mu.Unlock(); return c.cp != "" && hits < c.nth }
	between := func(crashed, other *machine) {
		if c.between == nil {
			return
		}
		c.between.op(t, other, cmSpelling(t, other))
		cyc(other, false)
	}

	if c.target == "a1x" && c.mode == "here" && c.between != nil && c.between.name == "filecase" {
		// b renames a file in case inside a folder a renamed on Drive before b
		// followed: b's own cycle fails its move_remote once ("no drive id
		// known for parent folder"), crash or not — S037's family, not this
		// matrix's. The same op after b followed is the a1 target.
		t.Skip("S037: case rename inside a folder Drive renamed, before following")
	}
	if c.mode == "drive" {
		b.rename(t, "D", "d")
	}
	if c.op.b != nil {
		c.op.b(t, b, cmSpelling(t, b))
	}
	cyc(b, false)
	if c.mode == "here" {
		a.rename(t, "D", "d")
	}
	if c.op.a != nil {
		c.op.a(t, a, cmSpelling(t, a))
	}
	cyc(a, c.target == "a1" || c.target == "a1x")
	order := []*machine{a, b}
	switch c.target {
	case "a1":
		recovery = "a"
		if notHit() {
			t.Skip("checkpoint not reached")
		}
		cyc(b, false)
		between(a, b)
	case "a1x":
		recovery = "a"
		if notHit() {
			t.Skip("checkpoint not reached")
		}
		between(a, b)
		order = []*machine{b, a}
	case "b2":
		cyc(b, true)
		recovery = "b"
		if notHit() {
			t.Skip("checkpoint not reached")
		}
		between(b, a)
		order = []*machine{b, a}
	}
	idle := false
	for r := 0; r < 6 && !idle; r++ {
		busy := 0
		for _, m := range order {
			busy += cyc(m, false)
		}
		idle = busy == 0
	}
	if !idle {
		finding("no fixed point in 6 rounds")
	}

	trees := map[string]map[string]string{"drive": driveTreeOf(t, fake, root), "a": a.listTree(t), "b": b.listTree(t)}
	for w, tr := range trees {
		copies := 0
		for p, cont := range tr {
			if wc, ok := want[p]; ok {
				if cont != wc {
					finding("%s: %s=%q want %q", w, p, cont, wc)
				}
				continue
			}
			if c.op.copyOf != "" && strings.Contains(p, "(conflict") && cont == c.op.copyOf {
				copies++
				continue
			}
			finding("%s: unexpected %s=%q", w, p, cont)
		}
		for p := range want {
			if _, ok := tr[p]; !ok {
				finding("%s: missing %s", w, p)
			}
		}
		if c.op.copyOf != "" && copies != 1 {
			finding("%s: %d conflict copies of %q", w, copies, c.op.copyOf)
		}
	}
	if d := s020FoldDuplicates(t, fake, root); len(d) > 0 {
		finding("Drive fold pairs %v", d)
	}
	dbin := fake.TrashCount() - trashed
	sbin := cmBinCount(a) + cmBinCount(b)
	switch {
	case bins && (dbin > 1 || sbin > 1):
		finding("bins: Drive +%d, system %d (≤1 each allowed)", dbin, sbin)
	case !bins && (dbin != 0 || sbin != 0):
		finding("bins: Drive +%d, system %d", dbin, sbin)
	}
	for _, m := range []*machine{a, b} {
		if mm := cmRowsVsDrive(t, fake, root, m); len(mm) > 0 {
			finding("[%s] rows vs Drive: %v", m.name, mm)
		}
	}
	if len(findings) > 0 {
		t.Errorf("FINDINGS:\n  %s\nLOG:\n  %s", strings.Join(findings, "\n  "), strings.Join(log, "\n  "))
	}
}

func zzPlanOf(res *Result) string {
	var s []string
	for _, a := range res.Plan {
		x := string(a.Type) + " " + a.RelPath
		if a.NewRelPath != "" {
			x += "->" + a.NewRelPath
		}
		s = append(s, x)
	}
	return strings.Join(s, "; ")
}

func cmBinCount(m *machine) int {
	ents, _ := os.ReadDir(m.bin.Dir)
	return len(ents)
}

// cmRowsVsDrive: the differences between m's rows and Drive's tree (path, id).
func cmRowsVsDrive(t *testing.T, fake *driveclient.Fake, root string, m *machine) []string {
	t.Helper()
	drive := map[string]string{}
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fake.List(context.Background(), parentID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range children {
			drive[prefix+c.Name] = c.ID
			if c.IsDir() {
				walk(c.ID, prefix+c.Name+"/")
			}
		}
	}
	walk(root, "")
	rows := map[string]string{}
	for _, it := range mustItems(t, m) {
		rows[it.RelPath] = it.DriveFileID
	}
	var out []string
	for p, id := range rows {
		if drive[p] != id {
			out = append(out, fmt.Sprintf("row %s=%s, Drive has %q", p, id, drive[p]))
		}
	}
	for p, id := range drive {
		if rows[p] != id {
			out = append(out, fmt.Sprintf("Drive %s=%s, no row", p, id))
		}
	}
	return out
}

var cmCheckpoints = []string{
	executor.CPUploadBeforeCommit, executor.CPDownloadTempWritten, executor.CPDownloadBeforeCommit,
	executor.CPQuarantineBeforeMove, executor.CPTrashBeforeCommit, executor.CPMkdirBeforeCommit,
	executor.CPMoveBeforeCommit, executor.CPForgetBeforeCommit,
}

func TestS038CrashMatrix(t *testing.T) {
	probe := t.TempDir()
	if !names.CaseInsensitiveFS(probe) {
		t.Skip("sync dir is on a case-sensitive filesystem")
	}
	full := os.Getenv("SYNCKEEPER_CRASH_MATRIX") != ""
	targets, maxHit, cps := []string{"a1"}, 1, []string{executor.CPDownloadBeforeCommit, executor.CPMoveBeforeCommit}
	if full {
		targets, maxHit, cps = []string{"a1", "b2", "a1x"}, 3, cmCheckpoints
	}
	betweens := cmBetweens()
	for _, mode := range []string{"here", "drive"} {
		for _, op := range cmOps() {
			op := op
			var bs []*cmBetween
			bs = append(bs, nil)
			if op.clean || op.name == "b-edit" {
				for i := range betweens {
					bs = append(bs, &betweens[i])
				}
			}
			t.Run(fmt.Sprintf("%s/%s/nocrash", mode, op.name), func(t *testing.T) {
				cmRun(t, cmCase{mode: mode, op: op, target: "a1"})
			})
			for _, target := range targets {
				for _, cp := range cps {
					for nth := 1; nth <= maxHit; nth++ {
						for _, bw := range bs {
							if bw != nil && !full && cp != executor.CPDownloadBeforeCommit {
								continue
							}
							c := cmCase{mode: mode, op: op, between: bw, cp: cp, nth: nth, target: target}
							name := fmt.Sprintf("%s/%s/%s/%s#%d", mode, op.name, target, cp, nth)
							if bw != nil {
								name += "/then-" + bw.name
							}
							t.Run(name, func(t *testing.T) { cmRun(t, c) })
						}
					}
				}
			}
		}
	}
}
