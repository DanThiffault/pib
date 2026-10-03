package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/runner"
	"pib/internal/ui/theme"
)

// planIssuesLoadedMsg carries one plan's issues and every review cycle in
// the plan, keyed by issue. The reviews are loaded with the issues rather
// than asked for when one is selected, so rendering a detail pane never
// reaches the store. seq is the load's number, so an answer older than the
// newest load started is dropped.
type planIssuesLoadedMsg struct {
	seq      int
	planSlug string
	issues   []issues.Status
	reviews  map[int64][]issues.Review
	err      error
}

func loadPlanIssues(store *issues.Store, planSlug string, cfg config.Config, seq int) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return planIssuesLoadedMsg{seq: seq, planSlug: planSlug, err: errors.New("no store")}
		}
		list, err := store.Statuses(issues.Filter{Plan: planSlug}, issues.StatusOptions{
			AgentFor:     cfg.AgentFor,
			ReviewCycles: cfg.ReviewCycles(),
		})
		if err != nil {
			return planIssuesLoadedMsg{seq: seq, planSlug: planSlug, err: err}
		}
		// One query for the whole plan's review history. Asking per issue
		// would be a lookup behind every row of the table.
		reviews, err := store.PlanReviews(planSlug)
		if err != nil {
			return planIssuesLoadedMsg{seq: seq, planSlug: planSlug, err: err}
		}
		return planIssuesLoadedMsg{seq: seq, planSlug: planSlug, issues: list, reviews: reviews}
	}
}

// refreshIssues numbers and starts a reload of the issues on screen,
// without raising the loading flag: a silent refresh keeps the table on
// screen instead of flashing a spinner over it.
func (m Model) refreshIssues() (Model, tea.Cmd) {
	m.issuesSeq++
	if m.drilled == "" {
		return m, nil
	}
	return m, loadPlanIssues(m.store, m.drilled, m.cfg, m.issuesSeq)
}

func (m Model) onPlanIssuesLoaded(msg planIssuesLoadedMsg) (tea.Model, tea.Cmd) {
	// A response for a plan the user has already left is stale. Leaving a
	// plan always starts a fresh load, so there is a live response still
	// coming for what is on screen. An older response is dropped the same
	// way: the newer answer is coming.
	if msg.planSlug != m.drilled || msg.seq != m.issuesSeq {
		return m, nil
	}
	m.planIssuesLoading = false
	if msg.err != nil {
		m.planIssuesErr = msg.err
		return m, nil
	}
	m.planIssuesErr = nil
	key := m.issueKey()
	m.planIssues = msg.issues
	m.planReviews = msg.reviews
	m.restoreIssueCursor(key)
	// The cursor is on the first issue from the moment the table appears, so
	// this is where that issue's prose is read. Leaving it to the cursor
	// keys means the detail pane is the one place with nothing to show until
	// the user moves off the issue and back.
	return m, m.selectIssueContent()
}

// issueKey names the selected issue across reloads.
func (m Model) issueKey() int64 {
	issue, ok := m.selectedIssue()
	if !ok {
		return 0
	}
	return issue.Number
}

// restoreIssueCursor puts the cursor back on the issue it was on before a
// reload, or leaves it where it was, clamped into the table.
func (m *Model) restoreIssueCursor(key int64) {
	if key != 0 {
		for i, issue := range m.visibleIssues() {
			if issue.Number == key {
				m.issueCursor = i
				return
			}
		}
	}
	m.clampCursors()
}

// visibleIssues is the issues table as filtered and ordered: an issue pib is
// starting reads as in progress until the store agrees, closed issues hide
// unless Z is on, the needs-you filter keeps only rows that need the user,
// and those rows sort ahead of the rest.
func (m Model) visibleIssues() []issues.Status {
	var out []issues.Status
	for _, issue := range m.planIssues {
		if m.inFlight[issue.Number] {
			// The run is recorded only once the agent's window exists — after
			// a worktree is checked out and tmux has opened — so until then
			// the store still calls the issue ready, and the bar would offer
			// to start a second agent on it.
			issue.InProgress = true
			issue.Ready = false
			issue.Launchable = false
		}
		if issue.State == issues.StateClosed && !m.showClosed {
			continue
		}
		if m.needsYou && !issueNeedsYou(issue) {
			continue
		}
		out = append(out, issue)
	}
	needsYouFirst(out, issueNeedsYou)
	return out
}

