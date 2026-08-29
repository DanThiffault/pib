package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pib/internal/protocol"
)

// fakeRunner records requests and returns a canned response, optionally after
// blocking until its context is cancelled.
type fakeRunner struct {
	resp     protocol.Response
	err      error
	block    bool
	requests chan protocol.Request
	released chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	f.requests <- req
	if f.block {
		<-ctx.Done()
		close(f.released)
		return protocol.Response{}, ctx.Err()
	}
	return f.resp, f.err
}

func newFake() *fakeRunner {
	return &fakeRunner{requests: make(chan protocol.Request, 4), released: make(chan struct{})}
}

func listen(t *testing.T, h Handler) *Server {
	t.Helper()

	srv, err := Listen(t.TempDir(), h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func call(t *testing.T, srv *Server, req protocol.Request) protocol.Response {
	t.Helper()

	conn, err := net.Dial("unix", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRoundTrip(t *testing.T) {
	fake := newFake()
	fake.resp = protocol.Response{Status: "done", Text: "found it", Session: "abc"}
	srv := listen(t, fake)

	got := call(t, srv, protocol.Request{Op: protocol.OpSpawn, Agent: "scout", Task: "explore"})

	if got.Status != "done" || got.Text != "found it" || got.Session != "abc" {
		t.Errorf("response = %+v, want the runner's result", got)
	}

	req := <-fake.requests
	if req.Agent != "scout" || req.Task != "explore" {
		t.Errorf("request = %+v, want it passed through", req)
	}
}

func TestRunnerErrorBecomesResponseError(t *testing.T) {
	fake := newFake()
	fake.err = context.DeadlineExceeded
	srv := listen(t, fake)

	if got := call(t, srv, protocol.Request{Op: protocol.OpSpawn, Agent: "scout"}); got.Error == "" {
		t.Errorf("response = %+v, want an error", got)
	}
}

// An abandoned caller must not leave the agent running.
func TestDisconnectCancelsRun(t *testing.T) {
	fake := newFake()
	fake.block = true
	srv := listen(t, fake)

	conn, err := net.Dial("unix", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(protocol.Request{Op: protocol.OpSpawn, Agent: "scout"}); err != nil {
		t.Fatal(err)
	}

	<-fake.requests
	conn.Close()

	select {
	case <-fake.released:
	case <-time.After(5 * time.Second):
		t.Fatal("run was not cancelled when the caller disconnected")
	}
}

func TestConcurrentCalls(t *testing.T) {
	fake := newFake()
	fake.resp = protocol.Response{Status: "done"}
	srv := listen(t, fake)

	done := make(chan protocol.Response, 3)
	for range 3 {
		go func() { done <- call(t, srv, protocol.Request{Op: protocol.OpSpawn, Agent: "scout"}) }()
	}

	for range 3 {
		select {
		case resp := <-done:
			if resp.Status != "done" {
				t.Errorf("response = %+v", resp)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent calls did not all complete")
		}
	}
}

func TestSecondListenerRefused(t *testing.T) {
	dir := t.TempDir()
	first, err := Listen(dir, newFake())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := Listen(dir, newFake()); err == nil {
		t.Error("second Listen succeeded, want it refused while the first is live")
	}
}

// A socket left behind by a crashed pib must not block the next run.
func TestStaleSocketReplaced(t *testing.T) {
	dir := t.TempDir()

	first, err := Listen(dir, newFake())
	if err != nil {
		t.Fatal(err)
	}
	first.listener.Close() // die without cleaning up

	second, err := Listen(dir, newFake())
	if err != nil {
		t.Fatalf("stale socket blocked a new listener: %v", err)
	}
	second.Close()
}

func TestCloseRemovesSocket(t *testing.T) {
	dir := t.TempDir()
	srv, err := Listen(dir, newFake())
	if err != nil {
		t.Fatal(err)
	}
	addr := srv.Addr()
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := net.Dial("unix", addr); err == nil {
		t.Error("socket still accepts connections after Close")
	}
	if _, err := os.Stat(filepath.Join(dir, PointerFileName)); !os.IsNotExist(err) {
		t.Error("pointer file survived Close")
	}
}

// A repository checked out at a deep path would otherwise exceed the kernel's
// socket path limit, which surfaces only as "bind: invalid argument".
func TestLongWorkspacePathStillBinds(t *testing.T) {
	deep := filepath.Join(t.TempDir(), strings.Repeat("nested-directory/", 8))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if len(filepath.Join(deep, protocol.SocketName)) <= maxSocketPath {
		t.Fatalf("test path is not long enough to exercise the fallback")
	}

	fake := newFake()
	fake.resp = protocol.Response{Status: "done"}
	srv, err := Listen(deep, fake)
	if err != nil {
		t.Fatalf("Listen failed on a deep path: %v", err)
	}
	defer srv.Close()

	if len(srv.Addr()) > maxSocketPath {
		t.Errorf("bound path %q is still too long", srv.Addr())
	}

	// The pointer file is how anything else finds the relocated socket.
	found, err := Discover(deep)
	if err != nil {
		t.Fatal(err)
	}
	if found != srv.Addr() {
		t.Errorf("Discover = %q, want %q", found, srv.Addr())
	}

	if got := call(t, srv, protocol.Request{Op: protocol.OpSpawn, Agent: "scout"}); got.Status != "done" {
		t.Errorf("relocated socket served %+v, want the runner's result", got)
	}
}
