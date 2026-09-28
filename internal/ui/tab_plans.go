package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/protocol"
	"pib/internal/runner"
	"pib/internal/triage"
	"pib/internal/ui/theme"
)

type plansLoadedMsg struct {
	plans []issues.Plan
	err   error
}

func loadPlans(store *issues.Store) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return plansLoadedMsg{err: errors.New("no store")}
		}
		plans, err := store.Plans()
		if err != nil {
			return plansLoadedMsg{err: err}
		}
		return plansLoadedMsg{plans: plans}
	}
}

type planIssuesLoadedMsg struct {
	planSlug string
	issues   []issues.Status
	reviews  map[int64][]issues.Review
	err      error
}

func loadPlanIssues(store *issues.Store, planSlug string, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return planIssuesLoadedMsg{planSlug: planSlug, err: errors.New("no store")}
		}
		list, err := store.Statuses(issues.Filter{Plan: planSlug}, issues.StatusOptions{AgentFor: cfg.AgentFor})
		if err != nil {
			return planIssuesLoadedMsg{planSlug: planSlug, err: err}
		}
		// One query for the whole plan's review history. Asking per issue
		// would be a lookup behind every row of the DAG.
		reviews, err := store.PlanReviews(planSlug)
		if err != nil {
			return planIssuesLoadedMsg{planSlug: planSlug, err: err}
		}
		return planIssuesLoadedMsg{planSlug: planSlug, issues: list, reviews: reviews}
	}
}

// outOfScopeInterval is how often the interface asks the collector to read
// the workspace's open pull requests again. Each pass is one GraphQL call per
// open pull request, so this is deliberately far slower than the tick that
// keeps the lists current, and is the same window reconciliation trusts a
// pull request's own state for.
//
// It is a variable so a test can run the arming pattern in milliseconds
// rather than in the half hour it takes to notice a chain that was armed
// twice.
var outOfScopeInterval = issues.DefaultPRWindow

// outOfScopeTickMsg drives the periodic scan for out-of-scope findings. It is
// separate from the listing tick because it is the one refresh path that
// talks to GitHub.
type outOfScopeTickMsg time.Time

func outOfScopeTick() tea.Cmd {
	return tea.Tick(outOfScopeInterval, func(t time.Time) tea.Msg { return outOfScopeTickMsg(t) })
}

// outOfScopeCollectedMsg reports that a scan of the open pull requests has
// run, or that there was nothing to scan. It says nothing: the collector is
// what a render reads, and its contents are the whole result.
type outOfScopeCollectedMsg struct{}

// collectOutOfScope hands the workspace's open pull requests to the shared
// triage collector, which reads their threads and keeps what it finds for the
// interface to render.
//
// The interface may not read GitHub itself, so this is where the threads come
// from. It names the pull requests the store already believes are open rather
// than reconciling them: settling a pull request — and closing the issue with
// it, and firing every hook that follows — is not something a screen refresh
// should do behind the user's back. Collect returns immediately; the reads
// and any agent they start run off the tick's path.
func collectOutOfScope(store *issues.Store, collector *triage.Collector, plan string) tea.Cmd {
	return func() tea.Msg {
		if store == nil || collector == nil {
			return outOfScopeCollectedMsg{}
		}
		prs, err := store.OpenPullRequests(plan)
		if err != nil {
			return outOfScopeCollectedMsg{}
		}
		collector.Collect(prs)
		return outOfScopeCollectedMsg{}
	}
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
// not on render: a render cannot reach the store, and the plans list and the
// DAG have no prose to show, so a read behind every row of either would buy
// nothing and cost a file open per frame.
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
	if m.store == nil || m.issueCursor < 0 || m.issueCursor >= len(m.planIssues) {
		return nil
	}
	number := m.planIssues[m.issueCursor].Number
	if _, held := m.issueProse[number]; held {
		return nil
	}
	return loadIssueContent(m.store, number)
}

