package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
)

// storeEventMsg reports that the store changed. It carries identity only:
// the model reloads the affected rows rather than reading the change out of
// the event, which is what lets any event — or a missed one — be treated
// the same way.
type storeEventMsg struct{ event issues.Event }

// eventsClosedMsg reports the subscription ended, so no further wait re-arms.
type eventsClosedMsg struct{}

// waitForEvent blocks until the store publishes the next change. Each
// delivery re-arms exactly one more wait, so there is always precisely one
// reader outstanding.
func waitForEvent(events <-chan issues.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-events
		if !ok {
			return eventsClosedMsg{}
		}
		return storeEventMsg{event: e}
	}
}

// onStoreEvent reloads what a change touched: the plans table always — a run
// starting or a plan being applied is what makes planning rows appear and
// disappear — and the issues table when the change names the plan on screen
// (or no plan, which an issue-less run publishes). Prose the change rewrote
// is dropped from the caches and re-read if the cursor is on it.
func (m Model) onStoreEvent(event issues.Event) (tea.Model, tea.Cmd) {
	// The wait re-arms only while there is a subscription to wait on: a
	// model without one — a test, or a store that never opened — gets the
	// reloads and nothing else.
	var cmds []tea.Cmd
	if m.events != nil {
		cmds = append(cmds, waitForEvent(m.events))
	}
	var plansCmd tea.Cmd
	m, plansCmd = m.refreshPlans()
	cmds = append(cmds, plansCmd)

	if m.screen == screenIssues || m.screen == screenIssue {
		if event.Plan == "" || event.Plan == m.drilled {
			var issuesCmd tea.Cmd
			m, issuesCmd = m.refreshIssues()
			cmds = append(cmds, issuesCmd)
		}
	}

	switch event.Kind {
	case issues.EventIssue:
		if event.Issue > 0 {
			delete(m.issueProse, event.Issue)
			if issue, ok := m.selectedIssue(); ok && issue.Number == event.Issue {
				cmds = append(cmds, m.selectIssueContent())
			}
		}
	case issues.EventPlan:
		if event.Plan != "" {
			delete(m.planProse, event.Plan)
			if plan, ok := m.selectedPlan(); ok && plan.Slug == event.Plan {
				cmds = append(cmds, m.selectPlanContent())
			}
		}
	}

	return m, tea.Batch(cmds...)
}
