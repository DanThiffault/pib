package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch m.plansView {
		case viewPlanDetail:
			if key.Matches(keyMsg, backKeys) {
				m.plansView = viewPlanList
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
		return m.planDetailView()
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

func (m Model) planDetailView() string {
	if m.planCursor >= len(m.plans) {
		return ""
	}
	plan := m.plans[m.planCursor]
	counts := m.planCounts[plan.Slug]

	var b strings.Builder
	b.WriteString(titleStyle.Render(plan.Title) + "\n\n")
	b.WriteString(itemStyle.Render("Slug: "+plan.Slug) + "\n")
	if !plan.CreatedAt.IsZero() {
		b.WriteString(itemStyle.Render("Created: "+plan.CreatedAt.Format("2006-01-02 15:04")) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(itemStyle.Render(fmt.Sprintf("Issues: %d total (%d open, %d closed)", counts.Total, counts.Open, counts.Closed)) + "\n")
	b.WriteString("\n" + helpStyle.Render("←/esc back • ↓/↑ navigate"))

	return b.String()
}
