package agent

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// defaultBody returns the definition built into this pib.
func defaultBody(name string) ([]byte, error) {
	return defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
}

// Missing names the default agents whose files do not exist on disk. It is
// meant for existing installations that already have an agents directory: a
// name that is new in the binary shows up here until it is written.
func Missing() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	var missing []string
	for _, name := range DefaultNames() {
		_, err := os.Stat(filepath.Join(dir, name+".md"))
		if os.IsNotExist(err) {
			missing = append(missing, name)
		} else if err != nil {
			return nil, err
		}
	}

	return missing, nil
}

// Outdated names the installed agents whose file no longer matches the
// definition built into this pib.
//
// It cannot say why they differ. A newer pib shipping a rewritten agent and a
// user editing their own copy look identical from here, which is why updating
// asks first and keeps what it replaces. An agent that is not installed at all
// is not outdated — InstallDefaults is what writes those.
func Outdated() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	var stale []string
	for _, name := range DefaultNames() {
		want, err := defaultBody(name)
		if err != nil {
			return nil, err
		}

		got, err := os.ReadFile(filepath.Join(dir, name+".md"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}

		if !bytes.Equal(got, want) {
			stale = append(stale, name)
		}
	}

	return stale, nil
}

// BackupDir is where UpdateDefaults saves the definitions it replaces.
const BackupDir = "agents-backup"

// UpdateDefaults rewrites the named agents with the definitions built into
// this pib, saving what was there first. pib cannot tell a stale default from
// a deliberate edit, so it never destroys the file it replaces: everything
// replaced in one run is copied to ~/.pib/agents-backup/<timestamp>/, and that
// directory is returned so the user can be told where their copies went.
func UpdateDefaults(names []string, now time.Time) (backup string, written []string, err error) {
	if len(names) == 0 {
		return "", nil, nil
	}

	dir, err := Dir()
	if err != nil {
		return "", nil, err
	}
	backup = filepath.Join(filepath.Dir(dir), BackupDir, now.UTC().Format("2006-01-02T15-04-05Z"))
	if err := os.MkdirAll(backup, 0o755); err != nil {
		return "", nil, err
	}

	for _, name := range names {
		want, err := defaultBody(name)
		if err != nil {
			return backup, written, err
		}

		path := filepath.Join(dir, name+".md")
		// A missing file is not an error: there is nothing to save, and
		// writing the default is what the caller asked for either way.
		if existing, err := os.ReadFile(path); err == nil {
			if err := os.WriteFile(filepath.Join(backup, name+".md"), existing, 0o644); err != nil {
				return backup, written, err
			}
		} else if !os.IsNotExist(err) {
			return backup, written, err
		}

		if err := os.WriteFile(path, want, 0o644); err != nil {
			return backup, written, err
		}
		written = append(written, name)
	}

	return backup, written, nil
}
