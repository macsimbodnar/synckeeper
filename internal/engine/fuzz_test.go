package engine

// W4 / FZ1 — a seeded, deterministic multi-machine fuzzer.
//
// N simulated machines share one fake Drive (the same harness as the S-series
// scenario tests). Each step applies a random local op on a random machine and
// then interleaves syncs; crashes are injected at executor checkpoints during
// the op phase. After the op phase every machine is driven to a quiescent
// fixed point and the oracle is checked.
//
// Oracle (all exact and non-flaky — see decisions.md 2026-07-23 "W4"):
//   - §4.5 structural invariant: every plan the engine returns passes
//     reconcile.ValidateTransferStage (the engine also enforces this inside
//     executor.Apply, so a violation surfaces as a sync error too), and
//     reconcile.ValidateLocalOverwrites against the input it was built from
//     (rule O1, S031: no local file whose bytes are not kept is replaced; the
//     engine enforces it before Apply).
//   - Eventual convergence + idempotence: within a bounded number of no-op
//     rounds all machines reach a zero-action, zero-failure fixed point.
//     Silent loss or corruption shows up here as machines that never agree or
//     a loop that never settles.
//   - No silent divergence: at the fixed point every machine's file tree is
//     byte-identical to every other's AND to the reconstructed Drive tree
//     (content is durable and consistent on both sides), and the directory
//     sets match.
//   - Identity stability (scoped): a clean single-writer rename of an
//     unmodified, in-sync file is a MoveRemote that preserves the Drive id and
//     plans zero delete-class actions (the quiet-rename probe).
//
// Op menu covers the classes that have shipped bugs and whose correct outcome
// is convergence: concurrent edits (S4), delete/recreate churn (C4), local
// file/dir renames (S7/A1), move-onto-occupied (R7), swaps (R6), new files
// under moved dirs (A4), cross-machine fold-equal *file* names (C2/R19), and —
// on a folding disk — case-only renames of files and folders (S030, S038).
// Deliberately excluded, each covered by its own dedicated regression test
// because its correct outcome is *not* clean convergence (decisions.md "W4"):
// type clashes (C5/R22 — a by-design standoff, both sides left alone). A name
// conflict (S030, S038) is a hold, excused only on the machine reporting it.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/executor"
	"github.com/macsimbodnar/synckeeper/internal/names"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// fuzzParams is one fuzzer configuration; env vars widen it for long runs.
type fuzzParams struct {
	runs      int   // number of seeds
	baseSeed  int64 // first seed; run i uses baseSeed+i
	machines  int
	steps     int
	maxRounds int     // convergence bound during quiesce
	crashProb float64 // chance to inject a one-shot crash before an op-phase sync
}

func defaultFuzzParams() fuzzParams {
	// Bounded by default for `go test ./...` (CI); env vars widen it for long
	// manual runs (plan.md W4.4). `-short` shrinks it further.
	p := fuzzParams{runs: 8, baseSeed: 1, machines: 3, steps: 70, maxRounds: 80, crashProb: 0.12}
	if testing.Short() {
		p.runs, p.steps = 3, 35
	}
	if v := os.Getenv("SYNCKEEPER_FUZZ_CRASH"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			p.crashProb = f
		}
	}
	if v := os.Getenv("SYNCKEEPER_FUZZ_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.runs = n
		}
	}
	if v := os.Getenv("SYNCKEEPER_FUZZ_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.steps = n
		}
	}
	if v := os.Getenv("SYNCKEEPER_FUZZ_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			p.baseSeed = n
		}
	}
	if v := os.Getenv("SYNCKEEPER_FUZZ_MACHINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 2 {
			p.machines = n
		}
	}
	return p
}

// fuzzer holds one run's world and deterministic generators.
type fuzzer struct {
	t         *testing.T
	seed      int64
	rng       *rand.Rand
	fake      *driveclient.Fake
	root      string
	ms        []*machine
	maxRounds int
	crashProb float64

	now      time.Time // deterministic clock for conflict-copy names
	clockN   int64     // monotonic mtime source (deterministic edit detection)
	nameN    int       // fresh-name counter
	contentN int       // fresh-content counter

	// Shared namespaces so machines collide on the same rel_paths.
	filePaths []string // candidate file rel-paths (slash-separated)
	dirPaths  []string // candidate pool dir names (distinct fold keys)
	folds     bool     // the test disk folds case: case-only renames are in the menu

	// tracing: SYNCKEEPER_FUZZ_TRACE set — every op and every cycle's plan and
	// failures are logged (2: each cycle's input too; 3: and the plan as
	// Plan returns it, before the collapse), so a red seed can be
	// minimized by hand. Reads no randomness: a traced replay is the same run.
	tracing int
	// disarmed: checkpoints drawn as usual but never fired
	// (SYNCKEEPER_FUZZ_DISARM, comma-separated) — the same run with one window
	// closed, to tell whether a red seed needs it.
	disarmed map[string]bool

	// settled: each machine's result in quiesce's last round — the fixed
	// point's skips, which the convergence oracle reads (S030).
	settled map[string]*Result
}

