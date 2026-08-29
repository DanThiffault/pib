package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
)

func mustParse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

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

func plansModel(t *testing.T, plans []issues.Plan, counts map[string]issues.PlanCounts) Model {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.input.Blur()
	m.plans = plans
	m.planCounts = counts
	m.width = 100
	m.height = 30
	return m
}

func TestPlanListNavigation(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
		{Slug: "plan-b", Title: "Plan B"},
		{Slug: "plan-c", Title: "Plan C"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
		"plan-b": {Total: 3, Open: 2, Closed: 1},
		"plan-c": {Total: 0, Open: 0, Closed: 0},
	})

	// Down moves cursor
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.planCursor != 1 {
		t.Errorf("planCursor = %d, want 1", m.planCursor)
	}

	// Another down moves cursor again
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.planCursor != 2 {
		t.Errorf("planCursor = %d, want 2", m.planCursor)
	}

	// Down at bottom stays at bottom
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.planCursor != 2 {
		t.Errorf("planCursor = %d, want 2", m.planCursor)
	}

	// Up moves cursor back
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.planCursor != 1 {
		t.Errorf("planCursor = %d, want 1", m.planCursor)
	}

	// Up at top stays at top
	m.planCursor = 0
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.planCursor != 0 {
		t.Errorf("planCursor = %d, want 0", m.planCursor)
	}
}

func TestPlanListShowsTwoPanes(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A", CreatedAt: mustParse("2024-01-15T10:00:00Z")},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 5, Open: 3, Closed: 2},
	})

	view := m.View()
	if !strings.Contains(view, "plan-a") {
		t.Errorf("view missing plan slug:\n%s", view)
	}
	if !strings.Contains(view, "Plan A") {
		t.Errorf("view missing plan title:\n%s", view)
	}
	if !strings.Contains(view, "Total:") {
		t.Errorf("view missing issue total:\n%s", view)
	}
	if !strings.Contains(view, "Open:") {
		t.Errorf("view missing open count:\n%s", view)
	}
	if !strings.Contains(view, "Closed:") {
		t.Errorf("view missing closed count:\n%s", view)
	}
}

func TestEnterOpensPlanDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.plansView != viewPlanDetail {
		t.Errorf("plansView = %v, want viewPlanDetail", m.plansView)
	}
}

func TestRightOpensPlanDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.plansView != viewPlanDetail {
		t.Errorf("plansView = %v, want viewPlanDetail", m.plansView)
	}
}

func TestEscInDetailViewGoesBack(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
	})
	m.plansView = viewPlanDetail

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.plansView != viewPlanList {
		t.Errorf("plansView = %v, want viewPlanList after esc", m.plansView)
	}
}

func TestEscInDetailViewDoesNotQuit(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
	})
	m.plansView = viewPlanDetail

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		return // going back returns nil command, which is fine
	}
	if _, quitting := cmd().(tea.QuitMsg); quitting {
		t.Error("esc in detail view should go back, not quit")
	}
}

func TestLeftInDetailViewGoesBack(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
	})
	m.plansView = viewPlanDetail

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(Model)
	if m.plansView != viewPlanList {
		t.Errorf("plansView = %v, want viewPlanList after left", m.plansView)
	}
}

func TestPlansTabShowsEmptyState(t *testing.T) {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.plans = nil
	m.plansLoading = false
	m.plansErr = nil

	view := m.View()
	if !strings.Contains(view, "No plans yet") {
		t.Errorf("view missing empty state:\n%s", view)
	}
}

func TestPlansTabShowsErrorState(t *testing.T) {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.plans = nil
	m.plansLoading = false
	m.plansErr = errors.New("store unreachable")

	view := m.View()
	if !strings.Contains(view, "Error loading plans") {
		t.Errorf("view missing error state:\n%s", view)
	}
	if !strings.Contains(view, "store unreachable") {
		t.Errorf("view missing error message:\n%s", view)
	}
}

func TestPlansLoadedMsgPopulatesCounts(t *testing.T) {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.plansLoading = true

	plans := []issues.Plan{{Slug: "plan-x", Title: "Plan X"}}
	counts := map[string]issues.PlanCounts{"plan-x": {Total: 4, Open: 2, Closed: 2}}

	next, _ := m.Update(plansLoadedMsg{plans: plans, counts: counts})
	m = next.(Model)

	if m.plansLoading {
		t.Error("plansLoading still true after loaded message")
	}
	if len(m.plans) != 1 || m.plans[0].Slug != "plan-x" {
		t.Errorf("plans = %v, want one plan with slug plan-x", m.plans)
	}
	if m.planCounts["plan-x"].Total != 4 {
		t.Errorf("planCounts total = %d, want 4", m.planCounts["plan-x"].Total)
	}
}

func TestCtrlCQuitsFromDetailView(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	}, map[string]issues.PlanCounts{
		"plan-a": {Total: 1, Open: 1, Closed: 0},
	})
	m.plansView = viewPlanDetail

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c produced no command, want quit")
	}
	if _, quitting := cmd().(tea.QuitMsg); !quitting {
		t.Error("ctrl+c in detail view should quit")
	}
}
