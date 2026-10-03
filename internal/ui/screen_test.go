package ui

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/agent"
	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/runner"
	"pib/internal/workspace"
)

func plansModel(t *testing.T, plans []issues.Plan) Model {
	t.Helper()
	m := ready(t)
	m.screen = screenPlans
	m.width = 100
	m.height = 30
	for _, plan := range plans {
		m.plans = append(m.plans, issues.PlanStatus{Plan: plan})
	}
	return m
}

// issuesModel parks the model on the issues screen of one plan, its rows
// already loaded.
func issuesModel(t *testing.T, list []issues.Status) Model {
	t.Helper()
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.screen = screenIssues
	m.drilled = "orders"
	m.planIssues = list
	return m
}

// collect runs a command and everything a tea.Batch fans out to, gathering
// the messages — the two halves of what the bubbletea runtime does on its
// own.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// deliver feeds messages back to the model, running each command that comes
// out of an update and delivering its messages in turn — the loop the
// bubbletea runtime runs, so a key press is tested by what it does, not by
// the command it returned.
func deliver(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	queue := append([]tea.Msg(nil), msgs...)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		next, cmd := m.Update(msg)
		m = next.(Model)
		queue = append(queue, collect(cmd)...)
	}
	return m
}

// keyPress sends one key and runs out everything it starts.
func keyPress(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	return deliver(t, m, msg)
}

func startable(number int64) issues.Status {
	return issues.Status{
		Issue:      issues.Issue{Number: number, Title: "Issue", Type: "task", State: issues.StateOpen},
		Ready:      true,
		Launchable: true,
		Agent:      "coder",
	}
}

// fakeSpawner stands in for the runner. Run blocks until released, the way a
// real spawn blocks for as long as the agent runs.
type fakeSpawner struct {
	mu        sync.Mutex
	reqs      []protocol.Request
	active    int
	maxActive int
	release   chan struct{}
	err       error
}

func (f *fakeSpawner) Run(_ context.Context, req protocol.Request) (protocol.Response, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.reqs = append(f.reqs, req)
	release := f.release
	f.mu.Unlock()

	if release != nil {
		<-release
	}
	f.mu.Lock()
	f.active--
	f.mu.Unlock()

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

// defaultCfg loads the configuration defaults, so AgentFor maps the built-in
// types and ReviewCycles is the workspace cap.
func defaultCfg(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadPaths(filepath.Join(t.TempDir(), "missing.toml"), "")
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}
	return cfg
}

// ── The three screens ────────────────────────────────────────────────────

func TestPlansScreenRendersTableBreadcrumbDetailAndBar(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.planProse = map[string]issueProse{
		"orders": {file: issues.File{Body: "Build the order pipeline.", Acceptance: []string{"orders can be placed"}}},
	}

	view := m.View()
	for _, want := range []string{"pib", "TITLE", "STATE", "READY", "RUNNING", "NEEDS YOU", "orders", "Build the order pipeline.", "orders can be placed", "? Help"} {
		if !strings.Contains(view, want) {
			t.Errorf("plans screen missing %q:\n%s", want, view)
		}
	}

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) > m.height {
		t.Errorf("rendered %d lines into %d", len(lines), m.height)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "? Help") {
		t.Errorf("last line is %q, want the command bar", last)
	}
}

func TestIssuesScreenRendersTableAndPreview(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(13)})
	m.issueProse = map[int64]issueProse{
		13: {file: issues.File{Body: "Aggregate the orders."}},
	}

	view := m.View()
	for _, want := range []string{"pib › orders", "#", "TYPE", "TITLE", "STATE", "AGENT", "PR", "#13", "task", "launchable", "coder", "Aggregate the orders.", "? Help"} {
		if !strings.Contains(view, want) {
			t.Errorf("issues screen missing %q:\n%s", want, view)
		}
	}
}

func TestIssueScreenRendersTheIssueFullScreen(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(13)})
	m.screen = screenIssue
	m.issueProse = map[int64]issueProse{
		13: {file: issues.File{Body: "Aggregate the orders."}},
	}

	view := m.View()
	for _, want := range []string{"pib › orders › #13", "State:", "open", "Aggregate the orders.", "? Help"} {
		if !strings.Contains(view, want) {
			t.Errorf("issue screen missing %q:\n%s", want, view)
		}
	}
}

// The detail pane takes the bottom third; a short terminal drops it and the
// tables get the height.
func TestShortTerminalDropsTheDetailPane(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.planProse = map[string]issueProse{
		"orders": {file: issues.File{Body: "Build the order pipeline."}},
	}

	m.height = 12
	if view := m.View(); strings.Contains(view, "Build the order pipeline.") {
		t.Errorf("the detail pane rendered into a short terminal:\n%s", view)
	}

	m.height = 30
	if view := m.View(); !strings.Contains(view, "Build the order pipeline.") {
		t.Errorf("the detail pane is missing at full height:\n%s", view)
	}
}

func TestPlansScreenShowsEmptyState(t *testing.T) {
	m := plansModel(t, nil)
	view := m.View()
	if !strings.Contains(view, "No plans yet") {
		t.Errorf("empty plans table does not say so:\n%s", view)
	}
	if !strings.Contains(view, "press n") {
		t.Errorf("empty plans table does not point at new:\n%s", view)
	}
}

func TestPlansScreenShowsErrorState(t *testing.T) {
	m := plansModel(t, nil)
	m.plansErr = context.DeadlineExceeded
	if view := m.View(); !strings.Contains(view, "Error loading plans") {
		t.Errorf("the load failure does not render:\n%s", view)
	}
}

func TestPlansScreenShowsLoadingState(t *testing.T) {
	m := plansModel(t, nil)
	m.plansLoading = true
	if view := m.View(); !strings.Contains(view, "Loading plans") {
		t.Errorf("the loading state does not render:\n%s", view)
	}
}

// ── Drill-down and back ──────────────────────────────────────────────────

