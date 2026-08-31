package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

func plansModel(t *testing.T, plans []issues.Plan) Model {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.input.Blur()
	m.plans = plans
	m.width = 100
	m.height = 30
	return m
}

func TestPlanListNavigation(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
		{Slug: "plan-b", Title: "Plan B"},
		{Slug: "plan-c", Title: "Plan C"},
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
	})

	view := m.View()
	if !strings.Contains(view, "plan-a") {
		t.Errorf("view missing plan slug:\n%s", view)
	}
	if !strings.Contains(view, "No issues in this plan.") {
		t.Errorf("view missing empty DAG message:\n%s", view)
	}
}

func TestEnterOpensPlanDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
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

	next, _ := m.Update(plansLoadedMsg{plans: plans})
	m = next.(Model)

	if m.plansLoading {
		t.Error("plansLoading still true after loaded message")
	}
	if len(m.plans) != 1 || m.plans[0].Slug != "plan-x" {
		t.Errorf("plans = %v, want one plan with slug plan-x", m.plans)
	}
}

func TestCtrlCQuitsFromDetailView(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
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

func TestEnterOpensPlanDetailAndLoadsIssues(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.plansView != viewPlanDetail {
		t.Errorf("plansView = %v, want viewPlanDetail", m.plansView)
	}
	if !m.planIssuesLoading {
		t.Error("planIssuesLoading = false, want true")
	}
	if cmd == nil {
		t.Fatal("enter produced no command, want issue load")
	}
	msg := cmd()
	loaded, ok := msg.(planIssuesLoadedMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want planIssuesLoadedMsg", msg)
	}
	if loaded.planSlug != "plan-a" {
		t.Errorf("loaded planSlug = %q, want plan-a", loaded.planSlug)
	}
}

func TestPlanIssuesLoadedMsgPopulatesIssues(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssuesLoading = true

	statuses := []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1", State: issues.StateOpen}, Ready: true},
		{Issue: issues.Issue{Number: 2, Title: "Issue 2", State: issues.StateOpen}, Blocked: true},
	}

	next, _ := m.Update(planIssuesLoadedMsg{planSlug: "plan-a", issues: statuses})
	m = next.(Model)

	if m.planIssuesLoading {
		t.Error("planIssuesLoading still true after loaded message")
	}
	if len(m.planIssues) != 2 {
		t.Errorf("planIssues = %d, want 2", len(m.planIssues))
	}
	if m.planIssues[0].Number != 1 {
		t.Errorf("first issue number = %d, want 1", m.planIssues[0].Number)
	}
	if m.planIssuesLoadedFor != "plan-a" {
		t.Errorf("planIssuesLoadedFor = %q, want plan-a", m.planIssuesLoadedFor)
	}
}

func TestIssueListNavigation(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
		{Issue: issues.Issue{Number: 2, Title: "Issue 2"}},
		{Issue: issues.Issue{Number: 3, Title: "Issue 3"}},
	}
	m.issueCursor = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.issueCursor != 1 {
		t.Errorf("issueCursor = %d, want 1", m.issueCursor)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.issueCursor != 2 {
		t.Errorf("issueCursor = %d, want 2", m.issueCursor)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.issueCursor != 2 {
		t.Errorf("issueCursor = %d, want 2 at bottom", m.issueCursor)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.issueCursor != 1 {
		t.Errorf("issueCursor = %d, want 1", m.issueCursor)
	}

	m.issueCursor = 0
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.issueCursor != 0 {
		t.Errorf("issueCursor = %d, want 0 at top", m.issueCursor)
	}
}

func TestPlanDetailShowsTwoPanes(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "First Issue", State: issues.StateOpen, Type: "task", Acceptance: []string{"it works"}, CreatedAt: mustParse("2024-01-15T10:00:00Z")}, Ready: true, Agent: "builder"},
		{Issue: issues.Issue{Number: 2, Title: "Second Issue", State: issues.StateOpen, Type: "task"}, Blocked: true, OpenBlockers: []int64{1}},
	}
	m.issueCursor = 0
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "First Issue") {
		t.Errorf("view missing issue title:\n%s", view)
	}
	if !strings.Contains(view, "#1") {
		t.Errorf("view missing issue number:\n%s", view)
	}
	if !strings.Contains(view, "ready") {
		t.Errorf("view missing ready flag:\n%s", view)
	}
	if !strings.Contains(view, "builder") {
		t.Errorf("view missing agent:\n%s", view)
	}
	if !strings.Contains(view, "it works") {
		t.Errorf("view missing acceptance criteria:\n%s", view)
	}
}

