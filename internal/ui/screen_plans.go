package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/agent"
	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/runner"
	"pib/internal/tmux"
	"pib/internal/ui/theme"
)

// plansLoadedMsg carries the plans table as the store derives it, planning
// rows first. seq is the load's number, so an answer older than the newest
// load started is dropped.
type plansLoadedMsg struct {
	seq   int
	plans []issues.PlanStatus
	err   error
}

func loadPlans(store *issues.Store, includeArchived bool, cfg config.Config, seq int) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return plansLoadedMsg{seq: seq, err: errors.New("no store")}
		}
		plans, err := store.PlanStatuses(includeArchived, issues.PlanStatusOptions{
			ReviewCycles: cfg.ReviewCycles(),
			PlanReview:   cfg.PlanReview(),
		})
		return plansLoadedMsg{seq: seq, plans: plans, err: err}
	}
}

// refreshPlans numbers and starts a plans reload, without raising the
// loading flag: a store event means the rows are stale, and a spinner over
// rows that are mostly right would flash every time an agent breathes.
func (m Model) refreshPlans() (Model, tea.Cmd) {
	m.plansSeq++
	return m, loadPlans(m.store, m.showClosed, m.cfg, m.plansSeq)
}

func (m Model) onPlansLoaded(msg plansLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.plansSeq {
		// An older load landed after a newer one started. The newer answer
		// is coming; applying this one would put stale rows back.
		return m, nil
	}
	m.plansLoading = false
	if msg.err != nil {
		m.plansErr = msg.err
		return m, nil
	}
	m.plansErr = nil
	key := m.planKey()
	m.plans = msg.plans
	m.restorePlanCursor(key)
	return m, m.selectPlanContent()
}

// planKey names the selected plan row across reloads: its slug, or the
// planner run behind a planning row, which has no slug yet.
func (m Model) planKey() string {
	plan, ok := m.selectedPlan()
	if !ok {
		return ""
	}
	if plan.Slug != "" {
		return "plan:" + plan.Slug
	}
	return "run:" + plan.Run
}

// restorePlanCursor puts the cursor back on the row it named before a
// reload. A row that vanished — an applied plan replaces its planning row —
// leaves the cursor where it was, clamped into the table.
func (m *Model) restorePlanCursor(key string) {
	for i, plan := range m.visiblePlans() {
		k := "plan:" + plan.Slug
		if plan.Slug == "" {
			k = "run:" + plan.Run
		}
		if k == key && key != "" {
			m.planCursor = i
			return
		}
	}
	m.clampCursors()
}

// visiblePlans is the plans table as filtered and ordered: the needs-you
// filter if it is on, then the rows that need the user ahead of the rest.
// Archived plans are filtered at the load, not here.
func (m Model) visiblePlans() []issues.PlanStatus {
	var out []issues.PlanStatus
	for _, plan := range m.plans {
		if m.needsYou && !planNeedsYou(plan) {
			continue
		}
		out = append(out, plan)
	}
	needsYouFirst(out, planNeedsYou)
	return out
}

// planNeedsYou reports a plan row that needs the user: work that could
// start, or an issue whose machine stopped.
func planNeedsYou(plan issues.PlanStatus) bool {
	return plan.Ready > 0 || plan.NeedsAttention > 0
}

func (m Model) selectedPlan() (issues.PlanStatus, bool) {
	visible := m.visiblePlans()
	if m.planCursor < 0 || m.planCursor >= len(visible) {
		return issues.PlanStatus{}, false
	}
	return visible[m.planCursor], true
}

