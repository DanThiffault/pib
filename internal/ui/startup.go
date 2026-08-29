package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/agent"
	"pib/internal/config"
	"pib/internal/extension"
	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/runner"
	"pib/internal/server"
	"pib/internal/workspace"
)

// phase tracks where startup is in the workspace checks. The main view is
// only reachable once the checks are done.
type phase int

const (
	phaseDetecting phase = iota
	phaseConfirmCreate
	phaseConfirmGitignore
	phaseCheckingAgents
	phaseConfirmAgents
	phaseLoadingPlanner
	phaseStartingServer
	phasePrompt
	phaseFailed
)

type detectedMsg struct {
	status workspace.Status
	err    error
}

type createdMsg struct{ err error }

type gitignoredMsg struct{ err error }

type plannerLoadedMsg struct {
	planner agent.Definition
	err     error
}

// agentsCheckedMsg reports whether ~/.pib/agents is already set up.
type agentsCheckedMsg struct {
	installed bool
	dir       string
	err       error
}

// agentsInstalledMsg reports the default agents pib wrote.
type agentsInstalledMsg struct {
	written []string
	err     error
}

// serverStartedMsg reports that the socket agents call pib through is up.
type serverStartedMsg struct {
	server    *server.Server
	store     *issues.Store
	extension string
	socket    string
	err       error
}

var (
	yesKeys    = key.NewBinding(key.WithKeys("y", "Y", "enter"))
	noKeys     = key.NewBinding(key.WithKeys("n", "N"))
	quitKeys   = key.NewBinding(key.WithKeys("q", "Q", "ctrl+c", "esc"))
	anyKeyHint = "press any key to exit"
)

func detectWorkspace() tea.Msg {
	status, err := workspace.Detect()
	return detectedMsg{status: status, err: err}
}

func checkAgents() tea.Msg {
	dir, err := agent.Dir()
	if err != nil {
		return agentsCheckedMsg{err: err}
	}

	installed, err := agent.Installed()
	return agentsCheckedMsg{installed: installed, dir: dir, err: err}
}

func installAgents() tea.Msg {
	written, err := agent.InstallDefaults()
	return agentsInstalledMsg{written: written, err: err}
}

func loadPlanner() tea.Msg {
	planner, err := agent.LoadPlanner()
	return plannerLoadedMsg{planner: planner, err: err}
}

// startServer installs the pi extension, opens the issue store, and opens the
// socket. That one socket carries both kinds of request: agents asking pib to
// spawn other agents, and the pib command line asking about issues.
func startServer(ws workspace.Status) tea.Cmd {
	return func() tea.Msg {
		extensionPath, err := extension.Install(ws.Dir)
		if err != nil {
			return serverStartedMsg{err: err}
		}

		if _, err := config.SeedGlobal(); err != nil {
			return serverStartedMsg{err: err}
		}
		cfg, err := config.Load(ws.Dir)
		if err != nil {
			return serverStartedMsg{err: err}
		}

		store, err := issues.Open(issues.DataDir(ws.Dir))
		if err != nil {
			return serverStartedMsg{err: err}
		}

		srv, err := server.Listen(ws.Dir, server.Router{
			Agents: runner.Runner{
				GitRoot:       ws.GitRoot,
				StateDir:      ws.Dir,
				ExtensionPath: extensionPath,
				SocketPath:    server.Path(ws.Dir),
				Record:        store,
			},
			Issues: issueops.Handler{
				Store:  store,
				Config: cfg,
				Lookup: pr.CLI{},
			},
		})
		if err != nil {
			store.Close()
			return serverStartedMsg{err: err}
		}

		return serverStartedMsg{server: srv, store: store, extension: extensionPath, socket: srv.Addr()}
	}
}

func createWorkspace(status workspace.Status) tea.Cmd {
	return func() tea.Msg {
		return createdMsg{err: status.Create()}
	}
}

func addToGitignore(status workspace.Status) tea.Cmd {
	return func() tea.Msg {
		return gitignoredMsg{err: status.AddToGitignore()}
	}
}