func TestEnterDrillsIntoAPlanAndLoadsItsIssues(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.store = store

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != screenIssues {
		t.Fatalf("screen = %v, want the issues screen", m.screen)
	}
	if m.drilled != "orders" {
		t.Errorf("drilled = %q, want orders", m.drilled)
	}
	if m.planIssuesLoading {
		t.Error("the load's answer arrived but the spinner is still up")
	}
}

func TestLAndRightDrillLikeEnter(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("l")},
		{Type: tea.KeyRight},
	} {
		m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
		next, _ := m.Update(key)
		m = next.(Model)
		if m.screen != screenIssues {
			t.Errorf("%v: screen = %v, want the issues screen", key, m.screen)
		}
	}
}

func TestEnterOnAPlanningRowGoesNowhere(t *testing.T) {
	m := plansModel(t, nil)
	m.plans = []issues.PlanStatus{{Plan: issues.Plan{Title: "r1"}, State: issues.PlanPlanning, Run: "r1"}}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != screenPlans {
		t.Errorf("screen = %v, want the plans screen still", m.screen)
	}
	if !strings.Contains(m.notice, "still writing") {
		t.Errorf("notice = %q, want it to say why there is no drilling", m.notice)
	}
}

func TestEnterDrillsIntoAnIssue(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(13)})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != screenIssue {
		t.Fatalf("screen = %v, want the issue screen", m.screen)
	}
	if m.issueScroll != 0 {
		t.Errorf("issueScroll = %d on opening, want 0", m.issueScroll)
	}
}

func TestEscBacksOutKeepingTheCursor(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}, {Slug: "b", Title: "B"}})
	m.planCursor = 1
	m.screen = screenIssues
	m.drilled = "b"
	m.planIssues = []issues.Status{startable(1), startable(2)}
	m.issueCursor = 1
	m.screen = screenIssue

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenIssues {
		t.Fatalf("screen = %v, want the issues screen", m.screen)
	}
	if m.issueCursor != 1 {
		t.Errorf("issueCursor = %d after esc, want it kept at 1", m.issueCursor)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Fatalf("screen = %v, want the plans screen", m.screen)
	}
	if m.planCursor != 1 {
		t.Errorf("planCursor = %d after esc, want it kept at 1", m.planCursor)
	}
}

func TestHAndLeftBackOut(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("h")},
		{Type: tea.KeyLeft},
	} {
		m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}})
		m.screen = screenIssues
		m.drilled = "a"
		next, _ := m.Update(key)
		m = next.(Model)
		if m.screen != screenPlans {
			t.Errorf("%v: screen = %v, want the plans screen", key, m.screen)
		}
	}
}

func TestQBacksOutAndQuitsFromTheRoot(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}})
	m.screen = screenIssue
	m.drilled = "a"
	m.planIssues = []issues.Status{startable(1)}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if m.screen != screenIssues {
		t.Fatalf("q on the issue screen: screen = %v, want issues", m.screen)
	}
	if _, quitting := cmdMsg(cmd).(tea.QuitMsg); quitting {
		t.Error("q on the issue screen quit; it backs out")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Fatalf("q on the issues screen: screen = %v, want plans", m.screen)
	}
	if _, quitting := cmdMsg(cmd).(tea.QuitMsg); quitting {
		t.Error("q on the issues screen quit; only the root quits")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Fatalf("q at the root moved to %v, want it to stay", m.screen)
	}
	if _, quitting := cmdMsg(cmd).(tea.QuitMsg); !quitting {
		t.Error("q at the root did not quit")
	}
}

func cmdMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestEscAtTheRootNeitherQuitsNorMoves(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenPlans {
		t.Errorf("esc at the root moved to %v", m.screen)
	}
	if _, quitting := cmdMsg(cmd).(tea.QuitMsg); quitting {
		t.Error("esc at the root quit; q does that")
	}
}

func TestCtrlCQuitsFromEveryScreen(t *testing.T) {
	for _, start := range []screen{screenPlans, screenIssues, screenIssue, screenSettings} {
		m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}})
		m.screen = start
		m.drilled = "a"
		m.planIssues = []issues.Status{startable(1)}

		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if _, quitting := cmdMsg(cmd).(tea.QuitMsg); !quitting {
			t.Errorf("screen %v: ctrl+c did not quit", start)
		}
	}
}

// ── Cursor motion ────────────────────────────────────────────────────────