func (m Model) updateScreenPlans(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case plansLoadedMsg:
		m.plansLoading = false
		if msg.err != nil {
			m.plansErr = msg.err
			return m, nil
		}
		m.plans = msg.plans
		m.planCursor = 0
		if slug := m.currentPlanSlug(); slug != "" && m.planIssuesLoadedFor != slug {
			m.planIssuesLoading = true
			return m, loadPlanIssues(m.store, slug, m.cfg)
		}
		return m, nil
	case planIssuesLoadedMsg:
		// A response for a plan the user has already left is stale. Nothing
		// downstream would catch it: every key in the detail view returns
		// before the end of this function, so the wrong plan's issues would
		// stay on screen until a resize. Leaving a plan always starts a fresh
		// load, so there is a live response still coming for what is on
		// screen and the loading flag will clear with it.
		if msg.planSlug != m.currentPlanSlug() {
			return m, nil
		}
		m.planIssuesLoading = false
		m.planIssuesLoadedFor = msg.planSlug
		if msg.err != nil {
			m.planIssuesErr = msg.err
			return m, nil
		}
		m.planIssues = m.markInFlight(msg.issues)
		m.planReviews = msg.reviews
		m.planIssuesErr = nil
		// The cursor is on the first issue from the moment the list appears, so
		// this is where that issue's prose is read. Leaving it to the cursor
		// keys means the preview pane — the pane on screen as the list arrives
		// — is the one place with no comment count until the user moves off the
		// issue and back.
		return m, m.selectIssueContent()
	// Semantic action messages — the contract for future backend handlers.
	case startIssueMsg:
		return m.handleStartIssue(msg.issue)
	case agentFinishedMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("%s #%d stopped: %v", msg.issue.Agent, msg.issue.Number, msg.err)
		} else {
			m.notice = fmt.Sprintf("%s #%d finished: %s", msg.issue.Agent, msg.issue.Number, msg.status)
		}
		delete(m.inFlight, msg.issue.Number)
		return m, m.refreshIssues()
	case refreshTickMsg:
		// The tick only runs while pib is waiting on an agent, so it stops on
		// its own once the last one ends.
		if len(m.inFlight) == 0 {
			m.polling = false
			return m, nil
		}
		return m, tea.Batch(m.refreshIssues(), refreshTick())
	case outOfScopeCollectedMsg:
		return m, nil
	case issueContentLoadedMsg:
		// The cache is keyed by issue number rather than by the selection, so a
		// response that arrives after the cursor has moved still fills the
		// entry the next visit to that issue will look for.
		if m.issueProse == nil {
			m.issueProse = map[int64]issueProse{}
		}
		m.issueProse[msg.number] = issueProse{file: msg.file, err: msg.err}
		return m, nil
	case viewIssueMsg:
		m.notice = fmt.Sprintf("View issue #%d", msg.issue.Number)
		return m, nil
	case cancelIssueMsg:
		m.notice = fmt.Sprintf("Cancel issue #%d", msg.issue.Number)
		return m, nil
	case leaveFeedbackMsg:
		m.notice = fmt.Sprintf("Leave feedback on issue #%d", msg.issue.Number)
		return m, nil
	case respondIssueMsg:
		m.notice = fmt.Sprintf("Respond to issue #%d", msg.issue.Number)
		return m, nil
	case viewPRMsg:
		m.notice = fmt.Sprintf("View PR for issue #%d", msg.issue.Number)
		return m, nil
	case viewBlockersMsg:
		m.notice = fmt.Sprintf("View blockers for issue #%d", msg.issue.Number)
		return m, nil
	case backMsg:
		m.screen = screenPlans
		m.notice = ""
		return m, nil
	case editIssueMsg:
		m.notice = fmt.Sprintf("Edit issue #%d", msg.issue.Number)
		return m, nil
	case commentIssueMsg:
		m.notice = fmt.Sprintf("Comment on issue #%d", msg.issue.Number)
		return m, nil
	case openIssueMsg:
		m.notice = fmt.Sprintf("Reopen issue #%d", msg.issue.Number)
		return m, nil
	case viewLogMsg:
		m.notice = fmt.Sprintf("View log for issue #%d", msg.issue.Number)
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch m.screen {
		case screenIssue:
			if key.Matches(keyMsg, backKeys) {
				m.screen = screenPlanDetail
				m.notice = ""
				return m, nil
			}
			// In the full-screen view the cursor keys move the content rather
			// than the selection: the issue on screen is the one the plan
			// detail view is parked on, and what that view is short of is rows.
			if m.scrollIssue(keyMsg) {
				return m, nil
			}
			m, cmd := m.issueActionKey(keyMsg)
			return m, cmd
		case screenPlanDetail:
			switch {
			case key.Matches(keyMsg, backKeys):
				m.screen = screenPlans
				m.notice = ""
				return m, nil
			case key.Matches(keyMsg, upKeys):
				if m.issueCursor > 0 {
					m.issueCursor--
					m.notice = ""
					m.issueScroll = 0
				}
				return m, m.selectIssueContent()
			case key.Matches(keyMsg, downKeys):
				if m.issueCursor < len(m.planIssues)-1 {
					m.issueCursor++
					m.notice = ""
					m.issueScroll = 0
				}
				return m, m.selectIssueContent()
			case key.Matches(keyMsg, selectKeys):
				if len(m.planIssues) > 0 {
					m.screen = screenIssue
					m.notice = ""
					m.issueScroll = 0
					return m, m.selectIssueContent()
				}
				return m, nil
			case key.Matches(keyMsg, startKeys):
				return m.handleStartAllReady()
			}

			m, cmd := m.issueActionKey(keyMsg)
			return m, cmd
		}

		// screenPlans
		switch {
		case key.Matches(keyMsg, upKeys):
			if m.planCursor > 1 {
				m.planCursor--
			} else if m.planCursor == 1 {
				m.screen = screenNewPlan
				m.planCursor = 0
				return m, m.input.Focus()
			}
			if slug := m.currentPlanSlug(); slug != "" && m.planIssuesLoadedFor != slug {
				m.planIssuesLoading = true
				return m, loadPlanIssues(m.store, slug, m.cfg)
			}
			return m, nil
		case key.Matches(keyMsg, downKeys):
			if m.planCursor < len(m.plans) {
				m.planCursor++
			}
			if slug := m.currentPlanSlug(); slug != "" && m.planIssuesLoadedFor != slug {
				m.planIssuesLoading = true
				return m, loadPlanIssues(m.store, slug, m.cfg)
			}
			return m, nil
		case key.Matches(keyMsg, selectKeys):
			if m.planCursor == 0 {
				m.screen = screenNewPlan
				return m, m.input.Focus()
			}
			if m.planCursor <= len(m.plans) {
				m.screen = screenPlanDetail
				m.issueCursor = 0
				m.issueScroll = 0
				m.planIssues = nil
				m.planReviews = nil
				m.planIssuesErr = nil
				m.planIssuesLoading = true
				m.planIssuesLoadedFor = ""
				m.notice = ""
				return m, loadPlanIssues(m.store, m.plans[m.planCursor-1].Slug, m.cfg)
			}
			return m, nil
		case key.Matches(keyMsg, newPlanKeys):
			m.screen = screenNewPlan
			m.planCursor = 0
			return m, m.input.Focus()
		case key.Matches(keyMsg, refreshKeys):
			m.plansLoading = true
			return m, loadPlans(m.store)
		}
	}

	if len(m.plans) == 0 && !m.plansLoading && m.plansErr == nil {
		m.plansLoading = true
		return m, loadPlans(m.store)
	}

	return m, nil
}

