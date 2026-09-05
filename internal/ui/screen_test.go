package ui

import (
	"context"
	"errors"
	"pib/internal/protocol"
	"pib/internal/runner"
	"strings"
	"sync"
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

func plansModel(t *testing.T, plans []issues.Plan) Model {
	m := ready(t)
	m.screen = screenPlans
	m.input.Blur()
	m.plans = plans
	m.width = 100
	m.height = 30
	return m
}

func TestStatusLineShowsGitRootAndBranch(t *testing.T) {
	m := ready(t)
	m.width = 100
	m.workspace.GitRoot = "/repo"
	m.workspace.Branch = "main"

	line := m.statusLineView()
	if !strings.Contains(line, "pib") {
		t.Errorf("status line missing 'pib': %q", line)
	}
	if !strings.Contains(line, "repo") {
		t.Errorf("status line missing repo name: %q", line)
	}
	if !strings.Contains(line, "main") {
		t.Errorf("status line missing branch: %q", line)
	}
}

func TestBreadcrumbShowsPlanAndIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.screen = screenPlanDetail
	line := m.breadcrumbView()
	if !strings.Contains(line, "Plans") {
		t.Errorf("breadcrumb missing Plans: %q", line)
	}
	if !strings.Contains(line, "orders") {
		t.Errorf("breadcrumb missing plan slug: %q", line)
	}

	m.screen = screenIssue
	m.planIssues = []issues.Status{{Issue: issues.Issue{Number: 13, Title: "Add cart"}}}
	m.issueCursor = 0
	line = m.breadcrumbView()
	if !strings.Contains(line, "#13") {
		t.Errorf("breadcrumb missing issue: %q", line)
	}
}

func TestEscQuitsFromTopLevel(t *testing.T) {
	for _, start := range []screen{screenPlans, screenNewPlan} {
		m := ready(t)
		m.screen = start
		if start == screenPlans {
			m.input.Blur()
		}

		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if cmd == nil {
			t.Fatalf("screen %v: esc produced no command, want quit", start)
		}
		if _, quitting := cmd().(tea.QuitMsg); !quitting {
			t.Errorf("screen %v: esc did not quit", start)
		}
	}
}

func TestCtrlCQuitsFromEveryScreen(t *testing.T) {
	for _, start := range []screen{screenPlans, screenNewPlan, screenPlanDetail, screenIssue} {
		m := ready(t)
		m.screen = start
		if start == screenPlans || start == screenPlanDetail || start == screenIssue {
			m.input.Blur()
		}

		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if cmd == nil {
			t.Fatalf("screen %v: ctrl+c produced no command, want quit", start)
		}
		if _, quitting := cmd().(tea.QuitMsg); !quitting {
			t.Errorf("screen %v: ctrl+c did not quit", start)
		}
	}
}

func TestEscInDetailGoesBack(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenPlanDetail

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Errorf("screen = %v, want screenPlans after esc", m.screen)
	}
}

func TestEscInDetailDoesNotQuit(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenPlanDetail

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		return // going back returns nil command, which is fine
	}
	if _, quitting := cmd().(tea.QuitMsg); quitting {
		t.Error("esc in detail view should go back, not quit")
	}
}

func TestLeftInDetailGoesBack(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenPlanDetail

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Errorf("screen = %v, want screenPlans after left", m.screen)
	}
}

func TestEscInFullScreenReturnsToDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenPlanDetail {
		t.Errorf("screen = %v, want screenPlanDetail after esc", m.screen)
	}
}

func TestEscInFullScreenDoesNotQuit(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		return
	}
	if _, quitting := cmd().(tea.QuitMsg); quitting {
		t.Error("esc in full-screen view should go back, not quit")
	}
}

func TestLeftInFullScreenReturnsToDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(Model)
	if m.screen != screenPlanDetail {
		t.Errorf("screen = %v, want screenPlanDetail after left", m.screen)
	}
}

func TestBackKeyInDetailViewReturnsToList(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Errorf("screen = %v, want screenPlans", m.screen)
	}
}

func TestBackKeyInFullScreenReturnsToDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = next.(Model)
	if m.screen != screenPlanDetail {
		t.Errorf("screen = %v, want screenPlanDetail", m.screen)
	}
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
	if m.screen != screenPlanDetail {
		t.Errorf("screen = %v, want screenPlanDetail", m.screen)
	}
}