func TestPlanTableNavigation(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}, {Slug: "b", Title: "B"}, {Slug: "c", Title: "C"}})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.planCursor != 1 {
		t.Fatalf("j: planCursor = %d, want 1", m.planCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.planCursor != 0 {
		t.Fatalf("k: planCursor = %d, want 0", m.planCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if m.planCursor != 2 {
		t.Fatalf("G: planCursor = %d, want 2", m.planCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.planCursor != 0 {
		t.Fatalf("g: planCursor = %d, want 0", m.planCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.planCursor != 1 {
		t.Fatalf("down: planCursor = %d, want 1", m.planCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.planCursor != 2 {
		t.Fatalf("pgdown clamps at the last row: planCursor = %d, want 2", m.planCursor)
	}
}

func TestIssueTableNavigation(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1), startable(2), startable(3)})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.issueCursor != 1 {
		t.Fatalf("down: issueCursor = %d, want 1", m.issueCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.issueCursor != 0 {
		t.Fatalf("up: issueCursor = %d, want 0", m.issueCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if m.issueCursor != 2 {
		t.Fatalf("G: issueCursor = %d, want 2", m.issueCursor)
	}
}

// The cursor is followed by identity, not position: a reload that reorders
// the table leaves the same row selected.
func TestPlanCursorFollowsItsPlanAcrossReloads(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "a", Title: "A"}, {Slug: "b", Title: "B"}})
	m.planCursor = 1

	next, _ := m.Update(plansLoadedMsg{plans: []issues.PlanStatus{
		{Plan: issues.Plan{Slug: "b", Title: "B"}, Ready: 1},
		{Plan: issues.Plan{Slug: "a", Title: "A"}},
	}})
	m = next.(Model)

	plan, ok := m.selectedPlan()
	if !ok || plan.Slug != "b" {
		t.Errorf("selected = %v, %v; want plan b still under the cursor", plan.Slug, ok)
	}
	// b has a ready issue now, so it sorted ahead of a — and the cursor came
	// with it rather than pointing at whatever moved into row 1.
	if m.planCursor != 0 {
		t.Errorf("planCursor = %d, want 0 after b sorted to the top", m.planCursor)
	}
}

func TestIssueCursorFollowsItsIssueAcrossReloads(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1), startable(2)})
	m.issueCursor = 1

	next, _ := m.Update(planIssuesLoadedMsg{planSlug: "orders", issues: []issues.Status{
		startable(2), startable(1),
	}})
	m = next.(Model)

	issue, ok := m.selectedIssue()
	if !ok || issue.Number != 2 {
		t.Errorf("selected = %v, %v; want #2 still under the cursor", issue.Number, ok)
	}
}

// A response for a plan the user has already left is stale.
func TestStalePlanIssuesResponseIsIgnored(t *testing.T) {
	m := issuesModel(t, nil)
	next, _ := m.Update(planIssuesLoadedMsg{planSlug: "billing", issues: []issues.Status{startable(9)}})
	m = next.(Model)
	if len(m.planIssues) != 0 {
		t.Errorf("a stale load filled the table with %d issues", len(m.planIssues))
	}
}

// ── Sorting and filters ──────────────────────────────────────────────────

func TestNeedsYouPlansSortFirst(t *testing.T) {
	m := plansModel(t, nil)
	m.plans = []issues.PlanStatus{
		{Plan: issues.Plan{Slug: "quiet", Title: "Quiet"}},
		{Plan: issues.Plan{Slug: "blocked-work", Title: "Blocked"}, NeedsAttention: 2},
		{Plan: issues.Plan{Slug: "ready-work", Title: "Ready"}, Ready: 3},
	}

	visible := m.visiblePlans()
	if visible[0].Slug != "blocked-work" || visible[1].Slug != "ready-work" || visible[2].Slug != "quiet" {
		t.Errorf("order = %s, %s, %s; want the rows that need the user first", visible[0].Slug, visible[1].Slug, visible[2].Slug)
	}
}

func TestNeedsYouIssuesSortFirst(t *testing.T) {
	blocked := issues.Status{
		Issue:   issues.Issue{Number: 1, Title: "Blocked", State: issues.StateOpen},
		Blocked: true,
	}
	attention := issues.Status{
		Issue:           issues.Issue{Number: 2, Title: "Failed", State: issues.StateOpen},
		NeedsAttention:  true,
		AttentionReason: issues.AttentionFailed,
	}
	m := issuesModel(t, []issues.Status{blocked, startable(3), attention})

	visible := m.visibleIssues()
	if visible[0].Number != 3 || visible[1].Number != 2 || visible[2].Number != 1 {
		t.Errorf("order = #%d, #%d, #%d; want launchable and needs attention first",
			visible[0].Number, visible[1].Number, visible[2].Number)
	}
}

// Z shows and hides the plans the store archived. It is a load parameter
// there, so toggling it reloads the table.
func TestZTogglesArchivedPlans(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := store.ArchivePlan("orders"); err != nil {
		t.Fatalf("ArchivePlan: %v", err)
	}

	m := plansModel(t, nil)
	m.store = store
	m.cfg = defaultCfg(t)

	m = deliver(t, m, collect(loadPlans(store, false, m.cfg, 0))...)
	if len(m.visiblePlans()) != 0 {
		t.Fatalf("an archived plan showed without Z: %v", m.visiblePlans())
	}

	m = deliver(t, m, toggleClosedMsg{})
	visible := m.visiblePlans()
	if len(visible) != 1 || visible[0].State != issues.PlanArchived {
		t.Fatalf("Z did not bring the archived plan back: %v", visible)
	}
}

// Z shows and hides closed issues. They are already loaded, so the table
// refilters what it holds.
func TestZTogglesClosedIssues(t *testing.T) {
	closed := issues.Status{Issue: issues.Issue{Number: 1, Title: "Done", State: issues.StateClosed}}
	m := issuesModel(t, []issues.Status{closed, startable(2)})

	if got := m.visibleIssues(); len(got) != 1 || got[0].Number != 2 {
		t.Fatalf("closed issues showed without Z: %v", got)
	}

	m = deliver(t, m, toggleClosedMsg{})
	if got := m.visibleIssues(); len(got) != 2 {
		t.Fatalf("Z did not bring the closed issue back: %v", got)
	}
}

// ! keeps only the rows that need the user.
func TestBangFiltersToNeedsYou(t *testing.T) {
	quiet := issues.Status{Issue: issues.Issue{Number: 1, Title: "Quiet", State: issues.StateOpen}, Ready: true}
	m := issuesModel(t, []issues.Status{quiet, startable(2)})

	m = deliver(t, m, toggleNeedsYouMsg{})
	if got := m.visibleIssues(); len(got) != 1 || got[0].Number != 2 {
		t.Fatalf("! kept %v, want only the launchable row", got)
	}
	if !strings.Contains(m.breadcrumbSummary(), "!") {
		t.Errorf("the breadcrumb does not show the filter is on: %q", m.breadcrumbSummary())
	}

	m = deliver(t, m, toggleNeedsYouMsg{})
	if got := m.visibleIssues(); len(got) != 2 {
		t.Fatalf("! did not toggle back off: %v", got)
	}
}

func TestBangOnThePlansTable(t *testing.T) {
	m := plansModel(t, nil)
	m.plans = []issues.PlanStatus{
		{Plan: issues.Plan{Slug: "quiet", Title: "Quiet"}},
		{Plan: issues.Plan{Slug: "busy", Title: "Busy"}, Ready: 2},
	}

	m = deliver(t, m, toggleNeedsYouMsg{})
	if got := m.visiblePlans(); len(got) != 1 || got[0].Slug != "busy" {
		t.Fatalf("! kept %v, want only the plan with ready work", got)
	}
}

// ── Store events ─────────────────────────────────────────────────────────

// The whole live path, end to end: startup subscribes to the store, the
// first load fills the table, and from then on the store's own events are
// what reload it. No tick is involved — there is none left to involve.
func TestStoreEventsDriveTheTables(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	m := NewModel()
	m, _ = step(t, m, detectedMsg{status: workspace.Status{GitRoot: "/repo", Dir: "/repo/.pib", Exists: true}})
	m, _ = step(t, m, agentsCheckedMsg{installed: true, dir: "/home/.pib/agents"})
	m, _ = step(t, m, plannerLoadedMsg{planner: agent.Definition{Name: "planner"}})
	next, cmd := m.Update(serverStartedMsg{store: store, config: defaultCfg(t)})
	m = next.(Model)

	if m.events == nil {
		t.Fatal("startup did not subscribe to the store")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("startup armed %v, want the wait, the first load and the scan", batch)
	}

	// The first load is a load, not an event: it fills the table once.
	m = deliver(t, m, batch[1]())
	if len(m.plans) != 1 || m.plans[0].Slug != "orders" {
		t.Fatalf("plans = %v after the first load, want orders", m.plans)
	}

	// A write through the store publishes; the armed wait is what hears it.
	heard := make(chan tea.Msg, 1)
	go func() { heard <- batch[0]() }()
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var event tea.Msg
	select {
	case event = <-heard:
	case <-time.After(5 * time.Second):
		t.Fatal("no store event arrived at the subscription")
	}
	msg, ok := event.(storeEventMsg)
	if !ok {
		t.Fatalf("the wait delivered %T, want a store event", event)
	}
	if msg.event.Kind != issues.EventIssue || msg.event.Issue != issue.Number {
		t.Errorf("event = %+v, want the issue created", msg.event)
	}

	// Delivering it reloads the plans table: the count the store derives is
	// what shows, not anything the event carried.
	next, cmd = m.Update(msg)
	m = next.(Model)
	reload, _ := cmd().(tea.BatchMsg)
	var msgs []tea.Msg
	for i, c := range reload {
		if i == 0 {
			// The first command is the re-armed wait; running it here would
			// block on the next change. The wait is the runtime's job.
			continue
		}
		if got := c(); got != nil {
			msgs = append(msgs, got)
		}
	}
	m = deliver(t, m, msgs...)
	if len(m.plans) != 1 || m.plans[0].Ready != 1 {
		t.Errorf("plans = %+v after the event, want orders with 1 ready", m.plans)
	}

	// Unsubscribing ends the wait rather than leaking it.
	m.unsubscribe()
	if _, ok := batch[0]().(eventsClosedMsg); !ok {
		t.Errorf("the wait outlived the subscription")
	}
}

func TestStoreEventReloadsThePlansAndTheIssuesOnScreen(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m := issuesModel(t, nil)
	m.store = store
	m.cfg = defaultCfg(t)

	next, cmd := m.Update(storeEventMsg{event: issues.Event{Kind: issues.EventIssue, Plan: "orders", Issue: issue.Number}})
	m = next.(Model)
	msgs := collect(cmd)

	var sawPlans, sawIssues bool
	for _, msg := range msgs {
		switch msg.(type) {
		case plansLoadedMsg:
			sawPlans = true
		case planIssuesLoadedMsg:
			sawIssues = true
		}
	}
	if !sawPlans {
		t.Error("the event reloaded no plans table")
	}
	if !sawIssues {
		t.Error("the event reloaded no issues for the plan on screen")
	}

	m = deliver(t, m, msgs...)
	if len(m.planIssues) != 1 || m.planIssues[0].Number != issue.Number {
		t.Errorf("after the reload the table holds %v, want #%d", m.planIssues, issue.Number)
	}
	if len(m.plans) != 1 || m.plans[0].Slug != "orders" {
		t.Errorf("after the reload the plans table holds %v, want orders", m.plans)
	}
}

// An event naming another plan leaves the issues table alone; the plans
// table always reloads, because a planning row can appear under any of them.
func TestStoreEventForAnotherPlanLeavesTheIssuesTableAlone(t *testing.T) {
	m := issuesModel(t, nil)
	m.store = testStore(t)

	_, cmd := m.Update(storeEventMsg{event: issues.Event{Kind: issues.EventIssue, Plan: "billing", Issue: 1}})
	for _, msg := range collect(cmd) {
		if _, ok := msg.(planIssuesLoadedMsg); ok {
			t.Error("an event for another plan reloaded the issues on screen")
		}
	}
}

// The run-started event is what makes a planning row appear: the event
// triggers a reload, and the reloaded table holds the planner's row.
func TestTheRunStartedEventMakesAPlanningRowAppear(t *testing.T) {
	m := plansModel(t, nil)
	m.store = testStore(t)

	_, cmd := m.Update(storeEventMsg{event: issues.Event{Kind: issues.EventRun}})
	msgs := collect(cmd)
	msgs = append(msgs, plansLoadedMsg{plans: []issues.PlanStatus{
		{Plan: issues.Plan{Title: "r1"}, State: issues.PlanPlanning, Run: "r1"},
	}})
	m = deliver(t, m, msgs...)

	view := m.View()
	if !strings.Contains(view, "planning") {
		t.Errorf("no planning row after the run-started event:\n%s", view)
	}
	if !strings.Contains(view, "r1") {
		t.Errorf("the planning row does not name its run:\n%s", view)
	}
}

// An issue's file changes when a comment or an edit lands, so the event
// drops the cached prose and re-reads it if the cursor is on the issue.
func TestAnIssueEventRefreshesTheProseOnScreen(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate", Body: "first"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m := issuesModel(t, []issues.Status{{Issue: issue}})
	m.store = store
	m.cfg = defaultCfg(t)
	m.issueProse = map[int64]issueProse{issue.Number: {file: issues.File{Body: "stale"}}}

	if _, err := store.Edit(issue.Number, issues.Edit{Body: strptr("fresh")}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	_, cmd := m.Update(storeEventMsg{event: issues.Event{Kind: issues.EventIssue, Plan: "orders", Issue: issue.Number}})
	m = deliver(t, m, collect(cmd)...)

	prose, held := m.issueProse[issue.Number]
	if !held {
		t.Fatal("the prose was dropped and never re-read")
	}
	if !strings.Contains(prose.file.Body, "fresh") {
		t.Errorf("the pane still holds the stale prose: %q", prose.file.Body)
	}
}

func strptr(s string) *string { return &s }

// A burst of store events starts loads that run concurrently and land in
// completion order. An older load landing after a newer one would put stale
// rows back, so it is dropped by number.
func TestAnOlderPlansLoadIsDropped(t *testing.T) {
	m := plansModel(t, nil)
	m.plansSeq = 2

	m = deliver(t, m, plansLoadedMsg{seq: 2, plans: []issues.PlanStatus{{Plan: issues.Plan{Slug: "new"}}}})
	if len(m.plans) != 1 || m.plans[0].Slug != "new" {
		t.Fatalf("the newest load did not apply: %v", m.plans)
	}

	m = deliver(t, m, plansLoadedMsg{seq: 1, plans: []issues.PlanStatus{{Plan: issues.Plan{Slug: "old"}}}})
	if len(m.plans) != 1 || m.plans[0].Slug != "new" {
		t.Errorf("an out-of-order load replaced the rows: %v", m.plans)
	}
}

func TestAnOlderIssuesLoadIsDropped(t *testing.T) {
	m := issuesModel(t, nil)
	m.issuesSeq = 2

	m = deliver(t, m, planIssuesLoadedMsg{seq: 2, planSlug: "orders", issues: []issues.Status{startable(2)}})
	if len(m.planIssues) != 1 || m.planIssues[0].Number != 2 {
		t.Fatalf("the newest load did not apply: %v", m.planIssues)
	}

	m = deliver(t, m, planIssuesLoadedMsg{seq: 1, planSlug: "orders", issues: []issues.Status{startable(1)}})
	if len(m.planIssues) != 1 || m.planIssues[0].Number != 2 {
		t.Errorf("an out-of-order load replaced the rows: %v", m.planIssues)
	}
}

// ── The wired commands ───────────────────────────────────────────────────

// Inside tmux, n opens the planner in front of pib: a foreground spawn
// through the runner, no task — the planner asks what to plan.
func TestNewInsideTmuxSpawnsThePlannerInTheForeground(t *testing.T) {
	old := insideTmux
	insideTmux = func() bool { return true }
	t.Cleanup(func() { insideTmux = old })

	agents := &fakeSpawner{}
	m := plansModel(t, nil)
	m.agents = agents

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("n sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Op != protocol.OpSpawn || req.Agent != "planner" {
		t.Errorf("request = %+v, want a planner spawn", req)
	}
	if !req.Foreground {
		t.Errorf("the planner spawned behind pib: %+v", req)
	}
	if req.Task != "" {
		t.Errorf("the planner was given a task %q; it asks what to plan itself", req.Task)
	}
	if req.Issue != 0 {
		t.Errorf("the planner was tied to issue #%d; it plans the workspace", req.Issue)
	}
}

// Outside tmux, n hands the terminal over as it always has: no run is
// recorded and no planning row appears.
func TestNewOutsideTmuxHandsOverTheTerminal(t *testing.T) {
	oldInside, oldExec := insideTmux, execProcess
	insideTmux = func() bool { return false }
	var handed *exec.Cmd
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		handed = c
		return func() tea.Msg { return plannerFinishedMsg{} }
	}
	t.Cleanup(func() { insideTmux, execProcess = oldInside, oldExec })

	agents := &fakeSpawner{}
	m := plansModel(t, nil)
	m.agents = agents
	m.workspace.GitRoot = "/repo"

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})

	if handed == nil {
		t.Fatal("n outside tmux handed nothing over")
	}
	if handed.Dir != "/repo" {
		t.Errorf("the planner runs in %q, want the repository", handed.Dir)
	}
	if got := agents.seen(); len(got) != 0 {
		t.Errorf("outside tmux a run was recorded through the runner: %v", got)
	}
}

// The run-started event the planner's spawn publishes is what makes the
// planning row appear, so n itself never touches the table.
func TestNewDoesNotTouchTheTableItself(t *testing.T) {
	old := insideTmux
	insideTmux = func() bool { return true }
	t.Cleanup(func() { insideTmux = old })

	m := plansModel(t, nil)
	m.agents = &fakeSpawner{}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})

	if len(m.plans) != 0 {
		t.Errorf("n put %d rows in the table itself; the store event does that", len(m.plans))
	}
}

