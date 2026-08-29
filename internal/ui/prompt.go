package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/agent"
	"pib/internal/runner"
	"pib/internal/tmux"
)

var (
	submitKeys  = key.NewBinding(key.WithKeys("enter"))
	newlineKeys = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"), key.WithHelp("alt+enter", "insert newline"))
	cancelKeys  = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
)

// sessionOpenedMsg reports that pi was started in its own tmux window.
type sessionOpenedMsg struct {
	window string
	err    error
}

// sessionFinishedMsg reports that an in-terminal pi process exited. It is only
// used when pib is running outside tmux.
type sessionFinishedMsg struct{ err error }

// launchConfig is everything needed to start a planner session.
type launchConfig struct {
	planner   agent.Definition
	dir       string
	prompt    string
	extension string
	socket    string
}

// env is what the planner needs to reach pib. tmux spawns windows from the
// server's environment rather than pib's, so this is passed explicitly.
func (c launchConfig) env() map[string]string {
	return map[string]string{
		runner.EnvSocket: c.socket,
		runner.EnvAgent:  c.planner.Name,
	}
}

func (c launchConfig) argv() []string {
	args := c.planner.Flags(agent.Options{Extensions: []string{c.extension}})
	args = append(args, "--", c.prompt)
	return append([]string{agent.Executable}, args...)
}

// launchPlanner starts pi rooted at the repository. Inside tmux it opens a new
// window, so pib keeps running alongside the planner; otherwise it hands over
// the current terminal and waits. It is a variable so tests can observe a
// launch without spawning a process.
var launchPlanner = func(cfg launchConfig) tea.Cmd {
	if tmux.Inside() {
		return func() tea.Msg {
			window, err := tmux.NewWindow(tmux.Options{
				Name: cfg.planner.Name,
				Dir:  cfg.dir,
				Env:  cfg.env(),
			}, cfg.argv())
			return sessionOpenedMsg{window: window.Index, err: err}
		}
	}

	argv := cfg.argv()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cfg.dir
	cmd.Env = os.Environ()
	for key, value := range cfg.env() {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return sessionFinishedMsg{err: err}
	})
}

func (m Model) updateTabPlan(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionOpenedMsg:
		if msg.err != nil {
			m.notice = "could not open tmux window: " + msg.err.Error()
			return m, nil
		}

		m.notice = fmt.Sprintf("planner opened in tmux window %s", msg.window)
		if m.planner.AutoExit {
			return m, tea.Quit
		}

		m.input.Reset()
		return m, m.input.Focus()

	case sessionFinishedMsg:
		if msg.err != nil {
			var exitErr *exec.ExitError
			if errors.As(msg.err, &exitErr) {
				m.notice = fmt.Sprintf("planner exited with status %d", exitErr.ExitCode())
			} else {
				m.notice = "could not run pi: " + msg.err.Error()
			}
		} else {
			m.notice = "planner session ended"
		}

		if m.planner.AutoExit {
			return m, tea.Quit
		}

		m.input.Reset()
		return m, m.input.Focus()

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, submitKeys):
			description := strings.TrimSpace(m.input.Value())
			if description == "" {
				m.notice = "enter a description first"
				return m, nil
			}
			m.notice = ""
			return m, launchPlanner(launchConfig{
				planner:   m.planner,
				dir:       m.workspace.GitRoot,
				prompt:    description,
				extension: m.extension,
				socket:    m.socket,
			})
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) tabPlanView() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("pib") + "\n\n")
	b.WriteString(itemStyle.Render(fmt.Sprintf("%s · %s", m.planner.Name, m.workspace.GitRoot)) + "\n")
	if m.planner.Model != "" {
		b.WriteString(itemStyle.Render(m.planner.Model) + "\n")
	}
	b.WriteString(helpStyle.Render(destination()) + "\n")
	b.WriteString("\n")
	b.WriteString(promptStyle.Render("What do you want to plan?") + "\n\n")
	b.WriteString(m.input.View() + "\n")

	if m.notice != "" {
		b.WriteString("\n" + noticeStyle.Render(m.notice) + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("enter plan • alt+enter newline • esc/ctrl+c quit"))

	return b.String()
}

// destination describes where a launched session will run, so the fallback to
// taking over this terminal is never a surprise.
func destination() string {
	switch {
	case tmux.Inside():
		return "sessions open in a new tmux window"
	case tmux.Available():
		return "not inside tmux — the planner will take over this terminal"
	default:
		return "tmux not found — the planner will take over this terminal"
	}
}

func promptWidth(total int) int {
	const (
		margin = 6
		min    = 20
		max    = 100
	)

	width := total - margin
	if width > max {
		width = max
	}
	if width < min {
		width = min
	}
	return width
}