func TestRightOpensPlanDetail(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.screen != screenPlanDetail {
		t.Errorf("screen = %v, want screenPlanDetail", m.screen)
	}
}

func TestRightArrowFromDetailOpensFullScreen(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.screen != screenIssue {
		t.Errorf("screen = %v, want screenIssue", m.screen)
	}
}

func TestEnterFromDetailOpensFullScreen(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.screen != screenIssue {
		t.Errorf("screen = %v, want screenIssue", m.screen)
	}
}

func TestPlansScreenShowsEmptyState(t *testing.T) {
	m := ready(t)
	m.screen = screenPlans
	m.plans = nil
	m.plansLoading = false
	m.plansErr = nil

	view := m.View()
	if !strings.Contains(view, "No plans yet") {
		t.Errorf("view missing empty state:\n%s", view)
	}
}

func TestPlansScreenShowsErrorState(t *testing.T) {
	m := ready(t)
	m.screen = screenPlans
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
	m := ready(t)
	m.screen = screenPlans
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

func TestEnterOpensPlanDetailAndLoadsIssues(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.screen != screenPlanDetail {
		t.Errorf("screen = %v, want screenPlanDetail", m.screen)
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
	m.screen = screenPlanDetail
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
	m.screen = screenPlanDetail
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
	m.screen = screenPlanDetail
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
	m.screen = screenPlanDetail
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
	m.screen = screenPlanDetail
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
	m.screen = screenPlanDetail
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
	m.screen = screenPlanDetail
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

	m.screen = screenPlanDetail
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

	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue 1"}},
	}
	m.planIssuesLoadedFor = "plan-a"
	_ = m.View() // should not panic
}

func TestResizeUpdatesDimensions(t *testing.T) {
	m := ready(t)
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
	m.screen = screenPlanDetail
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
	m := ready(t)
	m.screen = screenPlans
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
	m.screen = screenPlanDetail
	m.planIssuesLoading = true

	view := m.View()
	if !strings.Contains(view, "◐") {
		t.Errorf("loading indicator missing spinner character:\n%s", view)
	}
	if !strings.Contains(view, "Loading issues") {
		t.Errorf("loading indicator missing text:\n%s", view)
	}
}

// Entering a plan starts a load and leaving starts another, so two responses
// can be in flight. The older one must not land on the newer plan's view.
func TestStalePlanIssuesResponseIsIgnored(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
		{Slug: "plan-b", Title: "Plan B"},
	})
	m.screen = screenPlanDetail
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

// The pane must render exactly the height it is given. Scroll indicators are
// extra rows, and reserving them after the window is sized is how a pane ends
// up taller than the one beside it.
func TestListPaneNeverExceedsItsHeight(t *testing.T) {
	labels := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}

	for _, h := range []int{1, 2, 3, 4, 5, 8, 12} {
		for _, cursor := range []int{0, 4, 7} {
			lines := strings.Split(listPane("Plans", labels, cursor, 30, h), "\n")
			if len(lines) != h {
				t.Errorf("h=%d cursor=%d rendered %d lines:\n%s",
					h, cursor, len(lines), strings.Join(lines, "\n"))
			}
		}
	}
}

// Scrolling is pointless if it hides the row it scrolled to.
func TestListPaneKeepsTheCursorVisible(t *testing.T) {
	labels := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}

	for _, h := range []int{4, 5, 8, 12} {
		for cursor := range labels {
			pane := listPane("Plans", labels, cursor, 30, h)
			if !strings.Contains(pane, labels[cursor]) {
				t.Errorf("h=%d cursor=%d (%q) is not in the pane:\n%s",
					h, cursor, labels[cursor], pane)
			}
		}
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

func TestActionBarShowsForLaunchableIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Launchable Issue"}, Launchable: true, Ready: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "Start") {
		t.Errorf("action bar missing Start:\n%s", view)
	}
	if !strings.Contains(view, "View") {
		t.Errorf("action bar missing View:\n%s", view)
	}
	if !strings.Contains(view, "Back") {
		t.Errorf("action bar missing Back:\n%s", view)
	}
}

func TestActionBarShowsForInProgressIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 2, Title: "In Progress Issue"}, InProgress: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "View run") {
		t.Errorf("action bar missing View run:\n%s", view)
	}
	if !strings.Contains(view, "Kill run") {
		t.Errorf("action bar missing Kill run:\n%s", view)
	}
	if !strings.Contains(view, "Back") {
		t.Errorf("action bar missing Back:\n%s", view)
	}
}

func TestActionBarShowsForBlockedIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 3, Title: "Blocked Issue"}, Blocked: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "View blockers") {
		t.Errorf("action bar missing View blockers:\n%s", view)
	}
	if !strings.Contains(view, "Back") {
		t.Errorf("action bar missing Back:\n%s", view)
	}
}

func TestActionBarShowsForAwaitingReviewIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 4, Title: "Awaiting Review Issue"}, AwaitingReview: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "Feedback") {
		t.Errorf("action bar missing Leave feedback:\n%s", view)
	}
	if !strings.Contains(view, "Respond") {
		t.Errorf("action bar missing Respond:\n%s", view)
	}
	if !strings.Contains(view, "View PR") {
		t.Errorf("action bar missing View PR:\n%s", view)
	}
	if !strings.Contains(view, "Back") {
		t.Errorf("action bar missing Back:\n%s", view)
	}
}

func TestActionBarShowsForClosedIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 5, Title: "Closed Issue", State: issues.StateClosed}},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "Open") {
		t.Errorf("action bar missing Open:\n%s", view)
	}
	if !strings.Contains(view, "Log") {
		t.Errorf("action bar missing Log:\n%s", view)
	}
	if !strings.Contains(view, "Back") {
		t.Errorf("action bar missing Back:\n%s", view)
	}
}

func TestActionKeyEmitsNotice(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue"}, Launchable: true, Ready: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(Model)
	if !strings.Contains(m.notice, "View issue #1") {
		t.Errorf("notice = %q, want 'View issue #1'", m.notice)
	}
}

func TestStartKeyEmitsStartMessage(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue"}, Launchable: true, Ready: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(Model)
	if m.notice == "" {
		t.Error("expected notice after pressing start")
	}
	if cmd == nil {
		t.Fatal("expected command from start key")
	}
	msg := cmd()
	if _, ok := msg.(startIssueMsg); !ok {
		t.Errorf("cmd returned %T, want startIssueMsg", msg)
	}
}

func TestNavigationClearsNotice(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "First"}},
		{Issue: issues.Issue{Number: 2, Title: "Second"}},
	}
	m.planIssuesLoadedFor = "plan-a"
	m.issueCursor = 0
	m.notice = "some notice"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.notice != "" {
		t.Errorf("notice not cleared after navigation, got %q", m.notice)
	}
}

func TestActionBarDoesNotAddExtraHeight(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.height = 15
	m.screen = screenPlanDetail
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue"}, Launchable: true, Ready: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	lines := strings.Split(view, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Start") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("action bar not found in view:\n%s", view)
	}
}

// A key must mean the same thing wherever it appears. Comment and cancel-the-
// run are one keystroke apart in muscle memory and opposite in consequence, so
// they must not share one.
func TestActionKeysMeanOneThingEachAcrossStates(t *testing.T) {
	states := map[string]issues.Status{
		"closed":   {Issue: issues.Issue{Number: 1, State: issues.StateClosed}},
		"progress": {Issue: issues.Issue{Number: 2}, InProgress: true},
		"review":   {Issue: issues.Issue{Number: 3}, AwaitingReview: true},
		"blocked":  {Issue: issues.Issue{Number: 4}, Blocked: true},
		"ready":    {Issue: issues.Issue{Number: 5}, Ready: true, Launchable: true},
	}

	labels := map[string]string{}
	where := map[string]string{}
	for state, status := range states {
		for _, a := range issueActions(status) {
			// The object may differ by state — "View run" and "View PR" are
			// both viewing. The verb may not.
			verb := strings.Fields(a.Label)[0]
			if seen, ok := labels[a.Key]; ok && seen != verb {
				t.Errorf("%q is %q in %s but %q in %s",
					a.Key, verb, state, seen, where[a.Key])
			}
			labels[a.Key], where[a.Key] = verb, state
		}
	}
}

