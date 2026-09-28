package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/issues"
	"pib/internal/ui/theme"
)

// Semantic action messages. These are the contract between the UI and the
// backend handlers. Most are not wired to real operations yet; they set a
// notice so the user sees something happened.
type startIssueMsg struct{ issue issues.Status }
type agentFinishedMsg struct {
	issue  issues.Status
	status string
	err    error
}
type viewIssueMsg struct{ issue issues.Status }
type cancelIssueMsg struct{ issue issues.Status }
type leaveFeedbackMsg struct{ issue issues.Status }
type respondIssueMsg struct{ issue issues.Status }
type viewPRMsg struct{ issue issues.Status }
type viewBlockersMsg struct{ issue issues.Status }
type editIssueMsg struct{ issue issues.Status }
type commentIssueMsg struct{ issue issues.Status }
type openIssueMsg struct{ issue issues.Status }
type viewLogMsg struct{ issue issues.Status }
type backMsg struct{}

// Action represents one item in the BBS-style action bar.
type Action struct {
	Key   string
	Label string
}

// issueActions returns the contextual actions for an issue status.
func issueActions(status issues.Status) []Action {
	switch {
	case status.State == issues.StateClosed:
		return []Action{
			{Key: "o", Label: "Open"},
			{Key: "l", Label: "Log"},
			{Key: "c", Label: "Comment"},
			{Key: "e", Label: "Edit"},
		}
	case status.InProgress:
		return []Action{
			{Key: "v", Label: "View run"},
			{Key: "k", Label: "Kill run"},
		}
	case status.AwaitingReview:
		return []Action{
			{Key: "f", Label: "Feedback"},
			{Key: "r", Label: "Respond"},
			{Key: "v", Label: "View PR"},
		}
	case status.Blocked:
		return []Action{
			{Key: "v", Label: "View blockers"},
		}
	case status.Launchable:
		return []Action{
			{Key: "s", Label: "Start"},
			{Key: "v", Label: "View"},
		}
	case status.Ready:
		return []Action{
			{Key: "e", Label: "Edit"},
			{Key: "c", Label: "Comment"},
		}
	default:
		return []Action{
			{Key: "e", Label: "Edit"},
			{Key: "c", Label: "Comment"},
		}
	}
}

// screenActions returns the contextual actions for the current screen.
func (m Model) screenActions() []Action {
	if m.phase != phasePrompt {
		switch m.phase {
		case phaseConfirmAgents:
			return []Action{{Key: "y", Label: "Install"}, {Key: "n", Label: "Exit"}}
		case phaseConfirmUpdate:
			return []Action{{Key: "y", Label: "Update"}, {Key: "n", Label: "Keep"}}
		case phaseConfirmCreate:
			return []Action{{Key: "y", Label: "Create"}, {Key: "n", Label: "Exit"}}
		case phaseFailed:
			return nil
		default:
			return nil
		}
	}

	switch m.screen {
	case screenPlans:
		actions := []Action{
			{Key: "n", Label: "New plan"},
			{Key: "enter", Label: "Open"},
		}
		if !m.plansLoading && m.plansErr == nil {
			actions = append(actions, Action{Key: "r", Label: "Refresh"})
		}
		return actions
	case screenNewPlan:
		return []Action{
			{Key: "enter", Label: "Plan"},
			{Key: "alt+enter", Label: "Newline"},
			{Key: "esc", Label: "Back"},
		}
	case screenPlanDetail, screenIssue:
		var actions []Action
		if m.issueCursor >= len(m.planIssues) {
			actions = []Action{{Key: "b", Label: "Back"}}
		} else {
			actions = issueActions(m.planIssues[m.issueCursor])
			if m.screen == screenPlanDetail && m.hasLaunchableIssues() {
				startAllAction := Action{Key: "s", Label: "Start all"}
				rest := make([]Action, 0, len(actions))
				for _, action := range actions {
					if action.Key != startAllAction.Key {
						rest = append(rest, action)
					}
				}
				actions = append([]Action{startAllAction}, rest...)
			}
			actions = append(actions, Action{Key: "b", Label: "Back"})
		}
		return actions
	default:
		return nil
	}
}

// actionBarView renders the contextual action bar as the last row of the view.
func (m Model) actionBarView(width int) string {
	if width < 1 {
		width = 1
	}
	if m.notice != "" {
		notice := truncate(m.notice, width)
		pad := width - len([]rune(notice))
		if pad > 0 {
			notice += strings.Repeat(" ", pad)
		}
		return lipgloss.NewStyle().Foreground(theme.DefaultPalette.Tertiary).Render(notice)
	}

	actions := m.screenActions()
	if m.phase == phasePrompt && (m.screen != screenNewPlan || !m.input.Focused()) {
		actions = append(actions, Action{Key: "q", Label: "Quit"})
	}
	actions = append(actions, Action{Key: "?", Label: "Help"})

	return renderActionBar(actions, width)
}

