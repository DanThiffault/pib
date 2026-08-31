package theme

import "github.com/charmbracelet/lipgloss"

// Palette holds the Tokyo Moon color tokens.
type Palette struct {
	Bg          lipgloss.Color
	Fg          lipgloss.Color
	FgDim       lipgloss.Color
	FgBright    lipgloss.Color
	Primary     lipgloss.Color
	Secondary   lipgloss.Color
	Tertiary    lipgloss.Color
	Border      lipgloss.Color
	BorderFocus lipgloss.Color
	SelectedBg  lipgloss.Color
	SelectedFg  lipgloss.Color
	Gutter      lipgloss.Color
}

// DefaultPalette is the Tokyo Moon palette from folke/tokyonight.nvim.
var DefaultPalette = Palette{
	Bg:          lipgloss.Color("#222436"),
	Fg:          lipgloss.Color("#c8d3f5"),
	FgDim:       lipgloss.Color("#636da6"),
	FgBright:    lipgloss.Color("#e0e8f8"),
	Primary:     lipgloss.Color("#82aaff"),
	Secondary:   lipgloss.Color("#c099ff"),
	Tertiary:    lipgloss.Color("#c3e88d"),
	Border:      lipgloss.Color("#82aaff"),
	BorderFocus: lipgloss.Color("#c099ff"),
	SelectedBg:  lipgloss.Color("#2f334d"),
	SelectedFg:  lipgloss.Color("#82aaff"),
	Gutter:      lipgloss.Color("#1e2030"),
}

// Styles holds pre-built lipgloss styles for the TUI.
//
// Several are not referenced yet. They are the vocabulary ADR-002 specifies,
// and the issues that consume them — the themed panes, the dependency graph,
// the action bar — come after this one. They are ahead of their callers rather
// than left over from a previous design, so resist deleting them as dead code.
type Styles struct {
	Base        lipgloss.Style
	Title       lipgloss.Style
	Header      lipgloss.Style
	Body        lipgloss.Style
	Dim         lipgloss.Style
	Code        lipgloss.Style
	Tag         lipgloss.Style
	Selected    lipgloss.Style
	Border      lipgloss.Style
	Divider     lipgloss.Style
	BorderFocus lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	Help        lipgloss.Style
	Error       lipgloss.Style
	Notice      lipgloss.Style
	Loading     lipgloss.Style
	Item        lipgloss.Style
	Prompt      lipgloss.Style
	TabBar      lipgloss.Style
	PaneHeader  lipgloss.Style
	Primary     lipgloss.Style
	Secondary   lipgloss.Style
	Tertiary    lipgloss.Style
}

// NewStyles builds a Styles value from a Palette.
func NewStyles(p Palette) Styles {
	return Styles{
		Base: lipgloss.NewStyle().
			Background(p.Bg).
			Foreground(p.Fg),
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Primary).
			MarginLeft(2),
		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.FgBright).
			MarginLeft(2),
		Body: lipgloss.NewStyle().
			Foreground(p.Fg),
		Dim: lipgloss.NewStyle().
			Foreground(p.FgDim),
		Code: lipgloss.NewStyle().
			Background(p.Gutter).
			Foreground(p.FgBright),
		Tag: lipgloss.NewStyle().
			Background(p.Secondary).
			Foreground(p.Bg).
			Bold(true).
			Padding(0, 1),
		Selected: lipgloss.NewStyle().
			Background(p.SelectedBg).
			Foreground(p.SelectedFg).
			Bold(true).
			PaddingLeft(4),
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(p.Border),
		Divider: lipgloss.NewStyle().
			Foreground(p.Border),
		BorderFocus: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(p.BorderFocus),
		TabActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Bg).
			Background(p.Primary).
			Padding(0, 2),
		TabInactive: lipgloss.NewStyle().
			Foreground(p.FgDim).
			Padding(0, 2),
		Help: lipgloss.NewStyle().
			Foreground(p.FgDim).
			MarginLeft(2),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ff757f")).
			MarginLeft(2),
		Notice: lipgloss.NewStyle().
			Foreground(p.Tertiary).
			MarginLeft(2),
		Loading: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Tertiary).
			MarginLeft(2),
		Item: lipgloss.NewStyle().
			PaddingLeft(4).
			Foreground(p.Fg),
		Prompt: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.FgBright).
			MarginLeft(2),
		TabBar: lipgloss.NewStyle().
			MarginLeft(2).
			MarginBottom(1),
		PaneHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.FgBright),
		Primary: lipgloss.NewStyle().
			Foreground(p.Primary),
		Secondary: lipgloss.NewStyle().
			Foreground(p.Secondary),
		Tertiary: lipgloss.NewStyle().
			Foreground(p.Tertiary),
	}
}

// Default is the pre-built Tokyo Moon styles.
var Default = NewStyles(DefaultPalette)
