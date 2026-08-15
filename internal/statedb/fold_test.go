package statedb

import "testing"

// TestARepeatedEntryFoldsIntoOneRow is what keeps a broken daemon from erasing
// the user's history: a cycle that fails every retry recorded one row per
// retry, and at a backoff capped near ten minutes that fills the 500-row ring
// in a couple of days — which is exactly what the field report showed.
func TestARepeatedEntryFoldsIntoOneRow(t *testing.T) {
	db := openTemp(t)

	const err = "auth: cannot fetch token: 400 invalid_grant"
	for i := 0; i < 300; i++ {
		if e := db.AppendActivity(Activity{TS: int64(1000 + i), Kind: "error", Detail: err}); e != nil {
			t.Fatal(e)
		}
	}

	all, e := db.RecentActivity(50)
	if e != nil {
		t.Fatal(e)
	}
	if len(all) != 1 {
		t.Fatalf("300 identical failures wrote %d rows, want 1", len(all))
	}
	if all[0].Count != 300 {
		t.Errorf("count = %d, want 300", all[0].Count)
	}
	if all[0].TS != 1299 {
		t.Errorf("ts = %d, want the most recent occurrence (1299)", all[0].TS)
	}
}

// TestOnlyTheNewestRowFolds: a genuine event between two repeats ends the run,
// so nothing is ever merged across the history in between — and the history is
// still there to be read.
func TestOnlyTheNewestRowFolds(t *testing.T) {
	db := openTemp(t)

	rows := []Activity{
		{TS: 1, Kind: "error", Detail: "boom"},
		{TS: 2, Kind: "error", Detail: "boom"},
		{TS: 3, Kind: "upload", RelPath: "notes.md", Source: "local"},
		{TS: 4, Kind: "error", Detail: "boom"},
	}
	for _, a := range rows {
		if err := db.AppendActivity(a); err != nil {
			t.Fatal(err)
		}
	}

	all, err := db.RecentActivity(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d rows, want 3 (folded pair, upload, new error)", len(all))
	}
	if all[0].Kind != "error" || all[0].Count != 1 {
		t.Errorf("newest row = %+v, want the un-repeated error after the upload", all[0])
	}
	if all[1].Kind != "upload" {
		t.Errorf("the upload was folded away: %+v", all[1])
	}
	if all[2].Count != 2 {
		t.Errorf("the first two failures did not fold: count = %d", all[2].Count)
	}
}

// TestDifferingEntriesNeverFold: the fold keys on the whole visible entry, so
// two files, two details or two directions stay two rows.
func TestDifferingEntriesNeverFold(t *testing.T) {
	db := openTemp(t)

	rows := []Activity{
		{TS: 1, Kind: "upload", RelPath: "a.txt", Source: "local"},
		{TS: 2, Kind: "upload", RelPath: "b.txt", Source: "local"},
		{TS: 3, Kind: "upload", RelPath: "b.txt", Source: "remote"},
		{TS: 4, Kind: "upload", RelPath: "b.txt", Source: "remote", Detail: "-> c.txt"},
	}
	for _, a := range rows {
		if err := db.AppendActivity(a); err != nil {
			t.Fatal(err)
		}
	}

	all, err := db.RecentActivity(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(rows) {
		t.Fatalf("got %d rows, want %d — distinct entries were folded together", len(all), len(rows))
	}
	for _, a := range all {
		if a.Count != 1 {
			t.Errorf("row %+v has count %d, want 1", a, a.Count)
		}
	}
}
