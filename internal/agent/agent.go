// Package agent loads pib agent definitions — markdown files whose YAML
// frontmatter configures a pi session and whose body is the system prompt.
package agent

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// PlannerName is the agent pib launches to plan a project.
const PlannerName = "planner"

// Executable is the CLI that runs an agent.
const Executable = "pi"

// ErrNotFound reports a missing definition file.
var ErrNotFound = errors.New("agent definition not found")

// Definition is a parsed agent definition.
type Definition struct {
	Path        string
	Name        string
	Description string
	Tools       []string
	DenyTools   []string
	Model       string
	Thinking    string
	// AutoExit reports whether pib should quit once the session ends.
	AutoExit bool
	// SystemPrompt is "append" to add Body to pi's own system prompt, or
	// "replace" to use Body on its own.
	SystemPrompt string
	// Body is the markdown following the frontmatter.
	Body string
}

// Dir is the directory holding agent definitions, ~/.pib/agents.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pib", "agents"), nil
}

// Path returns the definition path for a named agent.
func Path(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".md"), nil
}

// LoadPlanner loads ~/.pib/agents/planner.md.
func LoadPlanner() (Definition, error) {
	path, err := Path(PlannerName)
	if err != nil {
		return Definition{}, err
	}
	return Load(path)
}

// Load reads and parses an agent definition.
func Load(path string) (Definition, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Definition{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return Definition{}, err
	}

	d, err := parse(string(body))
	if err != nil {
		return Definition{}, fmt.Errorf("%s: %w", path, err)
	}
	d.Path = path
	if d.Name == "" {
		d.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return d, nil
}

// parse splits the frontmatter from the body. The frontmatter is a flat set of
// `key: value` lines, which is all an agent definition uses.
func parse(src string) (Definition, error) {
	d := Definition{SystemPrompt: "append"}

	rest, ok := strings.CutPrefix(strings.TrimPrefix(src, "\ufeff"), "---")
	if !ok {
		return d, errors.New("missing frontmatter")
	}
	rest = strings.TrimLeft(rest, "\r")
	rest, ok = strings.CutPrefix(rest, "\n")
	if !ok {
		return d, errors.New("missing frontmatter")
	}

	scanner := bufio.NewScanner(strings.NewReader(rest))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	closed := false
	var consumed int
	for scanner.Scan() {
		line := scanner.Text()
		consumed += len(line) + 1

		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			closed = true
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return d, fmt.Errorf("malformed frontmatter line: %q", trimmed)
		}
		d.set(strings.TrimSpace(key), unquote(strings.TrimSpace(value)))
	}
	if err := scanner.Err(); err != nil {
		return d, err
	}
	if !closed {
		return d, errors.New("unterminated frontmatter")
	}

	if consumed > len(rest) {
		consumed = len(rest)
	}
	d.Body = strings.TrimSpace(rest[consumed:])

	return d, nil
}

func (d *Definition) set(key, value string) {
	switch strings.ToLower(key) {
	case "name":
		d.Name = value
	case "description":
		d.Description = value
	case "tools":
		d.Tools = splitList(value)
	case "deny-tools":
		d.DenyTools = splitList(value)
	case "model":
		d.Model = value
	case "thinking":
		d.Thinking = value
	case "auto-exit":
		d.AutoExit = strings.EqualFold(value, "true")
	case "system-prompt":
		d.SystemPrompt = strings.ToLower(value)
	}
	// Unknown keys are ignored so newer definitions still load.
}

// Options adjusts how an agent's command line is built.
type Options struct {
	// ExtraTools are added to the tools allowlist. They are ignored when the
	// agent declares no allowlist, because everything is already enabled and
	// introducing one would disable the rest.
	ExtraTools []string
	// Extensions are loaded with --extension.
	Extensions []string
}

// Args builds the pi arguments for this agent and a user prompt.
func (d Definition) Args(prompt string) []string {
	// Everything after -- is the message, so a prompt starting with a dash
	// is not mistaken for a flag.
	return append(d.Flags(Options{}), "--", prompt)
}

// Flags builds this agent's pi flags, without a prompt.
func (d Definition) Flags(opts Options) []string {
	var args []string

	if d.Model != "" {
		args = append(args, "--model", d.Model)
	}
	if d.Thinking != "" {
		args = append(args, "--thinking", d.Thinking)
	}
	if len(d.Tools) > 0 {
		args = append(args, "--tools", strings.Join(mergeTools(d.Tools, opts.ExtraTools), ","))
	}
	if len(d.DenyTools) > 0 {
		args = append(args, "--exclude-tools", strings.Join(d.DenyTools, ","))
	}
	if d.Name != "" {
		args = append(args, "--name", d.Name)
	}
	if d.Body != "" {
		flag := "--append-system-prompt"
		if d.SystemPrompt == "replace" {
			flag = "--system-prompt"
		}
		args = append(args, flag, d.Body)
	}

	for _, extension := range opts.Extensions {
		if extension != "" {
			args = append(args, "--extension", extension)
		}
	}

	return args
}

// mergeTools appends extras that are not already allowed, preserving order.
func mergeTools(tools, extras []string) []string {
	merged := append([]string(nil), tools...)
	for _, extra := range extras {
		if !slices.Contains(merged, extra) {
			merged = append(merged, extra)
		}
	}
	return merged
}

// Argv is the full command line for this agent, including the executable.
func (d Definition) Argv(prompt string) []string {
	return append([]string{Executable}, d.Args(prompt)...)
}

// Command builds the pi invocation, rooted at dir.
func (d Definition) Command(dir, prompt string) *exec.Cmd {
	cmd := exec.Command(Executable, d.Args(prompt)...)
	cmd.Dir = dir
	return cmd
}

func splitList(value string) []string {
	value = strings.Trim(value, "[]")
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = unquote(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
