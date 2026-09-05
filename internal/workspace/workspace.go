// Package workspace locates the .pib workspace directory for the current
// repository and reports whether it exists.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DirName is the workspace directory kept at the root of the repository.
const DirName = ".pib"

// ErrNotRepo is returned when the working directory is not inside a git
// repository, so there is no root to anchor the workspace to.
var ErrNotRepo = errors.New("not inside a git repository")

// Status describes the workspace directory as it was found on disk.
type Status struct {
	GitRoot string // absolute path to the repository root
	Dir     string // absolute path to the workspace directory
	Exists  bool   // the workspace directory is present
	Branch  string // current git branch
}

// Detect finds the repository root and inspects the workspace directory.
func Detect() (Status, error) {
	root, err := gitRoot()
	if err != nil {
		return Status{}, err
	}

	branch, _ := currentBranch()
	s := Status{
		GitRoot: root,
		Dir:     filepath.Join(root, DirName),
		Branch:  branch,
	}

	switch fi, err := os.Stat(s.Dir); {
	case err == nil && fi.IsDir():
		s.Exists = true
	case err == nil:
		return s, fmt.Errorf("%s exists but is not a directory", s.Dir)
	case !os.IsNotExist(err):
		return s, err
	}

	return s, nil
}

// Create makes the workspace directory.
func (s Status) Create() error {
	return os.MkdirAll(s.Dir, 0o755)
}

func gitRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", ErrNotRepo
		}
		return "", fmt.Errorf("running git: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func currentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
