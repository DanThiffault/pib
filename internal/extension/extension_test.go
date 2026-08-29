package extension_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pib/internal/extension"
	"pib/internal/protocol"
	"pib/internal/server"
)

// harness drives the real extension under node with a stub pi, so the
// TypeScript is exercised against the real Go socket server.
const harness = `
import ext from "%EXT%";

const tools = {};
const pi = { registerTool: (t) => { tools[t.name] = t; } };
ext(pi);

const [name, argsJson] = process.argv.slice(2);
const tool = tools[name];
if (!tool) {
  console.log(JSON.stringify({ missing: Object.keys(tools) }));
  process.exit(0);
}

const ctx = { shutdown: () => {} };
try {
  const result = await tool.execute("call-1", JSON.parse(argsJson), undefined, undefined, ctx);
  console.log(JSON.stringify({ ok: result?.content?.[0]?.text ?? "" }));
} catch (error) {
  console.log(JSON.stringify({ error: String(error?.message ?? error) }));
}
`

type fakeRunner struct {
	resp     protocol.Response
	requests chan protocol.Request
}

func (f *fakeRunner) Run(_ context.Context, req protocol.Request) (protocol.Response, error) {
	f.requests <- req
	return f.resp, nil
}

type outcome struct {
	OK      string   `json:"ok"`
	Error   string   `json:"error"`
	Missing []string `json:"missing"`
}

func runTool(t *testing.T, env map[string]string, tool string, args any) outcome {
	t.Helper()
	return runToolIn(t, "", env, tool, args)
}

// runToolIn runs the tool with a working directory, which is how the socket
// pointer file is found.
func runToolIn(t *testing.T, dir string, env map[string]string, tool string, args any) outcome {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}

	// Install from the embedded copy, which is what pib actually ships.
	extPath, err := extension.Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(t.TempDir(), "harness.mjs")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(harness, "%EXT%", extPath)), 0o644); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", script, tool, string(body))
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if ok := asExit(err, &exitErr); ok {
			t.Fatalf("node failed: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatal(err)
	}

	var got outcome
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unreadable harness output %q: %v", out, err)
	}
	if got.Missing != nil {
		t.Fatalf("tool %q not registered; got %v", tool, got.Missing)
	}
	return got
}

