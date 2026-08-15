package watch

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/statedb"
)

// driveAuthError is what a revoked refresh token produced in the field
// (2026-08-15): the message plus the whole HTTP response body, five lines.
const driveAuthError = "Get \"https://www.googleapis.com/drive/v3/changes?pageToken=204212\": " +
	"auth: cannot fetch token: 400\nResponse: {\n  \"error\": \"invalid_grant\",\n  " +
	"\"error_description\": \"Token has been expired or revoked.\"\n}"

func recorderDB(t *testing.T) *statedb.DB {
	t.Helper()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestStoredTextIsOneLine: the daemon flattens on the way into the database,
// so no reader has to. A five-line error stored as five lines is what pushed
// the dashboard off its own screen — every view budgets in rows.
func TestStoredTextIsOneLine(t *testing.T) {
	db := recorderDB(t)
	r := &recorder{db: db}

	r.recordError(errors.New(driveAuthError))
	acts, err := db.RecentActivity(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(acts))
	}
	if strings.ContainsAny(acts[0].Detail, "\n\r\t") {
		t.Errorf("stored detail is not one line: %q", acts[0].Detail)
	}
	if !strings.Contains(acts[0].Detail, "cannot fetch token: 400") {
		t.Errorf("flattening lost the message: %q", acts[0].Detail)
	}

	r.cycleDone(nil, time.Second, errors.New(driveAuthError), ModeBackoff, time.Now(), false, "")
	ds, err := db.GetDaemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(ds.LastError, "\n\r\t") {
		t.Errorf("stored last_error is not one line: %q", ds.LastError)
	}
}

// TestStoredTextIsBounded: an error carrying a whole response body is clipped
// before it reaches the database — the state db is not a log.
func TestStoredTextIsBounded(t *testing.T) {
	db := recorderDB(t)
	r := &recorder{db: db}

	r.recordError(errors.New(strings.Repeat("x", 5000)))
	acts, err := db.RecentActivity(1)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(acts[0].Detail)); n != maxStoredText {
		t.Errorf("stored detail is %d runes, want %d", n, maxStoredText)
	}
}
