// Package control is the local IPC between the watch daemon and the CLI: a
// Unix-domain socket speaking one line-delimited JSON request/response per
// connection. It carries commands that must reach the running process
// (sync-now, pause, resume, reload) — read-only monitoring goes through the
// state DB instead. There is no network listener; the socket's 0600
// permissions are the whole access-control model.
package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ProtocolVersion is bumped on any breaking change to the request/response
// shape so a mismatched CLI and daemon fail loudly instead of misparsing.
const ProtocolVersion = 1

// Command names.
const (
	CmdPing   = "ping"
	CmdSync   = "sync"
	CmdPause  = "pause"
	CmdResume = "resume"
	CmdReload = "reload"
)

// Request is one command sent to the daemon.
type Request struct {
	V    int             `json:"v"`
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Response is the daemon's single reply.
type Response struct {
	V     int             `json:"v"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Handler processes a request into a response. It runs in its own goroutine
// per connection.
type Handler func(ctx context.Context, req Request) Response

// dialTimeout and callTimeout bound client connects and round-trips so the
// CLI never hangs on a wedged daemon.
const (
	dialTimeout = 2 * time.Second
	callTimeout = 30 * time.Second
)

// Serve accepts connections on ln and dispatches each to h until ctx is
// cancelled, at which point the listener is closed and Serve returns nil.
func Serve(ctx context.Context, ln net.Listener, h Handler) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(ctx, conn, h)
	}
}

func serveConn(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(callTimeout))

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResponse(conn, Response{V: ProtocolVersion, OK: false, Error: "malformed request: " + err.Error()})
		return
	}
	if req.V != ProtocolVersion {
		writeResponse(conn, Response{V: ProtocolVersion, OK: false,
			Error: fmt.Sprintf("protocol version mismatch (daemon v%d, client v%d); rebuild so both match", ProtocolVersion, req.V)})
		return
	}
	resp := h(ctx, req)
	resp.V = ProtocolVersion
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		b, _ = json.Marshal(Response{V: ProtocolVersion, OK: false, Error: "marshal response: " + err.Error()})
	}
	conn.Write(append(b, '\n'))
}

// Call dials the socket, sends one request, and returns the reply. A dial
// error means the daemon isn't listening (see IsNotRunning).
func Call(socketPath string, req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(callTimeout))

	req.V = ProtocolVersion
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("unreadable daemon response: %w", err)
	}
	if resp.V != ProtocolVersion {
		return resp, fmt.Errorf("protocol version mismatch (client v%d, daemon v%d); rebuild so both match", ProtocolVersion, resp.V)
	}
	return resp, nil
}

// IsNotRunning reports whether err from Call means no daemon is listening (no
// socket file, or a stale socket with nothing accepting) rather than a real
// protocol failure.
func IsNotRunning(err error) bool {
	if err == nil {
		return false
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return false // reachable but slow: that's a real problem, not "down"
	}
	// Dial failures (ENOENT, ECONNREFUSED) surface as *net.OpError on "dial".
	var oerr *net.OpError
	if errors.As(err, &oerr) && oerr.Op == "dial" {
		return true
	}
	return false
}
