package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/config"
	"pib/internal/issues"
)

type plansLoadedMsg struct {
	plans  []issues.Plan
	counts map[string]issues.PlanCounts
	err    error
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
		counts, err := store.IssueCountsByPlan()
		if err != nil {
			return plansLoadedMsg{err: err}
		}
		return plansLoadedMsg{plans: plans, counts: counts}
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
		m.planCounts = msg.counts
		m.planCursor = 0
		return m, nil
	case planIssuesLoadedMsg:
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
			return m, nil
		case key.Matches(keyMsg, downKeys):
			if m.planCursor < len(m.plans)-1 {
				m.planCursor++
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

	if m.plansView == viewPlanDetail && !m.planIssuesLoading && m.planIssuesErr == nil {
		currentSlug := ""
		if m.planCursor < len(m.plans) {
			currentSlug = m.plans[m.planCursor].Slug
		}
		if m.planIssuesLoadedFor != currentSlug {
			m.planIssuesLoading = true
			return m, loadPlanIssues(m.store, currentSlug, m.cfg)
		}
	}

	return m, nil
}

func (m Model) tabPlansView() string {
	if m.plansLoading {
		return m.renderCentered(helpStyle.Render("Loading plans…"))
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
	leftW, rightW := paneWidths(m.width)
	h := m.contentHeight()

	leftPane := m.planListPane(leftW, h)
	rightPane := m.planMetadataPane(rightW, h)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, "│", rightPane)
}

func (m Model) planListPane(w, h int) string {
	header := lipgloss.NewStyle().Bold(true).Width(w).Render("Plans")

	maxItems := h - 1
	if maxItems < 1 {
		maxItems = 1
	}

	start := 0
	if m.planCursor >= maxItems {
		start = m.planCursor - maxItems + 1
	}
	end := start + maxItems
	if end > len(m.plans) {
		end = len(m.plans)
	}

	var lines []string
	lines = append(lines, header)
	for i := start; i < end; i++ {
		cursor := "  "
		label := m.plans[i].Slug
		var line string
		if i == m.planCursor {
			line = selectedItemStyle.Width(w).Render(truncate(cursor+"> "+label, w))
		} else {
			line = itemStyle.Width(w).Render(truncate(cursor+"  "+label, w))
		}
		lines = append(lines, line)
	}

	// Pad remaining height
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
}

func (m Model) planMetadataPane(w, h int) string {
	if m.planCursor >= len(m.plans) {
		return lipgloss.NewStyle().Width(w).Height(h).Render("")
	}

	plan := m.plans[m.planCursor]
	counts := m.planCounts[plan.Slug]

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Metadata") + "\n")
	b.WriteString(itemStyle.Render("Title: "+plan.Title) + "\n")
	b.WriteString(itemStyle.Render("Slug:  "+plan.Slug) + "\n")
	if !plan.CreatedAt.IsZero() {
		b.WriteString(itemStyle.Render("Created: "+plan.CreatedAt.Format("2006-01-02")) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("Issues") + "\n")
	b.WriteString(itemStyle.Render(fmt.Sprintf("Total:  %d", counts.Total)) + "\n")
	b.WriteString(itemStyle.Render(fmt.Sprintf("Open:   %d", counts.Open)) + "\n")
	b.WriteString(itemStyle.Render(fmt.Sprintf("Closed: %d", counts.Closed)) + "\n")

	return lipgloss.NewStyle().Width(w).Height(h).Render(b.String())
}

func (m Model) planDetailTwoPaneView() string {
	if m.planIssuesLoading {
		return m.renderCentered(helpStyle.Render("Loading issues…"))
	}
	if m.planIssuesErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading issues: " + m.planIssuesErr.Error()))
	}
	if len(m.planIssues) == 0 {
		return m.renderCentered(helpStyle.Render("No issues in this plan."))
	}

	leftW, rightW := paneWidths(m.width)
	h := m.contentHeight()

	leftPane := m.issueListPane(leftW, h)
	rightPane := m.issuePreviewPane(rightW, h)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, "│", rightPane)
}

func (m Model) issueListPane(w, h int) string {
	header := lipgloss.NewStyle().Bold(true).Width(w).Render("Issues")

	maxItems := h - 1
	if maxItems < 1 {
		maxItems = 1
	}

	start := 0
	if m.issueCursor >= maxItems {
		start = m.issueCursor - maxItems + 1
	}
	end := start + maxItems
	if end > len(m.planIssues) {
		end = len(m.planIssues)
	}

	var lines []string
	lines = append(lines, header)
	for i := start; i < end; i++ {
		cursor := "  "
		issue := m.planIssues[i]
		label := fmt.Sprintf("#%d %s", issue.Number, issue.Title)
		var line string
		if i == m.issueCursor {
			line = selectedItemStyle.Width(w).Render(truncate(cursor+"> "+label, w))
		} else {
			line = itemStyle.Width(w).Render(truncate(cursor+"  "+label, w))
		}
		lines = append(lines, line)
	}

	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
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
