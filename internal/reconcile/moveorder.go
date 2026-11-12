package reconcile

import (
	"container/heap"
	"path"
	"slices"
	"strings"

	"github.com/macsimbodnar/synckeeper/internal/conflicts"
)

// The moves stage in dependency order (spec §4.5, S031). Its file moves used
// to run in depth-then-path order, which is right only when the file leaving
// a path happens to sort before the one arriving: Drive renaming f.txt to
// h.txt and moving a.txt onto f.txt renamed a.txt over f.txt first.

// isFileMove: a local rename of one file — the only action that can sit in a
// chain or a cycle of moves.
func isFileMove(a Action) bool { return a.Type == MoveLocal && !a.IsDir }

func actionDepth(p string) int { return strings.Count(p, "/") }

// breakMoveCycles turns every cycle of file moves — a swap, a rotation, each
// destination another move's source — into a chain, through a temporary name
// in the source's folder: s0 → tmp first, the rest of the cycle, tmp → d0
// last. Each step is an ordinary MoveLocal and commits its row like any
// move, so no rename lands on an occupied path and no commit meets another
// row at its destination (was: a swap renamed one file over the other and
// both commits failed `UNIQUE constraint failed: items.rel_path`). The cycle
// is broken at the move whose source sorts first (depth, then path).
//
// The temporary name is the source's conflict-copy name: if the run dies
// between the first rename and its commit, the next cycle finds the file
// there as a new file and keeps it — uploaded beside the others, a conflict
// copy in fact. A file the plan leaves at the temporary name is never lost
// and never read as anything's edit. The step back is ProtectedBy the move
// that empties d0, which hangs, through the cycle, on the first step;
// transfers that act on the moved file depend on the step back.
func breakMoveCycles(moves, transfers []Action, in Input) ([]Action, []Action) {
	src := map[string]int{}
	var order []int
	for i, m := range moves {
		if isFileMove(m) {
			src[m.RelPath] = i
			order = append(order, i)
		}
	}
	if len(order) < 2 {
		return moves, transfers
	}
	before := func(i, j int) bool {
		a, b := moves[i].RelPath, moves[j].RelPath
		if d1, d2 := actionDepth(a), actionDepth(b); d1 != d2 {
			return d1 < d2
		}
		return a < b
	}
	slices.SortStableFunc(order, func(i, j int) int {
		switch {
		case before(i, j):
			return -1
		case before(j, i):
			return 1
		}
		return 0
	})
	planned := map[string]bool{}
	for _, a := range slices.Concat(moves, transfers) {
		planned[a.RelPath], planned[a.NewRelPath] = true, true
	}
	taken := func(p string) bool {
		_, l := in.Local[p]
		_, r := in.Remote[p]
		_, b := in.Base[p]
		return l || r || b || planned[p]
	}

	const (
		unseen = iota
		walking
		done
	)
	state := make([]int, len(moves))
	for _, start := range order {
		var walk []int
		for i, ok := start, true; ok && state[i] == unseen; i, ok = src[moves[i].NewRelPath] {
			state[i] = walking
			walk = append(walk, i)
			if n, next := src[moves[i].NewRelPath]; next && state[n] == walking {
				cycle := walk[slices.Index(walk, n):]
				m0 := slices.MinFunc(cycle, func(a, b int) int {
					switch {
					case before(a, b):
						return -1
					case before(b, a):
						return 1
					}
					return 0
				})
				s0, d0 := moves[m0].RelPath, moves[m0].NewRelPath
				tmp := conflicts.Path(s0, in.Machine, in.Now)
				for n := int64(2); taken(tmp); n++ {
					ext := path.Ext(s0)
					tmp = conflicts.Path(strings.TrimSuffix(s0, ext)+" "+itoa(n)+ext, in.Machine, in.Now)
				}
				planned[tmp] = true
				moves = append(moves, Action{Type: MoveLocal, RelPath: tmp, NewRelPath: d0,
					FileID: moves[m0].FileID, ProtectedBy: d0})
				moves[m0] = Action{Type: MoveLocal, RelPath: s0, NewRelPath: tmp, FileID: moves[m0].FileID}
				for k := range transfers {
					if transfers[k].ProtectedBy == s0 {
						transfers[k].ProtectedBy = tmp
					}
				}
				break
			}
		}
		for _, w := range walk {
			state[w] = done
		}
	}
	return moves, transfers
}