func TestPlanDetailShowsLoadingState(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssuesLoading = true

	view := m.View()
	if !strings.Contains(view, "Loading") {
		t.Errorf("view missing loading indicator:\n%s", view)
	}
}

func TestPlanDetailShowsEmptyState(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssuesLoading = false
	m.planIssuesErr = nil
	m.planIssues = nil
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "No issues") {
		t.Errorf("view missing empty state:\n%s", view)
	}
}

func TestPlanDetailShowsErrorState(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssuesLoading = false
	m.planIssuesErr = errors.New("store failed")
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "Error loading issues") {
		t.Errorf("view missing error state:\n%s", view)
	}
	if !strings.Contains(view, "store failed") {
		t.Errorf("view missing error message:\n%s", view)
	}
}

// A row that wraps costs the pane a line it has already counted, so the
// window can hold fewer issues than the scroll arithmetic thinks and the
// cursor ends up below the floor of the pane, invisible.
func TestIssueListRowsStayWithinThePane(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.height = 15
	m.plansView = viewPlanDetail
	m.planIssuesLoadedFor = "plan-a"
	titles := []string{
		"Add Tab Framework and Refactor Prompt into TabPlan",
		"Implement Plan List Tab with Two-Pane Browse",
		"Implement Plan Detail Drill-Down View",
		"Polish, Edge Cases, and Tests",
		"Review: Tabbed TUI with Plan Browser",
		"Sixth issue in the plan",
		"Seventh issue in the plan",
	}
	for i, title := range titles {
		m.planIssues = append(m.planIssues, issues.Status{
			Issue: issues.Issue{Number: int64(i + 1), Title: title},
		})
	}
	m.issueCursor = len(titles) - 1

	w, _ := paneWidths(m.width)
	h := m.contentHeight()
	lines := strings.Split(m.issueListPane(w, h), "\n")

	if len(lines) != h {
		t.Errorf("pane rendered %d lines into a height of %d:\n%s", len(lines), h, strings.Join(lines, "\n"))
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != w {
			t.Errorf("line %d width = %d, want %d: %q", i, got, w, line)
		}
	}

	selected := -1
	for i, line := range lines {
		if strings.Contains(line, "#7") {
			selected = i
		}
	}
	if selected < 0 {
		t.Errorf("selected issue #7 is not in the pane:\n%s", strings.Join(lines, "\n"))
	}
}

func TestNarrowTerminalShowsSinglePane(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.width = 60
	m.height = 20

	view := m.View()
	if strings.Contains(view, "│") {
		t.Errorf("narrow plan list should not contain pane divider:\n%s", view)
	}

	m.plansView = viewPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"
	view = m.View()
	if strings.Contains(view, "│") {
		t.Errorf("narrow detail view should not contain pane divider:\n%s", view)
	}
}

func TestWideTerminalShowsTwoPanes(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.width = 100
	m.height = 20

	view := m.View()
	if !strings.Contains(view, "│") {
		t.Errorf("wide plan list should contain pane divider:\n%s", view)
	}
}

func TestTruncateLongTitles(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := truncate(long, 20)
	if len([]rune(got)) != 20 {
		t.Errorf("truncate length = %d, want 20", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncate did not end with ellipsis: %q", got)
	}
	if strings.Contains(got, long) {
		t.Errorf("truncate returned the full string")
	}
}

func TestTruncateAtSmallMax(t *testing.T) {
	if got := truncate("hello", 0); got != "" {
		t.Errorf("truncate at 0 = %q, want empty", got)
	}
	if got := truncate("hello", -1); got != "" {
		t.Errorf("truncate at -1 = %q, want empty", got)
	}
	if got := truncate("hello", 2); got != "he" {
		t.Errorf("truncate at 2 = %q, want he", got)
	}
}

func TestVeryNarrowWidthNoPanic(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.width = 5
	m.height = 10

	_ = m.View() // should not panic

	m.plansView = viewPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"
	_ = m.View() // should not panic
}

func TestResizeUpdatesDimensions(t *testing.T) {
	m := readyWithTabs(t)
	if m.width != 0 || m.height != 0 {
		t.Skipf("initial dimensions non-zero: %dx%d", m.width, m.height)
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	if m.width != 120 {
		t.Errorf("width = %d, want 120", m.width)
	}
	if m.height != 40 {
		t.Errorf("height = %d, want 40", m.height)
	}
}

func TestResizeHandledInPlanListView(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})

	next, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, "plan-a") {
		t.Errorf("resized plan list view missing plan:\n%s", view)
	}
}

