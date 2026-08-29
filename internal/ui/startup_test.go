package ui

import (
	"errors"
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
	if m.phase != phaseConfirmGitignore {
		t.Fatalf("phase = %v, want phaseConfirmGitignore", m.phase)
	}
	if !m.created || !m.workspace.Exists {
		t.Errorf("created=%v exists=%v, want true/true", m.created, m.workspace.Exists)
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
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true,
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

func TestUnignoredDirDeclineContinues(t *testing.T) {
	m := NewModel()
	status := workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}

	m, _ = step(t, m, detectedMsg{status: status})
	if m.phase != phaseConfirmGitignore {
		t.Fatalf("phase = %v, want phaseConfirmGitignore", m.phase)
	}

	// Declining the gitignore prompt must not stop startup.
	next, cmd := m.Update(press("n"))
	if cmd == nil {
		t.Error("declining gitignore returned no command, want the planner load")
	}
	if p := next.(Model).phase; p != phaseCheckingAgents {
		t.Errorf("phase = %v, want phaseCheckingAgents", p)
	}
}

func TestUnignoredDirAcceptAdds(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})

	m, _ = step(t, m, press("enter"))
	m, _ = step(t, m, gitignoredMsg{})
	if m.phase != phaseCheckingAgents {
		t.Fatalf("phase = %v, want phaseCheckingAgents", m.phase)
	}
	if !m.workspace.Ignored {
		t.Error("Ignored = false, want true")
	}
}

func TestReadyWorkspaceSkipsPrompts(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true}})
	if m.phase != phaseCheckingAgents {
		t.Fatalf("phase = %v, want phaseCheckingAgents", m.phase)
	}
}

func TestServerFailureStopsStartup(t *testing.T) {
	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true}})
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
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true}})
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
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true,
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
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true,
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
		GitRoot: "/repo", Dir: "/repo/.pib", Exists: true, Ignored: true,
	}})
	m, _ = step(t, m, agentsCheckedMsg{installed: false, dir: "/home/.pib/agents"})
	m, _ = step(t, m, agentsInstalledMsg{err: errors.New("permission denied")})

	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
}
