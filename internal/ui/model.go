package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/agent"
	"pib/internal/config"
	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/server"
	"pib/internal/triage"
	"pib/internal/ui/command"
	"pib/internal/ui/theme"
	"pib/internal/workspace"
)

var (
	titleStyle   = theme.Default.Title
	itemStyle    = theme.Default.Item
	helpStyle    = theme.Default.Help
	promptStyle  = theme.Default.Prompt
	errorStyle   = theme.Default.Error
	loadingStyle = theme.Default.Loading
	dividerStyle = theme.Default.Divider
)

// screen is where the user is in the drill-down: a table of plans, the
// plan's table of issues, one issue full-screen, or the settings screen.
// enter replaces a table with its child; esc returns, cursor kept.
type screen int

const (
	screenPlans screen = iota
	screenIssues
	screenIssue
	screenSettings
)

type Model struct {
	width  int
	height int

	phase     phase
	workspace workspace.Status
	err       error

	planner   agent.Definition
	notice    string
	server    *server.Server
	store     *issues.Store
	agents    spawner
	extension string
	socket    string
	agentsDir string
	installed []string
	outdated  []string

	screen screen
	// settingsFrom is where the settings placeholder was opened from; esc
	// returns there. Settings is not part of the drill-down stack.
	settingsFrom screen

	// reg is the command registry of ADR-006: every key that is not motion
	// lives here, and the bar, the help and the ":" line all render from
	// it. line is the ":" prompt itself.
	reg  *command.Registry
	line *command.Line
	help bool

	// plans is the plans table as the store derives it, planning rows
	// included. The visible order is computed from it: rows needing the
	// user first, the rest in the store's own order.
	plans        []issues.PlanStatus
	plansErr     error
	plansLoading bool
	planCursor   int
	// planProse is each plan's markdown file — the goal and the acceptance
	// the detail pane shows — read when the cursor landed on the plan. A
	// render may not reach the store, so the pane reads from here.
	planProse map[string]issueProse

	// drilled is the plan whose issues are on screen. planIssues is that
	// plan's issues; planReviews its review cycles, loaded with the issues
	// rather than asked for at render.
	drilled           string
	planIssues        []issues.Status
	planReviews       map[int64][]issues.Review
	planIssuesErr     error
	planIssuesLoading bool
	issueCursor       int
	// issueProse is each issue's markdown file — the prose body and the
	// comments under `<!-- pib:comments -->` — read when the cursor landed
	// on that issue and held here, keyed by issue number, which is unique
	// across the workspace. A render may not reach the store; this is the
	// only place a pane can find prose.
	issueProse map[int64]issueProse
	// issueScroll is how far the full-screen issue view has scrolled. It
	// belongs to the issue on screen, so it resets when the issue changes.
	issueScroll int

	// The filters of ADR-006 §1. showClosed (Z) shows closed issues and
	// archived plans; needsYou (!) keeps only rows that are launchable or
	// need attention.
	showClosed bool
	needsYou   bool

	// triage holds what reconciliation has read off GitHub about
	// out-of-scope findings, and is the only place the interface may learn
	// it: a render cannot ask.
	triage *triage.Collector
	cfg    config.Config

	// inFlight holds the issues pib has an outstanding spawn for. A run is
	// only recorded once the agent's window exists, so until then the store
	// still reports the issue ready — this is what pib knows and the store
	// does not yet.
	inFlight map[int64]bool

	// plansSeq and issuesSeq order the loads of their table. Bubble Tea runs
	// commands concurrently, so a burst of store events starts loads that
	// can land in any order; each load is numbered when it starts, and a
	// landing older than the newest number assigned is dropped rather than
	// un-load the newer rows.
	plansSeq  int
	issuesSeq int

	// events is the store's change feed, subscribed once the store opens;
	// every delivery reloads the affected rows and re-arms the wait.
	events      <-chan issues.Event
	unsubscribe func()
}

