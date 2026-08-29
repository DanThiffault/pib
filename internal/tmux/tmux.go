// Package tmux opens and tracks windows in the tmux session pib is running
// inside.
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// ErrNotInside reports that pib is not running inside a tmux session, so
// there is no session to add a window to.
var ErrNotInside = errors.New("not running inside tmux")

// Window identifies a window pib created.
type Window struct {
	// ID is tmux's stable window id, e.g. "@3". It survives renumbering.
	ID string
	// Index is the position shown in the status bar, e.g. "2".
	Index string
}

// Options configures a new window.
type Options struct {
	Name string
	Dir  string
	// Env is set in the new window. tmux spawns windows from the server's
	// environment, not pib's, so anything the child needs must be passed
	// here explicitly.
	Env map[string]string
	// Background leaves the current window focused.
	Background bool
}

// Inside reports whether pib is running inside a tmux session.
func Inside() bool {
	return os.Getenv("TMUX") != ""
}

// Available reports whether tmux is installed.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// NewWindow opens a window running argv. tmux execs argv directly rather than
// through a shell, so arguments containing quotes, newlines, or shell
// metacharacters arrive intact.
func NewWindow(opts Options, argv []string) (Window, error) {
	if !Inside() {
		return Window{}, ErrNotInside
	}
	if len(argv) == 0 {
		return Window{}, errors.New("no command given")
	}

	args := []string{"new-window", "-P", "-F", "#{window_id} #{window_index}"}
	if opts.Name != "" {
		args = append(args, "-n", opts.Name)
	}
	if opts.Dir != "" {
		args = append(args, "-c", opts.Dir)
	}
	if opts.Background {
		args = append(args, "-d")
	}
	for _, key := range sortedKeys(opts.Env) {
		args = append(args, "-e", key+"="+opts.Env[key])
	}
	args = append(args, "--")
	args = append(args, argv...)

	out, err := run(args...)
	if err != nil {
		return Window{}, err
	}

	id, index, _ := strings.Cut(strings.TrimSpace(out), " ")
	return Window{ID: id, Index: index}, nil
}

// Alive reports whether the window is still open.
func Alive(id string) bool {
	out, err := run("list-windows", "-a", "-F", "#{window_id}")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == id {
			return true
		}
	}
	return false
}

// Kill closes the window, ignoring one that has already gone.
func Kill(id string) error {
	if !Alive(id) {
		return nil
	}
	_, err := run("kill-window", "-t", id)
	return err
}

func run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				return "", fmt.Errorf("tmux: %s", stderr)
			}
		}
		return "", fmt.Errorf("tmux: %w", err)
	}
	return string(out), nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