// scrollIssue moves the full-screen issue view through the content of the
// issue on screen, and reports whether the key was one of its own so the
// caller does not offer it to the action bar as well.
//
// Both ends of the offset are clamped here rather than left to the renderer.
// Clamping the drawn window alone is not enough: View works on a copy, so an
// offset that key-repeat carried past the last row would sit in the model while
// the pane showed the final page, and the up arrow would then spend its presses
// climbing back out of the gap instead of moving anything.
func (m *Model) scrollIssue(keyMsg tea.KeyMsg) bool {
	delta := 0
	switch {
	case key.Matches(keyMsg, upKeys):
		delta = -1
	case key.Matches(keyMsg, downKeys):
		delta = 1
	case key.Matches(keyMsg, pageUpKeys):
		delta = -m.contentHeight()
	case key.Matches(keyMsg, pageDownKeys):
		delta = m.contentHeight()
	default:
		return false
	}
	m.issueScroll += delta
	if m.issueScroll < 0 {
		m.issueScroll = 0
	}
	if last := m.maxIssueScroll(); m.issueScroll > last {
		m.issueScroll = last
	}
	m.notice = ""
	return true
}

// maxIssueScroll is how far down the full-screen view can go: the row that
// leaves the last line of the content on screen.
//
// It works out the row count by rendering what the pane renders, from data the
// model already holds, so asking costs no read of the store or GitHub — the
// same rule every other render in this file is under. scrollPane clamps to
// this too: a terminal that shrank between one keypress and the next can put
// an offset past rows that are no longer there.
func (m Model) maxIssueScroll() int {
	if last := len(wrapToWidth(m.width, m.issueFullScreenContent())) - m.contentHeight(); last > 0 {
		return last
	}
	return 0
}

// currentPlanSlug is the plan the cursor is on, empty when there is none.
func (m Model) currentPlanSlug() string {
	if m.planCursor == 0 || m.planCursor > len(m.plans) {
		return ""
	}
	return m.plans[m.planCursor-1].Slug
}

func (m Model) handleStartIssue(issue issues.Status) (Model, tea.Cmd) {
	// Ahead of the readiness check: an issue pib is already starting reads as
	// not ready precisely because it is starting, and "an agent is already
	// working on it" would be a confusing answer to pressing [S] twice.
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
	m.inFlight[issue.Number] = true

	// Show the issue as in progress now rather than waiting for the store to
	// agree, so the action bar stops offering to start it.
	m.planIssues = m.markInFlight(append([]issues.Status(nil), m.planIssues...))
	m.notice = fmt.Sprintf("Starting %s on #%d — %s", issue.Agent, issue.Number, issue.Title)

	cmds := []tea.Cmd{spawnAgentCmd(m.agents, issue)}
	if replacement, deprecated := m.cfg.DeprecatedFor(issue.Type); deprecated {
		cmds = append(cmds, deprecationCommentCmd(m.store, issue, replacement))
	}
	if !m.polling {
		m.polling = true
		cmds = append(cmds, refreshTick())
	}
	return m, tea.Batch(cmds...)
}

// handleStartAllReady starts every launchable issue in the current plan at
// once, matching what `pib plan start` does from the command line.
func (m Model) handleStartAllReady() (Model, tea.Cmd) {
	if m.agents == nil {
		m.notice = "Agent runner is not available"
		return m, nil
	}

	var toStart []issues.Status
	for _, issue := range m.planIssues {
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

	sem := make(chan struct{}, runner.MaxConcurrentAgents)
	var cmds []tea.Cmd
	for _, issue := range toStart {
		m.inFlight[issue.Number] = true
		cmds = append(cmds, spawnAgentCmd(m.agents, issue, sem))
	}

	// Show every started issue as in progress immediately, so the action bar
	// stops offering to start them and the list reflects the new state.
	m.planIssues = m.markInFlight(append([]issues.Status(nil), m.planIssues...))
	m.notice = fmt.Sprintf("Starting %d agents on plan %s", len(toStart), m.currentPlanSlug())

	if !m.polling {
		m.polling = true
		cmds = append(cmds, refreshTick())
	}
	return m, tea.Batch(cmds...)
}

// markInFlight reports the issues pib has an outstanding spawn for as in
// progress. The run is recorded only once the agent's window exists — after a
// worktree is checked out and tmux has opened — so until then the store still
// calls the issue ready, and the action bar would offer to start a second
// agent on it. Two agents on one issue share one worktree, which is the
// collision worktrees exist to prevent.
func (m Model) markInFlight(list []issues.Status) []issues.Status {
	for i := range list {
		if m.inFlight[list[i].Number] {
			list[i].InProgress = true
			list[i].Ready = false
			list[i].Launchable = false
		}
	}
	return list
}

// refreshIssues reloads the current plan without raising the loading flag: a
// silent refresh keeps the list on screen instead of flashing a spinner over
// it once a second.
func (m Model) refreshIssues() tea.Cmd {
	slug := m.currentPlanSlug()
	if slug == "" {
		return nil
	}
	return loadPlanIssues(m.store, slug, m.cfg)
}

// refreshTickMsg drives the poll that shows an agent's progress. A spawn
// blocks for as long as the agent runs, so without it nothing on screen would
// change between starting an agent and its finishing.
type refreshTickMsg time.Time

func refreshTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

// backgroundInterval is how often the plan views are re-read from the store.
// It is a variable for the same reason outOfScopeInterval is: a test that
// runs a tick's arming pattern should not take minutes to do it.
var backgroundInterval = 3 * time.Second

// backgroundTickMsg drives the periodic silent refresh that keeps the plan
// view up to date even when no agent is running.
type backgroundTickMsg time.Time

func backgroundTick() tea.Cmd {
	return tea.Tick(backgroundInterval, func(t time.Time) tea.Msg { return backgroundTickMsg(t) })
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

func (m Model) isShort() bool {
	return m.height < 20
}

// issueActionKey dispatches a contextual action key for the selected issue.
func (m Model) issueActionKey(keyMsg tea.KeyMsg) (Model, tea.Cmd) {
	if m.issueCursor >= len(m.planIssues) {
		return m, nil
	}
	issue := m.planIssues[m.issueCursor]
	for _, a := range issueActions(issue) {
		if keyMsg.String() != a.Key {
			continue
		}
		m.notice = actionNotice(a, issue)
		return m, actionCmd(a, issue)
	}
	return m, nil
}

func (m Model) plansView() string {
	if m.plansLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading plans…"))
	}
	if m.plansErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading plans: " + m.plansErr.Error()))
	}

	switch m.screen {
	case screenIssue:
		return m.issueFullScreenView()
	case screenPlanDetail:
		return m.planDetailTwoPaneView()
	default:
		return m.planListTwoPaneView()
	}
}

