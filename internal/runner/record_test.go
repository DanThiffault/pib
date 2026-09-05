package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"pib/internal/agent"
	"pib/internal/protocol"
	"pib/internal/session"
	"pib/internal/tmux"
)

// startCall is one recorded StartRun.
type startCall struct {
	id     string
	issue  int64
	agent  string
	window string
}

// fakeRecorder stands in for the issue store.
type fakeRecorder struct {
	started  []startCall
	finished map[string]string
	// known stands in for runs recorded before this process started.
	known map[string]string
	err   error
}

func newRecorder() *fakeRecorder {
	return &fakeRecorder{finished: map[string]string{}, known: map[string]string{}}
}

func (f *fakeRecorder) StartRun(id string, issue int64, agent, window string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, startCall{id, issue, agent, window})
	return nil
}

func (f *fakeRecorder) FinishRun(id, status string) error {
	f.finished[id] = status
	return nil
}

func (f *fakeRecorder) RunAgent(id string) (string, error) {
	for _, start := range f.started {
		if start.id == id {
			return start.agent, nil
		}
	}
	return f.known[id], nil
}

// finishing makes the fake window write the exit sidecar a real agent would,
// so the run has an outcome to record. An empty sidecar means the agent left
// none, which is what a window closed by hand looks like.
func finishing(t *testing.T, exit string) {
	t.Helper()

	original := newWindow
	newWindow = func(opts tmux.Options, _ []string) (tmux.Window, error) {
		if path := opts.Env[EnvExitFile]; path != "" && exit != "" {
			if err := os.WriteFile(path, []byte(exit), 0o644); err != nil {
				return tmux.Window{}, err
			}
		}
		return tmux.Window{ID: "@99", Index: "9"}, nil
	}
	t.Cleanup(func() { newWindow = original })
}

func scout(t *testing.T) Runner {
	t.Helper()
	dir := t.TempDir()
	return Runner{
		GitRoot:  dir,
		StateDir: dir,
		Load: func(string) (agent.Definition, error) {
			return agent.Definition{Name: "coder"}, nil
		},
	}
}

func TestSpawnRecordsTheRunAgainstItsIssue(t *testing.T) {
	finishing(t, `{"type":"done"}`)

	recorder := newRecorder()
	r := scout(t)
	r.Record = recorder

	resp, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "implement it", Issue: 7,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(recorder.started) != 1 {
		t.Fatalf("started %d runs, want 1", len(recorder.started))
	}
	start := recorder.started[0]
	if start.issue != 7 {
		t.Errorf("issue = %d, want 7", start.issue)
	}
	if start.agent != "coder" || start.window != "@99" {
		t.Errorf("start = %+v", start)
	}
	if start.id != resp.Session {
		t.Errorf("run id %q does not match the session %q", start.id, resp.Session)
	}

	if got := recorder.finished[start.id]; got != string(session.StatusDone) {
		t.Errorf("finished as %q, want done", got)
	}
}

func TestSpawnWithNoIssueStillRecords(t *testing.T) {
	finishing(t, `{"type":"done"}`)

	recorder := newRecorder()
	r := scout(t)
	r.Record = recorder

	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "explore",
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.started) != 1 || recorder.started[0].issue != 0 {
		t.Errorf("started = %+v, want one run against no issue", recorder.started)
	}
}

func TestAnAgentThatSaysNothingIsRecordedAsUnknown(t *testing.T) {
	// No exit sidecar: the window was closed by hand.
	finishing(t, "")

	recorder := newRecorder()
	r := scout(t)
	r.Record = recorder

	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "implement it", Issue: 3,
	}); err != nil {
		t.Fatal(err)
	}

	id := recorder.started[0].id
	if got := recorder.finished[id]; got != string(session.StatusUnknown) {
		t.Errorf("finished as %q, want unknown", got)
	}
}

func TestARunThatCannotBeRecordedDoesNotStart(t *testing.T) {
	finishing(t, `{"type":"done"}`)

	recorder := newRecorder()
	recorder.err = errors.New("issue #404 does not exist")
	r := scout(t)
	r.Record = recorder

	_, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "implement it", Issue: 404,
	})
	if err == nil {
		t.Fatal("the spawn succeeded against an issue that does not exist")
	}
	if len(recorder.finished) != 0 {
		t.Errorf("finished = %v, want nothing recorded for a run that never started", recorder.finished)
	}
}