// issueNeedsYou reports an issue row that needs the user: launchable, or
// needs attention.
func issueNeedsYou(issue issues.Status) bool {
	return issue.Launchable || issue.NeedsAttention
}

func (m Model) selectedIssue() (issues.Status, bool) {
	visible := m.visibleIssues()
	if m.issueCursor < 0 || m.issueCursor >= len(visible) {
		return issues.Status{}, false
	}
	return visible[m.issueCursor], true
}

// issueMotion moves the issues table's cursor and drills into an issue.
func (m Model) issueMotion(key string) (Model, tea.Cmd, bool) {
	visible := m.visibleIssues()
	switch key {
	case "j", "down":
		if m.issueCursor < len(visible)-1 {
			m.issueCursor++
			m.notice = ""
			m.issueScroll = 0
		}
		return m, m.selectIssueContent(), true
	case "k", "up":
		if m.issueCursor > 0 {
			m.issueCursor--
			m.notice = ""
			m.issueScroll = 0
		}
		return m, m.selectIssueContent(), true
	case "g":
		m.issueCursor = 0
		m.issueScroll = 0
		return m, m.selectIssueContent(), true
	case "G":
		if len(visible) > 0 {
			m.issueCursor = len(visible) - 1
			m.issueScroll = 0
		}
		return m, m.selectIssueContent(), true
	case "ctrl+d", "pgdown":
		m.issueCursor += m.pageSize()
		m.issueScroll = 0
		m.clampCursors()
		return m, m.selectIssueContent(), true
	case "ctrl+u", "pgup":
		m.issueCursor -= m.pageSize()
		m.issueScroll = 0
		m.clampCursors()
		return m, m.selectIssueContent(), true
	case "enter", "l", "right":
		if _, ok := m.selectedIssue(); ok {
			m.screen = screenIssue
			m.notice = ""
			m.issueScroll = 0
			return m, m.selectIssueContent(), true
		}
		return m, nil, true
	}
	return m, nil, false
}

// issueProse is one issue's markdown file as the interface holds it: the body
// and the comments, plus the failure to read them if there was one. A read
// that failed is kept rather than dropped so a second visit does not re-read a
// file that is not there, and so the pane can say the prose is missing rather
// than showing an issue with none.
type issueProse struct {
	file issues.File
	err  error
}

// issueContentLoadedMsg carries one issue's markdown file. The number is on the
// message so a response for an issue the cursor has since left still lands in
// the cache, and so nothing downstream has to guess whose prose this is.
type issueContentLoadedMsg struct {
	number int64
	file   issues.File
	err    error
}

// loadIssueContent reads one issue's markdown file. It runs on selection and
// not on render: a render cannot reach the store, and the tables have no
// prose to show, so a read behind every row of either would buy nothing and
// cost a file open per frame.
func loadIssueContent(store *issues.Store, number int64) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return issueContentLoadedMsg{number: number, err: errors.New("no store")}
		}
		file, err := store.Content(number)
		return issueContentLoadedMsg{number: number, file: file, err: err}
	}
}

// selectIssueContent returns the command that reads the markdown file of the
// issue the cursor has landed on, or nil when that file is already held. The
// cache is what makes this one read per issue rather than one per keystroke:
// without it, holding down the down arrow would re-read every issue on the way
// past, and the full-screen view would re-read one on every frame.
func (m Model) selectIssueContent() tea.Cmd {
	if m.store == nil {
		return nil
	}
	issue, ok := m.selectedIssue()
	if !ok {
		return nil
	}
	if _, held := m.issueProse[issue.Number]; held {
		return nil
	}
	return loadIssueContent(m.store, issue.Number)
}

// issuesView is the middle screen of the drill-down: the plan's issues
// table, with the selected issue's summary and most recent comment in the
// detail pane when the terminal has the rows.
func (m Model) issuesView() string {
	if m.planIssuesLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading issues…"))
	}
	if m.planIssuesErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading issues: " + m.planIssuesErr.Error()))
	}

	h := m.contentHeight()
	if m.isShort() {
		return m.issueTable(m.width, h)
	}
	top, detail := m.tableHeights()
	return lipgloss.JoinVertical(lipgloss.Left,
		m.rule(),
		m.issueTable(m.width, top),
		m.rule(),
		m.issuePreviewPane(m.width, detail),
		m.rule(),
	)
}