// planMotion moves the plans table's cursor and drills in.
func (m Model) planMotion(key string) (Model, tea.Cmd, bool) {
	visible := m.visiblePlans()
	switch key {
	case "j", "down":
		if m.planCursor < len(visible)-1 {
			m.planCursor++
			m.notice = ""
		}
		return m, m.selectPlanContent(), true
	case "k", "up":
		if m.planCursor > 0 {
			m.planCursor--
			m.notice = ""
		}
		return m, m.selectPlanContent(), true
	case "g":
		m.planCursor = 0
		return m, m.selectPlanContent(), true
	case "G":
		if len(visible) > 0 {
			m.planCursor = len(visible) - 1
		}
		return m, m.selectPlanContent(), true
	case "ctrl+d", "pgdown":
		m.planCursor += m.pageSize()
		return m.clampPlanCursor(), m.selectPlanContent(), true
	case "ctrl+u", "pgup":
		m.planCursor -= m.pageSize()
		return m.clampPlanCursor(), m.selectPlanContent(), true
	case "enter", "l", "right":
		return m.drillIntoPlan()
	}
	return m, nil, false
}

func (m Model) clampPlanCursor() Model {
	m.clampCursors()
	return m
}

// pageSize is how far a page key moves: the rows a table window holds.
func (m Model) pageSize() int {
	if m.isShort() {
		return m.contentHeight()
	}
	top, _ := m.tableHeights()
	if top < 1 {
		top = 1
	}
	return top
}

// drillIntoPlan replaces the plans table with the selected plan's issues.
func (m Model) drillIntoPlan() (Model, tea.Cmd, bool) {
	plan, ok := m.selectedPlan()
	if !ok {
		return m, nil, true
	}
	if plan.Slug == "" {
		// A planning row has no issues yet; the planner is still writing
		// them. Its run is what it has, and kill and window reach it.
		m.notice = "the planner is still writing this plan"
		return m, nil, true
	}
	m.screen = screenIssues
	m.drilled = plan.Slug
	m.issueCursor = 0
	m.issueScroll = 0
	m.planIssues = nil
	m.planReviews = nil
	m.planIssuesErr = nil
	m.planIssuesLoading = true
	m.notice = ""
	m.issuesSeq++
	return m, loadPlanIssues(m.store, plan.Slug, m.cfg, m.issuesSeq), true
}

// planContentLoadedMsg carries a plan's markdown file for the detail pane.
type planContentLoadedMsg struct {
	slug string
	file issues.File
	err  error
}

func loadPlanContent(store *issues.Store, slug string) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return planContentLoadedMsg{slug: slug, err: errors.New("no store")}
		}
		file, err := store.PlanContent(slug)
		return planContentLoadedMsg{slug: slug, file: file, err: err}
	}
}

// selectPlanContent returns the command that reads the plan file the cursor
// has landed on, or nil when that file is already held — one read per plan,
// as with the issue prose cache.
func (m Model) selectPlanContent() tea.Cmd {
	if m.store == nil {
		return nil
	}
	plan, ok := m.selectedPlan()
	if !ok || plan.Slug == "" {
		return nil
	}
	if _, held := m.planProse[plan.Slug]; held {
		return nil
	}
	return loadPlanContent(m.store, plan.Slug)
}

// plansView is the home screen: the plans table, with the selected plan's
// goal and acceptance in the detail pane when the terminal has the rows.
func (m Model) plansView() string {
	if m.plansLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading plans…"))
	}
	if m.plansErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading plans: " + m.plansErr.Error()))
	}

	h := m.contentHeight()
	if m.isShort() {
		return m.planTable(m.width, h)
	}
	top, detail := m.tableHeights()
	return lipgloss.JoinVertical(lipgloss.Left,
		m.rule(),
		m.planTable(m.width, top),
		m.rule(),
		m.planDetailPane(m.width, detail),
		m.rule(),
	)
}