// orderMoves sorts the file-moves stage topologically. An action runs after
//   - every protector it is ProtectedBy (the executor refuses it if one
//     failed, so the protector must have run), and
//   - every action that empties the path it fills: a file move, a backup, an
//     early bin — or a remote file move, whose commit moves its row off the
//     path the local move's commit needs — unless that action moves the same
//     file on (a cycle's temporary name).
//
// Among actions free to run, the old order: actions that empty a path
// (backups, early bins, forgets) first, then depth, then path. A cycle left
// over — none is known; breakMoveCycles removes those of file moves — keeps
// that order for what remains, and rule O1 refuses the plan if it would lose
// anything.
func orderMoves(moves []Action) []Action {
	n := len(moves)
	if n < 2 {
		return moves
	}
	emptier := func(t Type) bool { return t == ConflictBackup || t == QuarantineLocal || t == Forget }
	less := func(i, j int) bool {
		a, b := moves[i], moves[j]
		if ea, eb := emptier(a.Type), emptier(b.Type); ea != eb {
			return ea
		}
		if d1, d2 := actionDepth(a.RelPath), actionDepth(b.RelPath); d1 != d2 {
			return d1 < d2
		}
		if a.RelPath != b.RelPath {
			return a.RelPath < b.RelPath
		}
		return i < j
	}
	protector := func(t Type) bool {
		switch t {
		case ConflictBackup, MoveLocal, MoveRemote, Forget:
			return true
		}
		return false
	}
	vacates := func(a Action) bool {
		switch a.Type {
		case ConflictBackup, QuarantineLocal:
			return true
		case MoveLocal, MoveRemote:
			return !a.IsDir
		}
		return false
	}
	byRel := map[string][]int{}
	for i, m := range moves {
		byRel[m.RelPath] = append(byRel[m.RelPath], i)
	}
	succ := make([][]int, n)
	indeg := make([]int, n)
	edge := func(from, to int) {
		if from != to {
			succ[from] = append(succ[from], to)
			indeg[to]++
		}
	}
	for i, m := range moves {
		if m.ProtectedBy != "" {
			for _, j := range byRel[m.ProtectedBy] {
				if protector(moves[j].Type) {
					edge(j, i)
				}
			}
		}
		if isFileMove(m) || m.Type == ConflictBackup {
			for _, j := range byRel[m.NewRelPath] {
				// Not the same file moving on: a cycle's step back empties
				// the temporary name its first step fills.
				if vacates(moves[j]) && (m.FileID == "" || moves[j].FileID != m.FileID) {
					edge(j, i)
				}
			}
		}
	}
	ready := &actionHeap{less: less}
	for i := range moves {
		if indeg[i] == 0 {
			heap.Push(ready, i)
		}
	}
	out := make([]Action, 0, n)
	emitted := make([]bool, n)
	for len(out) < n {
		if ready.Len() == 0 {
			var rest []int
			for i := range moves {
				if !emitted[i] {
					rest = append(rest, i)
				}
			}
			slices.SortFunc(rest, func(a, b int) int {
				if less(a, b) {
					return -1
				}
				return 1
			})
			for _, i := range rest {
				out = append(out, moves[i])
			}
			break
		}
		i := heap.Pop(ready).(int)
		emitted[i] = true
		out = append(out, moves[i])
		for _, j := range succ[i] {
			if indeg[j]--; indeg[j] == 0 {
				heap.Push(ready, j)
			}
		}
	}
	return out
}

type actionHeap struct {
	idx  []int
	less func(i, j int) bool
}

func (h *actionHeap) Len() int           { return len(h.idx) }
func (h *actionHeap) Less(i, j int) bool { return h.less(h.idx[i], h.idx[j]) }
func (h *actionHeap) Swap(i, j int)      { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }
func (h *actionHeap) Push(x any)         { h.idx = append(h.idx, x.(int)) }
func (h *actionHeap) Pop() any {
	x := h.idx[len(h.idx)-1]
	h.idx = h.idx[:len(h.idx)-1]
	return x
}
