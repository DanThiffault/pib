package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `---
name: planner
description: Product planning specialist
tools: read, write, bash, edit
deny-tools: claude
model: openrouter/moonshotai/kimi-k2.6
thinking: medium
auto-exit: false
system-prompt: append
---

# Planner Agent

You are a **product planning specialist**.
`

func TestParse(t *testing.T) {
	d, err := parse(sample)
	if err != nil {
		t.Fatal(err)
	}

	if d.Name != "planner" {
		t.Errorf("Name = %q", d.Name)
	}
	if got, want := strings.Join(d.Tools, ","), "read,write,bash,edit"; got != want {
		t.Errorf("Tools = %q, want %q", got, want)
	}
	if got, want := strings.Join(d.DenyTools, ","), "claude"; got != want {
		t.Errorf("DenyTools = %q, want %q", got, want)
	}
	if d.Model != "openrouter/moonshotai/kimi-k2.6" {
		t.Errorf("Model = %q", d.Model)
	}
	if d.Thinking != "medium" {
		t.Errorf("Thinking = %q", d.Thinking)
	}
	if d.AutoExit {
		t.Error("AutoExit = true, want false")
	}
	if d.SystemPrompt != "append" {
		t.Errorf("SystemPrompt = %q", d.SystemPrompt)
	}
	if !strings.HasPrefix(d.Body, "# Planner Agent") {
		t.Errorf("Body = %q, want it to start at the heading", d.Body)
	}
	if strings.Contains(d.Body, "thinking:") {
		t.Errorf("Body leaked frontmatter:\n%s", d.Body)
	}
}

func TestArgs(t *testing.T) {
	d, err := parse(sample)
	if err != nil {
		t.Fatal(err)
	}

	args := d.Args("a todo app")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--model openrouter/moonshotai/kimi-k2.6",
		"--thinking medium",
		"--tools read,write,bash,edit",
		"--exclude-tools claude",
		"--name planner",
		"--append-system-prompt",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}

	// The prompt must be the final argument, after the -- separator.
	if got := args[len(args)-1]; got != "a todo app" {
		t.Errorf("last arg = %q, want the prompt", got)
	}
	if got := args[len(args)-2]; got != "--" {
		t.Errorf("arg before prompt = %q, want --", got)
	}
}

func TestArgsReplaceSystemPrompt(t *testing.T) {
	d := Definition{Body: "be brief", SystemPrompt: "replace"}
	if got := strings.Join(d.Args("x"), " "); !strings.Contains(got, "--system-prompt be brief") {
		t.Errorf("args = %q, want --system-prompt", got)
	}
}

func TestArgsOmitsUnsetFields(t *testing.T) {
	d := Definition{}
	if got := d.Args("x"); len(got) != 2 || got[0] != "--" || got[1] != "x" {
		t.Errorf("args = %v, want just the prompt", got)
	}
}

func TestCommandRunsFromGitRoot(t *testing.T) {
	d := Definition{Name: "planner"}
	cmd := d.Command("/repo", "a todo app")

	if cmd.Dir != "/repo" {
		t.Errorf("Dir = %q, want /repo", cmd.Dir)
	}
	if !strings.HasSuffix(cmd.Path, "pi") && cmd.Args[0] != "pi" {
		t.Errorf("command = %q, want pi", cmd.Path)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.md"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLoadNamesFromFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "planner.md")
	if err := os.WriteFile(path, []byte("---\nmodel: x\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "planner" {
		t.Errorf("Name = %q, want planner", d.Name)
	}
	if d.Body != "body" {
		t.Errorf("Body = %q", d.Body)
	}
}

func TestParseRejectsBadFrontmatter(t *testing.T) {
	for name, src := range map[string]string{
		"none":         "# just markdown\n",
		"unterminated": "---\nname: planner\nstill going\n",
	} {
		if _, err := parse(src); err == nil {
			t.Errorf("%s: parse succeeded, want an error", name)
		}
	}
}
