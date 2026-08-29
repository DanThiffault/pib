package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"pib/internal/agent"
	"pib/internal/protocol"
	"pib/internal/tmux"
)

func testRunner(t *testing.T, defs map[string]agent.Definition) Runner {
	t.Helper()

	dir := t.TempDir()
	return Runner{
		GitRoot:  dir,
		StateDir: dir,
		Load: func(name string) (agent.Definition, error) {
			def, ok := defs[name]
			if !ok {
				return agent.Definition{}, errors.New("no such agent: " + name)
			}
			return def, nil
		},
	}
}

func TestRefusesSelfSpawn(t *testing.T) {
	r := testRunner(t, map[string]agent.Definition{"planner": {Name: "planner"}})

	_, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "planner", Caller: "planner",
	})
	if !errors.Is(err, ErrSelfSpawn) {
		t.Errorf("err = %v, want ErrSelfSpawn", err)
	}
}

func TestUnknownAgent(t *testing.T) {
	r := testRunner(t, nil)

	_, err := r.Run(context.Background(), protocol.Request{Op: protocol.OpSpawn, Agent: "ghost"})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want it to name the missing agent", err)
	}
}

func TestUnknownOp(t *testing.T) {
	r := testRunner(t, nil)

	if _, err := r.Run(context.Background(), protocol.Request{Op: "explode"}); err == nil {
		t.Error("unknown op accepted, want an error")
	}
}

func TestSpawnRequiresAgent(t *testing.T) {
	r := testRunner(t, nil)

	if _, err := r.Run(context.Background(), protocol.Request{Op: protocol.OpSpawn}); err == nil {
		t.Error("spawn without an agent accepted, want an error")
	}
}

func TestResumeRequiresKnownSession(t *testing.T) {
	r := testRunner(t, nil)

	if _, err := r.Run(context.Background(), protocol.Request{Op: protocol.OpResume}); err == nil {
		t.Error("resume without a session accepted, want an error")
	}
	if _, err := r.Run(context.Background(), protocol.Request{Op: protocol.OpResume, Session: "nope"}); err == nil {
		t.Error("resume of an unknown session accepted, want an error")
	}
}

// A restricted agent must still be able to report that it finished, so the
// control tools are added on top of its own allowlist.
func TestChildAllowlistGainsControlTools(t *testing.T) {
	scout := agent.Definition{Name: "scout", Tools: []string{"read", "bash"}}

	flags := scout.Flags(agent.Options{ExtraTools: ControlTools, Extensions: []string{"/x/pib.ts"}})
	joined := strings.Join(flags, " ")

	if !strings.Contains(joined, "--tools read,bash,pib_done,pib_ask") {
		t.Errorf("flags = %q, want the control tools appended", joined)
	}
	if !strings.Contains(joined, "--extension /x/pib.ts") {
		t.Errorf("flags = %q, want the extension loaded", joined)
	}
}

// An agent with no allowlist already has everything enabled; introducing one
// would disable the rest of its tools.
func TestNoAllowlistStaysOpen(t *testing.T) {
	open := agent.Definition{Name: "open"}

	if joined := strings.Join(open.Flags(agent.Options{ExtraTools: ControlTools}), " "); strings.Contains(joined, "--tools") {
		t.Errorf("flags = %q, want no allowlist introduced", joined)
	}
}

// capture replaces window creation so the child's command line can be
// inspected without a terminal.
func capture(t *testing.T) *[]struct {
	opts tmux.Options
	argv []string
} {
	t.Helper()

	var seen []struct {
		opts tmux.Options
		argv []string
	}
	original := newWindow
	newWindow = func(opts tmux.Options, argv []string) (tmux.Window, error) {
		seen = append(seen, struct {
			opts tmux.Options
			argv []string
		}{opts, argv})
		return tmux.Window{ID: "@99", Index: "9"}, nil
	}
	t.Cleanup(func() { newWindow = original })

	return &seen
}

func TestSpawnBuildsChildCommand(t *testing.T) {
	windows := capture(t)

	dir := t.TempDir()
	r := Runner{
		GitRoot:       dir,
		StateDir:      dir,
		ExtensionPath: "/x/pib.ts",
		SocketPath:    "/x/pib.sock",
		Load: func(string) (agent.Definition, error) {
			return agent.Definition{
				Name:  "scout",
				Tools: []string{"read", "bash"},
				Model: "openrouter/moonshotai/kimi-k2.6",
				Body:  "You are a scout.",
			}, nil
		},
	}

	resp, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "scout", Name: "Scout", Task: "map the code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Session == "" {
		t.Error("no session returned; the caller could not resume this agent")
	}

	if len(*windows) != 1 {
		t.Fatalf("opened %d windows, want 1", len(*windows))
	}
	got := (*windows)[0]
	joined := strings.Join(got.argv, " ")

	if got.argv[0] != agent.Executable {
		t.Errorf("argv[0] = %q, want %q", got.argv[0], agent.Executable)
	}
	for _, want := range []string{
		"--session-dir " + filepath.Join(dir, "runs", resp.Session),
		"--tools read,bash,pib_done,pib_ask",
		"--extension /x/pib.ts",
		"--append-system-prompt",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q:\n%v", want, got.argv)
		}
	}
	if last := got.argv[len(got.argv)-1]; last != "map the code" {
		t.Errorf("last arg = %q, want the task", last)
	}
	if sep := got.argv[len(got.argv)-2]; sep != "--" {
		t.Errorf("arg before task = %q, want --", sep)
	}

	// tmux spawns from the server's environment, so anything the child needs
	// has to be passed through explicitly.
	if got.opts.Env[EnvExitFile] != filepath.Join(dir, "runs", resp.Session, "exit.json") {
		t.Errorf("%s = %q", EnvExitFile, got.opts.Env[EnvExitFile])
	}
	if got.opts.Env[EnvSocket] != "/x/pib.sock" {
		t.Errorf("%s = %q, want the socket so the child can spawn too", EnvSocket, got.opts.Env[EnvSocket])
	}
	// The window is titled "Scout", but the agent is "scout" — the name it
	// signs comments with and is checked against when it spawns.
	if got.opts.Env[EnvAgent] != "scout" {
		t.Errorf("%s = %q, want the definition name", EnvAgent, got.opts.Env[EnvAgent])
	}
	if got.opts.Name != "Scout" {
		t.Errorf("window name = %q, want the display name", got.opts.Name)
	}
	if got.opts.Dir != dir {
		t.Errorf("Dir = %q, want the git root", got.opts.Dir)
	}
	if !got.opts.Background {
		t.Error("child stole focus; want it opened in the background")
	}
}