// Close releases the socket, the store subscription and the issue store. It
// is safe to call when none of them were opened.
func (m Model) Close() error {
	if m.unsubscribe != nil {
		m.unsubscribe()
	}
	var err error
	if m.server != nil {
		err = m.server.Close()
	}
	if m.store != nil {
		if closeErr := m.store.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func NewModel() Model {
	reg := wiredRegistry()
	return Model{
		screen: screenPlans,
		reg:    reg,
		line:   command.NewLine(reg, nil),
	}
}

// Err reports a startup failure so the caller can exit non-zero.
func (m Model) Err() error {
	return m.err
}

func (m Model) Init() tea.Cmd {
	return detectWorkspace
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = sizeMsg.Width
		m.height = sizeMsg.Height
		return m, nil
	}

	m, cmd, handled := m.updateStartup(msg)
	if handled {
		return m, cmd
	}
	if m.phase != phasePrompt {
		return m, nil
	}

	// The ":" line owns the keyboard while it is open; everything else —
	// store events, loads — is handled below as usual.
	if m.line.Active() {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			_, cmd := m.line.Update(keyMsg)
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case storeEventMsg:
		return m.onStoreEvent(msg.event)
	case eventsClosedMsg:
		m.events = nil
		return m, nil
	case plansLoadedMsg:
		return m.onPlansLoaded(msg)
	case planIssuesLoadedMsg:
		return m.onPlanIssuesLoaded(msg)
	case planContentLoadedMsg:
		if m.planProse == nil {
			m.planProse = map[string]issueProse{}
		}
		m.planProse[msg.slug] = issueProse{file: msg.file, err: msg.err}
		return m, nil
	case issueContentLoadedMsg:
		// The cache is keyed by issue number rather than by the selection, so
		// a response that arrives after the cursor has moved still fills the
		// entry the next visit to that issue will look for.
		if m.issueProse == nil {
			m.issueProse = map[int64]issueProse{}
		}
		m.issueProse[msg.number] = issueProse{file: msg.file, err: msg.err}
		return m, nil

	// The out-of-scope scan is armed once, at startup, and re-armed only
	// from its own message. It reads GitHub, and no store event covers it.
	case outOfScopeTickMsg:
		return m, tea.Batch(outOfScopeTick(), collectOutOfScope(m.store, m.triage, m.triagePlan()))
	case outOfScopeCollectedMsg:
		return m, nil

	// Semantic messages from the registry's handlers.
	case newPlanMsg:
		return m.handleNewPlan()
	case plannerFinishedMsg:
		if msg.err != nil {
			m.notice = "planner: " + msg.err.Error()
		} else {
			m.notice = "planner session ended"
		}
		if m.planner.AutoExit {
			return m, tea.Quit
		}
		return m, nil
	case startIssueMsg:
		return m.handleStartIssue(msg.issue)
	case startAllMsg:
		return m, loadReadyIssues(m.store, msg.plan, m.cfg)
	case readyLoadedMsg:
		return m.handleReadyLoaded(msg)
	case agentFinishedMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("%s #%d stopped: %v", msg.issue.Agent, msg.issue.Number, msg.err)
		} else {
			m.notice = fmt.Sprintf("%s #%d finished: %s", msg.issue.Agent, msg.issue.Number, msg.status)
		}
		// Releasing the in-flight mark can make the issue launchable or
		// needing attention again, which sorts it back up; the cursor comes
		// with it.
		key := m.issueKey()
		delete(m.inFlight, msg.issue.Number)
		m.restoreIssueCursor(key)
		return m.refreshIssues()
	case killMsg:
		m.notice = ""
		return m, killRunCmd(m.store, msg.run, msg.issue)
	case killResultMsg:
		if msg.err != nil {
			m.notice = "could not kill the run: " + msg.err.Error()
		} else {
			m.notice = "killed run " + msg.run
		}
		return m, nil
	case windowMsg:
		return m, selectRunWindowCmd(m.store, msg.run, msg.issue)
	case windowResultMsg:
		if msg.err != nil {
			m.notice = "could not reach the window: " + msg.err.Error()
		}
		return m, nil
	case settingsMsg:
		// Opening settings over settings must not lose the way back.
		if m.screen != screenSettings {
			m.settingsFrom = m.screen
			m.screen = screenSettings
		}
		m.notice = ""
		return m, nil
	case toggleClosedMsg:
		m.showClosed = !m.showClosed
		// Archived plans are a load parameter, so the plans table reloads;
		// the issues table filters what it already holds.
		if m.screen == screenPlans {
			return m.refreshPlans()
		}
		m.restoreIssueCursor(m.issueKey())
		return m, nil
	case toggleNeedsYouMsg:
		m.needsYou = !m.needsYou
		m.restorePlanCursor(m.planKey())
		m.restoreIssueCursor(m.issueKey())
		return m, nil

	// The lifecycle verbs of ADR-006 §1. Text goes through $EDITOR; the
	// store's change feed, not these results, is what refreshes the tables.
	case commentIssueMsg:
		return m.handleComment(msg.issue)
	case commentTextMsg:
		if m.editorFailed("comment", msg.err) {
			return m, nil
		}
		return m, commentCmd(m.store, msg.issue.Number, msg.text)
	case commentResultMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("could not comment on #%d: %v", msg.number, msg.err)
		} else {
			m.notice = fmt.Sprintf("commented on #%d", msg.number)
		}
		return m, nil

	case followupIssueMsg:
		return m.handleFollowup(msg.issue)
	case followupTextMsg:
		if m.editorFailed("followup", msg.err) {
			return m, nil
		}
		return m, followupCmd(m.store, m.agents, msg.issue, msg.text)
	case followupResultMsg:
		m.notice = msg.err.Error()
		return m, nil

	case answerIssueMsg:
		return m.handleAnswer(msg.issue)
	case answerTextMsg:
		if m.editorFailed("answer", msg.err) {
			return m, nil
		}
		return m, m.issueOp(protocol.OpIssueAnswer, issueops.AnswerParams{
			Number: msg.issue.Number, Answer: msg.text,
		}, func(err error) tea.Msg { return answerResultMsg{number: msg.issue.Number, err: err} })
	case answerResultMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("could not answer #%d: %v", msg.number, msg.err)
		} else {
			m.notice = fmt.Sprintf("answered #%d — resuming the agent", msg.number)
		}
		return m, nil

	case retryIssueMsg:
		m.notice = ""
		return m, m.issueOp(protocol.OpIssueRetry, issueops.RetryParams{
			Number: msg.issue.Number,
		}, func(err error) tea.Msg { return retryResultMsg{issue: msg.issue, err: err} })
	case retryResultMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("could not retry #%d: %v", msg.issue.Number, msg.err)
		} else {
			m.notice = fmt.Sprintf("retrying #%d — %s is starting", msg.issue.Number, msg.issue.Agent)
		}
		return m, nil

	case editIssueMsg:
		return m.handleEdit(msg.issue)
	case editResultMsg:
		switch {
		case msg.err != nil:
			m.notice = fmt.Sprintf("could not edit #%d: %v", msg.issue.Number, msg.err)
		case msg.changed:
			m.notice = fmt.Sprintf("edited #%d", msg.issue.Number)
		default:
			m.notice = fmt.Sprintf("#%d unchanged", msg.issue.Number)
		}
		return m, nil

	case closeAskMsg:
		m.line.SetRow(m.currentRow())
		m.line.OpenWith("close ")
		return m, nil
	case closeIssueMsg:
		m.notice = ""
		return m, closeIssueCmd(m.store, msg.issue.Number, msg.reason)
	case closeResultMsg:
		switch {
		case msg.err != nil:
			m.notice = fmt.Sprintf("could not close #%d: %v", msg.number, msg.err)
		case len(msg.warnings) > 0:
			m.notice = fmt.Sprintf("closed #%d — %s", msg.number, strings.Join(msg.warnings, "; "))
		default:
			m.notice = fmt.Sprintf("closed #%d", msg.number)
		}
		return m, nil

	case reopenIssueMsg:
		m.notice = ""
		return m, reopenIssueCmd(m.store, msg.issue.Number)
	case reopenResultMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("could not reopen #%d: %v", msg.number, msg.err)
		} else {
			m.notice = fmt.Sprintf("reopened #%d", msg.number)
		}
		return m, nil

	case openPRMsg:
		return m, openPRCmd(msg.url)
	case prResultMsg:
		if msg.err != nil {
			m.notice = "could not open the pull request: " + msg.err.Error()
		}
		return m, nil

	case blockersMsg:
		return m.handleBlockers(msg.issue)

	case archivePlanMsg:
		m.notice = ""
		return m, archivePlanCmd(m.store, msg.slug, true)
	case unarchivePlanMsg:
		m.notice = ""
		return m, archivePlanCmd(m.store, msg.slug, false)
	case archiveResultMsg:
		verb := "archive"
		if !msg.archived {
			verb = "unarchive"
		}
		if msg.err != nil {
			m.notice = fmt.Sprintf("could not %s %s: %v", verb, msg.slug, msg.err)
		} else {
			m.notice = verb + "d " + msg.slug
		}
		return m, nil

	case reviewPlanMsg:
		return m.handleReviewPlan(msg.plan)
	case reviewResultMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("plan-reviewer on %s stopped: %v", msg.plan, msg.err)
		} else {
			m.notice = fmt.Sprintf("plan-reviewer on %s finished: %s", msg.plan, msg.status)
		}
		return m, nil

	// The ":" line's reports.
	case command.UnknownVerbMsg:
		m.notice = "no such command: " + msg.Input
		return m, nil
	case command.NoSelectionMsg:
		m.notice = "nothing here for " + msg.Verb
		return m, nil
	case command.InactiveMsg, command.Ran:
		m.notice = ""
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		return m.key(keyMsg)
	}
	return m, nil
}

