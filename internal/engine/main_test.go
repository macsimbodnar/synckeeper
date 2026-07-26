package engine

import (
	"os"
	"testing"

	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// The engine suite never touches the developer's own trash: the package
// default is pinned to a temporary bin for every test, and tests that assert
// on what the bin received inject their own Fake (see newHarness).
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
