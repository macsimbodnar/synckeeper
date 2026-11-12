package statedb

import (
	"database/sql"
	"testing"
)

// S038 round 2 (F1) — RenameItemPath renames exactly the rows at or under the
// old path, byte for byte. SQLite's LIKE is ASCII case-insensitive: a row
// under "D/S" matched the prefix "d/S/" and, its prefix not stripped, kept its
// whole old path under the new one ("d/s/D/S/s.txt", a path nowhere on disk).
func TestS038RenameItemPathIsCaseExact(t *testing.T) {
	db := openTemp(t)
	rows := []Item{
		{DriveFileID: "S", RelPath: "D/S", IsDir: true},
		{DriveFileID: "F", RelPath: "D/S/s.txt"},
		{DriveFileID: "G", RelPath: "d/S/g.txt"},
	}
	if err := db.Tx(func(tx *sql.Tx) error {
		for _, it := range rows {
			if err := UpsertItem(tx, it); err != nil {
				return err
			}
		}
		return RenameItemPath(tx, "d/S", "d/s")
	}); err != nil {
		t.Fatal(err)
	}
	items, err := db.AllItems()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.DriveFileID] = it.RelPath
	}
	want := map[string]string{"S": "D/S", "F": "D/S/s.txt", "G": "d/s/g.txt"}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("row %s at %q, want %q (all: %v)", id, got[id], p, got)
		}
	}
}
