package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// issueScreenView is the full-screen issue detail: the one screen with no
// table, scrollable, with the same command set as the issue's row.
func (m Model) issueScreenView() string {
	return m.issueFullScreenView()
}

// issueFullScreenView renders the issue on screen into the content area,
// scrolled to the model's offset.
func (m Model) issueFullScreenView() string {
	if m.planIssuesLoading {
		return m.renderCentered(loadingStyle.Render("◐ Loading issues…"))
	}
	if m.planIssuesErr != nil {
		return m.renderCentered(errorStyle.Render("Error loading issues: " + m.planIssuesErr.Error()))
	}
	if _, ok := m.selectedIssue(); !ok {
		return m.renderCentered(helpStyle.Render("No issues in this plan."))
	}

	return scrollPane(m.issueFullScreenContent(), m.width, m.contentHeight(), m.issueScroll)
}

// issueFullScreenContent is the full-screen view's content, at whatever
// length it comes to. It is a lookup in what the model already holds — the
// issue's fields, the reviews and comments gathered for it — so a render
// never reaches the store, and neither does the scroll clamp that has to
// know how tall the result is.
func (m Model) issueFullScreenContent() string {
	issue, ok := m.selectedIssue()
	if !ok {
		return ""
	}
	return issueDetailContent(issue, m.detailFor(issue), m.width)
}

// scrollMotion moves the full-screen issue view through its content. In this
// view the cursor keys move the content rather than the selection: the issue
// on screen is the one the issues table is parked on, and what that table is
// short of is rows.
func (m Model) scrollMotion(key string) (Model, tea.Cmd, bool) {
	delta := 0
	switch key {
	case "j", "down":
		delta = 1
	case "k", "up":
		delta = -1
	case "g":
		m.issueScroll = 0
		return m, nil, true
	case "G":
		m.issueScroll = m.maxIssueScroll()
		return m, nil, true
	case "ctrl+d", "pgdown":
		delta = m.contentHeight()
	case "ctrl+u", "pgup":
		delta = -m.contentHeight()
	default:
		return m, nil, false
	}

	m.issueScroll += delta
	// Both ends of the offset are clamped here rather than left to the
	// renderer. Clamping the drawn window alone is not enough: View works on
	// a copy, so an offset that key-repeat carried past the last row would
	// sit in the model while the pane showed the final page, and the up
	// arrow would then spend its presses climbing back out of the gap
	// instead of moving anything.
	if m.issueScroll < 0 {
		m.issueScroll = 0
	}
	if last := m.maxIssueScroll(); m.issueScroll > last {
		m.issueScroll = last
	}
	m.notice = ""
	return m, nil, true
}

// maxIssueScroll is how far down the full-screen view can go: the row that
// leaves the last line of the content on screen.
//
// It works out the row count by rendering what the pane renders, from data
// the model already holds, so asking costs no read of the store or GitHub —
// the same rule every other render is under. scrollPane clamps to this too:
// a terminal that shrank between one keypress and the next can put an offset
// past rows that are no longer there.
func (m Model) maxIssueScroll() int {
	if last := len(wrapToWidth(m.width, m.issueFullScreenContent())) - m.contentHeight(); last > 0 {
		return last
	}
	return 0
}
