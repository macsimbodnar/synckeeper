package control

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortSocket returns a socket path short enough to stay under the ~104-char
// sun_path limit (the long $TMPDIR under /var/folders would overflow it).
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "skctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "c.sock")
}

func serveTest(t *testing.T, h Handler) string {
	t.Helper()
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); Serve(ctx, ln, h) }()
	t.Cleanup(func() { cancel(); <-done })
	return sock
}

func TestCallRoundTrip(t *testing.T) {
	sock := serveTest(t, func(_ context.Context, req Request) Response {
		if req.Cmd != CmdPing {
			return Response{OK: false, Error: "unexpected cmd " + req.Cmd}
		}
		data, _ := json.Marshal(map[string]any{"pong": true})
		return Response{OK: true, Data: data}
	})

	resp, err := Call(sock, Request{Cmd: CmdPing})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("not ok: %s", resp.Error)
	}
	var got map[string]bool
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatal(err)
	}
	if !got["pong"] {
		t.Fatalf("unexpected data: %s", resp.Data)
	}
}

func TestServerRejectsVersionMismatch(t *testing.T) {
	sock := serveTest(t, func(_ context.Context, _ Request) Response {
		return Response{OK: true}
	})

	// Hand-write a request with a bad version, bypassing Call's stamping.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	conn.Write([]byte(`{"v":999,"cmd":"ping"}` + "\n"))
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	var resp Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("expected version mismatch to be rejected")
	}
}

func TestIsNotRunning(t *testing.T) {
	// No socket file at all -> dial fails -> "not running".
	_, err := Call(shortSocket(t), Request{Cmd: CmdPing})
	if err == nil || !IsNotRunning(err) {
		t.Fatalf("want not-running, got %v", err)
	}
}
