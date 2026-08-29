package agent

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultAgents is the set pib offers to install the first time it runs on a
// machine. They are embedded so a fresh install needs nothing but the binary.
//
//go:embed defaults/*.md
var defaultAgents embed.FS

const defaultsDir = "defaults"

// DefaultNames lists the agents pib can install, in the order shown to the
// user. The planner comes first because pib cannot start without it.
func DefaultNames() []string {
	entries, err := fs.ReadDir(defaultAgents, defaultsDir)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
	}

	sort.Slice(names, func(i, j int) bool {
		if (names[i] == PlannerName) != (names[j] == PlannerName) {
			return names[i] == PlannerName
		}
		return names[i] < names[j]
	})

	return names
}

// Installed reports whether the agents directory exists. A missing directory
// is what pib offers to fill in; an existing one is left alone, however the
// user has arranged it.
func Installed() (bool, error) {
	dir, err := Dir()
	if err != nil {
		return false, err
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return info.IsDir(), nil
}

// InstallDefaults writes the built-in agents into ~/.pib/agents and returns
// the names it wrote. A definition already on disk is never overwritten, so
// running this again cannot cost the user their edits.
func InstallDefaults() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	var written []string
	for _, name := range DefaultNames() {
		path := filepath.Join(dir, name+".md")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return written, err
		}

		body, err := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		if err != nil {
			return written, err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return written, err
		}
		written = append(written, name)
	}

	return written, nil
}
