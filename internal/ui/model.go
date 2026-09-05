package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pib/internal/agent"
	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/server"
	"pib/internal/ui/theme"
	"pib/internal/workspace"
)

var (
	titleStyle        = theme.Default.Title
	itemStyle         = theme.Default.Item
	helpStyle         = theme.Default.Help
	promptStyle       = theme.Default.Prompt
	errorStyle        = theme.Default.Error
	noticeStyle       = theme.Default.Notice
	loadingStyle      = theme.Default.Loading
	activeTabStyle    = theme.Default.TabActive
	inactiveTabStyle  = theme.Default.TabInactive
	tabBarStyle       = theme.Default.TabBar
	selectedItemStyle = theme.Default.Selected
	dividerStyle      = theme.Default.Divider
)

var (
	upKeys     = key.NewBinding(key.WithKeys("up"))
	downKeys   = key.NewBinding(key.WithKeys("down"))
	selectKeys = key.NewBinding(key.WithKeys("right", "enter"))
	backKeys   = key.NewBinding(key.WithKeys("left", "esc"))
)

type screen int

const (
	screenPlans      screen = iota // plans list + DAG of the selected plan
	screenNewPlan                  // plans list + prompt
	screenPlanDetail               // issue list + detail of the selected issue
	screenIssue                    // breadcrumb + full-width issue detail
)

type Model struct {
	width  int
	height int

	phase     phase
	workspace workspace.Status
	err       error

	planner   agent.Definition
	input     textarea.Model
	notice    string
	server    *server.Server
	store     *issues.Store
	agents    spawner
	extension string
	socket    string
	agentsDir string
	installed []string
	outdated  []string

	screen              screen
	plans               []issues.Plan
	plansErr            error
	plansLoading        bool
	planCursor          int
	issueCursor         int
	planIssues          []issues.Status
	planIssuesLoading   bool
	planIssuesErr       error
	planIssuesLoadedFor string
	cfg                 config.Config

	// inFlight holds the issues pib has an outstanding spawn for. A run is
	// only recorded once the agent's window exists, so until then the store
	// still reports the issue ready — this is what pib knows and the store
	// does not yet.
	inFlight map[int64]bool
	// polling is true while a refresh tick is in flight, so that starting a
	// second agent joins the existing poll instead of opening a second one.
	polling bool
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

	if _, ok := msg.(backgroundTickMsg); ok {
		var cmds []tea.Cmd
		cmds = append(cmds, backgroundTick())
		if (m.screen == screenPlans || m.screen == screenPlanDetail || m.screen == screenIssue) && m.currentPlanSlug() != "" {
			cmds = append(cmds, m.refreshIssues())
		}
		return m, tea.Batch(cmds...)
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		// Quitting works from the top level; below it esc means back.
		quitting := key.Matches(keyMsg, cancelKeys)
		if (m.screen == screenPlanDetail || m.screen == screenIssue) && key.Matches(keyMsg, backKeys) {
			quitting = false
		}
		if quitting {
			return m, tea.Quit
		}
	}

	switch m.screen {
	case screenNewPlan:
		return m.updateScreenNewPlan(msg)
	case screenPlans:
		return m.updateScreenPlans(msg)
	case screenPlanDetail:
		return m.updateScreenPlans(msg)
	case screenIssue:
		return m.updateScreenPlans(msg)
	default:
		return m, nil
	}
}

func (m Model) View() string {
	if m.phase != phasePrompt {
		return m.ground(m.startupView())
	}

	var b strings.Builder
	b.WriteString(m.statusLineView() + "\n")
	if m.screen == screenPlanDetail || m.screen == screenIssue {
		b.WriteString(m.breadcrumbView() + "\n")
	}
	switch m.screen {
	case screenNewPlan:
		b.WriteString(m.newPlanView())
	case screenPlans:
		b.WriteString(m.plansView())
	case screenPlanDetail:
		b.WriteString(m.plansView())
	case screenIssue:
		b.WriteString(m.plansView())
	}
	return m.ground(b.String())
}

// ground paints the theme's background behind the whole view.
//
// Every foreground colour in the palette was picked against that background,
// so leaving it unpainted renders them onto whatever the terminal happens to
// use — and the dim greys, chosen against near-black, are close to invisible
// on a light one. Width fills each line so the ground is not ragged.
func (m Model) ground(view string) string {
	if m.width == 0 {
		return view
	}
	return theme.Default.Base.Width(m.width).Render(view)
}

func (m Model) statusLineView() string {
	left := "pib"
	if m.workspace.GitRoot != "" {
		parts := strings.Split(m.workspace.GitRoot, "/")
		name := parts[len(parts)-1]
		if name != "" {
			left += " · " + name
		}
	}
	if m.workspace.Branch != "" {
		left += " · " + m.workspace.Branch
	}

	var right string
	if m.store != nil {
		if n := m.store.LiveRunCount(); n > 0 {
			right = fmt.Sprintf("%d run", n)
			if n > 1 {
				right += "s"
			}
		}
	}

	width := m.width
	if width < 1 {
		width = 1
	}

	avail := width - lipgloss.Width(left)
	if right != "" && avail > lipgloss.Width(right)+3 {
		return left + strings.Repeat(" ", avail-lipgloss.Width(right)) + right
	}
	return truncate(left, width)
}

func (m Model) breadcrumbView() string {
	parts := []string{"Plans"}
	if m.planCursor < len(m.plans) {
		parts = append(parts, m.plans[m.planCursor].Slug)
	}
	if m.screen == screenIssue && m.issueCursor < len(m.planIssues) {
		parts = append(parts, fmt.Sprintf("#%d %s", m.planIssues[m.issueCursor].Number, m.planIssues[m.issueCursor].Title))
	}
	return strings.Join(parts, " › ")
}

const (
	minTopHeight = 4
	maxTopHeight = 12
	topPercent   = 0.35
)

func paneHeights(total int) (top, bottom int) {
	top = int(float64(total) * topPercent)
	if top < minTopHeight {
		top = minTopHeight
	}
	if top > maxTopHeight {
		top = maxTopHeight
	}
	bottom = total - top - 1 // 1-row rule
	if bottom < 3 {
		bottom = 3
	}
	return
}

func (m Model) contentHeight() int {
	h := m.height - 1 // status line
	if m.screen == screenPlanDetail || m.screen == screenIssue {
		h-- // breadcrumb
	}
	if h < 1 {
		h = 1
	}
	return h
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