// start on an issue row starts the issue's agent — the same request the CLI
// sends.
func TestStartSendsTheSameRequestAsTheCLI(t *testing.T) {
	agents := &fakeSpawner{}
	m := issuesModel(t, []issues.Status{startable(7)})
	m.agents = agents

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("s sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Op != protocol.OpSpawn || req.Agent != "coder" || req.Issue != 7 {
		t.Errorf("request = %+v", req)
	}
	if req.Task != runner.Briefing(7, "Issue") {
		t.Errorf("task = %q, want the shared briefing", req.Task)
	}
	if req.Foreground {
		t.Error("an issue's agent opened in the foreground; only the planner does")
	}
}

// The run is recorded only once the agent's window exists, so until the
// store agrees the table has to carry that knowledge itself.
func TestStartShowsInProgressBeforeTheStoreAgrees(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})
	m.agents = &fakeSpawner{}

	// One step only: the spawn's answer belongs to the store's side of the
	// race, and delivering it would close the gap this test lives in.
	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)

	visible := m.visibleIssues()
	if !visible[0].InProgress {
		t.Error("the issue still reads as idle after s")
	}
	if visible[0].Ready || visible[0].Launchable {
		t.Error("the issue still reads as startable after s")
	}
	if row, ok := m.currentRow().(issueRow); ok && row.Launchable() {
		t.Error("the registry still offers start on an issue that is starting")
	}
}

