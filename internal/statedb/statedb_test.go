package statedb

import (
	"errors"
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
