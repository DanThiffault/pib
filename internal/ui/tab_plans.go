package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
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
		return plansLoadedMsg{plans: plans, err: err}
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
		return m, nil
	}

	if len(m.plans) == 0 && !m.plansLoading && m.plansErr == nil {
		m.plansLoading = true
		return m, loadPlans(m.store)
	}

	return m, nil
}

func (m Model) tabPlansView() string {
	if m.plansLoading {
		return helpStyle.Render("Loading plans…")
	}
	if m.plansErr != nil {
		return errorStyle.Render("Error loading plans: " + m.plansErr.Error())
	}
	if len(m.plans) == 0 {
		return helpStyle.Render("No plans yet.")
	}

	return m.plansListView()
}

func (m Model) plansListView() string {
	var b strings.Builder
	for i, plan := range m.plans {
		cursor := "  "
		if i == m.planCursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", cursor, plan.Slug))
	}
	return b.String()
}