// A refresh that lands before the store has the run must not resurrect the
// start key for an issue already starting.
func TestRefreshDoesNotUndoInFlightState(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1), startable(2)})
	m.agents = &fakeSpawner{}

	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	next, _ = m.Update(planIssuesLoadedMsg{planSlug: "orders", issues: []issues.Status{startable(1), startable(2)}})
	m = next.(Model)

	byNumber := map[int64]issues.Status{}
	for _, issue := range m.visibleIssues() {
		byNumber[issue.Number] = issue
	}
	if !byNumber[1].InProgress {
		t.Error("#1 lost its in-progress state to a refresh")
	}
	if byNumber[2].InProgress {
		t.Error("#2 was marked in progress without being started")
	}
}

// Two agents on one issue share one worktree — the collision worktrees exist
// to prevent. The registry's predicate is what turns the second s away: an
// issue that is starting is not launchable.
func TestStartRefusesASecondAgentOnTheSameIssue(t *testing.T) {
	agents := &fakeSpawner{}
	m := issuesModel(t, []issues.Status{startable(1)})
	m.agents = agents

	// The first start runs; its answer is the store's business, so it is
	// drained away — the in-flight mark is what this test needs standing.
	next, cmd := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	drain(cmd)
	next, _ = m.Update(planIssuesLoadedMsg{planSlug: "orders", issues: []issues.Status{startable(1)}})
	m = next.(Model)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	if got := len(agents.seen()); got != 1 {
		t.Errorf("spawned %d agents on one issue, want 1", got)
	}
}

