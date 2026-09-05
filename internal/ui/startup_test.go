package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/agent"
	"pib/internal/workspace"
)

func press(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	out := next.(Model)
	if cmd == nil {
		return out, nil
	}
	return out, cmd()
}

func TestMissingDirPromptsThenCreates(t *testing.T) {
	m := NewModel()
	status := workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib"}

	m, _ = step(t, m, detectedMsg{status: status})
	if m.phase != phaseConfirmCreate {
		t.Fatalf("phase = %v, want phaseConfirmCreate", m.phase)
	}
	if v := m.View(); v == "" {
		t.Error("create prompt view is empty")
	}

	// "y" issues the create command; feed back a successful result.
	m, _ = step(t, m, press("y"))
	m, _ = step(t, m, createdMsg{})
	if m.phase != phaseCheckingAgents {
		t.Fatalf("phase = %v, want phaseCheckingAgents", m.phase)
	}
	if !m.workspace.Exists {
		t.Error("workspace.Exists = false, want true")
	}
}

func TestMissingDirDeclineQuits(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib"}})

	_, cmd := m.Update(press("n"))
	if cmd == nil {
		t.Fatal("declining create returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd = %T, want tea.QuitMsg", cmd())
	}
}

// ready drives the model through startup to the description prompt.
func ready(t *testing.T) Model {
	t.Helper()

	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true,
	}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: "/home/.pib/agents"})
	if m.phase != phaseLoadingPlanner {
		t.Fatalf("phase = %v, want phaseLoadingPlanner", m.phase)
	}

	m, _ = step(t, m, plannerLoadedMsg{planner: agent.Definition{Name: "planner"}})
	if m.phase != phaseStartingServer {
		t.Fatalf("phase = %v, want phaseStartingServer", m.phase)
	}

	m, _ = step(t, m, serverStartedMsg{extension: "/repo/.pib/extension/pib.ts", socket: "/tmp/pib.sock"})
	if m.phase != phasePrompt {
		t.Fatalf("phase = %v, want phasePrompt", m.phase)
	}
	return m
}

// pib no longer asks about .gitignore, so an existing workspace goes straight
// to the agent checks whether or not git ignores it.
func TestExistingDirSkipsStraightToAgents(t *testing.T) {
	m := NewModel()

	next, cmd := m.Update(detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m = next.(Model)
	if cmd == nil {
		t.Error("detecting a ready workspace returned no command, want the agent check")
	}
	if m.phase != phaseCheckingAgents {
		t.Errorf("phase = %v, want phaseCheckingAgents", m.phase)
	}
}

func TestReadyWorkspaceSkipsPrompts(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	if m.phase != phaseCheckingAgents {
		t.Fatalf("phase = %v, want phaseCheckingAgents", m.phase)
	}
}

func TestServerFailureStopsStartup(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true})
	m, _ = step(t, m, plannerLoadedMsg{planner: agent.Definition{Name: "planner"}})
	m, _ = step(t, m, serverStartedMsg{err: errors.New("address already in use")})

	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
	if m.Err() == nil {
		t.Error("Err() = nil, want the listen failure")
	}
}

func TestMissingPlannerFails(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true})
	m, _ = step(t, m, plannerLoadedMsg{err: agent.ErrNotFound})

	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
	if !errors.Is(m.Err(), agent.ErrNotFound) {
		t.Errorf("Err() = %v, want ErrNotFound", m.Err())
	}
}

func TestDetectErrorFails(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{err: workspace.ErrNotRepo})

	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
	if !errors.Is(m.Err(), workspace.ErrNotRepo) {
		t.Errorf("Err() = %v, want ErrNotRepo", m.Err())
	}
}

