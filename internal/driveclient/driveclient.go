// Package driveclient is a thin wrapper around the Drive v3 API, defined as
// an interface so the rest of the engine can be tested against an in-memory
// fake. Retries with backoff live here; callers see only final outcomes.
package driveclient

import (
	"context"
	"io"
	"time"
)

// FolderMimeType is Drive's mimeType for folders.
const FolderMimeType = "application/vnd.google-apps.folder"

// GoogleAppsPrefix marks Google-native files (Docs, Sheets, ...) which are
// never synced.
const GoogleAppsPrefix = "application/vnd.google-apps"

// File is the subset of Drive file metadata the engine cares about.
type File struct {
	ID       string
	Name     string
	MimeType string
	MD5      string // empty for folders and Google-native files
	Size     int64
	Version  int64
	Parents  []string
	Trashed  bool

	// ModifiedTime is when Drive last saw the file change. Read by exactly one
	// decision — the init merge's conflict naming (spec §11, W18-E) — and the
	// zero value means "not known", which falls back to the clock-free rule.
	ModifiedTime time.Time
}

// IsDir reports whether the file is a Drive folder.
func (f File) IsDir() bool { return f.MimeType == FolderMimeType }

// IsGoogleNative reports whether the file is a Google-native document
// (excluding folders), which synckeeper skips.
func (f File) IsGoogleNative() bool {
	return !f.IsDir() && len(f.MimeType) >= len(GoogleAppsPrefix) && f.MimeType[:len(GoogleAppsPrefix)] == GoogleAppsPrefix
}

// Change is one entry from the changes feed.
type Change struct {
	FileID  string
	Removed bool  // permanently removed (or access lost); File is nil
	File    *File // state after the change, nil when Removed
}

// ChangePage is one page of the changes feed.
type ChangePage struct {
	Changes       []Change
	NextPageToken string // set when more pages follow
	NewStartToken string // set on the last page; persist after processing
}

// About is the subset of Drive's "about" resource synckeeper surfaces: the
// identity of the signed-in Google account.
type About struct {
	Email       string
	DisplayName string
}

// Client is the engine's view of Drive. Implementations: the real API
// wrapper (New) and the in-memory fake (NewFake).
type Client interface {
	// List returns the non-trashed children of the given folder.
	List(ctx context.Context, parentID string) ([]File, error)
	// Get returns metadata for one file id.
	Get(ctx context.Context, fileID string) (File, error)
	// StartPageToken returns the current changes-feed start token.
	StartPageToken(ctx context.Context) (string, error)
	// Changes returns one page of changes starting at pageToken.
	Changes(ctx context.Context, pageToken string) (ChangePage, error)
	// Upload creates a new file under parentID with the given content.
	Upload(ctx context.Context, parentID, name string, content io.Reader, size int64) (File, error)
	// Update replaces the content of an existing file.
	Update(ctx context.Context, fileID string, content io.Reader, size int64) (File, error)
	// Download streams the content of a file.
	Download(ctx context.Context, fileID string) (io.ReadCloser, error)
	// Trash moves a file to Drive trash (never permanent deletion).
	Trash(ctx context.Context, fileID string) error
	// Mkdir creates a folder under parentID.
	Mkdir(ctx context.Context, parentID, name string) (File, error)
	// Move renames and/or reparents a file.
	Move(ctx context.Context, fileID, newParentID, newName string) (File, error)
	// About returns the identity of the signed-in Google account.
	About(ctx context.Context) (About, error)
}

// FindOrCreateFolder returns the id of the first non-trashed folder with the
// given name under parentID, creating it if absent.
func FindOrCreateFolder(ctx context.Context, c Client, parentID, name string) (File, error) {
	children, err := c.List(ctx, parentID)
	if err != nil {
		return File{}, err
	}
	for _, f := range children {
		if f.IsDir() && f.Name == name {
			return f, nil
		}
	}
	return c.Mkdir(ctx, parentID, name)
}
