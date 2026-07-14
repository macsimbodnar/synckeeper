package names

import (
	"os"
	"path/filepath"
	"strings"
)

// CaseInsensitiveFS reports whether dir lives on a case-insensitive
// filesystem (macOS APFS/HFS+ default, most Windows volumes), where names
// differing only by case — a.txt and A.txt — map to the same path. It probes
// by creating a temp file and looking it up under a case-toggled name.
// Unprobeable dirs are treated as case-sensitive: the safe default never
// collapses distinct Drive names.
func CaseInsensitiveFS(dir string) bool {
	f, err := os.CreateTemp(dir, TempPrefix+"case*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)

	base := filepath.Base(name)
	toggled := strings.ToUpper(base)
	if toggled == base {
		toggled = strings.ToLower(base)
	}
	if toggled == base {
		return false // nothing to toggle
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(name), toggled))
	return err == nil
}
