//go:build !linux && !(darwin && cgo)

package trash

// Every build without a native trash implementation: pure-Go macOS
// (CGO_ENABLED=0), Windows until W8, and anything new. Reporting the trash
// unavailable is not a degradation of the durability invariant — the caller
// falls back to Synckeeper's quarantine directory, so a delete is still never
// permanent.

func moveToTrash(string) error { return ErrUnavailable }

func available() bool { return false }

func describe() string { return "unavailable on this platform (deletes use Synckeeper's quarantine)" }
