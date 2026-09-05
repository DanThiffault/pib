// Command layout renders candidate screens for the horizontal pib TUI.
//
// It is a prototype, not a step toward the implementation: the content is
// fixed, the size is fixed, and nothing here reads the store. It exists so the
// layouts can be compared as pictures rather than as prose.
//
//	go run ./docs/prototypes/layout
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pib/internal/ui/theme"
)

// The frame every screen is drawn at: a wide terminal, which is the case the
// horizontal layout is for.
const (
	W = 116
	H = 32
)

var (
	pal = theme.DefaultPalette

	base   = lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Fg)
	item   = lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Fg)
	sel    = lipgloss.NewStyle().Background(pal.SelectedBg).Foreground(pal.SelectedFg).Bold(true)
	dim    = lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.FgDim)
	head   = lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.FgBright).Bold(true)
	accent = lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Primary)
	green  = lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Tertiary)
	amber  = lipgloss.NewStyle().Background(pal.Bg).Foreground(lipgloss.Color("#e0af68"))
	red    = lipgloss.NewStyle().Background(pal.Bg).Foreground(lipgloss.Color("#ff757f"))
)

func main() {
	screens := []struct {
		caption string
		note    string
		lines   []string
	}{
		{"Plans — the home screen", "The tab bar is gone. Panes stack, so wide output gets the whole terminal.", screenPlans()},
		{"Option A — New plan takes the whole screen", "The list is replaced. Most room for the prompt, least context.", screenNewPlanA()},
		{"Option B — New plan fills the lower pane", "The list stays put and the prompt lands where detail normally is.", screenNewPlanB()},
		{"Option C — New plan keeps one line of context", "A breadcrumb where the list was; everything else is the prompt.", screenNewPlanC()},
		{"Issue detail — the case the layout is for", "Full width for a diff-shaped body, action keys pinned below.", screenIssue()},
	}

	for _, s := range screens {
		fmt.Println()
		fmt.Println(head.Render(s.caption))
		fmt.Println(dim.Render(s.note))
		fmt.Println(frame(s.lines))
	}
}

// frame draws a screen inside a border so its bounds are visible.
func frame(lines []string) string {
	for len(lines) < H {
		lines = append(lines, blank())
	}
	body := base.Render(strings.Join(lines[:H], "\n"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Border).
		Render(body)
}

func blank() string { return strings.Repeat(" ", W) }

// pad fits a styled string to exactly W columns.
func pad(s string) string {
	return lipgloss.NewStyle().Background(pal.Bg).Width(W).MaxWidth(W).Render(s)
}

// spread splits a multi-line render into W-wide screen lines.
func spread(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, pad(line))
	}
	return out
}

// ── chrome ────────────────────────────────────────────────────────────────

// statusLine replaces the tab bar: where pib is, and what it is doing.
func statusLine(right string) string {
	left := accent.Render("pib") + dim.Render(" · ~/dev/pib · main")
	gap := W - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return pad(left + strings.Repeat(" ", gap) + right)
}

// paneTitle is the one-line header above a pane.
func paneTitle(text string) string {
	return pad(head.Render(" " + text))
}

// rule is the horizontal divider between stacked panes. It carries the lower
// pane's title, so the split costs one line rather than two.
func rule(title string) string {
	label := " " + title + " "
	dashes := W - lipgloss.Width(label) - 2
	if dashes < 0 {
		dashes = 0
	}
	return pad(lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Border).Render("──") +
		head.Render(label) +
		lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Border).Render(strings.Repeat("─", dashes)))
}

func row(text string, selected bool) string {
	if selected {
		return lipgloss.NewStyle().Background(pal.SelectedBg).Width(W).MaxWidth(W).
			Render(sel.Background(pal.SelectedBg).Render("  ❯ " + text))
	}
	return pad(item.Render("    " + text))
}

// actionBar is the BBS-style key strip. Every screen has one.
func actionBar(pairs ...string) string {
	keyStyle := lipgloss.NewStyle().Background(pal.Bg).Foreground(pal.Primary).Bold(true)
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, keyStyle.Render("["+pairs[i]+"]")+item.Render(pairs[i+1]))
	}
	return pad(" " + strings.Join(parts, "  "))
}

// ── screens ───────────────────────────────────────────────────────────────

// plansRows is the list at the top of the home screen. "New plan" is the first
// row, so the thing the old Plan tab did is now the first thing on the screen.
func plansRows(cursor int) []string {
	labels := []string{
		accent.Render("+ New plan") + dim.Render("   describe something to plan"),
		item.Render("orders") + dim.Render("                 12 issues") + green.Render("   3 ready") + amber.Render("   1 in review"),
		item.Render("ui-horizontal-layout") + dim.Render("    8 issues") + green.Render("   2 ready"),
		item.Render("pr-review-cycles") + dim.Render("        6 issues") + red.Render("   1 blocked"),
		item.Render("issue-tracking") + dim.Render("         19 issues") + dim.Render("   done"),
	}
	rows := make([]string, 0, len(labels))
	for i, label := range labels {
		rows = append(rows, row(label, i == cursor))
	}
	return rows
}

func dagRows() []string {
	return []string{
		pad(dim.Render("  Order placement — customers can place an order end to end")),
		blank(),
		pad(item.Render("  #12 Order schema") + dim.Render("                    [closed]   coder      PR #41 merged")),
		pad(item.Render("  ├─► #13 Implement Order Aggregate") + amber.Render("   [open]     coder      PR #44 · review 2 of 3")),
		pad(item.Render("  │   ├─► #14 PlaceOrder command handler") + green.Render(" [open]     coder      ready")),
		pad(item.Render("  │   └─► #15 Order projections") + red.Render("          [open]     coder      blocked by #13")),
		pad(item.Render("  └─► #16 Checkout endpoint") + green.Render("           [open]     coder      ready")),
		pad(item.Render("  #17 Review: order placement") + red.Render("          [open]     plan-reviewer  blocked by #13 #14 #15 #16")),
	}
}

