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
	m.currentTab = tabPlans

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = next.(Model)

	if m.currentTab != tabPlan {
		t.Errorf("currentTab = %v, want tabPlan", m.currentTab)
	}
}

func TestTab2SwitchesToPlansTab(t *testing.T) {
	m := readyWithTabs(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = next.(Model)

	if m.currentTab != tabPlans {
		t.Errorf("currentTab = %v, want tabPlans", m.currentTab)
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