// trace logs one line of the replay when SYNCKEEPER_FUZZ_TRACE is set.
func (fz *fuzzer) trace(format string, args ...any) {
	if fz.tracing > 0 {
		fz.t.Logf(format, args...)
	}
}

// traceCycle logs what one cycle planned and what failed.
func (fz *fuzzer) traceCycle(m *machine, what string, res *Result) {
	if fz.tracing == 0 || res == nil {
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s [%s]: %d actions, %d failed", what, m.name, len(res.Plan), res.Failed)
	if fz.tracing > 1 {
		in := res.input
		fmt.Fprintf(&sb, "\n    fold case=%v norm=%v", in.CaseFold, in.NormFold)
		if len(in.ShadowedRemote)+len(in.UnreadableLocal)+len(in.UnscannedLocal)+len(in.UnreadableDirs)+len(in.UnreadableFiles) > 0 {
			fmt.Fprintf(&sb, "\n    held shadowed=%v unreadable=%v unscanned=%v udirs=%v ufiles=%v", in.ShadowedRemote, in.UnreadableLocal, in.UnscannedLocal, in.UnreadableDirs, in.UnreadableFiles)
		}
		for _, k := range slices.Sorted(maps.Keys(in.Base)) {
			b := in.Base[k]
			fmt.Fprintf(&sb, "\n    base   %q id=%s dir=%v md5=%s drive=%s size=%d mtime=%d", k, b.FileID, b.IsDir, b.MD5, b.DriveMD5, b.Size, b.MtimeNS)
		}
		for _, k := range slices.Sorted(maps.Keys(in.Local)) {
			l := in.Local[k]
			fmt.Fprintf(&sb, "\n    local  %q dir=%v md5=%s size=%d mtime=%d", k, l.IsDir, l.MD5, l.Size, l.MtimeNS)
		}
		for _, k := range slices.Sorted(maps.Keys(in.Remote)) {
			r := in.Remote[k]
			fmt.Fprintf(&sb, "\n    remote %q id=%s dir=%v md5=%s size=%d", k, r.FileID, r.IsDir, r.MD5, r.Size)
		}
	}
	if fz.tracing > 2 {
		raw, _ := reconcile.Plan(res.input)
		for _, a := range raw {
			fmt.Fprintf(&sb, "\n    raw %s %q -> %q", a.Type, a.RelPath, a.NewRelPath)
		}
	}
	for _, a := range res.Plan {
		fmt.Fprintf(&sb, "\n    %s %q", a.Type, a.RelPath)
		if a.NewRelPath != "" {
			fmt.Fprintf(&sb, " -> %q", a.NewRelPath)
		}
		if a.FileID != "" {
			fmt.Fprintf(&sb, " id=%s", a.FileID)
		}
		if a.ProtectedBy != "" {
			fmt.Fprintf(&sb, " protectedBy=%q", a.ProtectedBy)
		}
		if a.LocalExists {
			sb.WriteString(" pinned")
		}
	}
	for _, e := range res.Errors {
		fmt.Fprintf(&sb, "\n    ERR %s", e)
	}
	for _, k := range res.Skips {
		fmt.Fprintf(&sb, "\n    SKIP %s: %s", k.RelPath, k.Reason)
	}
	fz.t.Log(sb.String())
}

var crashCheckpoints = []string{
	executor.CPUploadBeforeCommit,
	executor.CPDownloadTempWritten,
	executor.CPDownloadBeforeCommit,
	executor.CPQuarantineBeforeMove,
	executor.CPMkdirBeforeCommit,  // W17: a folder on Drive with no row yet
	executor.CPMoveBeforeCommit,   // S031: renamed on disk, row at the old path (a chain or swap cut between steps)
	executor.CPForgetBeforeCommit, // S031: a forget's row drop fails; what depends on it must not run
}

func TestFuzzConvergence(t *testing.T) {
	p := defaultFuzzParams()
	// Seeds run sequentially: executor.FaultHook is a process-global, so
	// parallel fuzzers would clobber each other's crash injection.
	for i := 0; i < p.runs; i++ {
		seed := p.baseSeed + int64(i)
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			runFuzz(t, seed, p)
		})
	}
}

