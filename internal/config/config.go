// Package config loads the type → agent map: which agent implements an issue
// of a given type. A global file under ~/.pib holds the defaults and a file in
// the workspace overrides it key by key, so a repository can reroute one type
// without restating the rest.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

// FileName is the config file, looked for in ~/.pib and in the workspace.
const FileName = "config.toml"

// Template is written to the global config the first time pib runs. It is a
// literal rather than marshalled output so the comments survive.
const Template = `# Which agent implements an issue of each type.
#
# The value is an agent definition in ~/.pib/agents. An empty value marks a
# container type that never launches an agent. A type that is not listed here
# still works — pib simply has no way to launch it, and says so.

[types]
feature   = ""
task      = "worker"
research  = "researcher"
prototype = "prototype"
reviewer  = "reviewer"
`

// defaults mirrors Template. Used when no global config exists yet.
func defaults() map[string]string {
	return map[string]string{
		"feature":   "",
		"task":      "worker",
		"research":  "researcher",
		"prototype": "prototype",
		"reviewer":  "reviewer",
	}
}

// Config is the merged configuration.
type Config struct {
	types map[string]string
}

// file is the on-disk shape. Unknown keys are ignored, so a config written by
// a newer pib still loads.
type file struct {
	Types map[string]string `toml:"types"`
}

// Dir is pib's home directory, ~/.pib.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pib"), nil
}

// GlobalPath is ~/.pib/config.toml.
func GlobalPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the global config and merges the workspace config over it.
// workspaceDir is the .pib directory of the repository; either file may be
// missing.
func Load(workspaceDir string) (Config, error) {
	global, err := GlobalPath()
	if err != nil {
		return Config{}, err
	}
	return LoadPaths(global, filepath.Join(workspaceDir, FileName))
}

// LoadPaths merges two specific files. The global file is authoritative once
// it exists, so a type deleted from it stays deleted; the built-in defaults
// apply only while there is no global file at all.
func LoadPaths(global, workspace string) (Config, error) {
	types, found, err := read(global)
	if err != nil {
		return Config{}, err
	}
	if !found {
		types = defaults()
	}

	over, found, err := read(workspace)
	if err != nil {
		return Config{}, err
	}
	if found {
		for name, agent := range over {
			types[name] = agent
		}
	}

	return Config{types: types}, nil
}

// read parses one config file. A missing file is not an error.
func read(path string) (map[string]string, bool, error) {
	if path == "" {
		return nil, false, nil
	}

	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	var f file
	if _, err := toml.Decode(string(body), &f); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}

	types := make(map[string]string, len(f.Types))
	for name, agent := range f.Types {
		types[name] = agent
	}
	return types, true, nil
}

// Seed writes the default config to path if nothing is there. It reports
// whether it wrote the file.
func Seed(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(Template), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// SeedGlobal writes ~/.pib/config.toml if it does not exist yet.
func SeedGlobal() (bool, error) {
	path, err := GlobalPath()
	if err != nil {
		return false, err
	}
	return Seed(path)
}

// AgentFor returns the agent that implements this issue type. ok is false
// when the type is unknown or is a container that launches nothing — call
// Known to tell those apart.
func (c Config) AgentFor(issueType string) (agent string, ok bool) {
	agent, found := c.types[issueType]
	return agent, found && agent != ""
}

// Known reports whether the type appears in the config at all. An unknown
// type is a warning; a known one mapped to nothing is deliberate.
func (c Config) Known(issueType string) bool {
	_, found := c.types[issueType]
	return found
}

// Types lists the configured type names, sorted.
func (c Config) Types() []string {
	names := make([]string, 0, len(c.types))
	for name := range c.types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