// updateStartup handles every message while the workspace checks are running.
// It returns handled=false once startup is done and the main model should take
// over the message.
func (m Model) updateStartup(msg tea.Msg) (Model, tea.Cmd, bool) {
	if m.phase == phasePrompt {
		return m, nil, false
	}

	switch msg := msg.(type) {
	case detectedMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = msg.err
			return m, nil, true
		}
		m.workspace = msg.status
		next, cmd := m.afterWorkspaceExists()
		return next, cmd, true

	case createdMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = fmt.Errorf("creating %s: %w", m.workspace.Dir, msg.err)
			return m, nil, true
		}
		m.workspace.Exists = true
		m.created = true
		next, cmd := m.afterWorkspaceExists()
		return next, cmd, true

	case gitignoredMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = msg.err
			return m, nil, true
		}
		m.workspace.Ignored = true
		return m.afterWorkspaceReady(), checkAgents, true

	case agentsCheckedMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = msg.err
			return m, nil, true
		}
		m.agentsDir = msg.dir
		if !msg.installed {
			m.phase = phaseConfirmAgents
			return m, nil, true
		}
		m.phase = phaseLoadingPlanner
		return m, loadPlanner, true

	case agentsInstalledMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = msg.err
			return m, nil, true
		}
		m.installed = msg.written
		if len(msg.written) > 0 {
			m.notice = fmt.Sprintf("installed %d agents in %s", len(msg.written), m.agentsDir)
		}
		m.phase = phaseLoadingPlanner
		return m, loadPlanner, true

	case plannerLoadedMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = msg.err
			return m, nil, true
		}
		m.planner = msg.planner
		m.phase = phaseStartingServer
		return m, startServer(m.workspace), true

	case serverStartedMsg:
		if msg.err != nil {
			m.phase = phaseFailed
			m.err = msg.err
			return m, nil, true
		}
		m.server = msg.server
		m.store = msg.store
		m.extension = msg.extension
		m.socket = msg.socket
		m.phase = phasePrompt
		return m, m.input.Focus(), true

	case tea.KeyMsg:
		switch m.phase {
		case phaseConfirmCreate:
			switch {
			case key.Matches(msg, yesKeys):
				return m, createWorkspace(m.workspace), true
			case key.Matches(msg, noKeys), key.Matches(msg, quitKeys):
				return m, tea.Quit, true
			}
			return m, nil, true

		case phaseConfirmGitignore:
			switch {
			case key.Matches(msg, yesKeys):
				return m, addToGitignore(m.workspace), true
			case key.Matches(msg, noKeys):
				// Declining is fine — carry on without ignoring it.
				return m.afterWorkspaceReady(), checkAgents, true
			case key.Matches(msg, quitKeys):
				return m, tea.Quit, true
			}
			return m, nil, true

		case phaseConfirmAgents:
			switch {
			case key.Matches(msg, yesKeys):
				return m, installAgents, true
			case key.Matches(msg, noKeys), key.Matches(msg, quitKeys):
				// pib cannot plan anything without a planner definition.
				return m, tea.Quit, true
			}
			return m, nil, true

		case phaseFailed:
			return m, tea.Quit, true
		}
	}

	return m, nil, true
}

// afterWorkspaceExists moves on to the gitignore check, or straight to the
// create prompt if the directory is still missing. Once both are settled it
// loads the planner agent.
func (m Model) afterWorkspaceExists() (Model, tea.Cmd) {
	switch {
	case !m.workspace.Exists:
		m.phase = phaseConfirmCreate
	case !m.workspace.Ignored:
		m.phase = phaseConfirmGitignore
	default:
		return m.afterWorkspaceReady(), checkAgents
	}
	return m, nil
}

// afterWorkspaceReady moves past the workspace checks to the agent checks.
func (m Model) afterWorkspaceReady() Model {
	m.phase = phaseCheckingAgents
	return m
}

func (m Model) startupView() string {
	switch m.phase {
	case phaseDetecting:
		return helpStyle.Render("Checking workspace…")

	case phaseCheckingAgents:
		return helpStyle.Render("Checking agents…")

	case phaseConfirmAgents:
		var b strings.Builder
		b.WriteString(titleStyle.Render("pib") + "\n\n")
		b.WriteString(itemStyle.Render(fmt.Sprintf("No agents are installed in %s", m.agentsDir)) + "\n")
		b.WriteString(itemStyle.Render("pib runs agents defined there; it cannot plan without them.") + "\n\n")
		b.WriteString(promptStyle.Render("Install the default set?") + "\n\n")
		for _, name := range agent.DefaultNames() {
			b.WriteString(itemStyle.Render("• "+name) + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("y/enter install • n/q exit"))
		return b.String()

	case phaseLoadingPlanner:
		return helpStyle.Render("Loading planner agent…")

	case phaseStartingServer:
		return helpStyle.Render("Starting agent server…")

	case phaseFailed:
		return titleStyle.Render("pib") + "\n\n" +
			errorStyle.Render(m.err.Error()) + "\n\n" +
			helpStyle.Render(anyKeyHint)

	case phaseConfirmCreate:
		return titleStyle.Render("pib") + "\n\n" +
			itemStyle.Render(fmt.Sprintf("No %s directory found in %s", workspace.DirName, m.workspace.GitRoot)) + "\n" +
			itemStyle.Render("pib keeps its workspace there.") + "\n\n" +
			promptStyle.Render(fmt.Sprintf("Create %s?", m.workspace.Dir)) + "\n\n" +
			helpStyle.Render("y/enter create • n/q exit")

	case phaseConfirmGitignore:
		note := ""
		if m.created {
			note = itemStyle.Render("Created "+m.workspace.Dir) + "\n"
		}
		return titleStyle.Render("pib") + "\n\n" + note +
			itemStyle.Render(fmt.Sprintf("%s is not ignored by git.", workspace.DirName)) + "\n\n" +
			promptStyle.Render("Add it to .gitignore?") + "\n\n" +
			helpStyle.Render("y/enter add • n continue without • q exit")
	}

	return ""
}
