// Package worktree gives each issue its own checkout.
//
// Agents run as separate processes in one repository, and a branch is a
// property of a checkout rather than of a process. Two workers starting at
// once both run `git checkout -b`, and the second moves the tree out from
// under the first — they end up committing each other's work, or committing to
// the wrong branch. Files collide the same way without git involved at all:
// two prototypes writing the same scratch directory, two researchers writing
// the same ADR.
//
// A worktree per issue is the smallest thing that fixes both. They share the
// object store, so making one is a checkout rather than a fetch.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DirName is the workspace subdirectory holding per-issue checkouts.
const DirName = "worktrees"

// Manager creates and removes worktrees under a workspace directory.
type Manager struct {
	// GitRoot is the repository every worktree is cut from.
	GitRoot string
	// StateDir is the workspace directory, normally <git root>/.pib.
	StateDir string
}

// Dir is where the worktree for an issue lives, whether or not it exists.
func (m Manager) Dir(issue int64) string {
	return filepath.Join(m.StateDir, DirName, strconv.FormatInt(issue, 10))
}

// For returns a checkout of its own for an issue, creating it if this is the
// first agent to work that issue and reusing it otherwise — a followup has to
// land on the branch and the uncommitted work its earlier run left behind.
//
// The checkout is detached rather than on a branch. git refuses to have one
// branch checked out in two worktrees, so worktrees all sitting on the default
// branch could not coexist; detached, each agent's own `git checkout -b`
// creates its branch inside its own tree and nowhere else.
func (m Manager) For(issue int64) (string, error) {
	if issue == 0 {
		return m.GitRoot, nil
	}

	dir := m.Dir(issue)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return dir, nil
	}

	// A directory with no .git in it is the wreckage of a worktree removed
	// from under git, or an interrupted create. Either way git will not add
	// over the top of it.
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}

	head, err := m.git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err := m.git("worktree", "add", "--detach", dir, head); err != nil {
		return "", err
	}
	return dir, nil
}

// Remove discards the checkout for an issue. The branch it holds is left
// alone: the work is on it, and pib does not own branches.
func (m Manager) Remove(issue int64) error {
	if issue == 0 {
		return nil
	}
	if _, err := os.Stat(m.Dir(issue)); os.IsNotExist(err) {
		return nil
	}
	_, err := m.git("worktree", "remove", "--force", m.Dir(issue))
	return err
}

// Prune clears worktree records left by a pib that did not exit cleanly.
func (m Manager) Prune() error {
	_, err := m.git("worktree", "prune")
	return err
}

func (m Manager) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.GitRoot
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("git %s: %s",
				strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// List reports the issues that have a checkout.
func (m Manager) List() ([]int64, error) {
	entries, err := os.ReadDir(filepath.Join(m.StateDir, DirName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var issues []int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		number, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil {
			continue
		}
		issues = append(issues, number)
	}
	return issues, nil
}

// Sweep removes the checkouts of issues that are finished with.
//
// It runs at startup rather than when an issue closes: closing happens while
// pib is serving, and an issue can close through reconciliation with its own
// agent still running. Deleting that agent's files underneath it would be a
// worse bug than the disk it saves. Nothing is running when the store opens.
func (m Manager) Sweep(done func(issue int64) bool) (int, error) {
	if err := m.Prune(); err != nil {
		return 0, err
	}

	issues, err := m.List()
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, issue := range issues {
		if !done(issue) {
			continue
		}
		if err := m.Remove(issue); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
