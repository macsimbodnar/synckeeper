package names

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// The probe must agree with a direct case-toggled stat on whatever FS the
// test's temp dir lives on (case-insensitive on macOS APFS, sensitive on
// typical Linux) — so the assertion is host-independent.
func TestCaseInsensitiveFS(t *testing.T) {
	dir := t.TempDir()
	got := CaseInsensitiveFS(dir)

	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "PROBE.TXT"))
	want := err == nil

	if got != want {
		t.Errorf("CaseInsensitiveFS = %v, but direct case-toggled stat implies %v", got, want)
	}
}

// Host-independent like the case probe: the result must agree with a direct
// NFD stat of an NFC-written name on the test's temp FS.
func TestNormalizationInsensitiveFS(t *testing.T) {
	dir := t.TempDir()
	got := NormalizationInsensitiveFS(dir)

	nfc := filepath.Join(dir, "probe-"+norm.NFC.String("é")+".txt")
	nfd := filepath.Join(dir, "probe-"+norm.NFD.String("é")+".txt")
	if err := os.WriteFile(nfc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(nfd)
	want := err == nil

	if got != want {
		t.Errorf("NormalizationInsensitiveFS = %v, but direct NFD stat implies %v", got, want)
	}
}

func TestFoldKey(t *testing.T) {
	nfc, nfd := norm.NFC.String("é"), norm.NFD.String("é")
	if nfc == nfd {
		t.Fatal("test setup: NFC and NFD forms are identical")
	}
	cases := []struct {
		name               string
		a, b               string
		caseFold, normFold bool
		wantEqual          bool
	}{
		{"case only, folded", "A.txt", "a.txt", true, false, true},
		{"case only, not folded", "A.txt", "a.txt", false, false, false},
		{"norm only, folded", "caf" + nfc + ".txt", "caf" + nfd + ".txt", false, true, true},
		{"norm only, not folded", "caf" + nfc + ".txt", "caf" + nfd + ".txt", false, false, false},
		{"case+norm, both folded", "CAF" + nfc + ".txt", "caf" + nfd + ".txt", true, true, true},
		{"case+norm, only case folded", "CAF" + nfc + ".txt", "caf" + nfd + ".txt", true, false, false},
		{"genuinely distinct names", "a.txt", "b.txt", true, true, false},
	}
	for _, c := range cases {
		got := FoldKey(c.a, c.caseFold, c.normFold) == FoldKey(c.b, c.caseFold, c.normFold)
		if got != c.wantEqual {
			t.Errorf("%s: FoldKey collision = %v, want %v", c.name, got, c.wantEqual)
		}
	}
}
