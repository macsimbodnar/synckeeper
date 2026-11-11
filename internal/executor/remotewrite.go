package executor

// The remote-write gate (spec §7, S010): the remote counterpart of
// localwrite.go, and the ONLY file in the package permitted to call a Drive
// mutation on an existing item. Each one carries the Drive state its plan was
// built on (Action.RemoteWant) as a driveclient.Precondition; the client
// re-reads the item immediately before mutating it and refuses on drift. A
// refusal is an ordinary failed action — counted, logged, the journal left
// for the next cycle to discard — and the replan sees the other machine's
// write as the decision table's conflict or edit-beats-delete row instead of
// overwriting it. The companion AST test (remotegate_test.go) enforces the
// exclusivity, as gate_test.go does for local paths.

import (
	"context"
	"io"

	"github.com/macsimbodnar/synckeeper/internal/driveclient"
	"github.com/macsimbodnar/synckeeper/internal/reconcile"
)

// remoteGate attaches a's expected Drive state to ctx. An action without one
// — journaled before RemoteWant existed, or built by hand in a test — runs
// unchecked, as every mutation did before S010.
func remoteGate(ctx context.Context, a reconcile.Action) context.Context {
	w := a.RemoteWant
	if w == nil {
		return ctx
	}
	return driveclient.WithPrecondition(ctx, driveclient.Precondition{
		FileID: a.FileID, ParentID: w.ParentID, Name: w.Name, IsDir: w.IsDir, MD5: w.MD5,
	})
}

func (x *Executor) gatedUpdate(ctx context.Context, a reconcile.Action, content io.Reader, size int64) (driveclient.File, error) {
	return x.Client.Update(remoteGate(ctx, a), a.FileID, content, size)
}

func (x *Executor) gatedTrash(ctx context.Context, a reconcile.Action) error {
	return x.Client.Trash(remoteGate(ctx, a), a.FileID)
}

func (x *Executor) gatedMove(ctx context.Context, a reconcile.Action, parentID, name string) (driveclient.File, error) {
	return x.Client.Move(remoteGate(ctx, a), a.FileID, parentID, name)
}