func TestResumeContinuesTheSameRun(t *testing.T) {
	finishing(t, `{"type":"done"}`)

	recorder := newRecorder()
	recorder.known["abc123"] = "coder"
	r := scout(t)
	r.Record = recorder

	// A session left behind by an agent that stopped to ask something.
	runDir := filepath.Join(r.StateDir, "runs", "abc123")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "session.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpResume, Session: "abc123", Answer: "use Postgres",
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if len(recorder.started) != 1 {
		t.Fatalf("started = %+v", recorder.started)
	}
	if recorder.started[0].id != "abc123" {
		t.Errorf("run id = %q, want the session it is continuing", recorder.started[0].id)
	}
	if got := recorder.finished["abc123"]; got != string(session.StatusDone) {
		t.Errorf("finished as %q", got)
	}
}

func TestARunnerWithNoRecorderStillWorks(t *testing.T) {
	finishing(t, `{"type":"done"}`)

	r := scout(t)
	resp, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "implement it", Issue: 7,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Status != string(session.StatusDone) {
		t.Errorf("status = %q", resp.Status)
	}
}

func TestTheAgentIsToldWhichIssueItIsOn(t *testing.T) {
	var env map[string]string
	original := newWindow
	newWindow = func(opts tmux.Options, _ []string) (tmux.Window, error) {
		env = opts.Env
		if path := opts.Env[EnvExitFile]; path != "" {
			os.WriteFile(path, []byte(`{"type":"done"}`), 0o644)
		}
		return tmux.Window{ID: "@99"}, nil
	}
	t.Cleanup(func() { newWindow = original })

	r := scout(t)
	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "implement it", Issue: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if env[EnvIssue] != "7" {
		t.Errorf("%s = %q, want 7", EnvIssue, env[EnvIssue])
	}

	// An agent working on nothing in particular is told nothing in
	// particular, rather than being handed a zero.
	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpSpawn, Agent: "coder", Task: "explore",
	}); err != nil {
		t.Fatal(err)
	}
	if _, set := env[EnvIssue]; set {
		t.Errorf("%s = %q, want it unset", EnvIssue, env[EnvIssue])
	}
}

// A resumed agent is still the agent it was. Before this, it was handed its
// own run id as a name — so it signed comments with a hex string.
func TestResumeKeepsTheAgentsIdentity(t *testing.T) {
	var opts tmux.Options
	original := newWindow
	newWindow = func(o tmux.Options, _ []string) (tmux.Window, error) {
		opts = o
		if path := o.Env[EnvExitFile]; path != "" {
			os.WriteFile(path, []byte(`{"type":"done"}`), 0o644)
		}
		return tmux.Window{ID: "@99"}, nil
	}
	t.Cleanup(func() { newWindow = original })

	recorder := newRecorder()
	recorder.known["abc123"] = "coder"
	r := scout(t)
	r.Record = recorder

	runDir := filepath.Join(r.StateDir, "runs", "abc123")
	os.MkdirAll(runDir, 0o755)
	os.WriteFile(filepath.Join(runDir, "session.jsonl"), []byte("{}\n"), 0o644)

	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpResume, Session: "abc123", Answer: "address the review", Name: "coder #4", Issue: 4,
	}); err != nil {
		t.Fatal(err)
	}

	if opts.Env[EnvAgent] != "coder" {
		t.Errorf("%s = %q, want the agent it has always been", EnvAgent, opts.Env[EnvAgent])
	}
	if opts.Env[EnvIssue] != "4" {
		t.Errorf("%s = %q, want 4", EnvIssue, opts.Env[EnvIssue])
	}
	if opts.Name != "coder #4" {
		t.Errorf("window name = %q, want the caller's label", opts.Name)
	}
}

// Without a recorder there is nothing to ask, so it behaves as it always did.
func TestResumeWithoutARecorderFallsBack(t *testing.T) {
	var opts tmux.Options
	original := newWindow
	newWindow = func(o tmux.Options, _ []string) (tmux.Window, error) {
		opts = o
		if path := o.Env[EnvExitFile]; path != "" {
			os.WriteFile(path, []byte(`{"type":"done"}`), 0o644)
		}
		return tmux.Window{ID: "@99"}, nil
	}
	t.Cleanup(func() { newWindow = original })

	r := scout(t)
	runDir := filepath.Join(r.StateDir, "runs", "abc123")
	os.MkdirAll(runDir, 0o755)
	os.WriteFile(filepath.Join(runDir, "session.jsonl"), []byte("{}\n"), 0o644)

	if _, err := r.Run(context.Background(), protocol.Request{
		Op: protocol.OpResume, Session: "abc123", Answer: "carry on",
	}); err != nil {
		t.Fatal(err)
	}
	if opts.Env[EnvAgent] != "abc123" {
		t.Errorf("%s = %q, want the run id as before", EnvAgent, opts.Env[EnvAgent])
	}
}
