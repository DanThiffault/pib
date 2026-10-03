package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
	"pib/internal/triage"
)

// outOfScopeInterval is how often the interface asks the collector to read
// the workspace's open pull requests again. Each pass is one GraphQL call per
// open pull request, so this is deliberately far slower than the store events
// that keep the tables current, and is the same window reconciliation trusts
// a pull request's own state for.
//
// It is a variable so a test can run the arming pattern in milliseconds
// rather than in the half hour it takes to notice a chain that was armed
// twice.
var outOfScopeInterval = issues.DefaultPRWindow

// outOfScopeTickMsg drives the periodic scan for out-of-scope findings. It is
// the one refresh path that talks to GitHub, and no store event covers it, so
// it keeps its timer now that the tables have none.
type outOfScopeTickMsg time.Time

func outOfScopeTick() tea.Cmd {
	return tea.Tick(outOfScopeInterval, func(t time.Time) tea.Msg { return outOfScopeTickMsg(t) })
}

// outOfScopeCollectedMsg reports that a scan of the open pull requests has
// run, or that there was nothing to scan. It says nothing: the collector is
// what a render reads, and its contents are the whole result.
type outOfScopeCollectedMsg struct{}

// collectOutOfScope hands the workspace's open pull requests to the shared
// triage collector, which reads their threads and keeps what it finds for the
// interface to render.
//
// The interface may not read GitHub itself, so this is where the threads come
// from. It names the pull requests the store already believes are open rather
// than reconciling them: settling a pull request — and closing the issue with
// it, and firing every hook that follows — is not something a screen refresh
// should do behind the user's back. Collect returns immediately; the reads
// and any agent they start run off the tick's path.
func collectOutOfScope(store *issues.Store, collector *triage.Collector, plan string) tea.Cmd {
	return func() tea.Msg {
		if store == nil || collector == nil {
			return outOfScopeCollectedMsg{}
		}
		prs, err := store.OpenPullRequests(plan)
		if err != nil {
			return outOfScopeCollectedMsg{}
		}
		collector.Collect(prs)
		return outOfScopeCollectedMsg{}
	}
}

// triagePlan is the plan the scan reads pull requests for: the one on screen,
// or every plan's while the plans table is.
func (m Model) triagePlan() string {
	if m.screen == screenIssues || m.screen == screenIssue {
		return m.drilled
	}
	return ""
}
