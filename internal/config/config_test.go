package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// write puts a config file in a temp dir and returns its path.
func write(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPathsFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadPaths(filepath.Join(dir, "missing.toml"), filepath.Join(dir, "also-missing.toml"))
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}

	agent, ok := cfg.AgentFor("task")
	if !ok || agent != "coder" {
		t.Errorf("task maps to %q (ok=%v), want coder", agent, ok)
	}
	if got, want := cfg.Types(), []string{"feature", "plan-reviewer", "prototype", "research", "reviewer", "task"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
}

func TestGlobalIsAuthoritativeOnceItExists(t *testing.T) {
	dir := t.TempDir()
	global := write(t, dir, "[types]\ntask = \"builder\"\n")

	cfg, err := LoadPaths(global, "")
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}

	if agent, ok := cfg.AgentFor("task"); !ok || agent != "builder" {
		t.Errorf("task maps to %q (ok=%v), want builder", agent, ok)
	}
	// A type dropped from the global file must not come back from the
	// built-in defaults, or it could never be removed.
	if cfg.Known("reviewer") {
		t.Error("reviewer is known, but the global config does not list it")
	}
}

func TestWorkspaceOverridesKeyByKey(t *testing.T) {
	globalDir, workspaceDir := t.TempDir(), t.TempDir()
	global := write(t, globalDir, Template)
	workspace := write(t, workspaceDir, "[types]\ntask = \"elixir-worker\"\nspike = \"prototype\"\n")

	cfg, err := LoadPaths(global, workspace)
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}

	if agent, _ := cfg.AgentFor("task"); agent != "elixir-worker" {
		t.Errorf("task maps to %q, want elixir-worker", agent)
	}
	if agent, _ := cfg.AgentFor("research"); agent != "researcher" {
		t.Errorf("research maps to %q, want the global researcher", agent)
	}
	if agent, ok := cfg.AgentFor("spike"); !ok || agent != "prototype" {
		t.Errorf("spike maps to %q (ok=%v), want prototype", agent, ok)
	}
}

func TestEmptyWorkspaceTableOverridesNothing(t *testing.T) {
	globalDir, workspaceDir := t.TempDir(), t.TempDir()
	global := write(t, globalDir, Template)
	workspace := write(t, workspaceDir, "[types]\n")

	cfg, err := LoadPaths(global, workspace)
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}
	if agent, ok := cfg.AgentFor("task"); !ok || agent != "coder" {
		t.Errorf("task maps to %q (ok=%v), want coder", agent, ok)
	}
}

func TestUnmappedAndContainerTypes(t *testing.T) {
	cfg, err := LoadPaths("", "")
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}

	// A container type is known but launches nothing.
	if agent, ok := cfg.AgentFor("feature"); ok || agent != "" {
		t.Errorf("feature maps to %q (ok=%v), want unlaunchable", agent, ok)
	}
	if !cfg.Known("feature") {
		t.Error("feature should be a known type")
	}

	// An unmapped type is neither known nor launchable.
	if _, ok := cfg.AgentFor("chore"); ok {
		t.Error("chore is launchable, but nothing maps it")
	}
	if cfg.Known("chore") {
		t.Error("chore should not be known")
	}
}

func TestUnknownKeysAreIgnored(t *testing.T) {
	dir := t.TempDir()
	global := write(t, dir, "concurrency = 4\n\n[types]\ntask = \"worker\"\n\n[future]\nwhatever = true\n")

	cfg, err := LoadPaths(global, "")
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}
	if agent, _ := cfg.AgentFor("task"); agent != "coder" {
		t.Errorf("task maps to %q, want coder", agent)
	}
}

func TestMalformedConfigNamesThePath(t *testing.T) {
	dir := t.TempDir()
	global := write(t, dir, "[types\ntask = ")

	_, err := LoadPaths(global, "")
	if err == nil {
		t.Fatal("expected an error for malformed toml")
	}
	if !strings.Contains(err.Error(), global) {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestSeedWritesOnceAndParsesBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", FileName)

	written, err := Seed(path)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if !written {
		t.Fatal("Seed reported no write for a missing file")
	}

	// The template and the built-in defaults must not drift apart.
	cfg, err := LoadPaths(path, "")
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}
	seeded := map[string]string{}
	for _, name := range cfg.Types() {
		agent, _ := cfg.AgentFor(name)
		if !cfg.Known(name) {
			t.Fatalf("%s should be known", name)
		}
		seeded[name] = agent
	}
	if !reflect.DeepEqual(seeded, defaults()) {
		t.Errorf("template parses to %v, defaults are %v", seeded, defaults())
	}

	// Seeding again must leave an edited file alone.
	if err := os.WriteFile(path, []byte("[types]\ntask = \"mine\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err = Seed(path)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if written {
		t.Error("Seed overwrote an existing config")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "mine") {
		t.Error("Seed clobbered the edited config")
	}
}

func TestLoadUsesHomeAndWorkspace(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)

	if _, err := SeedGlobal(); err != nil {
		t.Fatalf("SeedGlobal: %v", err)
	}
	global, err := GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".pib", FileName); global != want {
		t.Errorf("GlobalPath() = %q, want %q", global, want)
	}

	write(t, workspace, "[types]\nreviewer = \"picky-reviewer\"\n")

	cfg, err := Load(workspace)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if agent, _ := cfg.AgentFor("reviewer"); agent != "picky-reviewer" {
		t.Errorf("reviewer maps to %q, want picky-reviewer", agent)
	}
	if agent, _ := cfg.AgentFor("task"); agent != "coder" {
		t.Errorf("task maps to %q, want the seeded coder", agent)
	}
}

func TestPlanReviewDefaultsOnAndCanBeTurnedOff(t *testing.T) {
	dir := t.TempDir()

	// Nothing configured at all.
	cfg, err := LoadPaths(filepath.Join(dir, "missing.toml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PlanReview() {
		t.Error("PlanReview() = false with no config, want true")
	}

	// A config that says nothing about it leaves the default alone — the
	// section is a pointer so absent does not read as false.
	global := write(t, dir, "[types]\ntask = \"worker\"\n")
	cfg, err = LoadPaths(global, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PlanReview() {
		t.Error("PlanReview() = false for a config with no [plan] section, want true")
	}

	// The workspace can turn it off without restating the types.
	workspace := filepath.Join(dir, "workspace.toml")
	if err := os.WriteFile(workspace, []byte("[plan]\nreview = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadPaths(global, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlanReview() {
		t.Error("PlanReview() = true after the workspace turned it off")
	}
	if agent, ok := cfg.AgentFor("task"); !ok || agent != "coder" {
		t.Errorf("task maps to %q (ok=%v); the override should not have dropped types", agent, ok)
	}
}