// key handles a key press once startup is done: help, the global keys, then
// motion, then whatever the registry bound. Nothing else may claim a key.
func (m Model) key(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := keyMsg.String()

	if m.help {
		m.help = false
		if key == "?" || key == "esc" {
			return m, nil
		}
		// Fall through to normal handling for any other key.
	}

	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case ":":
		m.line.SetRow(m.currentRow())
		m.line.Open()
		return m, nil
	}

	if next, cmd, ok := m.motion(key); ok {
		return next, cmd
	}

	if cmd, ok := m.reg.Press(m.currentRow(), key); ok {
		m.notice = ""
		return m, cmd
	}
	return m, nil
}

// motion is the reserved navigation of ADR-006 §1. These keys are never
// commands: they move the cursor, drill in and back out, page, and quit.
func (m Model) motion(key string) (Model, tea.Cmd, bool) {
	back := func() (Model, tea.Cmd, bool) {
		switch m.screen {
		case screenIssue:
			m.screen = screenIssues
			m.notice = ""
		case screenIssues:
			m.screen = screenPlans
			m.notice = ""
		case screenSettings:
			m.screen = m.settingsFrom
			m.notice = ""
		}
		return m, nil, true
	}

	switch key {
	case "esc", "h", "left":
		return back()
	case "q":
		if m.screen == screenPlans {
			return m, tea.Quit, true
		}
		return back()
	}

	switch m.screen {
	case screenPlans:
		return m.planMotion(key)
	case screenIssues:
		return m.issueMotion(key)
	case screenIssue:
		return m.scrollMotion(key)
	}
	return m, nil, false
}

