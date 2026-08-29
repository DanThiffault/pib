// Package workspace locates the .pib workspace directory for the current
// repository and reports whether it exists and is ignored by git.
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

// gitignoreEntry is written to the root .gitignore when the user opts in.
// The leading slash anchors it to the repository root.
const gitignoreEntry = "/" + DirName + "/"

// ErrNotRepo is returned when the working directory is not inside a git
// repository, so there is no root to anchor the workspace to.
var ErrNotRepo = errors.New("not inside a git repository")

// Status describes the workspace directory as it was found on disk.
type Status struct {
	GitRoot string // absolute path to the repository root
	Dir     string // absolute path to the workspace directory
	Exists  bool   // the workspace directory is present
	Ignored bool   // git ignores the workspace directory
}

// Detect finds the repository root and inspects the workspace directory.
func Detect() (Status, error) {
	root, err := gitRoot()
	if err != nil {
		return Status{}, err
	}

	s := Status{
		GitRoot: root,
		Dir:     filepath.Join(root, DirName),
	}

	switch fi, err := os.Stat(s.Dir); {
	case err == nil && fi.IsDir():
		s.Exists = true
	case err == nil:
		return s, fmt.Errorf("%s exists but is not a directory", s.Dir)
	case !os.IsNotExist(err):
		return s, err
	}

	s.Ignored, err = isIgnored(root, s.Dir)
	if err != nil {
		return s, err
	}

	return s, nil
}

// Create makes the workspace directory.
func (s Status) Create() error {
	return os.MkdirAll(s.Dir, 0o755)
}

// AddToGitignore appends the workspace entry to the repository's root
// .gitignore, creating the file if it does not exist.
func (s Status) AddToGitignore() error {
	path := filepath.Join(s.GitRoot, ".gitignore")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == gitignoreEntry {
			return nil
		}
	}

	var b strings.Builder
	if len(existing) > 0 {
		b.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("# pib workspace\n")
	b.WriteString(gitignoreEntry + "\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
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

// isIgnored asks git whether the path is excluded. check-ignore exits 0 when
// the path is ignored, 1 when it is not, and >1 on a real failure.
func isIgnored(root, path string) (bool, error) {
	cmd := exec.Command("git", "check-ignore", "--quiet", path)
	cmd.Dir = root

	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("checking gitignore: %w", err)
}