func (m Model) renderCentered(content string) string {
	h := m.contentHeight()
	return lipgloss.NewStyle().Height(h).Render(content)
}

func titledRule(w int, title string) string {
	if w < 1 {
		w = 1
	}
	prefix := "── "
	minSuffix := 2
	avail := w - lipgloss.Width(prefix) - minSuffix - 1 // space after title
	if avail < 1 {
		return dividerStyle.Render(strings.Repeat("─", w))
	}
	label := truncate(title, avail)
	rendered := prefix + label + " "
	remaining := w - lipgloss.Width(rendered)
	if remaining < 0 {
		remaining = 0
	}
	return dividerStyle.Render(rendered + strings.Repeat("─", remaining))
}

func (m Model) planListTwoPaneView() string {
	h := m.contentHeight()
	if m.isShort() {
		return m.planListPane(m.width, h)
	}

	topH, bottomH := paneHeights(h)
	topPane := m.planListPane(m.width, topH)

	title := m.currentPlanSlug()
	if title == "" {
		title = "plan"
	}
	rule := titledRule(m.width, title)
	bottomPane := m.planDAGPane(m.width, bottomH)

	return lipgloss.JoinVertical(lipgloss.Left, topPane, rule, bottomPane)
}

// listPane renders the left pane of a two-pane view: a themed header above a
// window of labels, scrolled to keep the cursor in view and padded out to h.
// Scroll indicators appear when the list extends beyond the visible window.
//
// Every row is exactly one line, which is what makes the arithmetic here
// honest. The row styles pad on the left, so a label is truncated to what is
// left of the pane after that padding; truncating to the full width pushes
// the row past w and wraps it onto a second line, and then the pane holds
// fewer rows than this thinks it does and the cursor can sit below its floor.
func listPane(header string, labels []string, cursor, w, h int) string {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	// The header takes one row, and each scroll indicator takes another. All
	// of it has to come out of h before the window is sized, or the pane
	// renders more lines than it was given and the pane beside it no longer
	// lines up.
	//
	// Which indicators appear depends on the window, and the window depends on
	// how many indicators appear. Two passes settle it: size the window with no
	// indicators, see which are needed, then re-size with that reserved.
	rows := func(reserved int) (start, end, maxItems int) {
		maxItems = h - 1 - reserved
		if maxItems < 1 {
			maxItems = 1
		}
		if cursor >= maxItems {
			start = cursor - maxItems + 1
		}
		end = start + maxItems
		if end > len(labels) {
			end = len(labels)
		}
		return start, end, maxItems
	}

	start, end, _ := rows(0)
	indicators := 0
	if start > 0 {
		indicators++
	}
	if end < len(labels) {
		indicators++
	}
	if indicators > 0 {
		start, end, _ = rows(indicators)
	}

	// A pane too short to hold the header, an indicator and a row cannot show
	// a window at all. Drop the indicators rather than overflow.
	if h-1-indicators < 1 {
		indicators = 0
		start, end, _ = rows(0)
	}

	lines := []string{theme.Default.PaneHeader.Width(w).Render(header)}

	if indicators > 0 && start > 0 {
		lines = append(lines, theme.Default.Dim.Width(w).Render("▲"))
	}

	for i := start; i < end; i++ {
		style, marker := itemStyle, "    "
		if i == cursor {
			style, marker = selectedItemStyle, "  > "
		}
		avail := w - style.GetPaddingLeft()
		if avail < 1 {
			avail = 1
		}
		lines = append(lines, style.Width(w).Render(truncate(marker+labels[i], avail)))
	}

	if indicators > 0 && end < len(labels) {
		lines = append(lines, theme.Default.Dim.Width(w).Render("▼"))
	}

	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
}

func (m Model) planListPane(w, h int) string {
	labels := make([]string, len(m.plans)+1)
	labels[0] = "+ New plan    describe something to plan"
	for i, plan := range m.plans {
		labels[i+1] = plan.Slug
	}
	return listPane("Plans", labels, m.planCursor, w, h)
}