func TestResizeHandledInPlanDetailView(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	m = next.(Model)

	view := m.View()
	if !strings.Contains(view, "Issue 1") {
		t.Errorf("resized detail view missing issue:\n%s", view)
	}
}

func TestLoadingIndicatorVisible(t *testing.T) {
	m := readyWithTabs(t)
	m.currentTab = tabPlans
	m.plansLoading = true

	view := m.View()
	if !strings.Contains(view, "◐") {
		t.Errorf("loading indicator missing spinner character:\n%s", view)
	}
	if !strings.Contains(view, "Loading plans") {
		t.Errorf("loading indicator missing text:\n%s", view)
	}
}

func TestDetailLoadingIndicatorVisible(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.plansView = viewPlanDetail
	m.planIssuesLoading = true

	view := m.View()
	if !strings.Contains(view, "◐") {
		t.Errorf("loading indicator missing spinner character:\n%s", view)
	}
	if !strings.Contains(view, "Loading issues") {
		t.Errorf("loading indicator missing text:\n%s", view)
	}
}

func TestTabBarKeysSwitchTabsButNavigationWorksInPane(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
		{Slug: "plan-b", Title: "Plan B"},
	})

	// Up/down navigate within the pane.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.planCursor != 1 {
		t.Errorf("planCursor = %d, want 1 after down", m.planCursor)
	}

	// Tab still switches tabs globally.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.currentTab != tabPlan {
		t.Errorf("currentTab = %v, want tabPlan after tab key", m.currentTab)
	}
}

// Entering a plan starts a load and leaving starts another, so two responses
// can be in flight. The older one must not land on the newer plan's view.
func TestStalePlanIssuesResponseIsIgnored(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
		{Slug: "plan-b", Title: "Plan B"},
	})
	m.plansView = viewPlanDetail
	m.planCursor = 1

	next, _ := m.Update(planIssuesLoadedMsg{
		planSlug: "plan-b",
		issues:   []issues.Status{{Issue: issues.Issue{Number: 9, Title: "Belongs to B"}}},
	})
	m = next.(Model)

	next, _ = m.Update(planIssuesLoadedMsg{
		planSlug: "plan-a",
		issues:   []issues.Status{{Issue: issues.Issue{Number: 1, Title: "Belongs to A"}}},
	})
	m = next.(Model)

	if m.planIssuesLoadedFor != "plan-b" {
		t.Errorf("planIssuesLoadedFor = %q, want plan-b", m.planIssuesLoadedFor)
	}
	if len(m.planIssues) != 1 || m.planIssues[0].Title != "Belongs to B" {
		t.Errorf("stale response replaced the issues on screen: %+v", m.planIssues)
	}
	if strings.Contains(m.View(), "Belongs to A") {
		t.Errorf("view shows the other plan's issues:\n%s", m.View())
	}
}

func TestPlanDAGPaneRendersTree(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Setup", State: issues.StateOpen}},
		{Issue: issues.Issue{Number: 2, Title: "Build", State: issues.StateOpen, BlockedBy: []int64{1}}},
		{Issue: issues.Issue{Number: 3, Title: "Test", State: issues.StateOpen, BlockedBy: []int64{1}}},
	}
	m.planIssuesLoadedFor = "plan-a"

	output := m.planDAGPane(60, 10)
	if !strings.Contains(output, "#1 Setup") {
		t.Errorf("DAG missing root issue:\n%s", output)
	}
	if !strings.Contains(output, "#2 Build") {
		t.Errorf("DAG missing child issue:\n%s", output)
	}
	if !strings.Contains(output, "#3 Test") {
		t.Errorf("DAG missing sibling issue:\n%s", output)
	}
	if !strings.Contains(output, "├─►") && !strings.Contains(output, "└─►") {
		t.Errorf("DAG missing tree connectors:\n%s", output)
	}
}
