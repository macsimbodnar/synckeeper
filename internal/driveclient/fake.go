package driveclient

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"
)

// FakeRootID is the id of the fake's Drive root ("My Drive").
const FakeRootID = "root"

// Fake is an in-memory Client used by tests: ids, versions, md5, trash,
// and a changes feed. Page tokens are integer offsets into the change log.
type Fake struct {
	mu         sync.Mutex
	nextID     int
	files      map[string]*fakeFile
	log        []Change
	holding    bool  // the changes feed is withheld past holdAt (W16-E1)
	holdAt     int   // change-log offset the feed stops reporting at
	trashCalls int   // API calls, so a test can prove a folder went in ONE
	AboutInfo  About // returned by About; tests set it to simulate an account

	// Now stamps ModifiedTime on every mutation, as Drive's own clock does.
	// nil means time.Now; a test pins it to make one side of a conflict
	// demonstrably older than the other (W18-E).
	Now func() time.Time
}

// now is the fake Drive's clock. Callers must hold f.mu.
func (f *Fake) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// TrashCount is how many Trash calls the client has made — the measure of
// whether a deleted folder cost one API call or one per file (W14-M4).
func (f *Fake) TrashCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trashCalls
}

type fakeFile struct {
	File
	content []byte
}

// NewFake returns an empty fake Drive containing only the root folder.
func NewFake() *Fake {
	f := &Fake{files: map[string]*fakeFile{}}
	f.files[FakeRootID] = &fakeFile{File: File{ID: FakeRootID, Name: "My Drive", MimeType: FolderMimeType}}
	return f
}

var _ Client = (*Fake)(nil)

func (f *Fake) newID() string {
	f.nextID++
	return "fake-" + strconv.Itoa(f.nextID)
}

func (f *Fake) get(id string) (*fakeFile, error) {
	file, ok := f.files[id]
	if !ok {
		// A sentinel, not a string: callers branch on "gone" versus "something
		// went wrong", and the real client maps Drive's 404 onto the same one.
		return nil, notFoundf("file", id)
	}
	return file, nil
}

// Forget removes a file from the fake entirely, as if it had been purged from
// Drive — every call naming it then answers ErrNotFound. Tests use it for the
// states a trash cannot model: a root folder that is gone rather than binned,
// and a parent a crashed run's journal still names.
func (f *Fake) Forget(fileID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, fileID)
}

// logChange appends the file's current state to the changes feed.
// Callers must hold f.mu.
func (f *Fake) logChange(file *fakeFile) {
	snapshot := file.File
	f.log = append(f.log, Change{FileID: snapshot.ID, File: &snapshot})
}

func (f *Fake) List(_ context.Context, parentID string) ([]File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(parentID); err != nil {
		return nil, err
	}
	var out []File
	for _, file := range f.files {
		if file.Trashed {
			continue
		}
		for _, p := range file.Parents {
			if p == parentID {
				out = append(out, file.File)
			}
		}
	}
	// Deterministic order for tests.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *Fake) Get(_ context.Context, fileID string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := f.get(fileID)
	if err != nil {
		return File{}, err
	}
	return file.File, nil
}

// HoldChanges withholds everything the changes feed has not reported yet:
// while held, Changes stops at the log offset the hold was taken at, and
// mutations keep appending behind it. Releasing makes them visible again.
//
// This models Drive's eventual consistency, which the fake otherwise lacks —
// an upload used to be instantly visible in the feed, so the window between
// our own write and the feed reporting it did not exist in tests. That gap is
// exactly what let W16 ship (decisions.md 2026-07-30).
func (f *Fake) HoldChanges(hold bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if hold && !f.holding {
		f.holdAt = len(f.log)
	}
	f.holding = hold
}

// visibleLog is the part of the change log the feed currently reports.
// Callers must hold f.mu.
func (f *Fake) visibleLog() int {
	if f.holding {
		return f.holdAt
	}
	return len(f.log)
}

func (f *Fake) StartPageToken(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// The visible horizon, not len(log): a token handed out past withheld
	// changes would skip them for good once the hold lifts.
	return strconv.Itoa(f.visibleLog()), nil
}

func (f *Fake) Changes(_ context.Context, pageToken string) (ChangePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	offset, err := strconv.Atoi(pageToken)
	if err != nil || offset < 0 || offset > len(f.log) {
		return ChangePage{}, fmt.Errorf("fake drive: bad page token %q", pageToken)
	}
	end := f.visibleLog()
	if offset > end { // a token from before the hold was taken
		end = offset
	}
	page := ChangePage{NewStartToken: strconv.Itoa(end)}
	for _, c := range f.log[offset:end] {
		snapshot := *c.File
		page.Changes = append(page.Changes, Change{FileID: c.FileID, Removed: c.Removed, File: &snapshot})
	}
	return page, nil
}

func (f *Fake) Upload(_ context.Context, parentID, name string, content io.Reader, _ int64) (File, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(parentID); err != nil {
		return File{}, err
	}
	sum := md5.Sum(data)
	file := &fakeFile{
		File: File{
			ID:           f.newID(),
			Name:         name,
			MimeType:     "application/octet-stream",
			MD5:          hex.EncodeToString(sum[:]),
			Size:         int64(len(data)),
			Version:      1,
			Parents:      []string{parentID},
			ModifiedTime: f.now(),
		},
		content: data,
	}
	f.files[file.ID] = file
	f.logChange(file)
	return file.File, nil
}

func (f *Fake) Update(_ context.Context, fileID string, content io.Reader, _ int64) (File, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := f.get(fileID)
	if err != nil {
		return File{}, err
	}
	sum := md5.Sum(data)
	file.content = data
	file.MD5 = hex.EncodeToString(sum[:])
	file.Size = int64(len(data))
	file.Version++
	file.ModifiedTime = f.now()
	f.logChange(file)
	return file.File, nil
}

func (f *Fake) Download(_ context.Context, fileID string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := f.get(fileID)
	if err != nil {
		return nil, err
	}
	if file.IsDir() {
		return nil, fmt.Errorf("fake drive: %s is a folder", fileID)
	}
	return io.NopCloser(bytes.NewReader(file.content)), nil
}

func (f *Fake) Trash(_ context.Context, fileID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trashCalls++
	file, err := f.get(fileID)
	if err != nil {
		return err
	}
	file.Trashed = true
	file.Version++
	f.logChange(file)
	return nil
}

func (f *Fake) Mkdir(_ context.Context, parentID, name string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(parentID); err != nil {
		return File{}, err
	}
	file := &fakeFile{File: File{
		ID:           f.newID(),
		Name:         name,
		MimeType:     FolderMimeType,
		Version:      1,
		Parents:      []string{parentID},
		ModifiedTime: f.now(),
	}}
	f.files[file.ID] = file
	f.logChange(file)
	return file.File, nil
}

func (f *Fake) Move(_ context.Context, fileID, newParentID, newName string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := f.get(fileID)
	if err != nil {
		return File{}, err
	}
	if _, err := f.get(newParentID); err != nil {
		return File{}, err
	}
	file.Name = newName
	file.Parents = []string{newParentID}
	file.Version++
	file.ModifiedTime = f.now()
	f.logChange(file)
	return file.File, nil
}

// AboutInfo is what About returns; tests set it to simulate a signed-in
// account. The zero value (empty email) models Drive returning no identity.
func (f *Fake) About(_ context.Context) (About, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AboutInfo, nil
}
