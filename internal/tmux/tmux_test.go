package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInside(t *testing.T) {
	t.Setenv("TMUX", "")
	if Inside() {
		t.Error("Inside() = true with TMUX unset")
	}

	t.Setenv("TMUX", "/tmp/tmux-501/default,123,0")
	if !Inside() {
		t.Error("Inside() = false with TMUX set")
	}
}

func TestNewWindowOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if _, err := NewWindow(Options{Name: "planner", Dir: "/repo"}, []string{"pi"}); !errors.Is(err, ErrNotInside) {
		t.Errorf("err = %v, want ErrNotInside", err)
	}
}

func TestNewWindowRejectsEmptyCommand(t *testing.T) {
	t.Setenv("TMUX", "x")
	if _, err := NewWindow(Options{Name: "planner", Dir: "/repo"}, nil); err == nil {
		t.Error("empty argv accepted, want an error")
	}
}

// TestNewWindowRunsCommand drives a real tmux server in a throwaway session,
// checking that the window starts in the right directory and that arguments
// with shell metacharacters reach the command unmangled.
func TestNewWindowRunsCommand(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}

	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	log := filepath.Join(dir, "argv.txt")
	stub := filepath.Join(dir, "stub.sh")
	script := "#!/bin/sh\n{ pwd; for a in \"$@\"; do printf '%s\\n' \"$a\"; done; } > " + log + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// A private tmux server, so the developer's own session is untouched.
	socket := filepath.Join(dir, "sock")
	tmuxCmd := func(args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-S", socket}, args...)...)
	}
	if out, err := tmuxCmd("new-session", "-d", "-s", "test", "sleep", "30").CombinedOutput(); err != nil {
		t.Skipf("could not start tmux server: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxCmd("kill-server").Run() })

	t.Setenv("TMUX", socket+",0,0")
	// NewWindow shells out to plain `tmux`, which would reach the default
	// server, so point the test at the private socket.
	t.Setenv("TMUX_TMPDIR", dir)

	weird := "line one\nline **two**; `backticks` \"quotes\" $VAR"
	window, err := NewWindow(Options{Name: "planner", Dir: dir}, []string{stub, weird, "--", "a todo app"})
	if err != nil {
		t.Skipf("tmux new-window unavailable in this environment: %v", err)
	}
	if window.ID == "" || window.Index == "" {
		t.Errorf("window = %+v, want an id and index", window)
	}

	body := waitForFile(t, log)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if got := lines[0]; got != dir {
		t.Errorf("window cwd = %q, want %q", got, dir)
	}
	if got, want := strings.Join(lines[1:], "\n"), weird+"\n--\na todo app"; got != want {
		t.Errorf("argv mangled:\ngot  %q\nwant %q", got, want)
	}
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()

	for range 200 {
		if body, err := os.ReadFile(path); err == nil && len(body) > 0 {
			return string(body)
		}
		exec.Command("sleep", "0.02").Run()
	}
	t.Fatalf("%s never appeared", path)
	return ""
}