// An issue that is starting sorts out of the needs-you block. The cursor
// has to come with it, or the next key lands on whatever row the sort slid
// under it — a second s would start a different agent.
func TestStartKeepsTheCursorOnTheIssueItStarted(t *testing.T) {
	agents := &fakeSpawner{}
	m := issuesModel(t, []issues.Status{startable(1), startable(2)})
	m.agents = agents

	// One step, so the spawn's answer stays the store's business and the
	// in-flight mark stands.
	next, cmd := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	drain(cmd)

	issue, ok := m.selectedIssue()
	if !ok || issue.Number != 1 {
		t.Fatalf("after s the selection moved to #%d; want it kept on #1", issue.Number)
	}

	// A second s lands on the same row and starts nothing: an issue that is
	// starting is not launchable, so the predicate turns the key away.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if got := len(agents.seen()); got != 1 {
		t.Errorf("spawned %d agents, want 1 — the second s went to another row", got)
	}

	// Behind the predicate, the guard says why for a message that arrives
	// some other way.
	next, _ = m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	if !strings.Contains(m.notice, "already has an agent starting") {
		t.Errorf("notice = %q, want the already-starting guard", m.notice)
	}
}

// The full-screen view shows the issue under the cursor; a start that moved
// the cursor would swap the issue on screen while the user is reading it.
func TestStartOnTheFullScreenIssueKeepsItOnScreen(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1), startable(2)})
	m.screen = screenIssue
	m.agents = &fakeSpawner{}

	next, _ := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)

	issue, ok := m.selectedIssue()
	if !ok || issue.Number != 1 {
		t.Fatalf("after s the full-screen view moved to #%d; want #1", issue.Number)
	}
	if crumb := m.breadcrumbView(); !strings.Contains(crumb, "#1") {
		t.Errorf("breadcrumb = %q, want it still on #1", crumb)
	}
}

// The same slide, from the other direction: an agent finishing releases its
// in-flight mark, and the row can sort back up. The cursor comes with it.
func TestAgentFinishedKeepsTheCursor(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1), startable(2)})
	m.agents = &fakeSpawner{}

	next, cmd := m.Update(startIssueMsg{issue: startable(1)})
	m = next.(Model)
	drain(cmd)
	if issue, _ := m.selectedIssue(); issue.Number != 1 {
		t.Fatalf("setup: cursor on #%d, want #1", issue.Number)
	}

	next, _ = m.Update(agentFinishedMsg{issue: startable(1), status: "done"})
	m = next.(Model)
	if issue, _ := m.selectedIssue(); issue.Number != 1 {
		t.Errorf("after the agent finished the cursor moved to #%d; want #1", issue.Number)
	}
}

// A start-all landing marks every launchable issue, sliding all of them
// under the cursor at once.
func TestStartAllKeepsTheCursorOnItsIssue(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1), startable(2), startable(3)})
	m.agents = &fakeSpawner{}
	m.issueCursor = 1

	next, cmd := m.Update(readyLoadedMsg{plan: "orders", issues: []issues.Status{startable(1), startable(2), startable(3)}})
	m = next.(Model)
	drain(cmd)

	if issue, _ := m.selectedIssue(); issue.Number != 2 {
		t.Errorf("after start-all the cursor moved to #%d; want #2", issue.Number)
	}
}

// Readiness is the registry's predicate now: a blocked issue or one whose
// type maps to no agent is not launchable, so the key is never offered and
// pressing it does nothing.
func TestStartIsNotOfferedOnIssuesThatCannotStart(t *testing.T) {
	agents := &fakeSpawner{}
	m := issuesModel(t, []issues.Status{
		{Issue: issues.Issue{Number: 1, Title: "Blocked", State: issues.StateOpen}, Blocked: true, Agent: "coder"},
		{Issue: issues.Issue{Number: 2, Title: "Unmapped", Type: "unknown", State: issues.StateOpen}, Ready: true},
	})
	m.agents = agents

	for _, cursor := range []int{0, 1} {
		m.issueCursor = cursor
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	}
	if got := len(agents.seen()); got != 0 {
		t.Errorf("spawned %d agents on issues that cannot start, want 0", got)
	}
}

func TestStartRefusesWhenRunnerUnavailable(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !strings.Contains(m.notice, "not available") {
		t.Errorf("notice = %q, want it to say the runner is missing", m.notice)
	}
}

