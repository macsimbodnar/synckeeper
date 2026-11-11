package reconcile

import "testing"

// S010: every remote mutation the planner emits pins the Drive state it was
// planned against (spec §7 remote write gate). An action without RemoteWant
// runs unchecked in the executor — that is how a pre-S010 journal row stays
// replayable — so a planner path that forgot it would silently reopen F04.
// One case per emission site.

func remIn(r RemoteItem, parent string) RemoteItem {
	r.ParentID = parent
	return r
}

func TestRemoteMutationsPinTheirPlannedDriveState(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		typ  Type
		rel  string
		want RemoteExpect
	}{
		{
			name: "UpdateRemote: local edit",
			in: Input{
				Base:   map[string]BaseItem{"d/a.txt": baseFile("f1", "m1", 3)},
				Local:  map[string]LocalItem{"d/a.txt": locFile("m2", 4)},
				Remote: map[string]RemoteItem{"d": remIn(remDir("d1", 1), "root"), "d/a.txt": remIn(remFile("f1", "m1", 3, 1), "d1")},
			},
			typ: UpdateRemote, rel: "d/a.txt",
			want: RemoteExpect{ParentID: "d1", Name: "a.txt", MD5: "m1"},
		},
		{
			name: "TrashRemote: local file delete",
			in: Input{
				Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
				Local:  map[string]LocalItem{},
				Remote: map[string]RemoteItem{"a.txt": remIn(remFile("f1", "m1", 3, 1), "root")},
			},
			typ: TrashRemote, rel: "a.txt",
			want: RemoteExpect{ParentID: "root", Name: "a.txt", MD5: "m1"},
		},
		{
			name: "MoveRemote: local file rename",
			in: Input{
				Base:   map[string]BaseItem{"a.txt": baseFile("f1", "m1", 3)},
				Local:  map[string]LocalItem{"b.txt": locFile("m1", 3)},
				Remote: map[string]RemoteItem{"a.txt": remIn(remFile("f1", "m1", 3, 1), "root")},
			},
			typ: MoveRemote, rel: "a.txt",
			want: RemoteExpect{ParentID: "root", Name: "a.txt", MD5: "m1"},
		},
		{
			name: "MoveRemote: local folder rename",
			in: Input{
				Base: map[string]BaseItem{
					"Docs":         baseDir("d1"),
					"Docs/one.txt": baseFile("f1", "m1", 3),
				},
				Local: map[string]LocalItem{
					"Papers":         locDir(),
					"Papers/one.txt": locFile("m1", 3),
				},
				Remote: map[string]RemoteItem{
					"Docs":         remIn(remDir("d1", 1), "root"),
					"Docs/one.txt": remIn(remFile("f1", "m1", 3, 1), "d1"),
				},
			},
			typ: MoveRemote, rel: "Docs",
			want: RemoteExpect{ParentID: "root", Name: "Docs", IsDir: true},
		},
		{
			name: "TrashRemote: local folder delete",
			in: Input{
				Base:   map[string]BaseItem{"Docs": baseDir("d1")},
				Local:  map[string]LocalItem{},
				Remote: map[string]RemoteItem{"Docs": remIn(remDir("d1", 1), "root")},
			},
			typ: TrashRemote, rel: "Docs",
			want: RemoteExpect{ParentID: "root", Name: "Docs", IsDir: true},
		},
		{
			name: "MoveRemote: init merge, Drive copy steps aside",
			in: Input{
				Base:        map[string]BaseItem{},
				Local:       map[string]LocalItem{"notes.txt": locFileM("local-md5", 5, newer)},
				Remote:      map[string]RemoteItem{"notes.txt": remIn(remFileAt("r1", "remote-md5", 7, 1, older), "root")},
				PreferNewer: true,
			},
			typ: MoveRemote, rel: "notes.txt",
			want: RemoteExpect{ParentID: "root", Name: "notes.txt", MD5: "remote-md5"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.Machine, c.in.Now = "test_box", testNow
			plan, _ := Plan(c.in)
			found := false
			for _, a := range plan {
				switch a.Type {
				case UpdateRemote, TrashRemote, MoveRemote:
				default:
					continue
				}
				if a.RemoteWant == nil {
					t.Errorf("%s %s carries no expected Drive state", a.Type, a.RelPath)
					continue
				}
				if a.Type == c.typ && a.RelPath == c.rel {
					found = true
					if *a.RemoteWant != c.want {
						t.Errorf("%s %s pins %+v, want %+v", a.Type, a.RelPath, *a.RemoteWant, c.want)
					}
				}
			}
			if !found {
				t.Fatalf("plan has no %s %s: %+v", c.typ, c.rel, plan)
			}
		})
	}
}
