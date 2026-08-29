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
	"strconv"
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
	// EnvIssue is the issue an agent was started for, when it was started
	// for one. It saves an agent depending on whoever wrote its task to
	// have mentioned the number.
	EnvIssue = "PIB_ISSUE"
)

// pollInterval is how often a running window is checked.
const pollInterval = 250 * time.Millisecond

// newWindow is a variable so tests can see exactly how a child would be
// started without needing a terminal.
var newWindow = tmux.NewWindow

// Recorder notes agent runs, so pib can tell which issues are being worked
// on and offer the window an agent is in. It is optional: a runner without
// one simply spawns agents and keeps no history.
type Recorder interface {
	StartRun(id string, issue int64, agent, window string) error
	FinishRun(id, status string) error
	// RunAgent names the agent a run belongs to, so a resumed session is
	// itself again rather than a session id.
	RunAgent(id string) (string, error)
}

// run identifies the agent run being recorded.
type run struct {
	id    string
	issue int64
	agent string
}

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
	// Record notes runs as they start and finish. Optional.
	Record Recorder
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

	return r.await(ctx,
		run{id: runID, issue: req.Issue, agent: def.Name},
		runDir, name, append([]string{agent.Executable}, args...))
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

	// A resumed agent carries on the run it stopped in the middle of, so it
	// keeps the same id rather than starting a second one — and it is still
	// the same agent, which the run record is what remembers.
	id := filepath.Base(runDir)
	name := r.agentOf(id)

	window := req.Name
	if window == "" {
		window = name
	}

	argv := []string{agent.Executable, "--session", transcript, "--", req.Answer}
	return r.await(ctx, run{id: id, issue: req.Issue, agent: name}, runDir, window, argv)
}

// agentOf names the agent a run belongs to. Without a recorder there is
// nothing to ask, so the run id stands in — which is what it always did.
func (r Runner) agentOf(id string) string {
	if r.Record == nil {
		return id
	}
	name, err := r.Record.RunAgent(id)
	if err != nil || name == "" {
		return id
	}
	return name
}

// await opens the window, waits for it to close, and reads the result. If the
// caller goes away the window is killed rather than left orphaned.
func (r Runner) await(ctx context.Context, info run, runDir, name string, argv []string) (protocol.Response, error) {
	window, err := newWindow(tmux.Options{
		Name:       name,
		Dir:        r.GitRoot,
		Env:        childEnv(runDir, r.SocketPath, info.agent, info.issue),
		Background: true,
	}, argv)
	if err != nil {
		return protocol.Response{}, err
	}

	// A run pib cannot record is a run it would lose track of, so the window
	// goes rather than being left working untracked. This is what rejects a
	// request naming an issue that does not exist.
	if r.Record != nil {
		if err := r.Record.StartRun(info.id, info.issue, info.agent, window.ID); err != nil {
			tmux.Kill(window.ID)
			return protocol.Response{}, err
		}

		// outcome is how the run gets recorded. It stays unknown unless the
		// agent gets far enough to say otherwise — a killed window and a
		// crash both land there.
		outcome := session.StatusUnknown
		defer func() {
			// Best effort: the agent's answer matters more than the
			// bookkeeping, and a run left open is closed out when pib next
			// opens the store.
			_ = r.Record.FinishRun(info.id, string(outcome))
		}()

		return r.wait(ctx, window.ID, runDir, &outcome)
	}

	var ignored session.Status
	return r.wait(ctx, window.ID, runDir, &ignored)
}

// childEnv is what an agent is told about itself. EnvAgent is the definition
// name rather than the window title: it is what the agent signs comments
// with, and what self-spawning is checked against, so a caller's choice of
// display name must not change it.
func childEnv(runDir, socket, agentName string, issue int64) map[string]string {
	env := map[string]string{
		EnvExitFile: filepath.Join(runDir, session.ExitFileName),
		EnvSocket:   socket,
		EnvAgent:    agentName,
	}
	if issue != 0 {
		env[EnvIssue] = strconv.FormatInt(issue, 10)
	}
	return env
}

// wait blocks until the agent's window closes and reads what it left behind,
// reporting the outcome through outcome so a deferred recorder can see it.
func (r Runner) wait(ctx context.Context, windowID, runDir string, outcome *session.Status) (protocol.Response, error) {
	if err := waitForWindow(ctx, windowID); err != nil {
		tmux.Kill(windowID)
		return protocol.Response{}, err
	}

	result, err := session.Collect(runDir)
	if err != nil {
		return protocol.Response{}, err
	}
	*outcome = result.Status

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