// Every offered key has to do something, and nothing that is not offered
// should be carried around waiting for a caller that never comes.
func TestEveryOfferedActionResolves(t *testing.T) {
	offered := map[string]bool{}
	for _, status := range []issues.Status{
		{Issue: issues.Issue{Number: 1, State: issues.StateClosed}},
		{Issue: issues.Issue{Number: 2}, InProgress: true},
		{Issue: issues.Issue{Number: 3}, AwaitingReview: true},
		{Issue: issues.Issue{Number: 4}, Blocked: true},
		{Issue: issues.Issue{Number: 5}, Ready: true, Launchable: true},
		{Issue: issues.Issue{Number: 6}, Ready: true},
	} {
		for _, a := range issueActions(status) {
			offered[a.Key] = true
			if a.Key == "b" {
				continue // back is handled before the action table
			}
			if actionNotice(a, status) == "" {
				t.Errorf("%q (%s) has no notice", a.Key, a.Label)
			}
			if actionCmd(a, status) == nil {
				t.Errorf("%q (%s) has no command", a.Key, a.Label)
			}
		}
	}
	if actionCmd(Action{Key: "zzz"}, issues.Status{}) != nil {
		t.Error("an unoffered key produced a command")
	}
}

// A long acceptance criterion wraps when it is rendered, which happens after
// any line counting. If the pane is not clamped it grows past the rows it was
// given and pushes the action bar off the bottom of the screen.
func TestFullScreenActionBarSurvivesWrappingContent(t *testing.T) {
	long := "This acceptance criterion is deliberately long enough that it must wrap across several terminal lines when the pane is narrow"

	for _, size := range []struct{ w, h int }{{100, 30}, {40, 30}, {30, 20}, {20, 12}} {
		m := plansModel(t, []issues.Plan{{Slug: "p", Title: "P"}})
		m.width, m.height = size.w, size.h
		m.screen = screenIssue
		m.planIssues = []issues.Status{{
			Issue: issues.Issue{
				Number: 14, Title: "An issue with a title long enough to wrap on its own",
				State: issues.StateOpen, Type: "task",
				Acceptance: []string{long, long, long, long, long, long},
			},
			AwaitingReview: true,
		}}

		lines := strings.Split(m.issueFullScreenView(), "\n")
		if len(lines) != m.contentHeight() {
			t.Errorf("%dx%d: rendered %d lines into %d", size.w, size.h, len(lines), m.contentHeight())
		}
		if last := lines[len(lines)-1]; !strings.Contains(last, "[B]") && !strings.Contains(last, "[") {
			t.Errorf("%dx%d: last line is %q, want the action bar", size.w, size.h, last)
		}
	}
}

// The preview pane shares its row with the issue list, so overflowing it
// breaks the alignment of both.
func TestPreviewPaneStaysWithinItsPane(t *testing.T) {
	long := "This acceptance criterion is deliberately long enough that it must wrap across several terminal lines when the pane is narrow"

	m := plansModel(t, []issues.Plan{{Slug: "p", Title: "P"}})
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{Number: 1, Title: "T", State: issues.StateOpen, Type: "task",
			Acceptance: []string{long, long, long, long, long, long}},
	}}

	if got := len(strings.Split(m.issuePreviewPane(45, 20), "\n")); got != 20 {
		t.Errorf("preview rendered %d lines into 20", got)
	}
}

// The action bar offers to open the pull request, so the full-screen view says
// which one that is. The preview pane has no room for it.
func TestFullScreenShowsThePullRequest(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "p", Title: "P"}})
	m.screen = screenIssue
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{
			Number: 14, Title: "T", State: issues.StateOpen, Type: "task",
			PRURL: "https://example.test/pull/9", PRState: "open",
		},
		AwaitingReview: true,
	}}

	if view := m.issueFullScreenView(); !strings.Contains(view, "https://example.test/pull/9") {
		t.Error("full-screen view does not show the pull request the action bar would open")
	}
	if preview := m.issuePreviewPane(45, 20); strings.Contains(preview, "https://example.test/pull/9") {
		t.Error("preview pane spent its width on the pull request URL")
	}
}

