package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func readyWithTabs(t *testing.T) Model {
	m := ready(t)
	m.currentTab = tabPlan
	return m
}

func TestTabBarRenders(t *testing.T) {
	m := readyWithTabs(t)
	view := m.View()
	if !strings.Contains(view, "Plan") {
		t.Errorf("view missing Plan tab:\n%s", view)
	}
	if !strings.Contains(view, "Plans") {
		t.Errorf("view missing Plans tab:\n%s", view)
	}
}

func TestTab1SwitchesToPlanTab(t *testing.T) {
	m := readyWithTabs(t)
	// Get to the Plans tab the way a user does, so the prompt gives up
	// focus and the number shortcuts are live.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = next.(Model)

	if m.currentTab != tabPlan {
		t.Errorf("currentTab = %v, want tabPlan", m.currentTab)
	}
}

func TestTab2SwitchesToPlansTab(t *testing.T) {
	m := readyWithTabs(t)
	// The prompt has focus on the Plan tab, so the shortcut is not live
	// there; leave with tab first, as a user would.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)

	if m.currentTab != tabPlans {
		t.Errorf("currentTab = %v, want tabPlans", m.currentTab)
	}
}

// The prompt is a textarea, and a description is allowed to contain digits.
// A shortcut that fires while someone is typing swallows the keystroke and
// everything after it.
func TestNumberKeysDoNotStealFromThePrompt(t *testing.T) {
	m := readyWithTabs(t)
	m = typeText(m, "plan v2 of the thing, in 1 pass")

	if got, want := m.input.Value(), "plan v2 of the thing, in 1 pass"; got != want {
		t.Errorf("prompt holds %q, want %q", got, want)
	}
	if m.currentTab != tabPlan {
		t.Errorf("currentTab = %v; typing must not switch tabs", m.currentTab)
	}
}

// Away from the prompt there is nothing to type into, so the shortcuts are
// live again.
func TestNumberKeysSwitchTabsWhenNotTyping(t *testing.T) {
	m := readyWithTabs(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab}) // leave the prompt
	m = next.(Model)
	if m.input.Focused() {
		t.Fatal("the prompt kept focus after switching away from it")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = next.(Model)
	if m.currentTab != tabPlan {
		t.Errorf("currentTab = %v, want tabPlan", m.currentTab)
	}
	if !m.input.Focused() {
		t.Error("returning to the Plan tab did not give the prompt focus back")
	}
}

// Quitting used to live in the prompt handler, which only runs on one tab.
func TestQuitWorksFromEveryTab(t *testing.T) {
	for _, start := range []tab{tabPlan, tabPlans} {
		for _, keyMsg := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
			m := readyWithTabs(t)
			m.currentTab = start
			if start == tabPlans {
				m.input.Blur()
			}

			_, cmd := m.Update(keyMsg)
			if cmd == nil {
				t.Fatalf("tab %v: %v produced no command, want quit", start, keyMsg)
			}
			if _, quitting := cmd().(tea.QuitMsg); !quitting {
				t.Errorf("tab %v: %v did not quit", start, keyMsg)
			}
		}
	}
}

func TestTabKeyCyclesTabs(t *testing.T) {
	m := readyWithTabs(t)

	// tab switches from Plan to Plans
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.currentTab != tabPlans {
		t.Errorf("currentTab = %v, want tabPlans after first tab", m.currentTab)
	}

	// tab switches back to Plan
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.currentTab != tabPlan {
		t.Errorf("currentTab = %v, want tabPlan after second tab", m.currentTab)
	}
}

func TestTabPlanPreservesPromptBehavior(t *testing.T) {
	launches := captureLaunches(t)
	m := readyWithTabs(t)
	m = typeText(m, "a todo app")

	// Enter should still launch planner while in tabPlan
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(*launches) != 1 {
		t.Errorf("launched %d sessions, want 1", len(*launches))
	}
}

func TestPlansTabShowsLoadingState(t *testing.T) {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.plansLoading = true

	view := m.View()
	if !strings.Contains(view, "Loading") {
		t.Errorf("view missing loading indicator:\n%s", view)
	}
}

func TestTabBarHighlightsActiveTab(t *testing.T) {
	m := readyWithTabs(t)
	view := m.View()

	// Both tabs should be present in the tab bar
	if !strings.Contains(view, "Plan") || !strings.Contains(view, "Plans") {
		t.Error("tab bar missing one or both tabs")
	}
}

func TestPromptViewShowsTabBar(t *testing.T) {
	m := readyWithTabs(t)
	view := m.View()

	// The Plan tab content should still be visible below the tab bar
	if !strings.Contains(view, "What do you want to plan?") {
		t.Errorf("prompt missing below tab bar:\n%s", view)
	}
}