func (m Model) planDAGPane(w, h int) string {
	if m.planIssuesLoading {
		return lipgloss.NewStyle().Width(w).Height(h).Render(loadingStyle.Render("◐ Loading issues…"))
	}
	if m.planIssuesErr != nil {
		return lipgloss.NewStyle().Width(w).Height(h).Render(errorStyle.Render("Error: " + m.planIssuesErr.Error()))
	}
	if len(m.planIssues) == 0 {
		return lipgloss.NewStyle().Width(w).Height(h).Render(helpStyle.Render("No issues in this plan."))
	}

	lines, styles := m.buildPlanDAG(w)
	if len(lines) > h {
		lines = lines[:h]
		styles = styles[:h]
	}

	rendered := make([]string, 0, h)
	for i, line := range lines {
		rendered = append(rendered, styles[i].Render(truncate(line, w)))
	}
	for len(rendered) < h {
		rendered = append(rendered, strings.Repeat(" ", w))
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(
		lipgloss.JoinVertical(lipgloss.Left, rendered...),
	)
}

// buildPlanDAG renders the plan's issues as a topologically-sorted ASCII tree.
// It returns one line and one style per row.
func (m Model) buildPlanDAG(w int) ([]string, []lipgloss.Style) {
	// How many review passes a pull request gets, so a row can say which
	// one this is out of. It is the same number the review loop runs to.
	cycles := m.cfg.ReviewCycles()
	byNumber := make(map[int64]issues.Status, len(m.planIssues))
	blocks := make(map[int64][]int64)
	for _, issue := range m.planIssues {
		byNumber[issue.Number] = issue
	}
	for _, issue := range m.planIssues {
		for _, blocker := range issue.BlockedBy {
			if _, ok := byNumber[blocker]; ok {
				blocks[blocker] = append(blocks[blocker], issue.Number)
			}
		}
	}

	// Find roots: issues with no blockers that are also in the plan.
	hasBlockerInPlan := make(map[int64]bool)
	for _, issue := range m.planIssues {
		for _, blocker := range issue.BlockedBy {
			if _, ok := byNumber[blocker]; ok {
				hasBlockerInPlan[issue.Number] = true
				break
			}
		}
	}
	var roots []int64
	for _, issue := range m.planIssues {
		if !hasBlockerInPlan[issue.Number] {
			roots = append(roots, issue.Number)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })

	var lines []string
	var styles []lipgloss.Style
	visited := make(map[int64]bool)
	inStack := make(map[int64]bool)

	var dfs func(number int64, ancestors []bool)
	dfs = func(number int64, ancestors []bool) {
		if inStack[number] {
			issue := byNumber[number]
			prefix := dagPrefix(ancestors)
			avail := w - lipgloss.Width(prefix) - 3
			if avail < 1 {
				avail = 1
			}
			lines = append(lines, prefix+"↻ "+formatDAGIssue(issue, avail, cycles))
			styles = append(styles, dagStyleForIssue(issue))
			return
		}
		if visited[number] {
			issue := byNumber[number]
			prefix := dagPrefix(ancestors)
			connector := "├─► "
			if len(ancestors) > 0 && ancestors[len(ancestors)-1] {
				connector = "└─► "
			}
			avail := w - lipgloss.Width(prefix) - lipgloss.Width(connector)
			if avail < 1 {
				avail = 1
			}
			lines = append(lines, prefix+connector+formatDAGIssue(issue, avail, cycles))
			styles = append(styles, theme.Default.Dim)
			return
		}
		visited[number] = true
		inStack[number] = true
		defer delete(inStack, number)

		issue := byNumber[number]
		prefix := dagPrefix(ancestors)
		var connector string
		if len(ancestors) == 0 {
			connector = ""
		} else if ancestors[len(ancestors)-1] {
			connector = "└─► "
		} else {
			connector = "├─► "
		}
		avail := w - lipgloss.Width(prefix) - lipgloss.Width(connector)
		if avail < 1 {
			avail = 1
		}
		lines = append(lines, prefix+connector+formatDAGIssue(issue, avail, cycles))
		styles = append(styles, dagStyleForIssue(issue))

		children := blocks[number]
		sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
		for i, child := range children {
			childAncestors := append(append([]bool(nil), ancestors...), i == len(children)-1)
			dfs(child, childAncestors)
		}
	}

	for _, root := range roots {
		dfs(root, []bool{})
	}
	return lines, styles
}

func dagPrefix(ancestors []bool) string {
	if len(ancestors) <= 1 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(ancestors)-1; i++ {
		if ancestors[i] {
			b.WriteString("   ")
		} else {
			b.WriteString("│  ")
		}
	}
	return b.String()
}

// formatDAGIssue renders one DAG row, truncated to the width it is given.
// cycles is the review cap for the workspace, and is only used by rows with a
// pull request on them.
func formatDAGIssue(issue issues.Status, maxWidth, cycles int) string {
	parts := []string{fmt.Sprintf("#%d %s", issue.Number, issue.Title), "[" + string(issue.State) + "]"}
	if issue.Agent != "" {
		parts = append(parts, issue.Agent)
	}
	if issue.PRURL != "" {
		parts = append(parts, pullRequestLabel(issue, cycles))
	}
	s := strings.Join(parts, " ")
	return truncate(s, maxWidth)
}

// pullRequestLabel says which pull request an issue is on and how far through
// its review it is. Both halves are on the issue already — the url and the
// newest cycle — so a row costs no more than a row without a pull request.
func pullRequestLabel(issue issues.Status, cycles int) string {
	label := pullRequestName(issue.PRURL)
	if issue.ReviewCycle < 1 {
		return label
	}
	label += fmt.Sprintf(" · review %d", issue.ReviewCycle)
	if cycles > 0 {
		label += fmt.Sprintf(" of %d", cycles)
	}
	return label
}

// pullRequestName is "PR #44", or the url itself when pib cannot read a
// number out of it: a row naming the request beats a row that says nothing,
// and a bad number would be worse than either.
func pullRequestName(url string) string {
	if n := pr.Number(url); n != "" {
		return "PR #" + n
	}
	return url
}

func dagStyleForIssue(issue issues.Status) lipgloss.Style {
	if issue.State == issues.StateClosed {
		return theme.Default.Dim
	}
	switch {
	case issue.Blocked:
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

func (m Model) planDetailTwoPaneView() string {
	if m.planIssuesLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading issues…"))
	}
	if m.planIssuesErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading issues: " + m.planIssuesErr.Error()))
	}
	if len(m.planIssues) == 0 {
		return m.renderCentered(helpStyle.Render("No issues in this plan."))
	}

	h := m.contentHeight()
	if m.isShort() {
		return lipgloss.JoinVertical(lipgloss.Left, m.issueListPane(m.width, h))
	}

	topH, bottomH := paneHeights(h)
	topPane := m.issueListPane(m.width, topH)

	title := "issue"
	if m.issueCursor < len(m.planIssues) {
		title = fmt.Sprintf("#%d", m.planIssues[m.issueCursor].Number)
	}
	rule := titledRule(m.width, title)
	bottomPane := m.issuePreviewPane(m.width, bottomH)

	return lipgloss.JoinVertical(lipgloss.Left,
		topPane,
		rule,
		bottomPane,
	)
}

func (m Model) issueListPane(w, h int) string {
	labels := make([]string, len(m.planIssues))
	for i, issue := range m.planIssues {
		labels[i] = fmt.Sprintf("#%d %s", issue.Number, issue.Title)
	}
	return listPane("Issues", labels, m.issueCursor, w, h)
}

func (m Model) issueFullScreenView() string {
	if m.planIssuesLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading issues…"))
	}
	if m.planIssuesErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading issues: " + m.planIssuesErr.Error()))
	}
	if len(m.planIssues) == 0 {
		return m.renderCentered(helpStyle.Render("No issues in this plan."))
	}

	h := m.contentHeight()
	return scrollPane(m.issueFullScreenContent(), m.width, h, m.issueScroll)
}