func TestMissingAgentsPromptsThenInstalls(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true,
	}})

	m, _ = step(t, m, agentsCheckedMsg{installed: false, dir: "/home/.pib/agents"})
	if m.phase != phaseConfirmAgents {
		t.Fatalf("phase = %v, want phaseConfirmAgents", m.phase)
	}

	// The prompt names the directory and every agent it would write.
	view := m.View()
	if !strings.Contains(view, "/home/.pib/agents") {
		t.Errorf("view does not name the directory:\n%s", view)
	}
	for _, name := range agent.DefaultNames() {
		if !strings.Contains(view, name) {
			t.Errorf("view does not list %q:\n%s", name, view)
		}
	}

	m, _ = step(t, m, press("y"))
	m, _ = step(t, m, agentsInstalledMsg{written: []string{"planner", "scout"}})
	if m.phase != phaseLoadingPlanner {
		t.Fatalf("phase = %v, want phaseLoadingPlanner", m.phase)
	}
	if !strings.Contains(m.notice, "2 agents") {
		t.Errorf("notice = %q, want it to report what was installed", m.notice)
	}
}

// Without agents there is no planner to run, so declining ends the session.
func TestDecliningAgentsQuits(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true,
	}})
	m, _ = step(t, m, agentsCheckedMsg{installed: false, dir: "/home/.pib/agents"})

	_, cmd := m.Update(press("n"))
	if cmd == nil {
		t.Fatal("declining returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd = %T, want tea.QuitMsg", cmd())
	}
}

func TestInstallFailureStopsStartup(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true,
	}})
	m, _ = step(t, m, agentsCheckedMsg{installed: false, dir: "/home/.pib/agents"})
	m, _ = step(t, m, agentsInstalledMsg{err: errors.New("permission denied")})

	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
}

func TestOutdatedAgentsPromptBeforePlanning(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})

	next, cmd := m.Update(agentsCheckedMsg{installed: true, dir: "/home/.pib/agents", outdated: []string{"reviewer", "coder"}})
	m = next.(Model)

	if m.phase != phaseConfirmUpdate {
		t.Fatalf("phase = %v, want phaseConfirmUpdate", m.phase)
	}
	if cmd != nil {
		t.Error("pib started loading the planner before the user answered")
	}
	view := m.startupView()
	for _, want := range []string{"reviewer", "coder", "2 agents", "Update them?"} {
		if !strings.Contains(view, want) {
			t.Errorf("prompt missing %q:\n%s", want, view)
		}
	}
}

// Agents that match the built-in definitions are the ordinary case, and must
// not put a question in front of the user.
func TestCurrentAgentsAskNothing(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})

	next, cmd := m.Update(agentsCheckedMsg{installed: true, dir: "/home/.pib/agents"})
	m = next.(Model)

	if m.phase != phaseLoadingPlanner {
		t.Errorf("phase = %v, want phaseLoadingPlanner", m.phase)
	}
	if cmd == nil {
		t.Error("no command; the planner never loads")
	}
}

// Declining an update is not a reason to stop: the definitions on disk are the
// ones pib has been running all along.
func TestDecliningAnUpdateCarriesOn(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: "/d", outdated: []string{"reviewer"}})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = next.(Model)

	if m.phase != phaseLoadingPlanner {
		t.Errorf("phase = %v, want phaseLoadingPlanner", m.phase)
	}
	if cmd == nil {
		t.Error("declining stopped startup instead of continuing")
	}
	if m.outdated != nil {
		t.Error("the declined list was kept")
	}
}

// A write that fails leaves the definitions already on disk in place, which is
// what pib was going to run anyway — so it reports and carries on.
func TestAFailedUpdateDoesNotStopStartup(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: "/d", outdated: []string{"reviewer"}})

	next, cmd := m.Update(agentsUpdatedMsg{err: errors.New("disk full")})
	m = next.(Model)

	if m.phase != phaseLoadingPlanner {
		t.Errorf("phase = %v, want phaseLoadingPlanner", m.phase)
	}
	if cmd == nil {
		t.Error("a failed update stopped startup")
	}
	if !strings.Contains(m.notice, "disk full") {
		t.Errorf("notice = %q, want the reason it failed", m.notice)
	}
}

