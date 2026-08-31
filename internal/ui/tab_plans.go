package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/config"
	"pib/internal/issues"
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
	err      error
}

func loadPlanIssues(store *issues.Store, planSlug string, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return planIssuesLoadedMsg{planSlug: planSlug, err: errors.New("no store")}
		}
		list, err := store.Statuses(issues.Filter{Plan: planSlug}, issues.StatusOptions{AgentFor: cfg.AgentFor})
		return planIssuesLoadedMsg{planSlug: planSlug, issues: list, err: err}
	}
}

func (m Model) updateTabPlans(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		m.planIssues = msg.issues
		m.planIssuesErr = nil
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch m.plansView {
		case viewPlanDetail:
			switch {
			case key.Matches(keyMsg, backKeys):
				m.plansView = viewPlanList
				return m, nil
			case key.Matches(keyMsg, upKeys):
				if m.issueCursor > 0 {
					m.issueCursor--
				}
				return m, nil
			case key.Matches(keyMsg, downKeys):
				if m.issueCursor < len(m.planIssues)-1 {
					m.issueCursor++
				}
				return m, nil
			}
			return m, nil
		}

		// viewPlanList
		switch {
		case key.Matches(keyMsg, upKeys):
			if m.planCursor > 0 {
				m.planCursor--
			}
			if slug := m.currentPlanSlug(); slug != "" && m.planIssuesLoadedFor != slug {
				m.planIssuesLoading = true
				return m, loadPlanIssues(m.store, slug, m.cfg)
			}
			return m, nil
		case key.Matches(keyMsg, downKeys):
			if m.planCursor < len(m.plans)-1 {
				m.planCursor++
			}
			if slug := m.currentPlanSlug(); slug != "" && m.planIssuesLoadedFor != slug {
				m.planIssuesLoading = true
				return m, loadPlanIssues(m.store, slug, m.cfg)
			}
			return m, nil
		case key.Matches(keyMsg, selectKeys):
			if len(m.plans) > 0 {
				m.plansView = viewPlanDetail
				m.issueCursor = 0
				m.planIssues = nil
				m.planIssuesErr = nil
				m.planIssuesLoading = true
				m.planIssuesLoadedFor = ""
				return m, loadPlanIssues(m.store, m.plans[m.planCursor].Slug, m.cfg)
			}
			return m, nil
		}
	}

	if len(m.plans) == 0 && !m.plansLoading && m.plansErr == nil {
		m.plansLoading = true
		return m, loadPlans(m.store)
	}

	return m, nil
}

// currentPlanSlug is the plan the cursor is on, empty when there is none.
func (m Model) currentPlanSlug() string {
	if m.planCursor >= len(m.plans) {
		return ""
	}
	return m.plans[m.planCursor].Slug
}

func (m Model) isNarrow() bool {
	return m.width < 80
}

func (m Model) tabPlansView() string {
	if m.plansLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading plans…"))
	}
	if m.plansErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading plans: " + m.plansErr.Error()))
	}
	if len(m.plans) == 0 {
		return m.renderCentered(helpStyle.Render("No plans yet."))
	}

	switch m.plansView {
	case viewPlanDetail:
		return m.planDetailTwoPaneView()
	default:
		return m.planListTwoPaneView()
	}
}

func (m Model) renderCentered(content string) string {
	h := m.contentHeight()
	return lipgloss.NewStyle().Height(h).Render(content)
}

func (m Model) planListTwoPaneView() string {
	h := m.contentHeight()
	if m.isNarrow() {
		return m.planListPane(m.width, h)
	}

	leftW, rightW := paneWidths(m.width)
	leftPane := m.planListPane(leftW, h)
	rightPane := m.planDAGPane(rightW, h)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, dividerStyle.Render("│"), rightPane)
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
	labels := make([]string, len(m.plans))
	for i, plan := range m.plans {
		labels[i] = plan.Slug
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
			lines = append(lines, prefix+"↻ "+formatDAGIssue(issue, avail))
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
			lines = append(lines, prefix+connector+formatDAGIssue(issue, avail))
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
		lines = append(lines, prefix+connector+formatDAGIssue(issue, avail))
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

func formatDAGIssue(issue issues.Status, maxWidth int) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("#%d %s", issue.Number, issue.Title))
	parts = append(parts, "["+string(issue.State)+"]")
	if issue.Agent != "" {
		parts = append(parts, issue.Agent)
	}
	s := strings.Join(parts, " ")
	return truncate(s, maxWidth)
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
	if m.isNarrow() {
		return m.issueListPane(m.width, h)
	}

	leftW, rightW := paneWidths(m.width)
	leftPane := m.issueListPane(leftW, h)
	rightPane := m.issuePreviewPane(rightW, h)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, dividerStyle.Render("│"), rightPane)
}

func (m Model) issueListPane(w, h int) string {
	labels := make([]string, len(m.planIssues))
	for i, issue := range m.planIssues {
		labels[i] = fmt.Sprintf("#%d %s", issue.Number, issue.Title)
	}
	return listPane("Issues", labels, m.issueCursor, w, h)
}

func (m Model) issuePreviewPane(w, h int) string {
	if m.issueCursor >= len(m.planIssues) {
		return lipgloss.NewStyle().Width(w).Height(h).Render("")
	}

	issue := m.planIssues[m.issueCursor]

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render(fmt.Sprintf("#%d %s", issue.Number, issue.Title)) + "\n")
	b.WriteString(itemStyle.Render("State: "+string(issue.State)) + "\n")
	if issue.Type != "" {
		b.WriteString(itemStyle.Render("Type:  "+issue.Type) + "\n")
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
		b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Status") + "\n")
		b.WriteString(itemStyle.Render(strings.Join(flags, ", ")) + "\n")
		b.WriteString("\n")
	}

	if len(issue.OpenBlockers) > 0 {
		b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Blockers") + "\n")
		var blockers []string
		for _, blocker := range issue.OpenBlockers {
			blockers = append(blockers, fmt.Sprintf("#%d", blocker))
		}
		b.WriteString(itemStyle.Render(strings.Join(blockers, ", ")) + "\n")
		b.WriteString("\n")
	}

	if len(issue.Acceptance) > 0 {
		b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Acceptance") + "\n")
		for _, ac := range issue.Acceptance {
			b.WriteString(itemStyle.Render("• "+ac) + "\n")
		}
		b.WriteString("\n")
	}

	if issue.Agent != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Agent") + "\n")
		b.WriteString(itemStyle.Render(issue.Agent) + "\n")
		b.WriteString("\n")
	}

	if issue.Run != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Run") + "\n")
		b.WriteString(itemStyle.Render(issue.Run) + "\n")
		b.WriteString("\n")
	}

	if !issue.CreatedAt.IsZero() {
		b.WriteString(itemStyle.Render("Created: "+issue.CreatedAt.Format("2006-01-02 15:04")) + "\n")
	}
	if !issue.UpdatedAt.IsZero() {
		b.WriteString(itemStyle.Render("Updated: "+issue.UpdatedAt.Format("2006-01-02 15:04")) + "\n")
	}

	content := b.String()
	lines := strings.Split(content, "\n")
	if len(lines) > h {
		lines = lines[:h]
		content = strings.Join(lines, "\n")
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}
