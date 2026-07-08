package driveclient

// Live smoke test against real Google Drive. Runs only with
// SYNCKEEPER_LIVE_TEST=1 and an initialized machine (token present).
// It creates a throwaway folder at the Drive root and trashes it at the end;
// it never touches the real Synckeeper folder.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/macsimbodnar/synckeeper/internal/auth"
	"github.com/macsimbodnar/synckeeper/internal/config"
)

func TestLiveSmoke(t *testing.T) {
	if os.Getenv("SYNCKEEPER_LIVE_TEST") != "1" {
		t.Skip("set SYNCKEEPER_LIVE_TEST=1 to run against real Drive")
	}
	ctx := context.Background()
	configDir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	ts, err := auth.TokenSource(ctx, configDir)
	if err != nil {
		t.Fatalf("token source (run `synckeeper init` first): %v", err)
	}
	client, err := New(ctx, ts)
	if err != nil {
		t.Fatal(err)
	}

	startToken, err := client.StartPageToken(ctx)
	if err != nil {
		t.Fatal(err)
	}

	folderName := fmt.Sprintf("synckeeper-livetest-%d", time.Now().Unix())
	folder, err := client.Mkdir(ctx, "root", folderName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Trash(ctx, folder.ID); err != nil {
			t.Errorf("cleanup: trash test folder %s: %v", folder.ID, err)
		}
	})
	t.Logf("test folder: %s (%s)", folderName, folder.ID)

	// Upload and verify metadata round trip.
	content := "synckeeper live smoke\n"
	up, err := client.Upload(ctx, folder.ID, "smoke.txt", strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if up.MD5 == "" || up.Size != int64(len(content)) {
		t.Errorf("upload metadata: md5=%q size=%d", up.MD5, up.Size)
	}

	// Download and compare.
	body, err := client.Download(ctx, up.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Errorf("downloaded %q, want %q", raw, content)
	}

	// Update produces a new version with new md5.
	up2, err := client.Update(ctx, up.ID, strings.NewReader("changed content\n"), 16)
	if err != nil {
		t.Fatal(err)
	}
	if up2.Version <= up.Version || up2.MD5 == up.MD5 {
		t.Errorf("update: version %d -> %d, md5 %q -> %q", up.Version, up2.Version, up.MD5, up2.MD5)
	}

	// Move (rename in place).
	moved, err := client.Move(ctx, up.ID, folder.ID, "renamed.txt")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Name != "renamed.txt" {
		t.Errorf("move: name = %q", moved.Name)
	}

	// List sees exactly our file.
	children, err := client.List(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].Name != "renamed.txt" {
		t.Errorf("list = %+v, want just renamed.txt", children)
	}

	// Trash the file; List no longer shows it.
	if err := client.Trash(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	children, err = client.List(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 0 {
		t.Errorf("after trash, list = %+v", children)
	}

	// The changes feed since startToken must mention our file id.
	// Drive's feed can lag slightly; poll briefly.
	deadline := time.Now().Add(30 * time.Second)
	for {
		sawFile := false
		token := startToken
		for {
			page, err := client.Changes(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range page.Changes {
				if c.FileID == up.ID {
					sawFile = true
				}
			}
			if page.NextPageToken == "" {
				break
			}
			token = page.NextPageToken
		}
		if sawFile {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("changes feed never mentioned the uploaded file")
		}
		time.Sleep(3 * time.Second)
	}
}
