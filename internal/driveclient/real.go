package driveclient

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/oauth2"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// fileFields is the metadata set the engine needs, per docs/spec.md.
const fileFields = "id, name, mimeType, md5Checksum, size, version, parents, trashed"

const uploadChunkSize = 8 * 1024 * 1024

// real implements Client against the Drive v3 API.
type real struct {
	svc *drive.Service
}

// New builds a Client backed by the real Drive API.
func New(ctx context.Context, ts oauth2.TokenSource) (Client, error) {
	svc, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("create drive service: %w", err)
	}
	return &real{svc: svc}, nil
}

func fromAPI(f *drive.File) File {
	return File{
		ID:       f.Id,
		Name:     f.Name,
		MimeType: f.MimeType,
		MD5:      f.Md5Checksum,
		Size:     f.Size,
		Version:  f.Version,
		Parents:  f.Parents,
		Trashed:  f.Trashed,
	}
}

func (r *real) List(ctx context.Context, parentID string) ([]File, error) {
	var out []File
	query := fmt.Sprintf("'%s' in parents and trashed = false", parentID)
	pageToken := ""
	for {
		var page *drive.FileList
		err := withRetry(ctx, func() error {
			var err error
			page, err = r.svc.Files.List().
				Q(query).
				Fields(googleapi.Field("nextPageToken, files(" + fileFields + ")")).
				PageSize(1000).
				PageToken(pageToken).
				Context(ctx).Do()
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("list children of %s: %w", parentID, err)
		}
		for _, f := range page.Files {
			out = append(out, fromAPI(f))
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}

func (r *real) Get(ctx context.Context, fileID string) (File, error) {
	var f *drive.File
	err := withRetry(ctx, func() error {
		var err error
		f, err = r.svc.Files.Get(fileID).Fields(fileFields).Context(ctx).Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("get %s: %w", fileID, err)
	}
	return fromAPI(f), nil
}

func (r *real) StartPageToken(ctx context.Context) (string, error) {
	var resp *drive.StartPageToken
	err := withRetry(ctx, func() error {
		var err error
		resp, err = r.svc.Changes.GetStartPageToken().Context(ctx).Do()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("get start page token: %w", err)
	}
	return resp.StartPageToken, nil
}

func (r *real) Changes(ctx context.Context, pageToken string) (ChangePage, error) {
	var resp *drive.ChangeList
	err := withRetry(ctx, func() error {
		var err error
		resp, err = r.svc.Changes.List(pageToken).
			IncludeRemoved(true).
			Fields(googleapi.Field("nextPageToken, newStartPageToken, changes(fileId, removed, file(" + fileFields + "))")).
			PageSize(1000).
			Context(ctx).Do()
		return err
	})
	if err != nil {
		return ChangePage{}, fmt.Errorf("list changes: %w", err)
	}
	page := ChangePage{
		NextPageToken: resp.NextPageToken,
		NewStartToken: resp.NewStartPageToken,
	}
	for _, c := range resp.Changes {
		ch := Change{FileID: c.FileId, Removed: c.Removed}
		if c.File != nil {
			f := fromAPI(c.File)
			ch.File = &f
		}
		page.Changes = append(page.Changes, ch)
	}
	return page, nil
}

func (r *real) Upload(ctx context.Context, parentID, name string, content io.Reader, size int64) (File, error) {
	meta := &drive.File{Name: name, Parents: []string{parentID}}
	call := r.svc.Files.Create(meta).Fields(fileFields).Context(ctx)
	if size > 5*1024*1024 {
		call = call.Media(content, googleapi.ChunkSize(uploadChunkSize))
	} else {
		call = call.Media(content)
	}
	// No withRetry around the whole upload: the media reader is consumed on
	// the first attempt. Resumable uploads retry chunks internally; hard
	// failures stay in pending_ops for the next run.
	f, err := call.Do()
	if err != nil {
		return File{}, fmt.Errorf("upload %s: %w", name, err)
	}
	return fromAPI(f), nil
}

func (r *real) Update(ctx context.Context, fileID string, content io.Reader, size int64) (File, error) {
	call := r.svc.Files.Update(fileID, nil).Fields(fileFields).Context(ctx)
	if size > 5*1024*1024 {
		call = call.Media(content, googleapi.ChunkSize(uploadChunkSize))
	} else {
		call = call.Media(content)
	}
	f, err := call.Do()
	if err != nil {
		return File{}, fmt.Errorf("update %s: %w", fileID, err)
	}
	return fromAPI(f), nil
}

func (r *real) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	var body io.ReadCloser
	err := withRetry(ctx, func() error {
		resp, err := r.svc.Files.Get(fileID).Context(ctx).Download()
		if err != nil {
			return err
		}
		body = resp.Body
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", fileID, err)
	}
	return body, nil
}

func (r *real) Trash(ctx context.Context, fileID string) error {
	err := withRetry(ctx, func() error {
		_, err := r.svc.Files.Update(fileID, &drive.File{Trashed: true}).Context(ctx).Do()
		return err
	})
	if err != nil {
		return fmt.Errorf("trash %s: %w", fileID, err)
	}
	return nil
}

func (r *real) Mkdir(ctx context.Context, parentID, name string) (File, error) {
	meta := &drive.File{Name: name, MimeType: FolderMimeType, Parents: []string{parentID}}
	var f *drive.File
	err := withRetry(ctx, func() error {
		var err error
		f, err = r.svc.Files.Create(meta).Fields(fileFields).Context(ctx).Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("mkdir %s: %w", name, err)
	}
	return fromAPI(f), nil
}

func (r *real) About(ctx context.Context) (About, error) {
	var a *drive.About
	err := withRetry(ctx, func() error {
		var err error
		// about.get requires an explicit fields mask or the API rejects it;
		// we only ever want the signed-in user's identity.
		a, err = r.svc.About.Get().Fields("user(emailAddress,displayName)").Context(ctx).Do()
		return err
	})
	if err != nil {
		return About{}, fmt.Errorf("get about: %w", err)
	}
	out := About{}
	if a.User != nil {
		out.Email = a.User.EmailAddress
		out.DisplayName = a.User.DisplayName
	}
	return out, nil
}

func (r *real) Move(ctx context.Context, fileID, newParentID, newName string) (File, error) {
	current, err := r.Get(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	call := r.svc.Files.Update(fileID, &drive.File{Name: newName}).Fields(fileFields).Context(ctx)
	if len(current.Parents) > 0 && (len(current.Parents) != 1 || current.Parents[0] != newParentID) {
		call = call.AddParents(newParentID).RemoveParents(current.Parents[0])
	}
	var f *drive.File
	err = withRetry(ctx, func() error {
		var err error
		f, err = call.Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("move %s: %w", fileID, err)
	}
	return fromAPI(f), nil
}
