// Package status is the read model behind every read-only view of the daemon:
// the one-shot `status` command (human and JSON), the live dashboard, and the
// shared display formatters `info`, `doctor`, `activity` and `account` render
// with. It gathers a Snapshot and renders it; it never writes, never migrates
// the schema, and never takes the instance lock (spec §14).
//
// One read model, one set of formatters: a field's value and its wording are
// defined here once, so `status`, `info` and the dashboard cannot drift into
// claiming different things about the same fact (CLAUDE.md, "doc claims are
// claims about code"; W15-U1).
package status

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/service"
	"github.com/macsimbodnar/synckeeper/internal/statedb"
	"github.com/macsimbodnar/synckeeper/internal/trash"
)

// DefaultActivityLimit is how many recent-activity rows the one-shot views
// show. The dashboard asks for more.
const DefaultActivityLimit = 5

// Daemon classifications reported as Snapshot.State.
const (
	StateRunning  = "running"
	StateStale    = "stale"
	StateStopped  = "stopped"
	StateNeverRun = "never-run"
)

// Snapshot is everything a read-only view shows, gathered at one instant.
// Now is that instant: every relative time is rendered against it, so one
// frame is internally consistent and a rendered Snapshot is reproducible in a
// test without a clock.
type Snapshot struct {
	Now time.Time

	Daemon statedb.DaemonStatus
	State  string // running | stale | stopped | never-run

	ConfigDir   string
	SyncDir     string
	DriveFolder string
	MachineName string
	RootID      string
	TokenOK     bool

	Items   int
	Pending int
	QFiles  int
	QBytes  int64

	BinAvailable bool
	BinDest      string

	Autostart    service.State
	AutostartErr error

	Activity []statedb.Activity
}

// Input is what Gather needs: the opened read-only DB and the resolved paths,
// plus seams for everything that touches the machine (clock, control socket,
// service manager, system bin, token file, quarantine walk) so a Snapshot can
// be gathered deterministically in a test.
type Input struct {
	DB          *statedb.DB
	ConfigDir   string
	SyncDir     string
	DriveFolder string
	MachineName string

	// StalenessWindow is how long since the last heartbeat before a daemon
	// still marked "running" is judged dead. The caller owns the policy (it
	// is a multiple of the daemon's heartbeat interval); Gather owns the
	// logic. Zero means never stale on heartbeat age alone.
	StalenessWindow time.Duration

	// ActivityLimit defaults to DefaultActivityLimit.
	ActivityLimit int

	// Seams; nil means the real implementation.
	Now           func() time.Time
	DaemonAlive   func() bool
	ServiceStatus func() (service.State, error)
	SystemBin     func() (bool, string)
	TokenPresent  func(configDir string) bool
	Quarantine    func(dir string) (int, int64)
}

func (in Input) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// Gather reads one Snapshot. Every read is best-effort: a field that cannot be
// read stays at its zero value rather than failing the whole view, because a
// half-broken environment is exactly when someone runs `status`.
func Gather(in Input) Snapshot {
	s := Snapshot{
		Now:         in.now(),
		ConfigDir:   in.ConfigDir,
		SyncDir:     in.SyncDir,
		DriveFolder: in.DriveFolder,
		MachineName: in.MachineName,
	}

	alive := false
	if in.DaemonAlive != nil {
		alive = in.DaemonAlive()
	}
	ds, err := in.DB.GetDaemonStatus()
	s.Daemon = ds
	s.State = DaemonState(ds, err == nil, alive, s.Now, in.StalenessWindow)

	if rootID, err := in.DB.GetMeta(statedb.MetaRootFolderID); err == nil {
		s.RootID = rootID
	}
	s.TokenOK = in.tokenPresent()
	s.Items, _ = in.DB.ItemCount()
	s.Pending, _ = in.DB.PendingOpCount()
	s.QFiles, s.QBytes = in.quarantine(filepath.Join(in.ConfigDir, "quarantine"))
	s.BinAvailable, s.BinDest = in.systemBin()
	s.Autostart, s.AutostartErr = in.serviceStatus()

	limit := in.ActivityLimit
	if limit <= 0 {
		limit = DefaultActivityLimit
	}
	s.Activity, _ = in.DB.RecentActivity(limit)
	return s
}

func (in Input) tokenPresent() bool {
	if in.TokenPresent != nil {
		return in.TokenPresent(in.ConfigDir)
	}
	_, err := os.Stat(auth.TokenPath(in.ConfigDir))
	return err == nil
}

func (in Input) quarantine(dir string) (int, int64) {
	if in.Quarantine != nil {
		return in.Quarantine(dir)
	}
	return QuarantineUsage(dir)
}

func (in Input) systemBin() (bool, string) {
	if in.SystemBin != nil {
		return in.SystemBin()
	}
	return trash.Available(), trash.Describe()
}

func (in Input) serviceStatus() (service.State, error) {
	if in.ServiceStatus != nil {
		return in.ServiceStatus()
	}
	return service.Status()
}

// DaemonState classifies the daemon. A live control socket (pingAlive) is
// authoritative; otherwise it falls back to the recorded status and heartbeat
// freshness, which still works when the daemon is down or has no socket.
func DaemonState(ds statedb.DaemonStatus, found, pingAlive bool, now time.Time, staleness time.Duration) string {
	if pingAlive {
		return StateRunning
	}
	if !found || ds.StartedAt == 0 {
		return StateNeverRun
	}
	if !ds.Running {
		return StateStopped
	}
	if staleness > 0 && now.Sub(time.Unix(ds.LastHeartbeatAt, 0)) > staleness {
		return StateStale
	}
	return StateRunning
}

// QuarantineUsage returns file count and total size under dir; zeros if the
// directory does not exist yet.
func QuarantineUsage(dir string) (count int, bytes int64) {
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			count++
			bytes += info.Size()
		}
		return nil
	})
	return count, bytes
}
