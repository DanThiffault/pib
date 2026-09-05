package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/agent"
	"pib/internal/config"
	"pib/internal/extension"
	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/recheck"
	"pib/internal/runner"
	"pib/internal/server"
	"pib/internal/workspace"
	"pib/internal/worktree"
)

// phase tracks where startup is in the workspace checks. The main view is
// only reachable once the checks are done.
type phase int

const (
	phaseDetecting phase = iota
	phaseConfirmCreate
	phaseCheckingAgents
	phaseConfirmAgents
	phaseConfirmUpdate
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

type plannerLoadedMsg struct {
	planner agent.Definition
	err     error
}

// agentsCheckedMsg reports whether ~/.pib/agents is already set up, which
// of the definitions there no longer match the ones built into this pib, and
// which defaults are missing from disk entirely.
type agentsCheckedMsg struct {
	installed bool
	outdated  []string
	missing   []string
	migrated  []string
	dir       string
	err       error
}

// agentsUpdatedMsg reports the definitions pib rewrote and where it saved the
// copies it replaced.
type agentsUpdatedMsg struct {
	written []string
	backup  string
	err     error
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
	agents    *runner.Runner
	extension string
	socket    string
	config    config.Config
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
	if err != nil || !installed {
		return agentsCheckedMsg{installed: installed, dir: dir, err: err}
	}

	// An agents directory that cannot be read is not worth failing startup
	// over: pib loads each definition by name when it needs it, and will
	// report a real problem then.
	migrated, _ := agent.MigrateLegacy()
	outdated, _ := agent.Outdated()
	missing, _ := agent.Missing()
	return agentsCheckedMsg{installed: true, outdated: outdated, missing: missing, migrated: migrated, dir: dir}
}

func updateAgents(names []string) tea.Cmd {
	return func() tea.Msg {
		backup, written, err := agent.UpdateDefaults(names, time.Now())
		return agentsUpdatedMsg{written: written, backup: backup, err: err}
	}
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

		agents := runner.Runner{
			GitRoot:       ws.GitRoot,
			StateDir:      ws.Dir,
			ExtensionPath: extensionPath,
			SocketPath:    server.Path(ws.Dir),
			Record:        store,
		}

		// A branch belongs to a checkout, not to a process, so agents working
		// different issues in one directory move each other's. Each issue gets
		// its own checkout instead.
		if cfg.PlanIsolate() {
			trees := worktree.Manager{GitRoot: ws.GitRoot, StateDir: ws.Dir}
			agents.Workspace = trees

			// Nothing is running yet, which is the only safe moment to take a
			// checkout away — an issue can close through reconciliation while
			// its own agent is still working in one.
			if _, err := trees.Sweep(func(issue int64) bool {
				current, err := store.Issue(issue)
				return err == nil && current.State == issues.StateClosed
			}); err != nil {
				return serverStartedMsg{err: err}
			}
		}

		// Whenever an issue closes — explicitly, or because pib saw its pull
		// request merge — look at what is left of the plan before anyone works
		// it as written.
		store.OnClosed = &recheck.Hook{Spawn: agents, Issues: store}

		// And whenever a worker links a pull request, review it while it is
		// still open. internal/review supplies the hook; until it lands, a nil
		// OnLinked means LinkPR behaves exactly as it did before.
		store.OnLinked = nil

		srv, err := server.Listen(ws.Dir, server.Router{
			Agents: agents,
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

		return serverStartedMsg{server: srv, store: store, agents: &agents, extension: extensionPath, socket: srv.Addr(), config: cfg}
	}
}

func createWorkspace(status workspace.Status) tea.Cmd {
	return func() tea.Msg {
		return createdMsg{err: status.Create()}
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
		next, cmd := m.afterWorkspaceExists()
		return next, cmd, true

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
		if len(msg.migrated) > 0 {
			m.notice = fmt.Sprintf("migrated legacy agents: %s", strings.Join(msg.migrated, ", "))
		}
		if len(msg.missing) > 0 {
			m.outdated = msg.outdated
			return m, installAgents, true
		}
		if len(msg.outdated) > 0 {
			m.outdated = msg.outdated
			m.phase = phaseConfirmUpdate
			return m, nil, true
		}
		m.phase = phaseLoadingPlanner
		return m, loadPlanner, true

	case agentsUpdatedMsg:
		// A failed update is not fatal: the definitions already on disk still
		// work, and they are the ones pib was going to use anyway.
		switch {
		case msg.err != nil:
			m.notice = "could not update agents: " + msg.err.Error()
		case len(msg.written) > 0:
			m.notice = fmt.Sprintf("updated %s — replaced copies saved in %s",
				countAgents(len(msg.written)), msg.backup)
		}
		m.outdated = nil
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
			m.notice = fmt.Sprintf("installed %s in %s: %s",
				countAgents(len(msg.written)), m.agentsDir, strings.Join(msg.written, ", "))
		}
		if len(m.outdated) > 0 {
			m.phase = phaseConfirmUpdate
			return m, nil, true
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
		// A nil *runner.Runner in a spawner interface is not a nil
		// interface, and would read as a runner that panics on use.
		if msg.agents != nil {
			m.agents = msg.agents
		}
		m.extension = msg.extension
		m.socket = msg.socket
		m.cfg = msg.config
		m.screen = screenNewPlan
		m.phase = phasePrompt
		return m, tea.Batch(m.input.Focus(), backgroundTick()), true

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

		case phaseConfirmAgents:
			switch {
			case key.Matches(msg, yesKeys):
				return m, installAgents, true
			case key.Matches(msg, noKeys), key.Matches(msg, quitKeys):
				// pib cannot plan anything without a planner definition.
				return m, tea.Quit, true
			}
			return m, nil, true

		case phaseConfirmUpdate:
			switch {
			case key.Matches(msg, yesKeys):
				return m, updateAgents(m.outdated), true
			// Declining is not a reason to stop: the definitions on disk are
			// the ones pib has been using, and they still work.
			case key.Matches(msg, noKeys):
				m.outdated = nil
				m.phase = phaseLoadingPlanner
				return m, loadPlanner, true
			case key.Matches(msg, quitKeys):
				return m, tea.Quit, true
			}
			return m, nil, true

		case phaseFailed:
			return m, tea.Quit, true
		}
	}

	return m, nil, true
}

// afterWorkspaceExists prompts to create the workspace directory when it is
// still missing, and otherwise moves straight on to the agent checks.
func (m Model) afterWorkspaceExists() (Model, tea.Cmd) {
	if !m.workspace.Exists {
		m.phase = phaseConfirmCreate
		return m, nil
	}
	m.phase = phaseCheckingAgents
	return m, checkAgents
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

	case phaseConfirmUpdate:
		var b strings.Builder
		b.WriteString(titleStyle.Render("pib") + "\n\n")
		differ := "differ"
		if len(m.outdated) == 1 {
			differ = "differs"
		}
		b.WriteString(itemStyle.Render(fmt.Sprintf("%s in %s %s from the version built into this pib:",
			countAgents(len(m.outdated)), m.agentsDir, differ)) + "\n\n")
		for _, name := range m.outdated {
			b.WriteString(itemStyle.Render("• "+name) + "\n")
		}
		// Saying which it is would be a guess, so the prompt says what pib
		// will do with the file instead.
		b.WriteString("\n" + itemStyle.Render("That is either a newer pib or your own edits — pib cannot tell.") + "\n")
		b.WriteString(itemStyle.Render("Updating saves your copies under ~/.pib/"+agent.BackupDir+" first.") + "\n\n")
		b.WriteString(promptStyle.Render("Update them?") + "\n\n")
		b.WriteString(helpStyle.Render("y/enter update • n keep mine • q exit"))
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
	}

	return ""
}

// countAgents renders "1 agent" or "3 agents".
func countAgents(n int) string {
	if n == 1 {
		return "1 agent"
	}
	return fmt.Sprintf("%d agents", n)
}
