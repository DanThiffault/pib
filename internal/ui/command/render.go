package command

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ellipsis marks a bar that had to be cut short to fit the width.
const ellipsis = "…"

// Bar renders the command bar for a row: the keys that do something here, in
// bar order, cut to the width given. The width is in cells; a non-positive
// width renders everything.
func Bar(reg *Registry, row Row, width int) string {
	parts := make([]string, 0, len(reg.commands))
	for _, c := range reg.For(row) {
		if c.Key == "" {
			continue
		}
		label := c.Label
		if c.Args != "" {
			label += " " + c.Args
		}
		parts = append(parts, c.Key+" "+label)
	}
	return fit(strings.Join(parts, "  "), width)
}

// Help renders the commands that apply to a row, one per line, as
// verb, key and label. It is the same registry and the same predicate as the
// bar, so help cannot list a key that does nothing.
func Help(reg *Registry, row Row) string {
	commands := reg.For(row)
	keyWidth, verbWidth := 0, 0
	for _, c := range commands {
		if w := lipgloss.Width(c.Key); w > keyWidth {
			keyWidth = w
		}
		if w := lipgloss.Width(c.Verb); w > verbWidth {
			verbWidth = w
		}
	}

	var b strings.Builder
	for _, c := range commands {
		label := c.Label
		if c.Args != "" {
			label += " " + c.Args
		}
		b.WriteString("  ")
		b.WriteString(pad(c.Key, keyWidth))
		b.WriteString("  ")
		b.WriteString(pad(c.Verb, verbWidth))
		b.WriteString("  ")
		b.WriteString(label)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// fit cuts s to width cells, on a separator where it can. A width that fits
// nothing is not a reason to render nothing: the first command always shows,
// because an empty bar is a dead bar.
func fit(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	parts := strings.Split(s, "  ")
	out := ""
	for _, p := range parts {
		candidate := p
		if out != "" {
			candidate = out + "  " + p
		}
		if lipgloss.Width(candidate)+2+lipgloss.Width(ellipsis) > width {
			break
		}
		out = candidate
	}
	if out == "" {
		return fitOne(parts[0], width)
	}
	return out + "  " + ellipsis
}

// fitOne cuts a single unbreakable label to width, keeping the ellipsis.
func fitOne(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= lipgloss.Width(ellipsis) {
		return ellipsis
	}
	r := []rune(s)
	return string(r[:width-lipgloss.Width(ellipsis)]) + ellipsis
}

func pad(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