// issueFullScreenContent is the full-screen view's content, at whatever length
// it comes to. It is a lookup in what the model already holds — the issue's
// fields, the reviews and comments gathered for it — so a render never reaches
// the store, and neither does the scroll clamp that has to know how tall the
// result is.
func (m Model) issueFullScreenContent() string {
	if m.issueCursor >= len(m.planIssues) {
		return ""
	}
	issue := m.planIssues[m.issueCursor]
	return issueDetailContent(issue, m.detailFor(issue), m.width, detailFull)
}

// issuePreviewPane renders the issue half of the plan detail view. It gets
// roughly half the height of the screen alongside the list, so it shows only
// what identifies the issue; the review history is one keystroke away.
func (m Model) issuePreviewPane(w, h int) string {
	if m.issueCursor >= len(m.planIssues) {
		return pad(w, h, "")
	}
	issue := m.planIssues[m.issueCursor]
	return issueDetail(issue, m.detailFor(issue), w, h, detailPreview)
}

// detailView is everything the detail pane renders that is not on the issue
// itself. It is passed in rather than looked up: a render must never reach
// the store or GitHub, and there is one of these per pane per frame.
type detailView struct {
	// Reviews are the issue's review cycles, oldest first.
	Reviews []issues.Review
	// OutOfScope are the marked findings the last scan of the issue's pull
	// request found, and is empty when no scan has read it.
	OutOfScope []triage.Marked
	// Body is the issue's prose and Comments its activity, in the order the
	// markdown file stores them. Both come from the file, which was read when
	// the cursor landed on the issue; neither is on issues.Status.
	Body     string
	Comments []issues.Comment
	// ContentErr is a failure to read that file, which means the prose below
	// is missing rather than absent.
	ContentErr error
}

// detailFor gathers what has already been collected about an issue. Every
// half is held — two in the store, loaded when the cursor landed on the issue,
// and one in the triage collector, filled by reconciliation — so this is a
// lookup in memory.
func (m Model) detailFor(issue issues.Status) detailView {
	d := detailView{Reviews: m.planReviews[issue.Number]}
	if m.triage != nil {
		d.OutOfScope = m.triage.Marked(issue.Number)
	}
	if prose, ok := m.issueProse[issue.Number]; ok {
		d.Body, d.Comments, d.ContentErr = prose.file.Body, prose.file.Comments, prose.err
	}
	return d
}

// detailDepth selects how much of an issue is worth rendering. The preview
// pane shares the plan detail view with the issue list and gets roughly half
// the width; the full-screen view has the terminal to itself and room for the
// fields that identify an issue outside pib.
type detailDepth int

const (
	detailPreview detailDepth = iota
	detailFull
)

// issueDetail renders an issue's fields into a pane of exactly w by h. It is
// the preview pane's renderer: half a height shared with the issue list is
// rows to spend, not rows to scroll, so what does not fit is cut.
func issueDetail(issue issues.Status, view detailView, w, h int, depth detailDepth) string {
	return pad(w, h, issueDetailContent(issue, view, w, depth))
}