// issueTable renders the issues table: number, type, title, state with what
// holds it up, agent and pull request, as ADR-006 §3 lays it out.
func (m Model) issueTable(w, h int) string {
	visible := m.visibleIssues()
	if len(visible) == 0 {
		empty := "No issues in this plan."
		if m.needsYou {
			empty = "Nothing needs you."
		} else if !m.showClosed {
			empty = "No open issues — Z shows closed ones."
		}
		return pad(w, h, helpStyle.Render(empty))
	}

	cols := []tableColumn{
		{title: "#", width: 5},
		{title: "TYPE", width: 11},
		{title: "TITLE", width: 0},
		{title: "STATE", width: 24},
		{title: "AGENT", width: 12},
		{title: "PR", width: 22},
	}
	rows := make([]tableRow, len(visible))
	for i, issue := range visible {
		rows[i] = tableRow{
			cells: []string{
				fmt.Sprintf("#%d", issue.Number),
				issue.Type,
				issue.Title,
				issueStateLabel(issue),
				issue.Agent,
				pullRequestLabel(issue, m.cfg.ReviewCycles()),
			},
			style: issueStateStyle(issue),
		}
	}
	return renderTable(cols, rows, m.issueCursor, w, h)
}

// issueStateLabel is the STATE column: where the issue is, with the blocker
// or attention reason that holds it there. The order is the precedence of
// ADR-005: needs attention is evaluated before ready.
func issueStateLabel(issue issues.Status) string {
	switch {
	case issue.State == issues.StateClosed:
		return "closed"
	case issue.InProgress:
		return "in progress"
	case issue.AwaitingReview:
		return "awaiting review"
	case issue.NeedsAttention:
		label := "needs attention"
		if issue.AttentionReason != "" {
			label += " · " + issue.AttentionReason
		}
		return label
	case issue.Blocked:
		if len(issue.OpenBlockers) > 0 {
			nums := make([]string, len(issue.OpenBlockers))
			for i, blocker := range issue.OpenBlockers {
				nums[i] = fmt.Sprintf("#%d", blocker)
			}
			return "blocked " + strings.Join(nums, ", ")
		}
		return "blocked"
	case issue.Launchable:
		return "launchable"
	case issue.Ready:
		return "ready"
	default:
		return "open"
	}
}

// issueStateStyle colours an issue row by where it is. Needs attention and
// blocked are red: both are a machine that has stopped.
func issueStateStyle(issue issues.Status) lipgloss.Style {
	if issue.State == issues.StateClosed {
		return theme.Default.Dim
	}
	switch {
	case issue.NeedsAttention, issue.Blocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ff757f"))
	case issue.InProgress:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	case issue.AwaitingReview:
		return theme.Default.Secondary
	case issue.Ready:
		return theme.Default.Tertiary
	default:
		return theme.Default.Primary
	}
}

// issuePreviewPane renders the issues screen's detail pane: the selected
// issue's summary and most recent comment, as ADR-006 §3 specifies. The
// review history and the thread are the full-screen view's work.
func (m Model) issuePreviewPane(w, h int) string {
	issue, ok := m.selectedIssue()
	if !ok {
		return pad(w, h, "")
	}
	return pad(w, h, m.issuePreviewContent(issue, w))
}

func (m Model) issuePreviewContent(issue issues.Status, w int) string {
	if w < 1 {
		w = 1
	}
	d := m.detailFor(issue)

	var b strings.Builder
	b.WriteString(theme.Default.PaneHeader.Width(w).Render(fmt.Sprintf("#%d %s", issue.Number, issue.Title)) + "\n")

	state := issueStateLabel(issue)
	if issue.Agent != "" {
		state += " · " + issue.Agent
	}
	b.WriteString(itemStyle.Render(state) + "\n")

	if d.ContentErr != nil {
		b.WriteString("\n" + helpStyle.Render("Could not read the issue file: "+d.ContentErr.Error()) + "\n")
		return strings.TrimRight(b.String(), "\n")
	}
	if body := strings.Trim(d.Body, "\n"); strings.TrimSpace(body) != "" {
		b.WriteString("\n")
		writeProse(&b, itemStyle, body, w-itemStyle.GetPaddingLeft())
	}
	if len(d.Comments) > 0 {
		latest := d.Comments[len(d.Comments)-1]
		b.WriteString("\n" + itemStyle.Render("Latest comment — "+commentHead(latest)) + "\n")
		writeProse(&b, commentStyle, strings.Trim(latest.Body, "\n"), w-commentStyle.GetPaddingLeft())
	}
	return strings.TrimRight(b.String(), "\n")
}