func (m Model) View() string {
	if m.phase != phasePrompt {
		view := m.startupView()
		if m.height > 0 {
			lines := strings.Count(view, "\n") + 1
			if pad := m.height - 1 - lines; pad > 0 {
				view += strings.Repeat("\n", pad)
			}
		}
		return m.ground(view + "\n" + m.startupBar(m.width))
	}

	var b strings.Builder
	b.WriteString(m.breadcrumbView() + "\n")
	if m.help {
		b.WriteString(m.helpView())
	} else {
		switch m.screen {
		case screenIssues:
			b.WriteString(m.issuesView())
		case screenIssue:
			b.WriteString(m.issueScreenView())
		case screenSettings:
			b.WriteString(m.settingsView())
		default:
			b.WriteString(m.plansView())
		}
	}
	b.WriteString("\n" + m.commandBarView(m.width))
	return m.ground(b.String())
}

// ground paints the theme's background behind the whole view.
//
// Every foreground colour in the palette was picked against that background,
// so leaving it unpainted renders them onto whatever the terminal happens to
// use — and the dim greys, chosen against near-black, are close to invisible
// on a light one. Width fills each line so the ground is not ragged.
func (m Model) ground(view string) string {
	if m.width == 0 {
		return view
	}
	return theme.Default.Base.Width(m.width).Render(view)
}

// breadcrumbView is the top row: where in the drill-down the user is, with
// what needs doing on the right, as ADR-006 §3 lays it out.
func (m Model) breadcrumbView() string {
	parts := []string{"pib"}
	switch m.screen {
	case screenIssues, screenIssue:
		if m.drilled != "" {
			parts = append(parts, m.drilled)
		}
	case screenSettings:
		parts = append(parts, "settings")
	}
	if m.screen == screenIssue {
		if issue, ok := m.selectedIssue(); ok {
			parts = append(parts, fmt.Sprintf("#%d %s", issue.Number, issue.Title))
		}
	}
	left := strings.Join(parts, " › ")

	right := m.breadcrumbSummary()
	width := m.width
	if width < 1 {
		width = 1
	}
	avail := width - lipgloss.Width(left)
	if right != "" && avail > lipgloss.Width(right)+3 {
		return left + strings.Repeat(" ", avail-lipgloss.Width(right)) + right
	}
	return truncate(left, width)
}

