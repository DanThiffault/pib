package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7D56F4")).
		MarginLeft(2)

	itemStyle = lipgloss.NewStyle().
		PaddingLeft(4)

	selectedItemStyle = lipgloss.NewStyle().
		PaddingLeft(2).
		Foreground(lipgloss.Color("#7D56F4"))

	helpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		MarginLeft(2)
)

type Model struct {
	choices  []string
	cursor   int
	selected map[int]struct{}
	width    int
	height   int
}

func NewModel() Model {
	return Model{
		choices: []string{
			"Write some Go code",
			"Build a TUI with Bubble Tea",
			"Style it with Lipgloss",
			"Add interactive bubbles",
		},
		selected: make(map[int]struct{}),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("up", "k"))):
			if m.cursor > 0 {
				m.cursor--
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("down", "j"))):
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("enter", " "))):
			_, ok := m.selected[m.cursor]
			if ok {
				delete(m.selected, m.cursor)
			} else {
				m.selected[m.cursor] = struct{}{}
			}
		}
	}

	return m, nil
}

func (m Model) View() string {
	s := titleStyle.Render("🍵 Your new Bubble Tea app") + "\n\n"

	for i, choice := range m.choices {
		cursor := " "
		if m.cursor == i {
			cursor = ">"
			choice = selectedItemStyle.Render(cursor + " " + choice)
		} else {
			choice = itemStyle.Render(cursor + " " + choice)
		}

		checked := " "
		if _, ok := m.selected[i]; ok {
			checked = "✓"
		}

		s += fmt.Sprintf("%s [%s]\n", choice, checked)
	}

	s += "\n" + helpStyle.Render("↑/k up • ↓/j down • space/enter toggle • q/ctrl+c quit")

	return s
}