// helpView renders a modal listing the current screen's key bindings.
func (m Model) helpView() string {
	h := m.contentHeight()
	var b strings.Builder
	b.WriteString(theme.Default.PaneHeader.Width(m.width).Render("Help") + "\n\n")

	for _, a := range m.screenActions() {
		b.WriteString(itemStyle.Render(fmt.Sprintf("[%s] %s", strings.ToUpper(a.Key), a.Label)) + "\n")
	}
	if m.phase == phasePrompt && (m.screen != screenNewPlan || !m.input.Focused()) {
		b.WriteString(itemStyle.Render("[Q] Quit") + "\n")
	}
	b.WriteString(itemStyle.Render("[?] Help") + "\n")

	return pad(m.width, h, b.String())
}

// hasLaunchableIssues reports whether the current plan has at least one
// issue that is launchable and not already starting.
func (m Model) hasLaunchableIssues() bool {
	for _, issue := range m.planIssues {
		if issue.Launchable && !m.inFlight[issue.Number] {
			return true
		}
	}
	return false
}

func renderActionBar(actions []Action, width int) string {
	var parts []string
	for _, a := range actions {
		parts = append(parts, formatAction(a))
	}
	content := strings.Join(parts, "  ")
	visible := lipgloss.Width(content)
	if visible > width {
		content = truncate(content, width)
		visible = lipgloss.Width(content)
	}
	if visible < width {
		content += strings.Repeat(" ", width-visible)
	}
	return content
}

func formatAction(a Action) string {
	keyStyle := lipgloss.NewStyle().Foreground(theme.DefaultPalette.Primary).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(theme.DefaultPalette.Fg)
	return keyStyle.Render("["+strings.ToUpper(a.Key)+"]") + labelStyle.Render(a.Label)
}

// actionNotice returns the human-readable notice for an action.
func actionNotice(a Action, issue issues.Status) string {
	switch a.Key {
	case "s":
		return fmt.Sprintf("Start issue #%d", issue.Number)
	case "v":
		switch {
		case issue.InProgress:
			return fmt.Sprintf("View log for issue #%d", issue.Number)
		case issue.AwaitingReview:
			return fmt.Sprintf("View PR for issue #%d", issue.Number)
		case issue.Blocked:
			return fmt.Sprintf("View blockers for issue #%d", issue.Number)
		default:
			return fmt.Sprintf("View issue #%d", issue.Number)
		}
	case "c":
		return fmt.Sprintf("Comment on issue #%d", issue.Number)
	case "k":
		return fmt.Sprintf("Kill the run on issue #%d", issue.Number)
	case "l":
		return fmt.Sprintf("View the run log for issue #%d", issue.Number)
	case "f":
		return fmt.Sprintf("Leave feedback on issue #%d", issue.Number)
	case "r":
		return fmt.Sprintf("Respond to issue #%d", issue.Number)
	case "e":
		return fmt.Sprintf("Edit issue #%d", issue.Number)
	case "o":
		return fmt.Sprintf("Reopen issue #%d", issue.Number)
	default:
		return ""
	}
}

// actionCmd returns a tea.Cmd that emits the semantic message for an action.
func actionCmd(a Action, issue issues.Status) tea.Cmd {
	switch a.Key {
	case "s":
		return func() tea.Msg { return startIssueMsg{issue: issue} }
	case "v":
		switch {
		case issue.InProgress:
			return func() tea.Msg { return viewLogMsg{issue: issue} }
		case issue.AwaitingReview:
			return func() tea.Msg { return viewPRMsg{issue: issue} }
		case issue.Blocked:
			return func() tea.Msg { return viewBlockersMsg{issue: issue} }
		default:
			return func() tea.Msg { return viewIssueMsg{issue: issue} }
		}
	case "c":
		return func() tea.Msg { return commentIssueMsg{issue: issue} }
	case "k":
		return func() tea.Msg { return cancelIssueMsg{issue: issue} }
	case "l":
		return func() tea.Msg { return viewLogMsg{issue: issue} }
	case "f":
		return func() tea.Msg { return leaveFeedbackMsg{issue: issue} }
	case "r":
		return func() tea.Msg { return respondIssueMsg{issue: issue} }
	case "e":
		return func() tea.Msg { return editIssueMsg{issue: issue} }
	case "o":
		return func() tea.Msg { return openIssueMsg{issue: issue} }
	default:
		return nil
	}
}
