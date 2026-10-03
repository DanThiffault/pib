package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/triage"
	"pib/internal/ui/theme"
)

// detailView is everything the detail panes render that is not on the issue
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

// issueDetailContent renders an issue into the lines the full-screen view
// scrolls, at whatever length it comes to.
func issueDetailContent(issue issues.Status, view detailView, w int) string {
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
	if issue.LocalID != "" {
		b.WriteString(itemStyle.Render("ID:    "+issue.LocalID) + "\n")
	}
	// The count is on the metadata so a pane can say there is something to
	// read here without spending its rows on the reading itself.
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
	if issue.NeedsAttention {
		flags = append(flags, "needs-attention")
	}
	if len(flags) > 0 {
		section("Status")
		b.WriteString(itemStyle.Render(strings.Join(flags, ", ")) + "\n\n")
	}

	// The whole dependency edge matters, including blockers already closed.
	if len(issue.BlockedBy) > 0 {
		section("Blocked by")
		nums := make([]string, len(issue.BlockedBy))
		for i, blocker := range issue.BlockedBy {
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

	// The pull request is how the work leaves pib, and the bar offers to
	// open it — so the full-screen view says which one it would open.
	if issue.PRURL != "" {
		section("Pull request")
		if issue.PRState != "" {
			b.WriteString(itemStyle.Render(issue.PRState) + "\n")
		}
		b.WriteString(itemStyle.Render(issue.PRURL) + "\n\n")
	}

	// The review history is the story of how the pull request got to the
	// state it is in, and it only exists on an issue that has one.
	if len(view.Reviews) > 0 {
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
	if len(view.OutOfScope) > 0 {
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
	// say, and what the agents found on it.
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

// pullRequestLabel says which pull request an issue is on and how far through
// its review it is. Both halves are on the issue already — the url and the
// newest cycle — so a row costs no more than a row without a pull request.
func pullRequestLabel(issue issues.Status, cycles int) string {
	if issue.PRURL == "" {
		return ""
	}
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
	// pane past the rows it was given and the command bar off the screen.
	if len(lines) > h {
		lines = lines[:h]
	}

	return lipgloss.NewStyle().Width(w).Height(h).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
}