// issueDetailContent renders an issue into the lines the pane shows, at whatever
// length it comes to. The full-screen view scrolls the result; the preview
// pane truncates it, which is why the two are separate.
func issueDetailContent(issue issues.Status, view detailView, w int, depth detailDepth) string {
	if w < 1 {
		w = 1
	}

	var b strings.Builder
	section := func(title string) {
		b.WriteString(theme.Default.PaneHeader.Width(w).Render(title) + "\n")
	}

	b.WriteString(theme.Default.PaneHeader.Width(w).Render(fmt.Sprintf("#%d %s", issue.Number, issue.Title)) + "\n")
	b.WriteString(itemStyle.Render("State: "+string(issue.State)) + "\n")
	if issue.Type != "" {
		b.WriteString(itemStyle.Render("Type:  "+issue.Type) + "\n")
	}
	if depth == detailFull && issue.LocalID != "" {
		b.WriteString(itemStyle.Render("ID:    "+issue.LocalID) + "\n")
	}
	// The count is on the metadata so the preview pane can say there is
	// something to read here without spending a row of a half-width pane on the
	// reading itself. It appears at both depths: the field is the issue's, and
	// the full-screen view has already spent a section on the comments.
	if n := len(view.Comments); n > 0 {
		b.WriteString(itemStyle.Render(fmt.Sprintf("Comments: %d", n)) + "\n")
	}
	b.WriteString("\n")

	var flags []string
	if issue.Blocked {
		flags = append(flags, "blocked")
	}
	if issue.Ready {
		flags = append(flags, "ready")
	}
	if issue.InProgress {
		flags = append(flags, "in-progress")
	}
	if issue.AwaitingReview {
		flags = append(flags, "awaiting-review")
	}
	if issue.Launchable {
		flags = append(flags, "launchable")
	}
	if len(flags) > 0 {
		section("Status")
		b.WriteString(itemStyle.Render(strings.Join(flags, ", ")) + "\n\n")
	}

	// The preview shows only what still holds the issue up. At full screen the
	// whole dependency edge matters, including blockers already closed.
	blockers := issue.OpenBlockers
	label := "Blockers"
	if depth == detailFull && len(issue.BlockedBy) > 0 {
		blockers, label = issue.BlockedBy, "Blocked by"
	}
	if len(blockers) > 0 {
		section(label)
		nums := make([]string, len(blockers))
		for i, blocker := range blockers {
			nums[i] = fmt.Sprintf("#%d", blocker)
		}
		b.WriteString(itemStyle.Render(strings.Join(nums, ", ")) + "\n\n")
	}

	if len(issue.Acceptance) > 0 {
		section("Acceptance")
		for _, ac := range issue.Acceptance {
			b.WriteString(itemStyle.Render("• "+ac) + "\n")
		}
		b.WriteString("\n")
	}

	if issue.Agent != "" {
		section("Agent")
		b.WriteString(itemStyle.Render(issue.Agent) + "\n\n")
	}

	if issue.Run != "" {
		section("Run")
		b.WriteString(itemStyle.Render(issue.Run) + "\n\n")
	}

	// The pull request is how the work leaves pib, and the action bar offers
	// to open it — so the full-screen view says which one it would open.
	if depth == detailFull && issue.PRURL != "" {
		section("Pull request")
		if issue.PRState != "" {
			b.WriteString(itemStyle.Render(issue.PRState) + "\n")
		}
		b.WriteString(itemStyle.Render(issue.PRURL) + "\n\n")
	}

	// The review history is the story of how the pull request got to the
	// state it is in, and it only exists on an issue that has one. The
	// preview pane is a list index that happens to show a preview: it has no
	// room for a history, and a cycle costs two rows the issue's own fields
	// would rather have.
	if depth == detailFull && len(view.Reviews) > 0 {
		section("Review")
		avail := w - itemStyle.GetPaddingLeft()
		for _, r := range view.Reviews {
			b.WriteString(itemStyle.Render(truncate(reviewRow(r, issue.PRURL), avail)) + "\n")
		}
		b.WriteString("\n")
	}

	// A finding the reviewer marked but could not fix here, and whether
	// anyone has asked for it to be filed. Nothing renders until a
	// reconciliation pass has read the pull request: there is no "loading"
	// to promise, because the interface is not the thing asking.
	if depth == detailFull && len(view.OutOfScope) > 0 {
		section("Out-of-scope comments on the PR")
		avail := w - itemStyle.GetPaddingLeft()
		for _, mk := range view.OutOfScope {
			b.WriteString(itemStyle.Render(truncate(outOfScopeRow(mk), avail)) + "\n")
		}
		b.WriteString("\n")
	}

	if !issue.CreatedAt.IsZero() {
		b.WriteString(itemStyle.Render("Created: "+issue.CreatedAt.Format("2006-01-02 15:04")) + "\n")
	}
	if !issue.UpdatedAt.IsZero() {
		b.WriteString(itemStyle.Render("Updated: "+issue.UpdatedAt.Format("2006-01-02 15:04")) + "\n")
	}

	// The prose and the comments are the issue itself: what it was written to
	// say, and what the agents found on it. The preview pane shows neither —
	// half a width cannot hold a fenced diff, and the count above is what it
	// has to say about them — so both are the full-screen view's work, which is
	// the pane the layout exists to fill.
	if depth == detailFull {
		if view.ContentErr != nil {
			section("Description")
			b.WriteString(helpStyle.Render("Could not read the issue file: "+view.ContentErr.Error()) + "\n\n")
		}
		if body := strings.Trim(view.Body, "\n"); strings.TrimSpace(body) != "" {
			section("Description")
			writeProse(&b, itemStyle, body, w-itemStyle.GetPaddingLeft())
			b.WriteString("\n")
		}
		if len(view.Comments) > 0 {
			section(fmt.Sprintf("Comments (%d)", len(view.Comments)))
			// The file's own order, oldest first: a comment thread is a
			// conversation, and a reordering would put an answer above the
			// finding it answers.
			for _, c := range view.Comments {
				b.WriteString(itemStyle.Render(commentHead(c)) + "\n")
				writeProse(&b, commentStyle, strings.Trim(c.Body, "\n"), w-commentStyle.GetPaddingLeft())
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

// commentStyle is a comment's own body, one step past the fields above it, so
// a thread reads as nested under its byline rather than as more metadata.
var commentStyle = lipgloss.NewStyle().
	PaddingLeft(6).
	Foreground(theme.DefaultPalette.Fg)

// commentHead is one comment's byline: who wrote it and when. The timestamp is
// the same form as the other times in the pane rather than the RFC 3339 the
// file stores, which is precise and unreadable at a glance.
func commentHead(c issues.Comment) string {
	if c.At.IsZero() {
		return c.Author
	}
	return c.Author + " · " + c.At.Format("2006-01-02 15:04")
}

// writeProse writes markdown into the pane, wrapped to the width the pane has
// left after the style's own padding.
//
// The wrapping is the same one the pane applies to everything else, done here
// so that a line longer than the pane continues under the text it belongs to
// instead of under the indent. It is deliberately not a markdown renderer: a
// comment can be a fenced diff or a table, and reflowing either would change
// what it says.
func writeProse(b *strings.Builder, style lipgloss.Style, text string, width int) {
	if width < 1 {
		width = 1
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		for _, wrapped := range strings.Split(lipgloss.NewStyle().Width(width).Render(line), "\n") {
			b.WriteString(style.Render(strings.TrimRight(wrapped, " ")) + "\n")
		}
	}
}

// reviewRow is one line of review history: which cycle, how it ended, and
// what it found. A cycle still running says who is on it and for how long,
// which is the only thing about it pib knows.
func reviewRow(r issues.Review, prURL string) string {
	label := fmt.Sprintf("cycle %d", r.Cycle)
	// A replacement pull request numbers its cycles from one again, so two
	// cycles of the same number mean two pull requests and the row has to
	// say which.
	if prURL != "" && r.PRURL != prURL {
		label += " (" + pullRequestName(r.PRURL) + ")"
	}

	state := r.Verdict
	switch {
	case r.Running():
		state = "running"
	case r.Verdict == issues.VerdictApproved:
		state = "approved"
	case r.Verdict == issues.VerdictError:
		state = "errored"
	}

	when := ""
	if r.Running() {
		if r.Run != "" {
			when = r.Run + " · "
		}
		when += "started " + ago(r.StartedAt)
	} else {
		if n := r.Findings; n == 0 {
			when = "no findings"
		} else if n == 1 {
			when = "1 finding"
		} else {
			when = fmt.Sprintf("%d findings", n)
		}
		if !r.EndedAt.IsZero() {
			when += " · " + ago(r.EndedAt)
		}
	}

	row := fmt.Sprintf("  %s  %s", label, state)
	if when != "" {
		row += "  " + when
	}
	return row
}

// outOfScopeRow is one marked finding, and whether it has been filed. The
// marker carries no issue number — a filing replies with a pib:filed marker
// rather than a local record — so "filed" is as much as pib can say.
func outOfScopeRow(mk triage.Marked) string {
	row := "  " + mk.ID
	if place := mk.Place(); place != "" {
		row += "  " + place
	}
	row += "  "
	if mk.Filed {
		row += "filed"
	} else {
		row += "not filed"
	}
	if mk.Summary != "" {
		row += "  " + mk.Summary
	}
	return row
}

// ago says how long ago a moment was, in the coarsest unit worth reading.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// pad fits content to exactly w by h. Height alone only pads, so a long line
// that wraps would push the pane past the rows it was given and shove whatever
// sits below it off the screen; MaxHeight truncates after wrapping, which is
// the only point at which the real line count is known.
func pad(w, h int, content string) string {
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(content)
}

// wrapToWidth renders content wrapped to the pane and returns the rows it
// came to as. Wrapping happens here, once, because a line that wraps is two
// rows: anything that counts the content's height — the scroll window, the
// scroll clamp — would otherwise be counting source newlines and be wrong by
// however much the pane wrapped.
func wrapToWidth(w int, content string) []string {
	if w < 1 {
		w = 1
	}
	return strings.Split(lipgloss.NewStyle().Width(w).Render(content), "\n")
}

// scrollPane fits the window at offset into exactly w by h, for content longer
// than the pane. It is the counterpart to pad: pad cuts at h, and a cut is the
// wrong answer for an issue whose comments run past the bottom of the screen.
//
// The offset is clamped to the content rather than trusted, so a stale offset —
// the terminal shrank, or the pane is shorter than the last one — cannot open a
// window on rows that are not there.
func scrollPane(content string, w, h, offset int) string {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	rows := wrapToWidth(w, content)
	total := len(rows)

	// Scrolling stops where the last row is on screen: any further and the
	// bottom of the content could never be read.
	if last := total - h; offset > last {
		offset = last
	}
	if offset < 0 {
		offset = 0
	}

	// The indicator is a row, but only while there is more below it to reach,
	// so the last page of the content gets all h rows.
	visible := h
	if offset+h < total {
		visible = h - 1
	}
	if visible < 1 {
		visible = 1
	}
	end := offset + visible
	if end > total {
		end = total
	}

	lines := make([]string, 0, h)
	lines = append(lines, rows[offset:end]...)
	if end < total {
		lines = append(lines, theme.Default.Dim.Width(w).Render("▼"))
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	// A pane too short for the indicator and a row of content keeps the
	// content: Height pads but does not cut, so the indicator would push the
	// pane past the rows it was given and the action bar off the screen.
	if len(lines) > h {
		lines = lines[:h]
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
}
