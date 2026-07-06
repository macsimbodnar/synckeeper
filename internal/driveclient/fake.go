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
)

// FakeRootID is the id of the fake's Drive root ("My Drive").
const FakeRootID = "root"

// Fake is an in-memory Client used by tests. Phase 0 ships the structural
// operations (list/get/mkdir/trash/upload/download/move); full changes-feed
// semantics land in phase 1 with the sync engine.
type Fake struct {
	mu     sync.Mutex
	nextID int
	files  map[string]*fakeFile
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
		return nil, fmt.Errorf("fake drive: no file %s", id)
	}
	return file, nil
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

func (f *Fake) StartPageToken(_ context.Context) (string, error) {
	return "fake-token-1", nil
}

func (f *Fake) Changes(_ context.Context, pageToken string) (ChangePage, error) {
	// Full changes-feed semantics arrive in phase 1.
	return ChangePage{NewStartToken: pageToken}, nil
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
			ID:       f.newID(),
			Name:     name,
			MimeType: "application/octet-stream",
			MD5:      hex.EncodeToString(sum[:]),
			Size:     int64(len(data)),
			Version:  1,
			Parents:  []string{parentID},
		},
		content: data,
	}
	f.files[file.ID] = file
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
	file, err := f.get(fileID)
	if err != nil {
		return err
	}
	file.Trashed = true
	file.Version++
	return nil
}

func (f *Fake) Mkdir(_ context.Context, parentID, name string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(parentID); err != nil {
		return File{}, err
	}
	file := &fakeFile{File: File{
		ID:       f.newID(),
		Name:     name,
		MimeType: FolderMimeType,
		Version:  1,
		Parents:  []string{parentID},
	}}
	f.files[file.ID] = file
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
	return file.File, nil
}