func runFuzz(t *testing.T, seed int64, p fuzzParams) {
	t.Helper()
	fake, root := newWorld(t)
	fz := &fuzzer{
		t: t, seed: seed, rng: rand.New(rand.NewSource(seed)),
		fake: fake, root: root, maxRounds: p.maxRounds, crashProb: p.crashProb,
		dirPaths: []string{"da", "db", "dc"},
		now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	fz.folds = names.CaseInsensitiveFS(t.TempDir())
	fz.disarmed = map[string]bool{}
	for _, cp := range strings.Split(os.Getenv("SYNCKEEPER_FUZZ_DISARM"), ",") {
		fz.disarmed[strings.TrimSpace(cp)] = true
	}
	if v := os.Getenv("SYNCKEEPER_FUZZ_TRACE"); v != "" {
		fz.tracing, _ = strconv.Atoi(v)
		fz.tracing = max(fz.tracing, 1)
	}
	// Deterministic conflict-copy timestamps: the clock advances one minute per
	// op step (below) and is frozen during quiesce, so replay from a seed is
	// exact. Restored after the run.
	prevNow := nowFunc
	nowFunc = func() time.Time { return fz.now }
	t.Cleanup(func() { nowFunc = prevNow })
	// File pool: top-level files, files under pool dirs, and a couple of
	// case-variant leaves so cross-machine fold collisions (C2) arise.
	for i := 0; i < 6; i++ {
		fz.filePaths = append(fz.filePaths, fmt.Sprintf("g%d.txt", i))
	}
	for _, d := range fz.dirPaths {
		for i := 0; i < 4; i++ {
			fz.filePaths = append(fz.filePaths, fmt.Sprintf("%s/f%d.txt", d, i))
		}
	}
	fz.filePaths = append(fz.filePaths, "da/F0.txt", "g0.TXT") // fold variants of da/f0.txt, g0.txt

	for i := 0; i < p.machines; i++ {
		m := newMachine(t, string(rune('a'+i)), fake, root)
		// A permanent anchor keeps every machine's dir non-empty, so the
		// unmounted-dir guard (G2) never trips mid-fuzz.
		fz.rawWrite(m, fmt.Sprintf("anchor-%s.txt", m.name), "anchor "+m.name)
		fz.ms = append(fz.ms, m)
	}
	fz.quiesce() // propagate anchors; everyone starts converged

	for step := 0; step < p.steps; step++ {
		fz.now = fz.now.Add(time.Minute)
		fz.trace("step %d", step)
		m := fz.ms[fz.rng.Intn(len(fz.ms))]
		fz.applyOp(m)
		// Sometimes replay W16's shape: a cycle writes to Drive and a second
		// one runs before the change feed has reported it.
		if fz.rng.Float64() < 0.15 {
			fz.syncWithheldFeed(m)
		}
		// Interleave: sync a random subset in random order.
		for _, sm := range fz.shuffledMachines() {
			if fz.rng.Float64() < 0.55 {
				fz.syncMaybeCrash(sm)
			}
		}
		// Occasionally probe identity stability from a clean state.
		if fz.rng.Float64() < 0.05 {
			fz.quietRenameProbe()
		}
	}

	fz.quiesce()
	fz.assertConverged()
}

// ---- deterministic content / names ---------------------------------------

func (fz *fuzzer) freshContent() string {
	fz.contentN++
	return fmt.Sprintf("content-%d-seed-%d", fz.contentN, fz.seed)
}

func (fz *fuzzer) freshFileName() string {
	fz.nameN++
	return fmt.Sprintf("r%d.txt", fz.nameN)
}

func (fz *fuzzer) freshDirName() string {
	fz.nameN++
	return fmt.Sprintf("dir%d", fz.nameN)
}

// ---- local ops (best-effort; a benign FS error just skips the op) --------

// rawWrite writes content with a monotonic mtime so every write is detected as
// a change regardless of size (the scanner trusts the baseline hash only when
// size AND mtime match).
func (fz *fuzzer) rawWrite(m *machine, rel, content string) {
	p := filepath.Join(m.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fz.t.Fatalf("seed %d: mkdir for %s: %v", fz.seed, rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		fz.t.Fatalf("seed %d: write %s: %v", fz.seed, rel, err)
	}
	fz.trace("op [%s] write %q = %q", m.name, rel, content)
	fz.clockN++
	mt := time.Unix(1_700_000_000+fz.clockN, 0)
	os.Chtimes(p, mt, mt)
}

func (fz *fuzzer) applyOp(m *machine) {
	ops := []func(*machine){
		fz.opWrite, fz.opWrite, fz.opWrite, // weight writes highest
		fz.opDelete,
		fz.opRenameFile,
		fz.opMkdir,
		fz.opRenameDir,
		fz.opSwap,
		fz.opMoveIntoDir,
	}
	if fz.folds {
		ops = append(ops, fz.opCaseRename) // S038; the menu is unchanged on a case-sensitive disk
	}
	ops[fz.rng.Intn(len(ops))](m)
}

// opCaseRename renames a file or folder here to another case of its own name
// (S030, S038): on a folding disk the same entry, renamed — its own rename,
// synced as one; Drive's name wins over another case made elsewhere.
func (fz *fuzzer) opCaseRename(m *machine) {
	all := fz.localFiles(m)
	for d := range fz.localDirs(m) {
		all = append(all, d)
	}
	sort.Strings(all)
	if len(all) == 0 {
		return
	}
	rel := all[fz.rng.Intn(len(all))]
	dir, leaf := filepath.Dir(filepath.FromSlash(rel)), filepath.Base(rel)
	var to string
	switch fz.rng.Intn(3) {
	case 0:
		to = strings.ToUpper(leaf)
	case 1:
		to = strings.ToLower(leaf)
	default:
		to = strings.ToUpper(leaf[:1]) + strings.ToLower(leaf[1:])
	}
	if to == leaf {
		return
	}
	if dir != "." {
		to = filepath.ToSlash(filepath.Join(dir, to))
	}
	fz.rawRename(m, rel, to)
}

// opWrite creates-or-edits a random pool path (drives concurrent edits, C4
// churn, and — via the fold-variant pool entries — cross-machine C2).
func (fz *fuzzer) opWrite(m *machine) {
	rel := fz.filePaths[fz.rng.Intn(len(fz.filePaths))]
	fz.rawWrite(m, rel, fz.freshContent())
}

func (fz *fuzzer) opDelete(m *machine) {
	files := fz.localFiles(m)
	if len(files) == 0 {
		return
	}
	rel := files[fz.rng.Intn(len(files))]
	os.RemoveAll(filepath.Join(m.dir, filepath.FromSlash(rel)))
	fz.trace("op [%s] delete %q", m.name, rel)
}

func (fz *fuzzer) opRenameFile(m *machine) {
	files := fz.localFiles(m)
	if len(files) == 0 {
		return
	}
	from := files[fz.rng.Intn(len(files))]
	// Rename to a fresh unique leaf (never a case-only variant — that is the
	// deferred tracked-case-rename gap), sometimes into a pool dir.
	to := fz.freshFileName()
	if fz.rng.Float64() < 0.5 {
		to = fz.dirPaths[fz.rng.Intn(len(fz.dirPaths))] + "/" + to
	}
	fz.rawRename(m, from, to)
}

func (fz *fuzzer) opMkdir(m *machine) {
	// A fresh empty dir (exercises empty-folder propagation, S8).
	rel := fz.freshDirName()
	os.MkdirAll(filepath.Join(m.dir, filepath.FromSlash(rel)), 0o755)
	fz.trace("op [%s] mkdir %q", m.name, rel)
}

func (fz *fuzzer) opRenameDir(m *machine) {
	dirs := fz.localPoolDirs(m)
	if len(dirs) == 0 {
		return
	}
	from := dirs[fz.rng.Intn(len(dirs))]
	fz.rawRename(m, from, fz.freshDirName())
}

// opSwap exchanges two files' names in one op (R6).
func (fz *fuzzer) opSwap(m *machine) {
	files := fz.localFiles(m)
	if len(files) < 2 {
		return
	}
	a := files[fz.rng.Intn(len(files))]
	b := files[fz.rng.Intn(len(files))]
	if a == b {
		return
	}
	tmp := "swap-" + fz.freshFileName()
	if !fz.rawRename(m, a, tmp) {
		return
	}
	if !fz.rawRename(m, b, a) {
		fz.rawRename(m, tmp, a) // put a back
		return
	}
	fz.rawRename(m, tmp, b)
}

// opMoveIntoDir moves a file into an existing dir, possibly onto an occupied
// name (R7 / A4).
func (fz *fuzzer) opMoveIntoDir(m *machine) {
	files := fz.localFiles(m)
	dirs := fz.localPoolDirs(m)
	if len(files) == 0 || len(dirs) == 0 {
		return
	}
	from := files[fz.rng.Intn(len(files))]
	dir := dirs[fz.rng.Intn(len(dirs))]
	to := dir + "/" + filepath.Base(from)
	if to == from {
		return
	}
	fz.rawRename(m, from, to)
}

func (fz *fuzzer) rawRename(m *machine, from, to string) bool {
	src := filepath.Join(m.dir, filepath.FromSlash(from))
	dst := filepath.Join(m.dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false
	}
	err := os.Rename(src, dst)
	fz.trace("op [%s] rename %q -> %q (err=%v)", m.name, from, to, err)
	return err == nil
}

// ---- machine introspection -----------------------------------------------

func isAnchor(rel string) bool { return strings.HasPrefix(filepath.Base(rel), "anchor-") }

// localFiles returns non-anchor file rel-paths currently on disk.
func (fz *fuzzer) localFiles(m *machine) []string {
	var out []string
	for rel := range m.listTree(fz.t) {
		if !isAnchor(rel) {
			out = append(out, rel)
		}
	}
	sort.Strings(out) // stable order → deterministic rng picks
	return out
}

// localPoolDirs returns pool dirs that currently exist on disk with ≥1 file
// under them (a non-empty dir is renamable without an FS error).
func (fz *fuzzer) localPoolDirs(m *machine) []string {
	files := m.listTree(fz.t)
	var out []string
	for _, d := range fz.dirPaths {
		pre := d + "/"
		for rel := range files {
			if strings.HasPrefix(rel, pre) {
				out = append(out, d)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func (fz *fuzzer) shuffledMachines() []*machine {
	out := append([]*machine(nil), fz.ms...)
	fz.rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// ---- syncing --------------------------------------------------------------

// syncMaybeCrash runs one op-phase cycle, sometimes injecting a one-shot crash
// at a random checkpoint. A crash is a soft failure (recorded, not returned);
// a hard error here would be a real finding.
func (fz *fuzzer) syncMaybeCrash(m *machine) *Result {
	what := "sync"
	if fz.rng.Float64() < fz.crashProb {
		cp := crashCheckpoints[fz.rng.Intn(len(crashCheckpoints))]
		what = "sync crash@" + cp
		// The transfer stage calls the hook from several workers at once, so
		// the one-shot latch must be atomic: exactly one worker crashes.
		var fired atomic.Bool
		executor.FaultHook = func(name string) error {
			if name == cp && !fz.disarmed[cp] && fired.CompareAndSwap(false, true) {
				return fmt.Errorf("injected crash at %s", name)
			}
			return nil
		}
		defer func() { executor.FaultHook = nil }()
	}
	res, err := fz.syncHolding(m)
	fz.traceCycle(m, what, res)
	if err != nil {
		// A crash is a soft failure (res.Failed), never a returned error. Any
		// hard error here — a §4.5-refused plan, a guard mis-fire — is a finding.
		fz.dumpAndFail("op-phase sync [%s] hard-errored: %v%s", m.name, err, fz.machineDump(m, res))
	}
	fz.checkPlan(m, res)
	return res
}

// syncHolding runs one cycle of m and checks that what it holds stays (S038
// round 2): a name conflict excuses its paths from the trees' equality at the
// fixed point (nameConflictExcuses), never from survival. Every file at or
// under a path the cycle reports as held — here (LocalPath, RelPath) or on
// Drive (DrivePath) — is still on that side with its bytes after the cycle:
// at its path, or wherever a folder move of the plan carried it (every write
// of the fuzzer is unique content, so the bytes find it). Only
// m's own plan runs in a cycle, so anything gone is that plan's doing; it may
// remove one only by §4.2's deletion rows: a file here whose Drive item was
// already gone (binned), a Drive file whose row's file was already gone here
// (trashed). On a folding disk only, where name conflicts arise.
func (fz *fuzzer) syncHolding(m *machine) (*Result, error) {
	if !fz.folds {
		return m.eng.Sync(context.Background(), Options{ConfirmDeletes: true})
	}
	var before holdState
	before.local = m.listTree(fz.t)
	before.drive, before.driveIDs = fz.driveFiles()
	before.rows, _ = m.db.AllItems()
	res, err := m.eng.Sync(context.Background(), Options{ConfirmDeletes: true})
	if res == nil {
		return res, err
	}
	var localAfter, driveAfter map[string]string
	for _, s := range res.Skips {
		if s.Kind != reconcile.SkipNameConflict {
			continue
		}
		if localAfter == nil {
			localAfter = m.listTree(fz.t)
			driveAfter, _ = fz.driveFiles()
		}
		if why := heldLost(s, before, localAfter, driveAfter); why != "" {
			fz.dumpAndFail("[%s] %s (held as %+v) by the cycle that holds it", m.name, why, s)
		}
	}
	return res, err
}

func fuzzMD5(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// holdState is what syncHolding reads before a cycle: m's files, Drive's
// files and their ids, m's rows.
type holdState struct {
	local, drive, driveIDs map[string]string
	rows                   []statedb.Item
}

// heldLost returns what the cycle lost of what skip s holds, or "" (S038).
// A held file must still be on its side with its bytes — at its path or
// wherever a folder move carried it (fuzz writes are unique content). The
// only exemptions are §4.2's deletions: a file here whose Drive item was gone
// before the cycle AND whose bytes were the synced ones (edit beats delete:
// an edited file is never binned), and a Drive file whose row's file was gone
// here before the cycle — looked up under the fold (a folder renamed in case
// here holds the row's file under the disk's spelling; round 3).
func heldLost(s reconcile.Skip, before holdState, localAfter, driveAfter map[string]string) string {
	under := func(p, root string) bool { return root != "" && (p == root || strings.HasPrefix(p, root+"/")) }
	has := func(m map[string]string) map[string]bool {
		out := map[string]bool{}
		for _, c := range m {
			out[c] = true
		}
		return out
	}
	fold := func(p string) string { return names.FoldKey(p, true, true) }
	localHas, driveHas := has(localAfter), has(driveAfter)
	live := map[string]bool{}
	for _, id := range before.driveIDs {
		live[id] = true
	}
	rowAt, rowByID := map[string]statedb.Item{}, map[string]statedb.Item{}
	for _, it := range before.rows {
		rowAt[it.RelPath], rowByID[it.DriveFileID] = it, it
	}
	hereFold := map[string]bool{}
	for p := range before.local {
		hereFold[fold(p)] = true
	}
	for _, p := range slices.Sorted(maps.Keys(before.local)) {
		c := before.local[p]
		if !under(p, s.LocalPath) && !under(p, s.RelPath) || localHas[c] {
			continue
		}
		if it, ok := rowAt[p]; ok && !live[it.DriveFileID] && fuzzMD5(c) == it.ContentMD5 {
			continue // unchanged here, gone from Drive before the cycle: binned (§4.2)
		}
		return fmt.Sprintf("held here: %s (%q) is gone or changed", p, c)
	}
	for _, p := range slices.Sorted(maps.Keys(before.drive)) {
		c := before.drive[p]
		if !under(p, s.DrivePath) || driveHas[c] {
			continue
		}
		if it, ok := rowByID[before.driveIDs[p]]; ok && !hereFold[fold(it.RelPath)] {
			continue // deleted here before the cycle: trashed on Drive (§4.2)
		}
		return fmt.Sprintf("held on Drive: %s (%q) is gone or changed", p, c)
	}
	return ""
}

// driveFiles is driveTree's files with each one's Drive id.
func (fz *fuzzer) driveFiles() (map[string]string, map[string]string) {
	files, ids := map[string]string{}, map[string]string{}
	ctx := context.Background()
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fz.fake.List(ctx, parentID)
		if err != nil {
			fz.t.Fatalf("seed %d: drive list %s: %v", fz.seed, parentID, err)
		}
		for _, c := range children {
			rel := prefix + c.Name
			if c.IsDir() {
				walk(c.ID, rel+"/")
				continue
			}
			rc, err := fz.fake.Download(ctx, c.ID)
			if err != nil {
				fz.t.Fatalf("seed %d: drive download %s: %v", fz.seed, c.ID, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				fz.t.Fatalf("seed %d: drive read %s: %v", fz.seed, c.ID, err)
			}
			files[rel], ids[rel] = string(data), c.ID
		}
	}
	walk(fz.root, "")
	return files, ids
}

// syncWithheldFeed runs two back-to-back cycles with Drive's change feed
// withheld — the shape that produced W16 in the field, where a watcher wake
// fired a cycle 0.67 s after the one that had just uploaded a 190 MB video
// and the mirror had not heard about it yet. Every remote-side commit must
// leave the mirror correct by itself; the feed cannot be relied on to repair
// it in time. Always released, so quiesce still converges.
//
// The oracle here is the E4 invariant, not convergence: W16 is *churn*, and a
// machine that trashes its own upload downloads it back next cycle and
// converges perfectly — measured, with the fix disabled, on this very fuzzer
// (decisions.md 2026-07-30 "the convergence oracle cannot see W16").
func (fz *fuzzer) syncWithheldFeed(m *machine) {
	fz.fake.HoldChanges(true)
	defer fz.fake.HoldChanges(false)
	res := fz.syncMaybeCrash(m) // the cycle that writes to Drive
	clean := res.Failed == 0
	res = fz.syncMaybeCrash(m) // the one that used to trash what the first uploaded
	if clean && res.Failed == 0 {
		// Only after two clean cycles: an injected crash legitimately leaves
		// a baseline row whose remote-side half is still owed.
		assertMirrorCoversBaseline(fz.t, m)
	}
}

// checkPlan asserts the §4.5 structural invariant on a returned plan. The
// engine also enforces it inside executor.Apply, so a violation would already
// have surfaced as a sync error; this makes the oracle explicit.
func (fz *fuzzer) checkPlan(m *machine, res *Result) {
	if err := reconcile.ValidateTransferStage(res.Plan); err != nil {
		fz.dumpAndFail("[%s] plan violates §4.5: %v%s", m.name, err, fz.machineDump(m, res))
	}
	if err := reconcile.ValidateLocalOverwrites(res.input, res.Plan); err != nil {
		fz.dumpAndFail("[%s] plan violates §4.5 rule O1: %v%s", m.name, err, fz.machineDump(m, res))
	}
}

// machineDump renders a machine's baseline, local tree, the Drive tree, and a
// plan — the state that makes a fuzzer failure reconstructable.
func (fz *fuzzer) machineDump(m *machine, res *Result) string {
	var sb strings.Builder
	items, _ := m.db.AllItems()
	fmt.Fprintf(&sb, "\nBASELINE [%s]:\n", m.name)
	for _, it := range items {
		fmt.Fprintf(&sb, "  rel=%q id=%s dir=%v md5=%s driveMd5=%s\n", it.RelPath, it.DriveFileID, it.IsDir, short(it.ContentMD5), short(it.DriveMD5))
	}
	fmt.Fprintf(&sb, "LOCAL [%s]: %v\n", m.name, sortedKeys(m.listTree(fz.t)))
	fmt.Fprintf(&sb, "PLAN [%s]:\n", m.name)
	for _, a := range res.Plan {
		fmt.Fprintf(&sb, "  %s rel=%q new=%q id=%s dir=%v\n", a.Type, a.RelPath, a.NewRelPath, a.FileID, a.IsDir)
	}
	return sb.String()
}

// quiesce drives every machine to a shared fixed point: round-robin syncs
// until a full round plans nothing and fails nothing. Crashes are never armed
// here, so repairs from op-phase crashes settle.
func (fz *fuzzer) quiesce() {
	fz.settled = map[string]*Result{}
	for round := 0; round < fz.maxRounds; round++ {
		activity := 0
		for _, m := range fz.ms {
			res, err := fz.syncHolding(m)
			fz.traceCycle(m, fmt.Sprintf("quiesce %d", round), res)
			if err != nil {
				fz.t.Fatalf("seed %d: quiesce sync [%s] round %d: %v", fz.seed, m.name, round, err)
			}
			fz.checkPlan(m, res)
			activity += len(res.Plan) + res.Failed
			fz.settled[m.name] = res
		}
		if activity == 0 {
			return
		}
	}
	var sb strings.Builder
	for _, m := range fz.ms {
		res, _ := m.eng.Sync(context.Background(), Options{ConfirmDeletes: true, DryRun: true})
		sb.WriteString(fz.machineDump(m, res))
	}
	fz.dumpAndFail("did not converge within %d rounds%s", fz.maxRounds, sb.String())
}

// ---- identity stability probe --------------------------------------------

// quietRenameProbe checks the scoped identity-stability invariant: from a
// clean fixed point, a single machine renames one unmodified file; the sync
// must be a MoveRemote that preserves the Drive id and plans no delete-class
// action.
func (fz *fuzzer) quietRenameProbe() {
	fz.quiesce()
	m := fz.ms[fz.rng.Intn(len(fz.ms))]
	files := fz.localFiles(m)
	if len(files) == 0 {
		return
	}
	from := files[fz.rng.Intn(len(files))]
	idBefore := fz.baselineID(m, from)
	if idBefore == "" {
		return // not a tracked file (shouldn't happen at a fixed point)
	}
	to := fz.freshFileName()
	if !fz.rawRename(m, from, to) {
		return
	}
	res, err := m.eng.Sync(context.Background(), Options{ConfirmDeletes: true})
	fz.traceCycle(m, "probe", res)
	if err != nil {
		fz.t.Fatalf("seed %d: identity probe sync [%s] errored: %v", fz.seed, m.name, err)
	}
	if res.Failed != 0 {
		fz.dumpAndFail("identity probe [%s] %s→%s: %d failed actions: %v", m.name, from, to, res.Failed, res.Errors)
	}
	sawMove := false
	for _, a := range res.Plan {
		switch a.Type {
		case reconcile.MoveRemote:
			if a.RelPath == from || a.NewRelPath == to {
				sawMove = true
			}
		case reconcile.TrashRemote, reconcile.QuarantineLocal, reconcile.Forget:
			fz.dumpAndFail("identity probe [%s] %s→%s planned a delete-class action %s on %q — a clean rename must be a move", m.name, from, to, a.Type, a.RelPath)
		}
	}
	if !sawMove {
		fz.dumpAndFail("identity probe [%s] %s→%s planned no MoveRemote: %v", m.name, from, to, res.Plan)
	}
	if idAfter := fz.baselineID(m, to); idAfter != idBefore {
		fz.dumpAndFail("identity probe [%s] %s→%s: Drive id changed %s → %s (delete+recreate, not a move)", m.name, from, to, idBefore, idAfter)
	}
	f, err := fz.fake.Get(context.Background(), idBefore)
	if err != nil || f.Trashed || f.Name != filepath.Base(to) {
		fz.dumpAndFail("identity probe [%s] %s→%s: Drive file %s is now name=%q trashed=%v err=%v", m.name, from, to, idBefore, f.Name, f.Trashed, err)
	}
}

// baselineID returns the Drive file id the machine's baseline binds to rel, or
// "" if untracked.
func (fz *fuzzer) baselineID(m *machine, rel string) string {
	items, err := m.db.AllItems()
	if err != nil {
		fz.t.Fatalf("seed %d: AllItems [%s]: %v", fz.seed, m.name, err)
	}
	for _, it := range items {
		if it.RelPath == rel {
			return it.DriveFileID
		}
	}
	return ""
}

// ---- convergence oracle ---------------------------------------------------

func (fz *fuzzer) assertConverged() {
	// Every machine against Drive: with no name-conflict hold that is the old
	// machine-vs-machine check by transitivity, and a hold excuses only the
	// machine that reports it (S030).
	driveFiles, driveDirs := fz.driveTree()
	for _, m := range fz.ms {
		lackHere, lackDrive := fz.nameConflictExcuses(m)
		if len(lackHere)+len(lackDrive) > 0 {
			fz.t.Logf("seed %d: [%s] name-conflict hold at the fixed point: may lack %v; Drive may lack %v",
				fz.seed, m.name, slices.Sorted(maps.Keys(lackHere)), slices.Sorted(maps.Keys(lackDrive)))
		}
		fz.assertTreeEqual(fmt.Sprintf("machine %s vs Drive", m.name), m.listTree(fz.t), driveFiles,
			fz.localDirs(m), driveDirs, lackHere, lackDrive)
	}
	// W16-E4: at the fixed point the state DB's two sides agree on every
	// machine. Asserted here rather than per cycle because mid-fuzz a failed
	// (crashed) delete legitimately leaves a baseline row whose remote is
	// already gone; at rest nothing failed and nothing is pending.
	assertMirrorCoversBaseline(fz.t, fz.ms...)
}

// nameConflictExcuses reads m's settled name-conflict skips (S030, DEC-011):
// on a folding disk m keeps its own file under its name and the incoming item
// where it was, so m may lack what Drive holds under Drive's name (DrivePath),
// and Drive may lack what waits here (LocalPath: the file here, a newcomer not
// uploaded; RelPath, when not DrivePath: a row whose move there waits).
// Each skip is checked to be one: the two names differ and fold together, and
// m holds LocalPath.
func (fz *fuzzer) nameConflictExcuses(m *machine) (lackHere, lackDrive map[string]bool) {
	lackHere, lackDrive = map[string]bool{}, map[string]bool{}
	res := fz.settled[m.name]
	if res == nil {
		return
	}
	files, dirs := m.listTree(fz.t), fz.localDirs(m)
	for _, s := range res.Skips {
		if s.Kind != reconcile.SkipNameConflict {
			continue
		}
		_, f := files[s.LocalPath]
		if !f && !dirs[s.LocalPath] || s.DrivePath == s.LocalPath ||
			names.FoldKey(s.DrivePath, true, true) != names.FoldKey(s.LocalPath, true, true) {
			fz.dumpAndFail("[%s] name-conflict skip %+v: want LocalPath here, DrivePath another case of it", m.name, s)
		}
		lackHere[s.DrivePath], lackDrive[s.LocalPath] = true, true
		if s.RelPath != s.DrivePath {
			lackDrive[s.RelPath] = true // a row here whose move to DrivePath waits
		}
	}
	return
}

// assertTreeEqual compares machine m's tree with Drive's. A file only on Drive
// is allowed if lackHere names it, one only on m if lackDrive names it; a file
// on both must have one content. A folder on one side only is allowed only as
// an ancestor of such an allowed file on the side that holds it.
func (fz *fuzzer) assertTreeEqual(what string, mFiles, dFiles map[string]string, mDirs, dDirs, lackHere, lackDrive map[string]bool) {
	holds := func(allowed map[string]bool, d string) bool {
		for p := range allowed {
			if strings.HasPrefix(p, d+"/") {
				return true
			}
		}
		return false
	}
	// A name conflict on a folder (S038) excuses what is under it too.
	covered := func(allowed map[string]bool, p string) bool {
		for q := p; ; q = filepath.ToSlash(filepath.Dir(q)) {
			if allowed[q] {
				return true
			}
			if !strings.Contains(q, "/") {
				return false
			}
		}
	}
	for p, c := range mFiles {
		if dc, ok := dFiles[p]; ok && dc != c || !ok && !covered(lackDrive, p) {
			fz.dumpAndFail("%s: %q differs: %q vs %q\n  %v\n  %v", what, p, c, dc, sortedKeys(mFiles), sortedKeys(dFiles))
		}
	}
	for p := range dFiles {
		if _, ok := mFiles[p]; !ok && !covered(lackHere, p) {
			fz.dumpAndFail("%s: %q only on Drive\n  %v\n  %v", what, p, sortedKeys(mFiles), sortedKeys(dFiles))
		}
	}
	for d := range mDirs {
		if !dDirs[d] && !holds(lackDrive, d) && !covered(lackDrive, d) {
			fz.dumpAndFail("%s: dir %q missing on one side", what, d)
		}
	}
	for d := range dDirs {
		if !mDirs[d] && !holds(lackHere, d) && !covered(lackHere, d) {
			fz.dumpAndFail("%s: dir %q missing on one side", what, d)
		}
	}
}

// localDirs returns the set of directory rel-paths under a machine's tree
// (excluding the root itself).
func (fz *fuzzer) localDirs(m *machine) map[string]bool {
	out := map[string]bool{}
	filepath.WalkDir(m.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(m.dir, p)
		if rel != "." {
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return out
}

// driveTree reconstructs the non-trashed tree under root as
// (rel_path→content) files plus a dir set.
func (fz *fuzzer) driveTree() (map[string]string, map[string]bool) {
	files := map[string]string{}
	dirs := map[string]bool{}
	ctx := context.Background()
	var walk func(parentID, prefix string)
	walk = func(parentID, prefix string) {
		children, err := fz.fake.List(ctx, parentID)
		if err != nil {
			fz.t.Fatalf("seed %d: drive list %s: %v", fz.seed, parentID, err)
		}
		for _, c := range children {
			rel := c.Name
			if prefix != "" {
				rel = prefix + "/" + c.Name
			}
			if c.IsDir() {
				dirs[rel] = true
				walk(c.ID, rel)
				continue
			}
			rc, err := fz.fake.Download(ctx, c.ID)
			if err != nil {
				fz.t.Fatalf("seed %d: drive download %s: %v", fz.seed, c.ID, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				fz.t.Fatalf("seed %d: drive read %s: %v", fz.seed, c.ID, err)
			}
			files[rel] = string(data)
		}
	}
	walk(fz.root, "")
	return files, dirs
}

// ---- failure reporting ----------------------------------------------------

func (fz *fuzzer) dumpAndFail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	var b strings.Builder
	fmt.Fprintf(&b, "FUZZ FAILURE (seed %d): %s\n", fz.seed, msg)
	fmt.Fprintf(&b, "replay: SYNCKEEPER_FUZZ_SEED=%d SYNCKEEPER_FUZZ_RUNS=1 go test ./internal/engine -run TestFuzzConvergence\n", fz.seed)
	for _, m := range fz.ms {
		fmt.Fprintf(&b, "--- machine %s files: %v\n", m.name, sortedKeys(m.listTree(fz.t)))
	}
	df, _ := fz.driveTree()
	fmt.Fprintf(&b, "--- drive files: %v\n", sortedKeys(df))
	fz.t.Fatal(b.String())
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func short(s string) string {
	if len(s) > 6 {
		return s[:6]
	}
	return s
}
