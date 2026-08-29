// Package pr reports the state of a pull request on GitHub.
//
// pib owns issues; GitHub still owns pull requests. This is the one place
// pib looks across at them, so that a merge can close the issue its worker
// opened the pull request for.
package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// The states pib records for a linked pull request.
const (
	Open   = "open"
	Merged = "merged"
	Closed = "closed"
)

// ErrUnavailable reports that gh could not be run at all — usually because
// it is not installed. Issue tracking carries on without it; only automatic
// closure pauses.
var ErrUnavailable = errors.New("gh is not available")

// Lookup reports the state of one pull request. The store depends on this
// shape, not on this package, so tests can supply their own.
type Lookup interface {
	State(ctx context.Context, url string) (string, error)
}

// CLI asks the gh command line tool.
type CLI struct {
	// Path is the executable to run. Empty means "gh" on PATH.
	Path string
}

// State runs gh and maps its answer onto the states pib stores.
func (c CLI) State(ctx context.Context, url string) (string, error) {
	if strings.TrimSpace(url) == "" {
		return "", errors.New("no pull request url")
	}

	path := c.Path
	if path == "" {
		path = "gh"
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "pr", "view", url, "--json", "state")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// A bare name that is not on PATH gives ErrNotFound; an explicit
		// path that is not there gives ENOENT. Both mean the same thing.
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrUnavailable, path)
		}
		if message := firstLine(stderr.String()); message != "" {
			return "", fmt.Errorf("gh pr view %s: %s", url, message)
		}
		return "", fmt.Errorf("gh pr view %s: %w", url, err)
	}

	var out struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", fmt.Errorf("gh pr view %s: unreadable reply: %w", url, err)
	}

	switch strings.ToUpper(strings.TrimSpace(out.State)) {
	case "MERGED":
		return Merged, nil
	case "CLOSED":
		return Closed, nil
	case "OPEN":
		return Open, nil
	default:
		return "", fmt.Errorf("gh pr view %s: unknown state %q", url, out.State)
	}
}

// firstLine trims gh's error output to something worth putting in a warning.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