// start on a plan row is start-all: every launchable issue in the plan, as
// `pib plan start` does.
func TestStartAllStartsEveryLaunchableIssue(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	for _, title := range []string{"First", "Second"} {
		if _, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: title}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	agents := &fakeSpawner{}
	m := plansModel(t, nil)
	m.store = store
	m.cfg = defaultCfg(t)
	m.agents = agents
	m.plans = []issues.PlanStatus{{Plan: issues.Plan{Slug: "orders", Title: "Orders"}, Ready: 2}}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	reqs := agents.seen()
	if len(reqs) != 2 {
		t.Fatalf("start-all sent %d requests, want 2", len(reqs))
	}
	for _, req := range reqs {
		if req.Op != protocol.OpSpawn || req.Issue == 0 {
			t.Errorf("request = %+v", req)
		}
	}
}

// The plan the key landed on is the plan that starts, even though its issues
// are not on screen.
func TestStartAllOnAPlanSkipsIssuesAlreadyStarting(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	first, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "First"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Second"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	agents := &fakeSpawner{}
	m := plansModel(t, nil)
	m.store = store
	m.cfg = defaultCfg(t)
	m.agents = agents
	m.inFlight = map[int64]bool{first.Number: true}
	m.plans = []issues.PlanStatus{{Plan: issues.Plan{Slug: "orders", Title: "Orders"}, Ready: 2}}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	if got := len(agents.seen()); got != 1 {
		t.Errorf("start-all spawned %d agents, want 1 — the one not already starting", got)
	}
}

func TestStartAllCapsConcurrency(t *testing.T) {
	agents := &fakeSpawner{release: make(chan struct{})}
	m := plansModel(t, nil)
	m.agents = agents

	var list []issues.Status
	for i := int64(1); i <= 6; i++ {
		list = append(list, startable(i))
	}

	next, cmd := m.Update(readyLoadedMsg{plan: "orders", issues: list})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("no command from start-all")
	}
	go func() { collect(cmd) }()

	for {
		agents.mu.Lock()
		active := agents.active
		agents.mu.Unlock()
		if active > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	agents.mu.Lock()
	maxActive := agents.maxActive
	agents.mu.Unlock()
	if maxActive > runner.MaxConcurrentAgents {
		t.Errorf("max concurrent agents = %d, want at most %d", maxActive, runner.MaxConcurrentAgents)
	}

	for i := 0; i < 6; i++ {
		agents.release <- struct{}{}
	}
	time.Sleep(100 * time.Millisecond)

	if got := len(agents.seen()); got != 6 {
		t.Errorf("sent %d requests, want 6", got)
	}
}

// ctrl+k kills the run on the row: the window the runner opened for it.
func TestKillClosesTheRunsWindow(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.StartRun(issues.RunStart{ID: "run-1", Issue: issue.Number, Agent: "coder", Window: "@9"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	old := killWindow
	var killed string
	killWindow = func(id string) error { killed = id; return nil }
	t.Cleanup(func() { killWindow = old })

	m := issuesModel(t, []issues.Status{{
		Issue:      issue,
		InProgress: true,
		Run:        "run-1",
	}})
	m.store = store

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})

	if killed != "@9" {
		t.Errorf("killed window %q, want @9", killed)
	}
	if !strings.Contains(m.notice, "killed run run-1") {
		t.Errorf("notice = %q", m.notice)
	}
}

// A planning row has no issue; the planner's window is found by run id.
func TestKillOnAPlanningRow(t *testing.T) {
	store := testStore(t)
	if err := store.StartRun(issues.RunStart{ID: "run-p", Agent: "planner", Window: "@7"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	old := killWindow
	var killed string
	killWindow = func(id string) error { killed = id; return nil }
	t.Cleanup(func() { killWindow = old })

	m := plansModel(t, nil)
	m.store = store
	m.plans = []issues.PlanStatus{{Plan: issues.Plan{Title: "run-p"}, State: issues.PlanPlanning, Run: "run-p"}}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})

	if killed != "@7" {
		t.Errorf("killed window %q, want @7", killed)
	}
}

// w selects the window of the row's run.
func TestWindowSelectsTheRunsWindow(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.StartRun(issues.RunStart{ID: "run-1", Issue: issue.Number, Agent: "coder", Window: "@9"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	old := selectWindow
	var selected string
	selectWindow = func(id string) error { selected = id; return nil }
	t.Cleanup(func() { selectWindow = old })

	m := issuesModel(t, []issues.Status{{
		Issue:      issue,
		InProgress: true,
		Run:        "run-1",
	}})
	m.store = store

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})

	if selected != "@9" {
		t.Errorf("selected window %q, want @9", selected)
	}
}

