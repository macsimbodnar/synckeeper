package statedb

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func open(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	db := open(t, path)
	v, err := db.GetMeta(MetaSchemaVersion)
	if err != nil || v != strconv.Itoa(len(migrations)) {
		t.Fatalf("schema_version = %q, %v; want %d", v, err, len(migrations))
	}
	if err := db.SetMeta(MetaRootFolderID, "folder123"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db2 := open(t, path)
	got, err := db2.GetMeta(MetaRootFolderID)
	if err != nil || got != "folder123" {
		t.Fatalf("after reopen root_folder_id = %q, %v; want folder123", got, err)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "state.db"))
	if _, err := db.GetMeta("absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := db.SetMeta("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("k", "v2"); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMeta("k")
	if err != nil || got != "v2" {
		t.Fatalf("GetMeta = %q, %v; want v2", got, err)
	}
}

func TestCounts(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "state.db"))
	n, err := db.ItemCount()
	if err != nil || n != 0 {
		t.Fatalf("ItemCount = %d, %v; want 0", n, err)
	}
	p, err := db.PendingOpCount()
	if err != nil || p != 0 {
		t.Fatalf("PendingOpCount = %d, %v; want 0", p, err)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	if err := db.SetMeta(MetaSchemaVersion, "999"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("want error opening db with newer schema, got nil")
	}
}

// R5 regression (2026-07-17, testing.md): read-only commands run without the
// instance lock, so their open must never migrate the schema under a live
// daemon (spec §14). OpenRead accepts only an exact version match, refuses
// skew in either direction with guidance, and never creates a missing DB.
func TestR5OpenReadNeverMigrates(t *testing.T) {
	// Exact version: reads work.
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	if err := db.SetDaemonStatus(DaemonStatus{Running: true, PID: 42}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	rdb, err := OpenRead(path)
	if err != nil {
		t.Fatal(err)
	}
	if ds, err := rdb.GetDaemonStatus(); err != nil || ds.PID != 42 {
		t.Fatalf("GetDaemonStatus = %+v, %v; want PID 42", ds, err)
	}
	rdb.Close()

	// Older schema: refused, and the version marker must stay untouched.
	oldPath := filepath.Join(t.TempDir(), "old.db")
	odb := open(t, oldPath)
	if err := odb.SetMeta(MetaSchemaVersion, "2"); err != nil {
		t.Fatal(err)
	}
	odb.Close()
	if _, err := OpenRead(oldPath); err == nil {
		t.Fatal("want refusal opening an older-schema db read-only, got nil")
	}
	raw, err := sql.Open("sqlite", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v string
	if err := raw.QueryRow(`select value from meta where key = ?`, MetaSchemaVersion).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "2" {
		t.Fatalf("schema_version = %q after OpenRead; the read path migrated the db", v)
	}

	// Newer schema: refused.
	newPath := filepath.Join(t.TempDir(), "new.db")
	ndb := open(t, newPath)
	if err := ndb.SetMeta(MetaSchemaVersion, "999"); err != nil {
		t.Fatal(err)
	}
	ndb.Close()
	if _, err := OpenRead(newPath); err == nil {
		t.Fatal("want refusal opening a newer-schema db read-only, got nil")
	}

	// Missing db: refused without creating the file.
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := OpenRead(missing); err == nil {
		t.Fatal("want error opening a missing db read-only, got nil")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("OpenRead created %s (stat err %v); it must never create a db", missing, err)
	}
}