// planTable renders the plans table: title, state, and the ready / running /
// needs-attention counts of ADR-006 §3.
func (m Model) planTable(w, h int) string {
	visible := m.visiblePlans()
	if len(visible) == 0 {
		empty := "No plans yet — press n to plan something."
		if m.needsYou {
			empty = "Nothing needs you."
		}
		return pad(w, h, helpStyle.Render(empty))
	}

	cols := []tableColumn{
		{title: "TITLE", width: 0},
		{title: "STATE", width: 24},
		{title: "READY", width: 7},
		{title: "RUNNING", width: 9},
		{title: "NEEDS YOU", width: 11},
	}
	rows := make([]tableRow, len(visible))
	for i, plan := range visible {
		rows[i] = tableRow{
			cells: []string{
				plan.Title,
				planStateLabel(plan.State),
				fmt.Sprintf("%d", plan.Ready),
				fmt.Sprintf("%d", plan.Running),
				fmt.Sprintf("%d", plan.NeedsAttention),
			},
			style: planStateStyle(plan),
		}
	}
	return renderTable(cols, rows, m.planCursor, w, h)
}

// planStateLabel renders a plan state the way the ADR names it: spaces, not
// underscores.
func planStateLabel(state issues.PlanState) string {
	return strings.ReplaceAll(string(state), "_", " ")
}

// planStateStyle colours a plan row by where the plan is. A plan holding an
// issue that needs the user is red whatever its own state.
func planStateStyle(plan issues.PlanStatus) lipgloss.Style {
	switch {
	case plan.State == issues.PlanArchived:
		return theme.Default.Dim
	case plan.NeedsAttention > 0:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ff757f"))
	}
	switch plan.State {
	case issues.PlanPlanning, issues.PlanUnderReview:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	case issues.PlanAwaitingReview, issues.PlanAwaitingClosingReview:
		return theme.Default.Secondary
	case issues.PlanComplete:
		return theme.Default.Tertiary
	default:
		return theme.Default.Primary
	}
}

// planDetailPane renders the selected plan's goal and acceptance. A planning
// row has no file yet; its pane says what is running instead.
func (m Model) planDetailPane(w, h int) string {
	plan, ok := m.selectedPlan()
	if !ok {
		return pad(w, h, "")
	}

	if plan.State == issues.PlanPlanning {
		var b strings.Builder
		b.WriteString(theme.Default.PaneHeader.Width(w).Render("planning") + "\n")
		b.WriteString(itemStyle.Render("planner run "+plan.Run+" is writing this plan.") + "\n")
		if !plan.CreatedAt.IsZero() {
			b.WriteString(itemStyle.Render("started "+ago(plan.CreatedAt)) + "\n")
		}
		return pad(w, h, strings.TrimRight(b.String(), "\n"))
	}

	prose, held := m.planProse[plan.Slug]
	if !held {
		return pad(w, h, "")
	}
	if prose.err != nil {
		return pad(w, h, helpStyle.Render("Could not read the plan file: "+prose.err.Error()))
	}

	var b strings.Builder
	b.WriteString(theme.Default.PaneHeader.Width(w).Render(plan.Slug+" · "+planStateLabel(plan.State)) + "\n")
	if body := strings.Trim(prose.file.Body, "\n"); strings.TrimSpace(body) != "" {
		writeProse(&b, itemStyle, body, w-itemStyle.GetPaddingLeft())
	}
	if len(prose.file.Acceptance) > 0 {
		if body := strings.TrimSpace(prose.file.Body); body != "" {
			b.WriteString("\n")
		}
		for _, ac := range prose.file.Acceptance {
			b.WriteString(itemStyle.Render("• "+ac) + "\n")
		}
	}
	return pad(w, h, strings.TrimRight(b.String(), "\n"))
}

func (m Model) renderCentered(content string) string {
	h := m.contentHeight()
	return lipgloss.NewStyle().Height(h).Render(content)
}

// plannerFinishedMsg reports the planner's session ended, however it was
// launched — the tmux window's run returned, or the handed-over terminal's
// process exited.
type plannerFinishedMsg struct {
	err error
}

