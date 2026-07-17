package names

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
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

// NormalizationInsensitiveFS reports whether dir lives on a filesystem that
// folds Unicode normalization (macOS APFS/HFS+), where a name in NFC and the
// same name in NFD map to the same path. It probes by creating a file whose
// name ends in "é" (U+00E9) spelled NFC and looking it up spelled NFD
// (U+0065 U+0301) — the two forms are derived from one escape so the source is
// unambiguous. Unprobeable dirs are treated as normalization-sensitive: the
// safe default never collapses distinct Drive names.
func NormalizationInsensitiveFS(dir string) bool {
	f, err := os.CreateTemp(dir, TempPrefix+"norm*")
	if err != nil {
		return false
	}
	stem := f.Name()
	f.Close()
	os.Remove(stem) // reuse only its unique, collision-free stem

	const eAcute = "é" // é
	nfc := stem + norm.NFC.String(eAcute)
	nfd := stem + norm.NFD.String(eAcute)
	if err := os.WriteFile(nfc, nil, 0o600); err != nil {
		return false
	}
	defer os.Remove(nfc)
	_, err = os.Stat(nfd)
	return err == nil
}
