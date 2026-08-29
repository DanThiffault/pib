// Package runner spawns agents in tmux windows and waits for them to stop,
// turning what they leave behind into a result the caller can act on.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"pib/internal/agent"
	"pib/internal/protocol"
	"pib/internal/session"
	"pib/internal/tmux"
)

// ControlTools are handed to every child on top of its own allowlist. pi
// applies --tools to extension tools too, so without this a restricted agent
// such as scout (read, bash) could never report that it had finished.
var ControlTools = []string{"pib_done", "pib_ask"}

// Env var names passed to a child.
const (
	EnvExitFile = "PIB_EXIT_FILE"
	EnvSocket   = "PIB_SOCKET"
	EnvAgent    = "PIB_AGENT"
)

// pollInterval is how often a running window is checked.
const pollInterval = 250 * time.Millisecond

// newWindow is a variable so tests can see exactly how a child would be
// started without needing a terminal.
var newWindow = tmux.NewWindow

// Runner spawns agents for one repository.
type Runner struct {
	// GitRoot is the working directory agents run in.
	GitRoot string
	// StateDir holds per-run directories, normally <git root>/.pib.
	StateDir string
	// ExtensionPath is the pib extension every agent loads.
	ExtensionPath string
	// SocketPath lets a child reach pib to spawn agents of its own.
	SocketPath string
	// Load resolves an agent definition. Defaults to agent.Load by name.
	Load func(name string) (agent.Definition, error)
}

// ErrSelfSpawn reports an agent trying to spawn itself.
var ErrSelfSpawn = errors.New("an agent cannot spawn itself")

// Run carries out a spawn or resume request and blocks until the agent stops.
func (r Runner) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	switch req.Op {
	case protocol.OpSpawn:
		return r.spawn(ctx, req)
	case protocol.OpResume:
		return r.resume(ctx, req)
	default:
		return protocol.Response{}, fmt.Errorf("unknown op %q", req.Op)
	}
}

func (r Runner) spawn(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	if req.Agent == "" {
		return protocol.Response{}, errors.New("no agent named")
	}
	if req.Caller != "" && req.Caller == req.Agent {
		return protocol.Response{}, fmt.Errorf("%w: %s", ErrSelfSpawn, req.Agent)
	}

	def, err := r.load(req.Agent)
	if err != nil {
		return protocol.Response{}, err
	}

	runID, err := newRunID()
	if err != nil {
		return protocol.Response{}, err
	}
	runDir := filepath.Join(r.StateDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return protocol.Response{}, err
	}

	args := []string{"--session-dir", runDir}
	args = append(args, def.Flags(agent.Options{
		ExtraTools: ControlTools,
		Extensions: []string{r.ExtensionPath},
	})...)
	args = append(args, "--", req.Task)

	name := req.Name
	if name == "" {
		name = def.Name
	}

	return r.await(ctx, runDir, name, append([]string{agent.Executable}, args...))
}

func (r Runner) resume(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	if req.Session == "" {
		return protocol.Response{}, errors.New("no session given")
	}

	runDir := filepath.Join(r.StateDir, "runs", filepath.Base(req.Session))
	transcript, err := session.FindTranscript(runDir)
	if err != nil {
		return protocol.Response{}, err
	}
	if transcript == "" {
		return protocol.Response{}, fmt.Errorf("no session to resume for %q", req.Session)
	}

	// The previous answer is stale once the agent is resumed.
	if err := os.Remove(filepath.Join(runDir, session.ExitFileName)); err != nil && !os.IsNotExist(err) {
		return protocol.Response{}, err
	}

	argv := []string{agent.Executable, "--session", transcript, "--", req.Answer}
	return r.await(ctx, runDir, filepath.Base(runDir), argv)
}

// await opens the window, waits for it to close, and reads the result. If the
// caller goes away the window is killed rather than left orphaned.
func (r Runner) await(ctx context.Context, runDir, name string, argv []string) (protocol.Response, error) {
	window, err := newWindow(tmux.Options{
		Name: name,
		Dir:  r.GitRoot,
		Env: map[string]string{
			EnvExitFile: filepath.Join(runDir, session.ExitFileName),
			EnvSocket:   r.SocketPath,
			EnvAgent:    name,
		},
		Background: true,
	}, argv)
	if err != nil {
		return protocol.Response{}, err
	}

	if err := waitForWindow(ctx, window.ID); err != nil {
		tmux.Kill(window.ID)
		return protocol.Response{}, err
	}

	result, err := session.Collect(runDir)
	if err != nil {
		return protocol.Response{}, err
	}

	return protocol.Response{
		Status:  string(result.Status),
		Text:    result.Text,
		Session: filepath.Base(runDir),
	}, nil
}

func waitForWindow(ctx context.Context, id string) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !tmux.Alive(id) {
				return nil
			}
		}
	}
}

func (r Runner) load(name string) (agent.Definition, error) {
	if r.Load != nil {
		return r.Load(name)
	}

	path, err := agent.Path(name)
	if err != nil {
		return agent.Definition{}, err
	}
	return agent.Load(path)
}

func newRunID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
