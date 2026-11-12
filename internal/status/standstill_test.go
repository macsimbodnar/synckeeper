package status

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// S002: a plan refused cycle after cycle is named as a standstill in the plain
// report and in --json, distinct from the `last error` that repeats with it.

const refusedMsg = `plan violates §4.5: download and upload both act on "N/f.txt" in the concurrent transfer stage; refusing the plan, replanning next cycle`

func stuckSnapshot(ref time.Time) Snapshot {
	snap := fixtures(ref)["running_watching"]
	snap.Daemon.Mode = ModeBackoff
	snap.Daemon.LastError = refusedMsg
	snap.Standstill = &Standstill{Since: ref.Add(-12*time.Minute - 5*time.Second).Unix(), Cycles: 4,
		Paths: []string{"N/f.txt"}, Reason: refusedMsg}
	return snap
}

func TestAStandstillIsNamedInPlainStatus(t *testing.T) {
	ref := time.Unix(1_800_000_000, 0)
	var buf bytes.Buffer
	PrintHuman(&buf, stuckSnapshot(ref))
	const want = `stuck:         SYNCING STOPPED for 12m, 4 cycles in a row: the plan is refused (§4.5) on "N/f.txt"; ` +
		`nothing syncs in either direction — no file is touched while it lasts and retrying won't clear it — ` +
		`a Synckeeper bug: report it with the daemon log` + "\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("plain status does not name the standstill; want line\n%s\ngot:\n%s", want, buf.String())
	}
	// Distinct from the error line, which still says what was refused.
	if !strings.Contains(buf.String(), "last error:    "+refusedMsg+"\n") {
		t.Errorf("plain status lost the last error:\n%s", buf.String())
	}

	var plain bytes.Buffer
	PrintHuman(&plain, fixtures(ref)["running_watching"])
	if strings.Contains(plain.String(), "stuck:") {
		t.Errorf("a daemon that is not stuck printed a stuck line:\n%s", plain.String())
	}
}

func TestAStandstillIsInStatusJSON(t *testing.T) {
	ref := time.Unix(1_800_000_000, 0)
	var buf bytes.Buffer
	if err := WriteJSON(&buf, stuckSnapshot(ref)); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Daemon struct {
			Standstill *Standstill `json:"standstill"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	st := doc.Daemon.Standstill
	if st == nil || st.Since != ref.Add(-12*time.Minute-5*time.Second).Unix() || st.Cycles != 4 ||
		len(st.Paths) != 1 || st.Paths[0] != "N/f.txt" || st.Reason != refusedMsg {
		t.Errorf("daemon.standstill = %+v", st)
	}

	// Not stuck: the key is there and null, so a script can test it.
	buf.Reset()
	if err := WriteJSON(&buf, fixtures(ref)["running_watching"]); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Daemon map[string]json.RawMessage `json:"daemon"`
	}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw.Daemon["standstill"]; !ok || string(v) != "null" {
		t.Errorf("daemon.standstill = %s (present %v), want null", v, ok)
	}
}

// Gather keeps the daemon's report only while the daemon is judged running: a
// stopped daemon syncs nothing for another reason, and its last report is
// stale by definition.
func TestGatherKeepsAStandstillOnlyWhileRunning(t *testing.T) {
	db, err := statedb.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := &Standstill{Since: 1, Cycles: 2, Paths: []string{"x"}, Reason: "r"}
	in := Input{
		DB: db, Standstill: st,
		ServiceStatus: func() (service.State, error) { return service.State{}, nil },
		SystemBin:     func() (bool, string) { return true, "bin" },
		TokenPresent:  func(string) bool { return true },
		Quarantine:    func(string) (int, int64) { return 0, 0 },
	}
	in.DaemonAlive = func() bool { return true }
	if got := Gather(in).Standstill; got != st {
		t.Errorf("running daemon: standstill = %+v, want the one passed in", got)
	}
	in.DaemonAlive = func() bool { return false }
	if got := Gather(in).Standstill; got != nil {
		t.Errorf("daemon not running: standstill = %+v, want nil", got)
	}
}
