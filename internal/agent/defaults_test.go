package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefaultNamesLeadWithPlanner(t *testing.T) {
	names := DefaultNames()
	if len(names) == 0 {
		t.Fatal("no default agents embedded")
	}
	if names[0] != PlannerName {
		t.Errorf("names[0] = %q, want the planner first", names[0])
	}
	for _, want := range []string{"planner", "scout", "researcher", "reviewer", "worker", "prototype"} {
		if !slices.Contains(names, want) {
			t.Errorf("default set is missing %q: %v", want, names)
		}
	}
}

// Every shipped definition must parse, or pib would install agents it cannot
// run.
func TestEmbeddedDefaultsParse(t *testing.T) {
	for _, name := range DefaultNames() {
		body, err := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}

		d, err := parse(string(body))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if d.Name != name {
			t.Errorf("%s: name = %q, want it to match the filename", name, d.Name)
		}
		if d.Model == "" {
			t.Errorf("%s: no model set", name)
		}
		if d.Body == "" {
			t.Errorf("%s: no system prompt", name)
		}
	}
}

// The planner has to be able to delegate, and delegating agents need the tool
// in their allowlist or it is silently unavailable.
func TestDelegatingDefaultsAllowPib(t *testing.T) {
	for _, name := range []string{"planner", "researcher"} {
		body, _ := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		d, err := parse(string(body))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(d.Tools, "pib") {
			t.Errorf("%s: tools = %v, want pib among them", name, d.Tools)
		}
	}
}

func TestNoDefaultReferencesRetiredTools(t *testing.T) {
	for _, name := range DefaultNames() {
		body, _ := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		if strings.Contains(string(body), "subagent") {
			t.Errorf("%s still references the subagent tool", name)
		}
	}
}

func TestInstallDefaultsWritesAll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	written, err := InstallDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != len(DefaultNames()) {
		t.Errorf("wrote %d agents, want %d", len(written), len(DefaultNames()))
	}

	installed, err := Installed()
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Error("Installed() = false after installing")
	}

	d, err := LoadPlanner()
	if err != nil {
		t.Fatalf("planner not loadable after install: %v", err)
	}
	if d.Name != PlannerName {
		t.Errorf("planner name = %q", d.Name)
	}
}

// Re-running must never cost the user their edits.
func TestInstallDefaultsKeepsExistingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".pib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "planner.md")
	if err := os.WriteFile(mine, []byte("---\nname: planner\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := InstallDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(written, "planner") {
		t.Error("InstallDefaults overwrote an existing planner")
	}

	body, err := os.ReadFile(mine)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "mine") {
		t.Errorf("planner.md = %q, want it untouched", body)
	}
}

func TestInstalledFalseWithoutDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	installed, err := Installed()
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Error("Installed() = true with no ~/.pib")
	}
}