func TestWindowFailureSurfaces(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.StartRun(issues.RunStart{ID: "run-1", Issue: issue.Number, Agent: "coder", Window: "@9"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	old := selectWindow
	selectWindow = func(id string) error { return context.DeadlineExceeded }
	t.Cleanup(func() { selectWindow = old })

	m := issuesModel(t, []issues.Status{{
		Issue:      issue,
		InProgress: true,
		Run:        "run-1",
	}})
	m.store = store

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if !strings.Contains(m.notice, "could not reach the window") {
		t.Errorf("notice = %q", m.notice)
	}
}

// , opens the settings placeholder, and esc returns where it opened from.
func TestSettingsOpensAPlaceholderAndEscReturns(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if m.screen != screenSettings {
		t.Fatalf("screen = %v, want the settings screen", m.screen)
	}
	if view := m.View(); !strings.Contains(view, "Settings") {
		t.Errorf("the placeholder does not render:\n%s", view)
	}

	// Opening settings over settings does not lose the way back.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(",")})
	if m.screen != screenSettings {
		t.Fatalf("a second , left the settings screen: %v", m.screen)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenIssues {
		t.Errorf("esc from settings went to %v, want the issues screen it opened from", m.screen)
	}
}

// ── The registry, the bar, the line and help ─────────────────────────────

// The bar shows the wired verbs and nothing else: the verbs that are not
// wired yet are not registered, so they cannot show.
func TestTheBarShowsOnlyWiredVerbs(t *testing.T) {
	m := issuesModel(t, []issues.Status{{
		Issue:      issues.Issue{Number: 1, Title: "Working", State: issues.StateOpen},
		InProgress: true,
		Run:        "run-1",
	}})

	bar := m.commandBarView(120)
	for _, want := range []string{"n New", "ctrl+k Kill", "w Window", ", Settings", "Z Closed", "! Needs you", ": Cmd", "? Help"} {
		if !strings.Contains(bar, want) {
			t.Errorf("bar missing %q: %q", want, bar)
		}
	}
	for _, unwanted := range []string{"r Retry", "a Answer", "f Follow up", "c Comment", "e Edit", "x Close", "X Reopen", "p PR", "b Blockers", "z Archive", "v Review"} {
		if strings.Contains(bar, unwanted) {
			t.Errorf("bar shows %q, which is not wired: %q", unwanted, bar)
		}
	}
}

func TestTheBarOffersStartOnlyOnLaunchableRows(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})
	if bar := m.commandBarView(120); !strings.Contains(bar, "s Start") {
		t.Errorf("bar on a launchable issue missing start: %q", bar)
	}

	m = issuesModel(t, []issues.Status{{
		Issue:   issues.Issue{Number: 1, Title: "Blocked", State: issues.StateOpen},
		Blocked: true,
	}})
	if bar := m.commandBarView(120); strings.Contains(bar, "s Start") {
		t.Errorf("bar on a blocked issue offers start: %q", bar)
	}
}

// The same registry the bar renders from is what the keys run.
func TestTheColonLineRunsAVerb(t *testing.T) {
	agents := &fakeSpawner{}
	m := issuesModel(t, []issues.Status{startable(1)})
	m.agents = agents

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	if !m.line.Active() {
		t.Fatal(": did not open the line")
	}
	for _, r := range "start" {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := len(agents.seen()); got != 1 {
		t.Errorf(":start spawned %d agents, want 1", got)
	}
	if m.line.Active() {
		t.Error("the line stayed open after running")
	}
}

func TestTheColonLineReportsAnUnknownVerb(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	for _, r := range "bogus" {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.notice, "no such command") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestEscClosesTheLine(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.line.Active() {
		t.Error("esc did not close the line")
	}
}

func TestTheLineRendersOnTheBarRow(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, ":") {
		t.Errorf("the bar row is %q, want the \":\" line", last)
	}
	if !strings.Contains(last, "s") {
		t.Errorf("the line does not show what was typed: %q", last)
	}
}

// Help is generated from the same registry as the bar, so it cannot list a
// key that does nothing: a launchable row gets start, a running row gets
// kill and window, and the unwired verbs appear for neither.
func TestHelpListsMotionAndTheWiredCommands(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.help {
		t.Fatal("? did not open help")
	}
	view := m.View()
	for _, want := range []string{"Motion", "drill in", "Commands", "new", "start", "settings"} {
		if !strings.Contains(view, want) {
			t.Errorf("help missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"retry", "comment", "archive"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("help lists %q, which is not wired:\n%s", unwanted, view)
		}
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if m.help {
		t.Error("? did not close help")
	}
}

func TestHelpOnARunningRowOffersKillAndWindow(t *testing.T) {
	running := issues.Status{
		Issue:      issues.Issue{Number: 1, Title: "Working", State: issues.StateOpen},
		InProgress: true,
		Run:        "run-1",
	}
	m := issuesModel(t, []issues.Status{running})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	view := m.View()
	for _, want := range []string{"kill", "window"} {
		if !strings.Contains(view, want) {
			t.Errorf("help on a running row missing %q:\n%s", want, view)
		}
	}
}

// ── Startup keys are dead after startup ──────────────────────────────────

// The y/n prompts keep their own keys, but only while a startup phase is
// showing. Once startup finishes, y does nothing and n is the registry's new
// — never a belated answer to a prompt that is gone.
func TestStartupKeysAreDeadAfterStartup(t *testing.T) {
	m := ready(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if cmd != nil {
		t.Error("y produced a command after startup; nothing may reach the startup keys")
	}
	if m.phase != phasePrompt {
		t.Errorf("phase = %v after y, want it untouched", m.phase)
	}

	oldInside, oldExec := insideTmux, execProcess
	insideTmux = func() bool { return false }
	var handed *exec.Cmd
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		handed = c
		return nil
	}
	t.Cleanup(func() { insideTmux, execProcess = oldInside, oldExec })

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if handed == nil {
		t.Error("n did not reach the registry's new command")
	}
}

// ── Table shape ──────────────────────────────────────────────────────────

func TestTheTableKeepsTheCursorVisibleAndFits(t *testing.T) {
	var plans []issues.Plan
	for i := 0; i < 40; i++ {
		plans = append(plans, issues.Plan{Slug: strings.Repeat("p", 1) + string(rune('a'+i%26)), Title: "Plan"})
	}
	m := plansModel(t, plans)

	m.planCursor = 30
	view := m.View()
	if !strings.Contains(view, "▼") {
		t.Errorf("no indicator that more rows follow:\n%s", view)
	}
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) > m.height {
		t.Errorf("rendered %d lines into %d", len(lines), m.height)
	}
}

func TestVeryNarrowWidthNoPanic(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})
	for _, w := range []int{0, 1, 5, 20} {
		m.width = w
		_ = m.View()
	}
}

func TestTheBarRowSurvivesEveryCoveredSize(t *testing.T) {
	m := issuesModel(t, []issues.Status{startable(1)})
	for _, size := range []struct{ w, h int }{{120, 40}, {100, 30}, {40, 30}, {30, 20}, {20, 12}} {
		m.width, m.height = size.w, size.h
		lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: rendered %d lines into %d", size.w, size.h, len(lines), m.height)
		}
		if last := lines[len(lines)-1]; !strings.Contains(last, "? Help") {
			t.Errorf("%dx%d: last line is %q, want the command bar", size.w, size.h, last)
		}
	}
}