// handleStartIssue starts the issue's agent, matching what `pib issue start`
// does from the command line.
func (m Model) handleStartIssue(issue issues.Status) (tea.Model, tea.Cmd) {
	// Ahead of the readiness check: an issue pib is already starting reads as
	// not ready precisely because it is starting, and "an agent is already
	// working on it" would be a confusing answer to pressing s twice.
	if m.inFlight[issue.Number] {
		m.notice = fmt.Sprintf("#%d already has an agent starting", issue.Number)
		return m, nil
	}
	if issue.Agent == "" {
		m.notice = fmt.Sprintf("No agent is mapped to type %q", issue.Type)
		return m, nil
	}
	if !issue.Ready {
		m.notice = fmt.Sprintf("#%d is not ready: %s", issue.Number, runner.Blocking(issue))
		return m, nil
	}
	if m.agents == nil {
		m.notice = "Agent runner is not available"
		return m, nil
	}

	if m.inFlight == nil {
		m.inFlight = map[int64]bool{}
	}
	// An issue that is starting is no longer launchable, so it sorts out of
	// the needs-you block. The cursor has to follow it there, or the next
	// key lands on whatever row the sort slid under it.
	key := m.issueKey()
	m.inFlight[issue.Number] = true
	m.restoreIssueCursor(key)
	m.notice = fmt.Sprintf("Starting %s on #%d — %s", issue.Agent, issue.Number, issue.Title)

	cmds := []tea.Cmd{spawnAgentCmd(m.agents, issue)}
	if replacement, deprecated := m.cfg.DeprecatedFor(issue.Type); deprecated {
		cmds = append(cmds, deprecationCommentCmd(m.store, issue, replacement))
	}
	return m, tea.Batch(cmds...)
}

// agentFinishedMsg reports a spawn returned: the agent's window closed and
// the runner recorded how it ended. The store event that end publishes is
// what refreshes the tables; this is what releases the in-flight mark and
// says how it went.
type agentFinishedMsg struct {
	issue  issues.Status
	status string
	err    error
}

// spawner starts an agent. The UI names the one method it needs rather than
// taking *runner.Runner, so the launch path can be tested without tmux.
type spawner interface {
	Run(ctx context.Context, req protocol.Request) (protocol.Response, error)
}

func spawnAgentCmd(r spawner, issue issues.Status, sem ...chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if len(sem) > 0 && sem[0] != nil {
			sem[0] <- struct{}{}
			defer func() { <-sem[0] }()
		}
		req := protocol.Request{
			Op:    protocol.OpSpawn,
			Agent: issue.Agent,
			Name:  fmt.Sprintf("%s #%d", issue.Agent, issue.Number),
			Task:  runner.Briefing(issue.Number, issue.Title),
			Issue: issue.Number,
		}
		resp, err := r.Run(context.Background(), req)
		if err != nil {
			return agentFinishedMsg{issue: issue, err: err}
		}
		return agentFinishedMsg{issue: issue, status: resp.Status}
	}
}

// deprecationCommentMsg is a no-op message returned after checking whether
// a deprecation comment needs to be written.
type deprecationCommentMsg struct{}

// deprecationCommentCmd reads an issue's comments and, if no deprecation
// warning is present yet, appends one. It reports a no-op message so the
// caller can ignore it.
func deprecationCommentCmd(store *issues.Store, issue issues.Status, replacement string) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return deprecationCommentMsg{}
		}
		comments, err := store.Comments(issue.Number)
		if err != nil {
			return deprecationCommentMsg{}
		}
		marker := fmt.Sprintf("Type %q is deprecated", issue.Type)
		for _, c := range comments {
			if strings.Contains(c.Body, marker) {
				return deprecationCommentMsg{}
			}
		}
		body := fmt.Sprintf(
			"Type %q is deprecated and was launched as %q. Change this issue's type; the alias goes away in a future release.",
			issue.Type, replacement)
		_ = store.Comment(issue.Number, "pib", body)
		return deprecationCommentMsg{}
	}
}