func screenPlans() []string {
	lines := []string{statusLine(green.Render("● 1 agent running"))}
	lines = append(lines, paneTitle("Plans"))
	lines = append(lines, plansRows(1)...)
	lines = append(lines, blank())
	lines = append(lines, rule("orders"))
	lines = append(lines, dagRows()...)
	for len(lines) < H-1 {
		lines = append(lines, blank())
	}
	return append(lines, actionBar(
		"N", "New plan", "↵", "Open", "R", "Refresh", "?", "Help", "Q", "Quit"))
}

// piArt is the mark the prompt screen currently carries.
const piArt = ` ██████████████████
███████████████████
    ███       ███
    ███       ███
    ███       ███
   ███        ███
  ████        ████`

func promptBox(width, height int) []string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.BorderFocus).
		Background(pal.Bg).
		Width(width - 2).
		Height(height).
		Render(dim.Render("Describe the project you want to plan…"))
	return spread(box)
}

func screenNewPlanA() []string {
	lines := []string{statusLine(dim.Render("planner · claude-opus-5"))}
	lines = append(lines, blank())
	for _, art := range strings.Split(piArt, "\n") {
		lines = append(lines, pad("    "+accent.Render(art)))
	}
	lines = append(lines, blank())
	lines = append(lines, pad("  "+head.Render("New plan")))
	lines = append(lines, pad("  "+dim.Render("the planner opens in a new tmux window")))
	lines = append(lines, blank())
	lines = append(lines, promptBox(W-4, 8)...)
	for len(lines) < H-1 {
		lines = append(lines, blank())
	}
	return append(lines, actionBar(
		"↵", "Plan", "⌥↵", "Newline", "Esc", "Back", "Q", "Quit"))
}

func screenNewPlanB() []string {
	lines := []string{statusLine(dim.Render("planner · claude-opus-5"))}
	lines = append(lines, paneTitle("Plans"))
	lines = append(lines, plansRows(0)...)
	lines = append(lines, blank())
	lines = append(lines, rule("New plan"))
	lines = append(lines, blank())
	lines = append(lines, pad("  "+dim.Render("the planner opens in a new tmux window")))
	lines = append(lines, blank())
	lines = append(lines, promptBox(W-4, 6)...)
	for len(lines) < H-1 {
		lines = append(lines, blank())
	}
	return append(lines, actionBar(
		"↵", "Plan", "⌥↵", "Newline", "Esc", "Back", "Q", "Quit"))
}

func screenNewPlanC() []string {
	lines := []string{statusLine(dim.Render("planner · claude-opus-5"))}
	lines = append(lines, pad(dim.Render("  Plans ")+accent.Render("› ")+head.Render("New plan")))
	lines = append(lines, rule("New plan"))
	lines = append(lines, blank())
	for _, art := range strings.Split(piArt, "\n") {
		lines = append(lines, pad("    "+accent.Render(art)))
	}
	lines = append(lines, blank())
	lines = append(lines, pad("  "+dim.Render("the planner opens in a new tmux window")))
	lines = append(lines, blank())
	lines = append(lines, promptBox(W-4, 8)...)
	for len(lines) < H-1 {
		lines = append(lines, blank())
	}
	return append(lines, actionBar(
		"↵", "Plan", "⌥↵", "Newline", "Esc", "Back", "Q", "Quit"))
}

func screenIssue() []string {
	lines := []string{statusLine(amber.Render("● coder #13 running · 4m"))}
	lines = append(lines, pad(dim.Render("  Plans › orders › ")+head.Render("#13 Implement Order Aggregate")))
	lines = append(lines, rule("Issue"))
	lines = append(lines, pad(item.Render("  State  ")+amber.Render("open · awaiting review")+dim.Render("      Type  task      Agent  coder      ID  order-agg")))
	lines = append(lines, pad(item.Render("  PR     ")+accent.Render("https://github.com/dan/orders/pull/44")+dim.Render("   open · review cycle 2 of 3")))
	lines = append(lines, blank())
	lines = append(lines, pad(head.Render("  Acceptance")))
	lines = append(lines, pad(item.Render("  • Order aggregate handles the PlaceOrder command")))
	lines = append(lines, pad(item.Render("  • Rejects an order with no line items, with a typed error")))
	lines = append(lines, blank())
	lines = append(lines, pad(head.Render("  Review")))
	lines = append(lines, pad(dim.Render("  cycle 1  ")+red.Render("2 findings")+dim.Render("   → coder addressed them, pushed 3 commits")))
	lines = append(lines, pad(dim.Render("  cycle 2  ")+amber.Render("running")+dim.Render("    code-reviewer #7 · started 1m ago")))
	lines = append(lines, blank())
	lines = append(lines, pad(head.Render("  Out-of-scope comments on the PR")))
	lines = append(lines, pad(item.Render("  ▸ ")+dim.Render("code-reviewer: the money type should not be a float — outside this PR")))
	lines = append(lines, pad(item.Render("    ")+green.Render("dan: yes, file it")+dim.Render("   → pib will create an issue")))
	for len(lines) < H-1 {
		lines = append(lines, blank())
	}
	return append(lines, actionBar(
		"V", "View PR", "F", "Feedback", "K", "Kill run", "C", "Comment", "L", "Log", "B", "Back", "Q", "Quit"))
}