// The user needs to be told where the definitions they had went.
func TestUpdateReportsWhereTheOldCopiesWent(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: "/d", outdated: []string{"reviewer"}})

	next, _ := m.Update(agentsUpdatedMsg{written: []string{"reviewer"}, backup: "/home/.pib/agents-backup/2026-09-03T10-00-00Z"})
	m = next.(Model)

	if !strings.Contains(m.notice, "agents-backup/2026-09-03T10-00-00Z") {
		t.Errorf("notice = %q, want the backup location", m.notice)
	}
	if !strings.Contains(m.notice, "1 agent") {
		t.Errorf("notice = %q, want a singular count", m.notice)
	}
}

// A missing agents directory is still an install, not an update.
func TestUninstalledAgentsStillPromptToInstall(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})

	next, _ := m.Update(agentsCheckedMsg{installed: false, dir: "/d"})
	m = next.(Model)

	if m.phase != phaseConfirmAgents {
		t.Errorf("phase = %v, want phaseConfirmAgents", m.phase)
	}
}

// Missing default agents on an existing installation are installed silently
// and the notice names them.
func TestMissingAgentsInstalledSilently(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true,
	}})

	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: "/d", missing: []string{"code-reviewer"}})
	if m.phase != phaseCheckingAgents {
		t.Fatalf("phase = %v, want phaseCheckingAgents while installing", m.phase)
	}

	m, _ = step(t, m, agentsInstalledMsg{written: []string{"code-reviewer"}})
	if m.phase != phaseLoadingPlanner {
		t.Fatalf("phase = %v, want phaseLoadingPlanner", m.phase)
	}
	if !strings.Contains(m.notice, "code-reviewer") {
		t.Errorf("notice = %q, want it to name the installed agent", m.notice)
	}
}

// When some agents are missing and others are outdated, the missing ones are
// installed silently first and the outdated prompt is still shown afterwards.
func TestMissingAndOutdatedAgents(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true,
	}})

	m, _ = step(t, m, agentsCheckedMsg{
		installed: true,
		dir:       "/d",
		missing:   []string{"code-reviewer"},
		outdated:  []string{"reviewer"},
	})

	m, _ = step(t, m, agentsInstalledMsg{written: []string{"code-reviewer"}})
	if m.phase != phaseConfirmUpdate {
		t.Fatalf("phase = %v, want phaseConfirmUpdate", m.phase)
	}
	if len(m.outdated) != 1 || m.outdated[0] != "reviewer" {
		t.Errorf("outdated = %v, want [reviewer]", m.outdated)
	}
	if !strings.Contains(m.notice, "code-reviewer") {
		t.Errorf("notice = %q, want it to name the silently installed agent", m.notice)
	}
}

// Pressing y actually rewrites the file and saves the old one.
func TestAcceptingAnUpdateRewritesTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".pib", "agents")
	os.MkdirAll(dir, 0o755)
	if _, err := agent.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	mine := []byte("# my code-reviewer\n")
	os.WriteFile(filepath.Join(dir, "code-reviewer.md"), mine, 0o644)

	stale, err := agent.Outdated()
	if err != nil || len(stale) != 1 {
		t.Fatalf("outdated = %v, %v", stale, err)
	}

	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: dir, outdated: stale})
	if m.phase != phaseConfirmUpdate {
		t.Fatalf("phase = %v", m.phase)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("y produced no update command")
	}
	msg := cmd().(agentsUpdatedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next, _ = m.Update(msg)
	m = next.(Model)

	got, _ := os.ReadFile(filepath.Join(dir, "code-reviewer.md"))
	if strings.Contains(string(got), "my code-reviewer") {
		t.Error("code-reviewer.md was not updated")
	}
	if !strings.Contains(string(got), "one pull request") {
		t.Error("code-reviewer.md is not the built-in definition")
	}
	saved, err := os.ReadFile(filepath.Join(msg.backup, "code-reviewer.md"))
	if err != nil || string(saved) != string(mine) {
		t.Errorf("backup missing or wrong: %q %v", saved, err)
	}
	if left, _ := agent.Outdated(); len(left) != 0 {
		t.Errorf("still outdated after updating: %v", left)
	}
	t.Logf("notice: %s", m.notice)
}
