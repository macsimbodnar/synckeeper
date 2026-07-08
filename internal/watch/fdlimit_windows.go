//go:build windows

package watch

// raiseFDLimit is a no-op on Windows: ReadDirectoryChangesW holds one
// handle per directory, and there is no per-process descriptor limit to
// lift.
func raiseFDLimit() {}
