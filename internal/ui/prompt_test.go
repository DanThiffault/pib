package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/agent"
)

// launch records what a submit would have handed to pi.
type launch = launchConfig

// captureLaunches replaces the pi handoff for the duration of a test.
func captureLaunches(t *testing.T) *[]launch {
	t.Helper()

	var seen []launch
	original := launchPlanner
	launchPlanner = func(cfg launchConfig) tea.Cmd {
		seen = append(seen, cfg)
		return nil
	}
	t.Cleanup(func() { launchPlanner = original })

	return &seen
}

func typeText(m Model, s string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(Model)
}

func TestEmptyDescriptionDoesNotLaunch(t *testing.T) {
	launches := captureLaunches(t)
	m := ready(t)

	// Whitespace is not a description either.
	m = typeText(m, "   ")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(*launches) != 0 {
		t.Errorf("launched %d sessions, want 0", len(*launches))
	}
	if n := next.(Model).notice; n == "" {
		t.Error("no notice shown for empty description")
	}
}

func TestEnterLaunchesPlanner(t *testing.T) {
	launches := captureLaunches(t)
	m := ready(t)
	m = typeText(m, "a todo app")

	if got := m.input.Value(); got != "a todo app" {
		t.Fatalf("input = %q, want %q", got, "a todo app")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(*launches) != 1 {
		t.Fatalf("launched %d sessions, want 1", len(*launches))
	}
	got := (*launches)[0]
	if got.prompt != "a todo app" {
		t.Errorf("prompt = %q, want the description", got.prompt)
	}
	if got.dir != "/repo" {
		t.Errorf("dir = %q, want the git root", got.dir)
	}
	if got.planner.Name != "planner" {
		t.Errorf("planner = %q, want the loaded definition", got.planner.Name)
	}
}

func TestAltEnterInsertsNewlineInsteadOfLaunching(t *testing.T) {
	launches := captureLaunches(t)
	m := ready(t)
	m = typeText(m, "a todo app")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = next.(Model)
	m = typeText(m, "with tags")

	if len(*launches) != 0 {
		t.Errorf("alt+enter launched %d sessions, want 0", len(*launches))
	}
	if got, want := m.input.Value(), "a todo app\nwith tags"; got != want {
		t.Errorf("input = %q, want %q", got, want)
	}

	// The multi-line description still launches on a plain enter.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(*launches) != 1 {
		t.Fatalf("launched %d sessions after enter, want 1", len(*launches))
	}
	if got := (*launches)[0].prompt; got != "a todo app\nwith tags" {
		t.Errorf("prompt = %q, want the multi-line description", got)
	}
}

func TestQuitFromPrompt(t *testing.T) {
	m := ready(t)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd = %T, want tea.QuitMsg", cmd())
	}
}

func TestSessionOpenedInTmuxKeepsPibRunning(t *testing.T) {
	m := ready(t)
	m = typeText(m, "a todo app")

	next, _ := m.Update(sessionOpenedMsg{window: "3"})
	m = next.(Model)

	if m.phase != phasePrompt {
		t.Errorf("phase = %v, want phasePrompt", m.phase)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared for the next plan", m.input.Value())
	}
	if !strings.Contains(m.notice, "3") {
		t.Errorf("notice = %q, want it to name the tmux window", m.notice)
	}
}

func TestSessionOpenErrorKeepsDescription(t *testing.T) {
	m := ready(t)
	m = typeText(m, "a todo app")

	next, cmd := m.Update(sessionOpenedMsg{err: errors.New("no server running")})
	m = next.(Model)

	if cmd != nil {
		t.Error("failed launch returned a command, want none")
	}
	if !strings.Contains(m.notice, "no server running") {
		t.Errorf("notice = %q, want the tmux failure", m.notice)
	}
	// The description is not thrown away on a failed launch.
	if m.input.Value() != "a todo app" {
		t.Errorf("input = %q, want the description kept", m.input.Value())
	}
}

func TestSessionOpenedQuitsWhenAutoExit(t *testing.T) {
	m := ready(t)
	m.planner = agent.Definition{Name: "planner", AutoExit: true}

	_, cmd := m.Update(sessionOpenedMsg{window: "3"})
	if cmd == nil {
		t.Fatal("auto-exit planner returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd = %T, want tea.QuitMsg", cmd())
	}
}

func TestSessionEndReturnsToPrompt(t *testing.T) {
	m := ready(t)
	m = typeText(m, "a todo app")

	next, _ := m.Update(sessionFinishedMsg{})
	m = next.(Model)

	if m.phase != phasePrompt {
		t.Errorf("phase = %v, want phasePrompt", m.phase)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared", m.input.Value())
	}
	if m.notice == "" {
		t.Error("no notice after session ended")
	}
}

func TestSessionEndQuitsWhenAutoExit(t *testing.T) {
	m := ready(t)
	m.planner = agent.Definition{Name: "planner", AutoExit: true}

	_, cmd := m.Update(sessionFinishedMsg{})
	if cmd == nil {
		t.Fatal("auto-exit planner returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd = %T, want tea.QuitMsg", cmd())
	}
}

func TestSessionErrorSurfaces(t *testing.T) {
	m := ready(t)

	next, _ := m.Update(sessionFinishedMsg{err: errors.New("exec: \"pi\": not found")})
	if n := next.(Model).notice; !strings.Contains(n, "not found") {
		t.Errorf("notice = %q, want it to mention the failure", n)
	}
}

func TestPromptViewShowsContext(t *testing.T) {
	m := ready(t)
	m.planner.Model = "openrouter/moonshotai/kimi-k2.6"

	view := m.View()
	for _, want := range []string{"planner", "/repo", "kimi-k2.6", "What do you want to plan?"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}
