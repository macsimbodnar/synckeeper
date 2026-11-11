package reconcile

import "testing"

// S023: both sides renamed one tracked file to the same new name (the R25
// shape: baseline row at the old path, the local file and the remote item
// both at the new one). The row is still three-way — the baseline says which
// side changed the content. Was: any md5 difference between the two sides was
// a conflict, so a local edit lost the canonical name to Drive's older bytes.

func TestS023DoubleRenameLocalEditUploads(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"new/q.txt": locFile("Y", 4)},                          // renamed and edited here
		Remote: map[string]RemoteItem{"new/q.txt": remIn(remFile("F", "X", 3, 2), "newID")}, // only renamed there
	}
	runPlan(t, in, []step{{t: UpdateRemote, rel: "new/q.txt", fileID: "F"}})
	plan, _ := Plan(in)
	want := RemoteExpect{ParentID: "newID", Name: "q.txt", MD5: "X"}
	if plan[0].RemoteWant == nil || *plan[0].RemoteWant != want {
		t.Errorf("update pins %+v, want %+v", plan[0].RemoteWant, want)
	}
}

// The reviewer's shape: the row is S011's honest record of a partial upload —
// content md5 = Drive md5 = the partial bytes, local stat unknown (size -1).
// The complete file at the new name is a local change; Drive's partial bytes,
// renamed by the other machine, are this machine's own write, unchanged.
func TestS023DoubleRenameAfterPartialUploadUploadsCompleteFile(t *testing.T) {
	partial := BaseItem{FileID: "F", Size: -1, MD5: "PARTIAL", DriveMD5: "PARTIAL", DriveVersion: 2}
	runPlan(t, Input{
		Base:   map[string]BaseItem{"video.mkv": partial},
		Local:  map[string]LocalItem{"final.mkv": locFile("WHOLE", 20)},
		Remote: map[string]RemoteItem{"final.mkv": remFile("F", "PARTIAL", 14, 3)},
	}, []step{{t: UpdateRemote, rel: "final.mkv", fileID: "F"}})
}

// Control: both sides edited too, to different content — still a conflict,
// both copies kept, remote keeping the canonical name.
func TestS023DoubleRenameDoubleEditStillConflicts(t *testing.T) {
	cp := "new/q (conflict test_box 2026-07-08_103000).txt"
	runPlan(t, Input{
		Base:   map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"new/q.txt": locFile("Y", 4)},
		Remote: map[string]RemoteItem{"new/q.txt": remFile("F", "Z", 5, 2)},
	}, []step{
		{t: ConflictBackup, rel: "new/q.txt", newRel: cp},
		{t: Upload, rel: cp},
		{t: Download, rel: "new/q.txt", fileID: "F"},
	})
}

// The mirror row: only the remote edited. The local file is the baseline's
// content, so the download replaces it, pinned to the scanned stat (§7) —
// no conflict copy of bytes Drive already holds as the previous revision.
func TestS023DoubleRenameRemoteEditDownloads(t *testing.T) {
	in := Input{
		Base:   map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"new/q.txt": locFileM("X", 3, 2000)},
		Remote: map[string]RemoteItem{"new/q.txt": remFile("F", "Z", 5, 2)},
	}
	runPlan(t, in, []step{{t: Download, rel: "new/q.txt", fileID: "F"}})
	plan, _ := Plan(in)
	if a := plan[0]; !a.LocalExists || a.LocalSize != 3 || a.LocalMtimeNS != 2000 || a.MD5 != "Z" {
		t.Errorf("download = %+v, want it to expect the scanned local file and verify md5 Z", a)
	}
}

// Both edited to the same bytes: record, no transfer (§4.2's same-md5 row).
func TestS023DoubleRenameSameEditRecords(t *testing.T) {
	runPlan(t, Input{
		Base:   map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:  map[string]LocalItem{"new/q.txt": locFile("Y", 4)},
		Remote: map[string]RemoteItem{"new/q.txt": remFile("F", "Y", 4, 2)},
	}, []step{{t: Record, rel: "new/q.txt", fileID: "F"}})
}

// The path holds (S013/S014) still come first: the new name is unreadable
// here, so nothing is decided for the row on either side.
func TestS023DoubleRenameHeldAtNewPathPlansNothing(t *testing.T) {
	in := Input{
		Base:            map[string]BaseItem{"old/p.txt": baseFile("F", "X", 3)},
		Local:           map[string]LocalItem{},
		Remote:          map[string]RemoteItem{"new/q.txt": remFile("F", "X", 3, 2)},
		UnscannedLocal:  map[string]string{"new/q.txt": "permission denied"},
		UnreadableFiles: map[string]LocalItem{"new/q.txt": locFile("", 4)},
	}
	runPlan(t, in, nil)
	_, skips := Plan(in)
	if len(skips) != 1 || skips[0].RelPath != "old/p.txt" {
		t.Errorf("skips = %+v, want the row held at old/p.txt", skips)
	}
}