// handleNewPlan spawns the planner. Inside tmux it goes through the runner
// in the foreground: the window opens in front of pib, the run is recorded,
// and the run-started event is what makes a planning row appear. Outside
// tmux the planner takes over the terminal as it always has; no run is
// recorded and no planning row appears.
func (m Model) handleNewPlan() (tea.Model, tea.Cmd) {
	if m.planner.Name == "" {
		m.notice = "no planner definition is loaded"
		return m, nil
	}
	if !insideTmux() {
		return m, handOverPlanner(m.planner, m.workspace.GitRoot, m.extension, m.socket)
	}
	if m.agents == nil {
		m.notice = "Agent runner is not available"
		return m, nil
	}
	return m, spawnPlannerCmd(m.agents, m.planner)
}

// insideTmux is a variable so a test can stand inside tmux without a server.
var insideTmux = tmux.Inside

// spawnPlannerCmd runs the planner with no task — it asks the user what to
// plan. The runner blocks until the session ends, records the run and its
// exit, and Foreground puts the window in front of pib.
func spawnPlannerCmd(r spawner, planner agent.Definition) tea.Cmd {
	return func() tea.Msg {
		_, err := r.Run(context.Background(), protocol.Request{
			Op:         protocol.OpSpawn,
			Agent:      planner.Name,
			Name:       planner.Name,
			Foreground: true,
		})
		return plannerFinishedMsg{err: err}
	}
}

// handOverPlanner execs the planner in pib's own terminal, suspending the
// interface until it exits. It carries no task for the same reason the tmux
// spawn carries none.
func handOverPlanner(planner agent.Definition, dir, extension, socket string) tea.Cmd {
	argv := append([]string{agent.Executable}, planner.Flags(agent.Options{Extensions: []string{extension}})...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		runner.EnvSocket+"="+socket,
		runner.EnvAgent+"="+planner.Name,
	)
	return execProcess(cmd, func(err error) tea.Msg {
		return plannerFinishedMsg{err: err}
	})
}

// execProcess is tea.ExecProcess, named so a test can see the hand-over
// without running a process.
var execProcess = tea.ExecProcess

// readyLoadedMsg carries the launchable issues of the plan a start-all was
// issued on, loaded off the key's path because the plans table has no issue
// rows of its own.
type readyLoadedMsg struct {
	plan   string
	issues []issues.Status
	err    error
}

func loadReadyIssues(store *issues.Store, plan string, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return readyLoadedMsg{plan: plan, err: errors.New("no store")}
		}
		list, err := store.Ready(issues.Filter{Plan: plan}, issues.StatusOptions{
			AgentFor:     cfg.AgentFor,
			ReviewCycles: cfg.ReviewCycles(),
		})
		return readyLoadedMsg{plan: plan, issues: list, err: err}
	}
}

// handleReadyLoaded starts every launchable issue in the plan, matching what
// `pib plan start` does from the command line. The in-flight marks land here,
// in Update, so a second press finding them already set starts nothing.
func (m Model) handleReadyLoaded(msg readyLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "could not load ready issues: " + msg.err.Error()
		return m, nil
	}
	if m.agents == nil {
		m.notice = "Agent runner is not available"
		return m, nil
	}

	var toStart []issues.Status
	for _, issue := range msg.issues {
		if issue.Launchable && !m.inFlight[issue.Number] {
			toStart = append(toStart, issue)
		}
	}
	if len(toStart) == 0 {
		m.notice = "No issues are ready to start"
		return m, nil
	}

	if m.inFlight == nil {
		m.inFlight = map[int64]bool{}
	}
	// Every issue marked here sorts out of the needs-you block; keep the
	// cursor on the row it was on.
	key := m.issueKey()
	sem := make(chan struct{}, runner.MaxConcurrentAgents)
	var cmds []tea.Cmd
	for _, issue := range toStart {
		m.inFlight[issue.Number] = true
		cmds = append(cmds, spawnAgentCmd(m.agents, issue, sem))
	}
	m.restoreIssueCursor(key)
	m.notice = fmt.Sprintf("Starting %d agents on plan %s", len(toStart), msg.plan)
	return m, tea.Batch(cmds...)
}