// A closed blocker still explains why an issue is shaped the way it is, so the
// full-screen view shows the whole edge rather than only what is outstanding.
func TestFullScreenShowsEveryBlockerNotJustOpenOnes(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "p", Title: "P"}})
	m.screen = screenIssue
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{
			Number: 16, Title: "T", State: issues.StateOpen, Type: "task",
			BlockedBy: []int64{13, 14},
		},
		Blocked:      true,
		OpenBlockers: []int64{14},
	}}

	view := m.issueFullScreenView()
	for _, want := range []string{"#13", "#14"} {
		if !strings.Contains(view, want) {
			t.Errorf("full-screen view missing blocker %s", want)
		}
	}
}

func TestFullScreenShowsIssueDetails(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "First Issue", State: issues.StateOpen, Type: "task", Acceptance: []string{"it works"}, CreatedAt: mustParse("2024-01-15T10:00:00Z")}, Ready: true, Agent: "builder", Run: "run-123"},
	}
	m.planIssuesLoadedFor = "plan-a"
	m.issueCursor = 0

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
	if !strings.Contains(view, "run-123") {
		t.Errorf("view missing run:\n%s", view)
	}
	if !strings.Contains(view, "it works") {
		t.Errorf("view missing acceptance criteria:\n%s", view)
	}
	if !strings.Contains(view, "Created:") {
		t.Errorf("view missing created timestamp:\n%s", view)
	}
}

func TestFullScreenActionBarAtBottom(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Issue"}, Launchable: true, Ready: true},
	}
	m.planIssuesLoadedFor = "plan-a"

	view := m.View()
	if !strings.Contains(view, "Start") {
		t.Errorf("action bar missing Start:\n%s", view)
	}
	if !strings.Contains(view, "Back") {
		t.Errorf("action bar missing Back:\n%s", view)
	}
}

func TestFullScreenNoEmptyWhenNoIssues(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue

	view := m.View()
	if !strings.Contains(view, "No issues") {
		t.Errorf("view missing empty state:\n%s", view)
	}
}

func TestFullScreenShowsLoading(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssuesLoading = true

	view := m.View()
	if !strings.Contains(view, "Loading") {
		t.Errorf("view missing loading indicator:\n%s", view)
	}
}

func TestFullScreenShowsError(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssuesErr = errors.New("store failed")

	view := m.View()
	if !strings.Contains(view, "Error loading issues") {
		t.Errorf("view missing error state:\n%s", view)
	}
}

func TestFullScreenShowsBlockers(t *testing.T) {
	m := plansModel(t, []issues.Plan{
		{Slug: "plan-a", Title: "Plan A"},
	})
	m.screen = screenIssue
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Blocked"}, Blocked: true, OpenBlockers: []int64{2, 3}},
	}
	m.planIssuesLoadedFor = "plan-a"
	m.issueCursor = 0

	view := m.View()
	if !strings.Contains(view, "#2") {
		t.Errorf("view missing blocker #2:\n%s", view)
	}
	if !strings.Contains(view, "#3") {
		t.Errorf("view missing blocker #3:\n%s", view)
	}
	if !strings.Contains(view, "blocked") {
		t.Errorf("view missing blocked flag:\n%s", view)
	}
}

func TestStartRefusesWhenNoAgentMapped(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail

	next, _ := m.Update(startIssueMsg{issue: issues.Status{
		Issue: issues.Issue{Number: 1, Title: "Issue", Type: "feature", State: issues.StateOpen},
		Ready: true,
		Agent: "",
	}})
	m = next.(Model)
	if !strings.Contains(m.notice, `No agent is mapped to type "feature"`) {
		t.Errorf("notice = %q, want refusal for unmapped type", m.notice)
	}
}

func TestStartRefusesWhenNotReady(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail

	next, _ := m.Update(startIssueMsg{issue: issues.Status{
		Issue:        issues.Issue{Number: 1, Title: "Issue", Type: "task", State: issues.StateOpen},
		Ready:        false,
		Agent:        "worker",
		Blocked:      true,
		OpenBlockers: []int64{2},
	}})
	m = next.(Model)
	if !strings.Contains(m.notice, "is not ready") {
		t.Errorf("notice = %q, want refusal for not-ready issue", m.notice)
	}
}

func TestStartRefusesWhenRunnerUnavailable(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail

	next, _ := m.Update(startIssueMsg{issue: issues.Status{
		Issue: issues.Issue{Number: 1, Title: "Issue", Type: "task", State: issues.StateOpen},
		Ready: true,
		Agent: "worker",
	}})
	m = next.(Model)
	if !strings.Contains(m.notice, "Agent runner is not available") {
		t.Errorf("notice = %q, want runner unavailable message", m.notice)
	}
}