func asExit(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func TestPibToolRoundTripsThroughSocket(t *testing.T) {
	fake := &fakeRunner{
		resp:     protocol.Response{Status: "done", Text: "found three contexts", Session: "run-1"},
		requests: make(chan protocol.Request, 1),
	}
	srv, err := server.Listen(t.TempDir(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	got := runTool(t,
		map[string]string{"PIB_SOCKET": srv.Addr(), "PIB_AGENT": "planner"},
		"pib",
		map[string]string{"agent": "scout", "task": "map the codebase"})

	if got.Error != "" {
		t.Fatalf("tool errored: %s", got.Error)
	}
	if !strings.Contains(got.OK, "found three contexts") {
		t.Errorf("result = %q, want the agent's answer", got.OK)
	}

	req := <-fake.requests
	if req.Op != protocol.OpSpawn || req.Agent != "scout" || req.Task != "map the codebase" {
		t.Errorf("request = %+v, want the spawn passed through", req)
	}
	if req.Caller != "planner" {
		t.Errorf("Caller = %q, want it taken from PIB_AGENT", req.Caller)
	}
}

func TestPibToolPassesTheIssueItIsWorkingOn(t *testing.T) {
	fake := &fakeRunner{
		resp:     protocol.Response{Status: "done", Text: "implemented"},
		requests: make(chan protocol.Request, 1),
	}
	srv, err := server.Listen(t.TempDir(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	got := runTool(t,
		map[string]string{"PIB_SOCKET": srv.Addr()},
		"pib",
		map[string]any{"agent": "worker", "task": "implement it", "issue": 7})

	if got.Error != "" {
		t.Fatalf("tool errored: %s", got.Error)
	}

	req := <-fake.requests
	if req.Issue != 7 {
		t.Errorf("Issue = %d, want 7", req.Issue)
	}
}

func TestPibToolLeavesTheIssueOutWhenThereIsNone(t *testing.T) {
	fake := &fakeRunner{
		resp:     protocol.Response{Status: "done", Text: "mapped it"},
		requests: make(chan protocol.Request, 1),
	}
	srv, err := server.Listen(t.TempDir(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if got := runTool(t,
		map[string]string{"PIB_SOCKET": srv.Addr()},
		"pib",
		map[string]string{"agent": "scout", "task": "map the codebase"}); got.Error != "" {
		t.Fatalf("tool errored: %s", got.Error)
	}

	if req := <-fake.requests; req.Issue != 0 {
		t.Errorf("Issue = %d, want none", req.Issue)
	}
}

func TestPibToolSurfacesNeedsInput(t *testing.T) {
	fake := &fakeRunner{
		resp:     protocol.Response{Status: "needs_input", Text: "which database?", Session: "run-2"},
		requests: make(chan protocol.Request, 1),
	}
	srv, err := server.Listen(t.TempDir(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	got := runTool(t, map[string]string{"PIB_SOCKET": srv.Addr()}, "pib",
		map[string]string{"agent": "scout", "task": "explore"})

	if !strings.Contains(got.OK, "which database?") || !strings.Contains(got.OK, "run-2") {
		t.Errorf("result = %q, want the question and the session to resume", got.OK)
	}
}

func TestPibToolResume(t *testing.T) {
	fake := &fakeRunner{
		resp:     protocol.Response{Status: "done", Text: "postgres it is"},
		requests: make(chan protocol.Request, 1),
	}
	srv, err := server.Listen(t.TempDir(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	runTool(t, map[string]string{"PIB_SOCKET": srv.Addr()}, "pib",
		map[string]string{"session": "run-2", "answer": "postgres"})

	req := <-fake.requests
	if req.Op != protocol.OpResume || req.Session != "run-2" || req.Answer != "postgres" {
		t.Errorf("request = %+v, want a resume", req)
	}
}

// With no PIB_SOCKET the extension has to find the socket itself, which is how
// a pi session started by hand reaches pib.
func TestPibToolFindsSocketFromPointerFile(t *testing.T) {
	fake := &fakeRunner{
		resp:     protocol.Response{Status: "done", Text: "found it"},
		requests: make(chan protocol.Request, 1),
	}

	// A deep workspace forces the socket out of .pib, which is exactly when
	// the hardcoded fallback would fail.
	repo := t.TempDir()
	workspace := filepath.Join(repo, ".pib")
	deep := filepath.Join(workspace, strings.Repeat("nested-directory/", 8))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := server.Listen(deep, fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// pib writes the pointer beside the workspace it bound for.
	pointer, err := server.Discover(deep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, server.PointerFileName), []byte(pointer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runToolIn(t, repo, map[string]string{"PIB_SOCKET": ""}, "pib",
		map[string]string{"agent": "scout", "task": "explore"})

	if got.Error != "" {
		t.Fatalf("tool errored instead of reading the pointer: %s", got.Error)
	}
	if !strings.Contains(got.OK, "found it") {
		t.Errorf("result = %q, want the agent's answer", got.OK)
	}
}

// The error path we chose: no listener means a clear message, not a hang.
func TestPibToolWithoutPibRunning(t *testing.T) {
	got := runTool(t,
		map[string]string{"PIB_SOCKET": filepath.Join(t.TempDir(), "absent.sock")},
		"pib",
		map[string]string{"agent": "scout", "task": "explore"})

	if !strings.Contains(got.Error, "pib is not running") {
		t.Errorf("error = %q, want it to say pib is not running", got.Error)
	}
}

func TestPibToolRejectsEmptyCall(t *testing.T) {
	got := runTool(t, map[string]string{"PIB_SOCKET": "/nonexistent"}, "pib", map[string]string{})
	if got.Error == "" {
		t.Error("empty call accepted, want an error naming the required fields")
	}
}

// The control tools only exist inside an agent pib started.
func TestControlToolsAbsentForCallers(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}

	extPath, err := extension.Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "h.mjs")
	os.WriteFile(script, []byte(strings.ReplaceAll(harness, "%EXT%", extPath)), 0o644)

	cmd := exec.Command("node", script, "pib_done", "{}")
	cmd.Env = append(os.Environ(), "PIB_EXIT_FILE=")
	out, _ := cmd.Output()

	if !strings.Contains(string(out), "missing") {
		t.Errorf("pib_done was registered without PIB_EXIT_FILE: %s", out)
	}
}

func TestPibDoneWritesSidecar(t *testing.T) {
	exitFile := filepath.Join(t.TempDir(), "exit.json")
	runTool(t, map[string]string{"PIB_EXIT_FILE": exitFile}, "pib_done", map[string]string{})

	body, err := os.ReadFile(exitFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); !strings.Contains(got, `"done"`) {
		t.Errorf("sidecar = %s, want a done marker", got)
	}
}

func TestPibAskWritesQuestion(t *testing.T) {
	exitFile := filepath.Join(t.TempDir(), "exit.json")
	runTool(t, map[string]string{"PIB_EXIT_FILE": exitFile}, "pib_ask",
		map[string]string{"question": "which database?"})

	body, err := os.ReadFile(exitFile)
	if err != nil {
		t.Fatal(err)
	}
	var exit map[string]string
	if err := json.Unmarshal(body, &exit); err != nil {
		t.Fatal(err)
	}
	if exit["type"] != "ask" || exit["message"] != "which database?" {
		t.Errorf("sidecar = %v, want the question", exit)
	}
}
