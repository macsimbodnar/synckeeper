package executor

import (
	"os"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// No test may move a file into the developer's own trash. Pinning the
// package default here means a test that forgets to inject one still cannot
// reach the real bin — the fake is the floor, not a per-test courtesy.
// Tests that care about the bin's contents inject their own Fake.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "synckeeper-suite-trash")
	if err != nil {
		panic(err)
	}
	defaultTrash = trash.NewFake(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
