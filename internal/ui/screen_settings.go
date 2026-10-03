package ui

import (
	"strings"

	"pib/internal/ui/theme"
)

// settingsView is the placeholder the settings command opens until the
// settings screen of ADR-008 lands. esc returns where it was opened from.
func (m Model) settingsView() string {
	var b strings.Builder
	b.WriteString(theme.Default.PaneHeader.Width(m.width).Render("Settings") + "\n\n")
	b.WriteString(itemStyle.Render("The settings screen is not built yet.") + "\n")
	b.WriteString(itemStyle.Render("Until it lands, `pib config` and the workspace's config.toml are the way to change settings.") + "\n\n")
	b.WriteString(helpStyle.Render("esc back"))
	return pad(m.width, m.contentHeight(), b.String())
}