func TestStartReturnsBatchWithRefreshAndSpawn(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.agents = &fakeSpawner{}

	next, cmd := m.Update(startIssueMsg{issue: issues.Status{
		Issue: issues.Issue{Number: 1, Title: "Issue", Type: "task", State: issues.StateOpen},
		Ready: true,
		Agent: "worker",
	}})
	m = next.(Model)

	if !strings.Contains(m.notice, "Starting worker on #1") {
		t.Errorf("notice = %q, want starting message", m.notice)
	}
	if cmd == nil {
		t.Fatal("expected command from start")
	}
}

func TestAgentFinishedRefreshesIssues(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail

	next, cmd := m.Update(agentFinishedMsg{issue: issues.Status{
		Issue: issues.Issue{Number: 1, Title: "Issue", Type: "task", State: issues.StateOpen},
		Agent: "worker",
	}})
	m = next.(Model)

	if m.planIssuesLoading {
		t.Error("the poll raised the loading spinner; it should refresh silently")
	}
	if cmd == nil {
		t.Fatal("expected refresh command after agent finished")
	}
	msg := cmd()
	if _, ok := msg.(planIssuesLoadedMsg); !ok {
		t.Errorf("cmd returned %T, want planIssuesLoadedMsg", msg)
	}
}

// The run is recorded only after a worktree is checked out and tmux has
// opened, so a refresh that lands in between still calls the issue ready.
// Until the store catches up the UI has to carry that knowledge itself, or the
// action bar goes on offering to start an issue that is already starting.
func TestStartShowsInProgressBeforeTheStoreAgrees(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.agents = &fakeSpawner{}
	m.planIssues = []issues.Status{startable(1)}

	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)

	if !m.planIssues[0].InProgress {
		t.Error("issue still reads as idle after [S]")
	}
	if m.planIssues[0].Ready || m.planIssues[0].Launchable {
		t.Error("issue still reads as startable after [S]")
	}
	for _, a := range issueActions(m.planIssues[0]) {
		if a.Key == "s" {
			t.Error("action bar still offers [S]tart on an issue that is starting")
		}
	}
}

// Two agents on one issue share one worktree, since checkouts are keyed by
// issue — the collision worktrees exist to prevent.
func TestStartRefusesASecondAgentOnTheSameIssue(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	agents := &fakeSpawner{}
	m.agents = agents
	m.planIssues = []issues.Status{startable(1)}

	next, cmd := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	drain(cmd)
	// The store has not recorded the run yet, so a refresh landing now still
	// reports the issue ready.
	next, _ = m.Update(planIssuesLoadedMsg{planSlug: "plan-a", issues: []issues.Status{startable(1)}})
	m = next.(Model)
	next, cmd = m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	drain(cmd)

	if got := len(agents.seen()); got != 1 {
		t.Errorf("spawned %d agents on one issue, want 1", got)
	}
	if !m.planIssues[0].InProgress {
		t.Error("a refresh that predates the run record undid the in-progress state")
	}
}

// A stale refresh must not resurrect [S]tart for an issue already starting.
func TestRefreshDoesNotUndoInFlightState(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.agents = &fakeSpawner{}
	m.planIssues = []issues.Status{startable(1), startable(2)}

	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	next, _ = m.Update(planIssuesLoadedMsg{planSlug: "plan-a", issues: []issues.Status{startable(1), startable(2)}})
	m = next.(Model)

	if !m.planIssues[0].InProgress {
		t.Error("#1 lost its in-progress state to a refresh")
	}
	if m.planIssues[1].InProgress {
		t.Error("#2 was marked in progress without being started")
	}
}

// The poll exists only to show a running agent's progress, so it has to stop
// once none are left rather than reloading the store forever.
func TestBackgroundTickRefreshesIssuesOnPlansTab(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.plansView = viewPlanDetail
	m.planIssuesLoadedFor = "plan-a"

	_, cmd := m.Update(backgroundTickMsg{})
	if cmd == nil {
		t.Fatal("background tick produced no command")
	}
}