// breadcrumbSummary is the right-hand side of the breadcrumb: how much of
// what is on screen needs the user, plus the filters that are on.
func (m Model) breadcrumbSummary() string {
	var ready, needs int
	switch m.screen {
	case screenPlans:
		for _, p := range m.visiblePlans() {
			ready += p.Ready
			needs += p.NeedsAttention
		}
	case screenIssues, screenIssue:
		for _, issue := range m.visibleIssues() {
			if issue.Launchable {
				ready++
			}
			if issue.NeedsAttention {
				needs++
			}
		}
	}

	var parts []string
	if ready > 0 {
		parts = append(parts, fmt.Sprintf("%d ready", ready))
	}
	if needs == 1 {
		parts = append(parts, "1 needs you")
	} else if needs > 1 {
		parts = append(parts, fmt.Sprintf("%d need you", needs))
	}
	if m.needsYou {
		parts = append(parts, "!")
	}
	if m.showClosed {
		parts = append(parts, "Z")
	}
	return strings.Join(parts, " · ")
}

// commandBarView renders the bottom row: the ":" line while it is open, a
// notice while one stands, and otherwise the registry's bar for the selected
// row — so the keys on show are exactly the keys that do something.
func (m Model) commandBarView(width int) string {
	if width < 1 {
		width = 1
	}
	if m.line.Active() {
		return padLine(m.line.View(), width)
	}
	if m.notice != "" {
		// The notice stands in for the bar. noticeStyle's margin would push
		// the row past its width, so the colour is applied plainly.
		return lipgloss.NewStyle().Foreground(theme.DefaultPalette.Tertiary).
			Render(padLine(truncate(m.notice, width), width))
	}
	// The hints are not commands, so the registry does not render them; they
	// are reserved their cells before the bar is cut.
	hints := ": Cmd  ? Help"
	if lipgloss.Width(hints) > width {
		hints = truncate(hints, width)
	}
	bar := command.Bar(m.reg, m.currentRow(), width-lipgloss.Width(hints)-2)
	if bar != "" {
		bar += "  "
	}
	return padLine(bar+hints, width)
}

// padLine fills a line to the width with spaces.
func padLine(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// helpView renders the motion keys and the registry's commands for the
// selected row, one per line. It is the same registry the bar renders from,
// so help cannot list a key that does nothing.
func (m Model) helpView() string {
	h := m.contentHeight()
	var b strings.Builder
	b.WriteString(theme.Default.PaneHeader.Width(m.width).Render("Help") + "\n\n")
	b.WriteString(itemStyle.Render("Motion") + "\n")
	for _, line := range []string{
		"  j k ↑ ↓     move",
		"  g G         top / bottom",
		"  ctrl+d/u    page",
		"  enter l →   drill in",
		"  esc h ←     back",
		"  q           back · quit from the top",
		"  :           command line",
		"  ?           help",
		"  ctrl+c      quit",
	} {
		b.WriteString(itemStyle.Render(line) + "\n")
	}
	b.WriteString("\n" + itemStyle.Render("Commands") + "\n")
	for _, line := range strings.Split(command.Help(m.reg, m.currentRow()), "\n") {
		b.WriteString(itemStyle.Render(line) + "\n")
	}
	return pad(m.width, h, strings.TrimRight(b.String(), "\n"))
}

const (
	minTopHeight = 4
	maxTopHeight = 12
	topPercent   = 0.35
)

func paneHeights(total int) (top, bottom int) {
	top = int(float64(total) * topPercent)
	if top < minTopHeight {
		top = minTopHeight
	}
	if top > maxTopHeight {
		top = maxTopHeight
	}
	bottom = total - top
	if bottom < 3 {
		bottom = 3
	}
	return
}

// contentHeight is the rows between the breadcrumb and the command bar.
func (m Model) contentHeight() int {
	h := m.height - 2
	if h < 1 {
		h = 1
	}
	return h
}

// tableHeights splits the content between the table and the detail pane,
// accounting for the three rules the tall layout draws.
func (m Model) tableHeights() (top, detail int) {
	return paneHeights(m.contentHeight() - 3)
}

func (m Model) isShort() bool {
	return m.height < 20
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

// rule is a full-width horizontal rule.
func (m Model) rule() string {
	w := m.width
	if w < 1 {
		w = 1
	}
	return dividerStyle.Render(strings.Repeat("─", w))
}

// clampCursors brings the cursors back inside their tables after the rows
// changed under them.
func (m *Model) clampCursors() {
	if last := len(m.visiblePlans()) - 1; m.planCursor > last {
		m.planCursor = last
	}
	if m.planCursor < 0 {
		m.planCursor = 0
	}
	if last := len(m.visibleIssues()) - 1; m.issueCursor > last {
		m.issueCursor = last
	}
	if m.issueCursor < 0 {
		m.issueCursor = 0
	}
}
