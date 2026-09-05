// Package server exposes the pib runner over a unix socket so agents running
// in other windows can ask pib to spawn agents for them.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pib/internal/protocol"
)

// maxSocketPath is the practical limit for a unix socket path. The kernel
// struct holds 104 bytes on macOS and 108 on Linux; staying under the smaller
// figure keeps a repository checked out at a deep path working.
const maxSocketPath = 100

// PointerFileName records the socket pib is actually listening on, so an
// agent started by hand can find it without guessing.
const PointerFileName = "socket"

// Handler carries out a request. An agent operation blocks until the agent
// stops; an issue operation answers immediately.
type Handler interface {
	Run(ctx context.Context, req protocol.Request) (protocol.Response, error)
}

// Router sends agent operations to one handler and issue operations to
// another, so the transport stays unaware of either.
type Router struct {
	// Agents runs spawn and resume.
	Agents Handler
	// Issues runs the plan and issue operations. A nil Issues rejects them,
	// which is what a pib with no store open should do.
	Issues Handler
}

// Run dispatches by operation.
func (r Router) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	if req.Op.IsAgent() {
		if r.Agents == nil {
			return protocol.Response{}, fmt.Errorf("this pib cannot run agents")
		}
		return r.Agents.Run(ctx, req)
	}
	if r.Issues == nil {
		return protocol.Response{}, fmt.Errorf("this pib has no issue store open")
	}
	return r.Issues.Run(ctx, req)
}

// Server accepts one request per connection and holds the connection open
// until the agent it started stops.
type Server struct {
	path     string
	pointer  string
	listener net.Listener
	handler  Handler

	wg sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

// Path picks a bindable socket path for a workspace. It prefers a socket
// inside the workspace, which is easy to find and cleaned up with it, and
// falls back to a short path keyed by the repository when that would be too
// long for the kernel to bind.
func Path(stateDir string) string {
	inWorkspace := filepath.Join(stateDir, protocol.SocketName)
	if len(inWorkspace) <= maxSocketPath {
		return inWorkspace
	}

	sum := sha256.Sum256([]byte(stateDir))
	name := fmt.Sprintf("pib-%x.sock", sum[:6])

	for _, dir := range []string{os.TempDir(), "/tmp"} {
		if candidate := filepath.Join(dir, name); len(candidate) <= maxSocketPath {
			return candidate
		}
	}

	return filepath.Join("/tmp", name)
}

// Discover reports the socket a workspace's pib is listening on.
func Discover(stateDir string) (string, error) {
	body, err := os.ReadFile(filepath.Join(stateDir, PointerFileName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// Listen starts serving on the workspace socket. A socket left behind by a
// previous run is replaced, since a unix socket cannot be bound twice.
func Listen(stateDir string, h Handler) (*Server, error) {
	path := Path(stateDir)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	pointer := filepath.Join(stateDir, PointerFileName)
	if err := os.WriteFile(pointer, []byte(path+"\n"), 0o644); err != nil {
		listener.Close()
		os.Remove(path)
		return nil, err
	}

	s := &Server{path: path, pointer: pointer, listener: listener, handler: h}
	s.wg.Add(1)
	go s.accept()

	return s, nil
}

// Addr is the path the server is listening on.
func (s *Server) Addr() string {
	return s.path
}

// Close stops accepting and waits for in-flight agents to be released.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	err := s.listener.Close()
	s.wg.Wait()
	os.Remove(s.path)
	os.Remove(s.pointer)
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

// handle reads one request and replies once the agent has stopped. The client
// closing the connection cancels the run, so an abandoned agent does not keep
// a window open forever.
func (s *Server) handle(conn net.Conn) {
	var req protocol.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResponse(conn, protocol.Response{Error: "could not read request: " + err.Error()})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if req.Op != protocol.OpSpawnBackground {
		go watchClose(conn, cancel)
	}

	resp, err := s.handler.Run(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		resp = protocol.Response{Error: err.Error()}
	}

	writeResponse(conn, resp)
}

// watchClose cancels the run when the client disconnects. The extension never
// sends a second message, so any read result means the connection is done.
func watchClose(conn net.Conn, cancel context.CancelFunc) {
	buf := make([]byte, 1)
	for {
		if _, err := conn.Read(buf); err != nil {
			cancel()
			return
		}
	}
}

func writeResponse(conn net.Conn, resp protocol.Response) {
	body, err := json.Marshal(resp)
	if err != nil {
		return
	}
	conn.Write(append(body, '\n'))
}

// removeStale clears a socket file that no live pib is listening on, and
// refuses to touch one that is still in use.
func removeStale(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	conn, err := net.Dial("unix", path)
	if err == nil {
		conn.Close()
		return errors.New("another pib is already listening on " + path)
	}

	return os.Remove(path)
}
