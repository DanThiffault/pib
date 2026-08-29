package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/agent"
	"pib/internal/config"
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

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666")).
			Padding(0, 2)

	tabBarStyle = lipgloss.NewStyle().
			MarginLeft(2).
			MarginBottom(1)
)

var (
	tabKeys  = key.NewBinding(key.WithKeys("tab"))
	tab1Keys = key.NewBinding(key.WithKeys("1"))
	tab2Keys = key.NewBinding(key.WithKeys("2"))
)

type tab int

const (
	tabPlan tab = iota
	tabPlans
)

type plansView int

const (
	viewPlanList plansView = iota
	viewPlanDetail
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

	currentTab   tab
	plansView    plansView
	plans        []issues.Plan
	plansErr     error
	plansLoading bool
	planCursor   int
	issueCursor  int
	cfg          config.Config
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

	if m.phase != phasePrompt {
		return m, nil
	}

	// Global tab switching.
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(keyMsg, tabKeys):
			m = m.switchTab()
			return m.maybeLoadPlans()
		case key.Matches(keyMsg, tab1Keys):
			m.currentTab = tabPlan
			return m, nil
		case key.Matches(keyMsg, tab2Keys):
			m.currentTab = tabPlans
			return m.maybeLoadPlans()
		}
	}

	switch m.currentTab {
	case tabPlan:
		return m.updateTabPlan(msg)
	case tabPlans:
		return m.updateTabPlans(msg)
	default:
		return m, nil
	}
}

func (m Model) View() string {
	if m.phase != phasePrompt {
		return m.startupView()
	}

	var b strings.Builder
	b.WriteString(m.tabBarView() + "\n")
	switch m.currentTab {
	case tabPlan:
		b.WriteString(m.tabPlanView())
	case tabPlans:
		b.WriteString(m.tabPlansView())
	}
	return b.String()
}

func (m Model) switchTab() Model {
	switch m.currentTab {
	case tabPlan:
		m.currentTab = tabPlans
	case tabPlans:
		m.currentTab = tabPlan
	}
	return m
}

func (m Model) maybeLoadPlans() (tea.Model, tea.Cmd) {
	if m.currentTab == tabPlans && len(m.plans) == 0 && !m.plansLoading && m.plansErr == nil {
		m.plansLoading = true
		return m, loadPlans(m.store)
	}
	return m, nil
}

func (m Model) tabBarView() string {
	var tabs []string
	for i, label := range []string{"Plan", "Plans"} {
		t := tab(i)
		if t == m.currentTab {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}
	return tabBarStyle.Render(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
}
