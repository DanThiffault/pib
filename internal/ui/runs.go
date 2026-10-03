package ui

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
	"pib/internal/tmux"
)

// killResultMsg reports how killing a run's window went.
type killResultMsg struct {
	run string
	err error
}

// windowResultMsg reports a failure to reach a run's window. A success says
// nothing: the user is looking at the window, which is the whole reply.
type windowResultMsg struct{ err error }

// The tmux calls behind kill and window, named so a test can stand in for a
// server that is not there.
var (
	killWindow   = tmux.Kill
	selectWindow = tmux.Select
)

// killRunCmd closes the window a run is in. The runner watching that window
// records the run's end, and the store event it publishes is what refreshes
// the tables.
func killRunCmd(store *issues.Store, runID string, issue int64) tea.Cmd {
	return func() tea.Msg {
		window, err := runWindow(store, runID, issue)
		if err != nil {
			return killResultMsg{run: runID, err: err}
		}
		if err := killWindow(window); err != nil {
			return killResultMsg{run: runID, err: err}
		}
		return killResultMsg{run: runID}
	}
}

// selectRunWindowCmd makes the window a run is in the current one.
func selectRunWindowCmd(store *issues.Store, runID string, issue int64) tea.Cmd {
	return func() tea.Msg {
		window, err := runWindow(store, runID, issue)
		if err != nil {
			return windowResultMsg{err: err}
		}
		return windowResultMsg{err: selectWindow(window)}
	}
}

// runWindow finds the tmux window a run is in. A run on an issue is found
// through the issue's history; a run with no issue — a planning row's
// planner — is looked up by id.
func runWindow(store *issues.Store, runID string, issue int64) (string, error) {
	if store == nil {
		return "", errors.New("no store")
	}
	if runID == "" {
		return "", errors.New("no run to reach")
	}
	if issue != 0 {
		runs, err := store.Runs(issue)
		if err != nil {
			return "", err
		}
		for _, run := range runs {
			if run.ID == runID {
				return run.Window, nil
			}
		}
		return "", fmt.Errorf("run %q is not on issue #%d", runID, issue)
	}
	run, err := store.Run(runID)
	if err != nil {
		return "", err
	}
	return run.Window, nil
}
