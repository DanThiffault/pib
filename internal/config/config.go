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
feature       = ""
task          = "worker"
research      = "researcher"
prototype     = "prototype"
reviewer      = "reviewer"
plan-reviewer = "plan-reviewer"

# Applying a new plan adds an issue that reviews it against the codebase, and
# blocks the rest of the plan until it closes. Set this to false to plan
# without one.
#
# isolate gives each issue its own git worktree under .pib/worktrees, so agents
# working different issues at once cannot move each other's branch. Turn it off
# for a project where a fresh checkout needs expensive setup — installed
# dependencies, a build cache — and run one agent at a time instead.
[plan]
review = true
isolate = true
`

// defaults mirrors Template. Used when no global config exists yet.
func defaults() map[string]string {
	return map[string]string{
		"feature":       "",
		"task":          "worker",
		"research":      "researcher",
		"prototype":     "prototype",
		"reviewer":      "reviewer",
		"plan-reviewer": "plan-reviewer",
	}
}

// Config is the merged configuration.
type Config struct {
	types       map[string]string
	planReview  bool
	planIsolate bool
}

// PlanIsolate reports whether each issue gets its own checkout. On unless a
// config turns it off.
func (c Config) PlanIsolate() bool { return c.planIsolate }

// PlanReview reports whether applying a new plan should add an issue that
// reviews it. On unless a config turns it off.
func (c Config) PlanReview() bool { return c.planReview }

// file is the on-disk shape. Unknown keys are ignored, so a config written by
// a newer pib still loads.
type file struct {
	Types map[string]string `toml:"types"`
	Plan  struct {
		// Both are pointers so an absent [plan] section leaves the
		// defaults alone rather than reading as false.
		Review  *bool `toml:"review"`
		Isolate *bool `toml:"isolate"`
	} `toml:"plan"`
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
	cfg := Config{planReview: true, planIsolate: true}

	base, found, err := read(global)
	if err != nil {
		return Config{}, err
	}
	cfg.types = base.types()
	if !found {
		cfg.types = defaults()
	}
	if base.Plan.Review != nil {
		cfg.planReview = *base.Plan.Review
	}
	if base.Plan.Isolate != nil {
		cfg.planIsolate = *base.Plan.Isolate
	}

	over, found, err := read(workspace)
	if err != nil {
		return Config{}, err
	}
	if found {
		for name, agent := range over.types() {
			cfg.types[name] = agent
		}
	}
	if over.Plan.Review != nil {
		cfg.planReview = *over.Plan.Review
	}
	if over.Plan.Isolate != nil {
		cfg.planIsolate = *over.Plan.Isolate
	}

	return cfg, nil
}

// read parses one config file. A missing file is not an error.
func read(path string) (file, bool, error) {
	if path == "" {
		return file{}, false, nil
	}

	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return file{}, false, nil
		}
		return file{}, false, err
	}

	var f file
	if _, err := toml.Decode(string(body), &f); err != nil {
		return file{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return f, true, nil
}

// types copies the map so a caller can merge into it without touching what
// was parsed.
func (f file) types() map[string]string {
	types := make(map[string]string, len(f.Types))
	for name, agent := range f.Types {
		types[name] = agent
	}
	return types
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