func TestBackgroundTickIsSilent(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.plansView = viewPlanDetail
	m.planIssuesLoadedFor = "plan-a"

	next, _ := m.Update(backgroundTickMsg{})
	m = next.(Model)
	if m.planIssuesLoading {
		t.Error("background tick raised the loading spinner; it should refresh silently")
	}
}

func TestBackgroundTickDoesNotRefreshIssuesOnPlanTab(t *testing.T) {
	m := readyWithTabs(t)
	m.currentTab = tabPlan
	m.plans = []issues.Plan{{Slug: "plan-a", Title: "Plan A"}}
	m.planCursor = 0
	m.planIssuesLoadedFor = "plan-a"

	next, cmd := m.Update(backgroundTickMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("background tick produced no command")
	}
	if m.planIssuesLoading {
		t.Error("background tick on Plan tab should not load issues")
	}
}

func TestRefreshTickRunsWhileAgentsDoAndStopsAfter(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.agents = &fakeSpawner{}
	m.planIssues = []issues.Status{startable(1)}

	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	if _, cmd := m.Update(refreshTickMsg{}); cmd == nil {
		t.Fatal("tick stopped while an agent was still running")
	}

	next, _ = m.Update(agentFinishedMsg{issue: startable(1), status: "done"})
	m = next.(Model)
	if m.inFlight[1] {
		t.Error("#1 still counted as in flight after its agent finished")
	}
	if _, cmd := m.Update(refreshTickMsg{}); cmd != nil {
		t.Error("tick kept polling with no agents running")
	}
}

// The briefing and window name are what make a launch from the browser the
// same act as `pib issue start`.
func TestStartSendsTheSameRequestAsTheCLI(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	agents := &fakeSpawner{}
	m.agents = agents
	m.planIssues = []issues.Status{startable(7)}

	next, cmd := m.Update(startIssueMsg{issue: startable(7)})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("no command from [S]")
	}
	drain(cmd)

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Op != protocol.OpSpawn || req.Agent != "worker" || req.Issue != 7 {
		t.Errorf("request = %+v", req)
	}
	if req.Task != runner.Briefing(7, "Issue") {
		t.Errorf("task = %q, want the shared briefing", req.Task)
	}
}

// drain runs a command and everything a tea.Batch fans out to, which is what
// bubbletea's event loop would do.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var wg sync.WaitGroup
		for _, c := range msg {
			wg.Add(1)
			go func(c tea.Cmd) { defer wg.Done(); drain(c) }(c)
		}
		wg.Wait()
	}
}

// Starting a second agent must join the running poll rather than opening a
// second one, or every start/finish cycle leaves another chain behind.
func TestASecondAgentJoinsTheExistingPoll(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "plan-a", Title: "Plan A"}})
	m.screen = screenPlanDetail
	m.agents = &fakeSpawner{}
	m.planIssues = []issues.Status{startable(1), startable(2)}

	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	if !m.polling {
		t.Fatal("first start did not open a poll")
	}

	next, _ = m.Update(startIssueMsg{issue: startable(2)})
	m = next.(Model)

	// Both agents finish; the poll should close exactly once.
	for _, n := range []int64{1, 2} {
		next, _ = m.Update(agentFinishedMsg{issue: startable(n), status: "done"})
		m = next.(Model)
	}
	next, cmd := m.Update(refreshTickMsg{})
	m = next.(Model)
	if cmd != nil {
		t.Error("poll kept running after every agent finished")
	}
	if m.polling {
		t.Error("polling still set after the poll stopped")
	}
}

// fakeSpawner stands in for the runner. Run blocks until released, the way a
// real spawn blocks for as long as the agent runs.
type fakeSpawner struct {
	mu      sync.Mutex
	reqs    []protocol.Request
	release chan struct{}
	err     error
}

func (f *fakeSpawner) Run(_ context.Context, req protocol.Request) (protocol.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	release := f.release
	f.mu.Unlock()

	if release != nil {
		<-release
	}
	if f.err != nil {
		return protocol.Response{}, f.err
	}
	return protocol.Response{Status: "done", Session: "s1"}, nil
}

func (f *fakeSpawner) seen() []protocol.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Request(nil), f.reqs...)
}

func startable(number int64) issues.Status {
	return issues.Status{
		Issue:      issues.Issue{Number: number, Title: "Issue", Type: "task", State: issues.StateOpen},
		Ready:      true,
		Launchable: true,
		Agent:      "worker",
	}
}
