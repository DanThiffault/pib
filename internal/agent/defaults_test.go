package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pib/internal/issues"
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

// Issues are pib's now. gh is still how the agents work with pull requests,
// which pib deliberately does not own, so only the issue commands are retired.
func TestNoDefaultTracksIssuesThroughGH(t *testing.T) {
	for _, name := range DefaultNames() {
		body, _ := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		text := string(body)

		for _, retired := range []string{"gh issue", "--add-blocked-by", "--parent "} {
			if strings.Contains(text, retired) {
				t.Errorf("%s still reaches for %q", name, retired)
			}
		}
	}
}

// Each agent has to name the pib commands its job depends on — and have bash,
// since that is how it runs them. tools is an allowlist, so an agent missing
// it would find the command simply unavailable.
func TestAgentsCanRunThePibCommandsTheyNeed(t *testing.T) {
	needs := map[string][]string{
		"planner":  {"pib plan apply", "pib issue ready", "blockedBy"},
		"worker":   {"pib issue view", "pib issue link-pr"},
		"reviewer": {"pib issue view", "pib issue comment", "pib issue close"},
	}

	for name, commands := range needs {
		body, _ := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		d, err := parse(string(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !slices.Contains(d.Tools, "bash") {
			t.Errorf("%s: tools = %v, want bash so it can run pib", name, d.Tools)
		}
		for _, command := range commands {
			if !strings.Contains(d.Body, command) {
				t.Errorf("%s does not mention %q", name, command)
			}
		}
	}
}

// Only a merged pull request closes a task, so no agent may close one itself.
// Both mention pib issue close: the worker to forbid it, the reviewer to close
// its own review issue — neither may aim it at a task.
func TestNoAgentClosesATaskIssue(t *testing.T) {
	body, _ := defaultAgents.ReadFile(defaultsDir + "/worker.md")
	if !strings.Contains(string(body), "must not close your issue") {
		t.Error("the worker is no longer told to leave its issue open")
	}

	body, _ = defaultAgents.ReadFile(defaultsDir + "/reviewer.md")
	if !strings.Contains(string(body), "Never close a task issue") {
		t.Error("the reviewer is no longer told to leave task issues alone")
	}
}

// The planner is told to copy this document verbatim. If it stops being one
// pib accepts, the planner is teaching every agent to write something that
// will be rejected.
func TestPlannerExampleDocumentApplies(t *testing.T) {
	body, _ := defaultAgents.ReadFile(defaultsDir + "/planner.md")

	document := jsonBlock(t, string(body))
	doc, err := issues.ParseDocument([]byte(document))
	if err != nil {
		t.Fatalf("the planner's example document does not parse: %v", err)
	}

	store, err := issues.Open(issues.DataDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	result, err := store.Apply(doc, issues.ApplyOptions{})
	if err != nil {
		t.Fatalf("the planner's example document does not apply: %v", err)
	}
	if len(result.Created) != 3 {
		t.Errorf("created %v, want the three issues in the example", result.Created)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("the example applies with warnings: %v", result.Warnings)
	}

	// What the feature is belongs on the plan. An issue for it would launch
	// nothing and never close.
	if doc.Plan.Body == "" || len(doc.Plan.Acceptance) == 0 {
		t.Error("the example plan carries no goal or criteria of its own")
	}
	for _, item := range doc.Issues {
		if item.Type == "feature" {
			t.Errorf("the example still creates a container issue %q", item.ID)
		}
		if item.Parent != "" {
			t.Errorf("%q sets a parent; belonging to a plan is not what parent is for", item.ID)
		}
	}

	// Only the first task can start: the second waits on it, and the
	// reviewer waits on both.
	ready, err := store.Ready(issues.Filter{}, issues.StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 {
		t.Errorf("%d issues are ready, want only the unblocked task", len(ready))
	}
}

// jsonBlock returns the first fenced json block in a markdown document.
func jsonBlock(t *testing.T, text string) string {
	t.Helper()

	_, rest, found := strings.Cut(text, "```json\n")
	if !found {
		t.Fatal("no json block found")
	}
	block, _, found := strings.Cut(rest, "\n```")
	if !found {
		t.Fatal("unterminated json block")
	}
	return block
}

// An agent that writes the deliverable and then exits without touching its
// issue looks, to everything downstream, like an agent that did nothing. The
// finishing checklist is what stops that, so it has to stay in front of
// pib_done rather than after it.
func TestIssueAgentsRecordBeforeFinishing(t *testing.T) {
	for _, name := range []string{"worker", "reviewer", "researcher", "prototype"} {
		body, _ := defaultAgents.ReadFile(defaultsDir + "/" + name + ".md")
		text := string(body)

		if !strings.Contains(text, "before you call it") {
			t.Errorf("%s does not gate pib_done on recording its work", name)
		}
		if !strings.Contains(text, `"$PIB_ISSUE"`) {
			t.Errorf("%s does not use $PIB_ISSUE in its commands", name)
		}

		// The checklist has to come before the call it guards.
		gate := strings.Index(text, "before you call it")
		call := strings.LastIndex(text, "Then call `pib_done`")
		if gate < 0 || call < 0 || gate > call {
			t.Errorf("%s: the finishing checklist is not in front of pib_done", name)
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
