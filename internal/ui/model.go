package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/agent"
	"pib/internal/issues"
	"pib/internal/server"
	"pib/internal/workspace"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginLeft(2)

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(4)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666")).
			MarginLeft(2)

	promptStyle = lipgloss.NewStyle().
			Bold(true).
			MarginLeft(2)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D75F5F")).
			MarginLeft(2)

	noticeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5FAF5F")).
			MarginLeft(2)
)

type Model struct {
	width  int
	height int

	phase     phase
	workspace workspace.Status
	created   bool
	err       error

	planner   agent.Definition
	input     textarea.Model
	notice    string
	server    *server.Server
	store     *issues.Store
	extension string
	socket    string
	agentsDir string
	installed []string
}

// Close releases the socket and the issue store. It is safe to call when
// neither was opened.
func (m Model) Close() error {
	var err error
	if m.server != nil {
		err = m.server.Close()
	}
	if m.store != nil {
		if closeErr := m.store.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func NewModel() Model {
	ta := textarea.New()
	ta.Placeholder = "Describe the project you want to plan…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(6)
	ta.SetWidth(72)
	// Enter submits the description, so newlines move to alt+enter.
	ta.KeyMap.InsertNewline = newlineKeys

	return Model{input: ta}
}

// Err reports a startup failure so the caller can exit non-zero.
func (m Model) Err() error {
	return m.err
}

func (m Model) Init() tea.Cmd {
	return detectWorkspace
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = sizeMsg.Width
		m.height = sizeMsg.Height
		m.input.SetWidth(promptWidth(m.width))
		return m, nil
	}

	m, cmd, handled := m.updateStartup(msg)
	if handled {
		return m, cmd
	}

	return m.updatePrompt(msg)
}

func (m Model) View() string {
	if m.phase != phasePrompt {
		return m.startupView()
	}
	return m.promptView()
}
