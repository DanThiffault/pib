package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pib/internal/ui/theme"
)

// tableColumn is one column of a table. Width is in cells; a zero width
// marks the flexible column, which takes what the fixed columns leave.
type tableColumn struct {
	title string
	width int
}

// tableRow is one row of cells, in column order, with the style the row's
// state earns it. The cursor row ignores it and renders selected.
type tableRow struct {
	cells []string
	style lipgloss.Style
}

// markerWidth is the cursor gutter every row leaves, so the header and the
// rows line up whether or not they are under the cursor.
const markerWidth = 2

// selectedRowStyle highlights the cursor row. It is the theme's Selected
// colours without its padding: a padded style would shift the row's cells
// out from under the header.
var selectedRowStyle = lipgloss.NewStyle().
	Background(theme.DefaultPalette.SelectedBg).
	Foreground(theme.DefaultPalette.SelectedFg).
	Bold(true)

// renderTable draws a table into a window of exactly w by h: a header row,
// then a window of rows scrolled to keep the cursor visible, with ▲/▼
// indicators where the table extends past the window.
//
// Every row is exactly one line, which is what makes the arithmetic honest.
// The window sizing takes two passes because which indicators appear depends
// on the window, and the window depends on how many indicators appear.
func renderTable(cols []tableColumn, rows []tableRow, cursor, w, h int) string {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	widths := columnWidths(cols, w)

	lines := make([]string, 0, h)
	lines = append(lines, renderTableHeader(cols, widths, w))

	window := func(reserved int) (start, end int) {
		maxItems := h - 1 - reserved
		if maxItems < 1 {
			maxItems = 1
		}
		if cursor >= maxItems {
			start = cursor - maxItems + 1
		}
		end = start + maxItems
		if end > len(rows) {
			end = len(rows)
		}
		return start, end
	}

	start, end := window(0)
	indicators := 0
	if start > 0 {
		indicators++
	}
	if end < len(rows) {
		indicators++
	}
	if indicators > 0 {
		start, end = window(indicators)
	}

	// A pane too short to hold the header, an indicator and a row cannot
	// show a window at all. Drop the indicators rather than overflow.
	if h-1-indicators < 1 {
		indicators = 0
		start, end = window(0)
	}

	if indicators > 0 && start > 0 {
		lines = append(lines, theme.Default.Dim.Width(w).Render("▲"))
	}
	for i := start; i < end; i++ {
		lines = append(lines, renderTableRow(rows[i], widths, w, i == cursor))
	}
	if indicators > 0 && end < len(rows) {
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

// columnWidths resolves the flexible column against the fixed ones. Two
// cells of gap separate columns; the marker gutter comes off the front.
func columnWidths(cols []tableColumn, w int) []int {
	widths := make([]int, len(cols))
	fixed := markerWidth + 2*(len(cols)-1)
	flex := -1
	for i, col := range cols {
		if col.width <= 0 {
			flex = i
			continue
		}
		widths[i] = col.width
		fixed += col.width
	}
	if flex >= 0 {
		rest := w - fixed
		if rest < 1 {
			rest = 1
		}
		widths[flex] = rest
	}
	return widths
}

func renderTableHeader(cols []tableColumn, widths []int, w int) string {
	cells := make([]string, len(cols))
	for i, col := range cols {
		cells[i] = padCell(truncate(col.title, widths[i]), widths[i])
	}
	header := strings.Repeat(" ", markerWidth) + strings.Join(cells, "  ")
	return theme.Default.Dim.Width(w).Render(truncate(header, w))
}

func renderTableRow(row tableRow, widths []int, w int, selected bool) string {
	cells := make([]string, len(row.cells))
	for i, cell := range row.cells {
		cells[i] = padCell(truncate(cell, widths[i]), widths[i])
	}
	marker := strings.Repeat(" ", markerWidth)
	style := row.style
	if selected {
		marker = "▶ "
		style = selectedRowStyle
	}
	line := marker + strings.Join(cells, "  ")
	return style.Width(w).Render(truncate(line, w))
}

// padCell pads a cell to its column width.
func padCell(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// needsYouFirst stably sorts the rows that need the user — launchable or
// needing attention — ahead of the rest, as ADR-006 §3 orders every table.
func needsYouFirst[T any](rows []T, needsYou func(T) bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		return needsYou(rows[i]) && !needsYou(rows[j])
	})
}
