package ui

import (
	"pib/internal/issues"
	"pib/internal/ui/command"
)

// planRow adapts a plan's derived status to the registry's Row. The
// predicates live in the registry; this is the handful of facts they read.
type planRow struct {
	status issues.PlanStatus
}

func (r planRow) Kind() command.Kind { return command.KindPlan }

func (r planRow) State() command.State {
	switch r.status.State {
	case issues.PlanPlanning:
		return command.StatePlanning
	case issues.PlanArchived:
		return command.StateArchived
	case issues.PlanComplete:
		return command.StateComplete
	case issues.PlanInProgress, issues.PlanUnderReview:
		return command.StateInProgress
	case issues.PlanAwaitingReview, issues.PlanAwaitingClosingReview:
		return command.StateAwaitingReview
	default:
		return command.StateOpen
	}
}

// Launchable reports the plan holds work that could start now. The store
// counts ready issues; a ready issue of an unmapped type is the rare case
// the count cannot see, and start on such a plan simply starts nothing.
func (r planRow) Launchable() bool { return r.status.Ready > 0 }

func (r planRow) NeedsAttention() bool { return r.status.NeedsAttention > 0 }

func (r planRow) NeedsInput() bool { return false }

// Reviewable follows ADR-005: review is offered while a plan awaits its
// opening review, is in progress, or awaits its closing review — not while
// a reviewer is already running.
func (r planRow) Reviewable() bool {
	switch r.status.State {
	case issues.PlanAwaitingReview, issues.PlanInProgress, issues.PlanAwaitingClosingReview:
		return true
	default:
		return false
	}
}

// HasRun reports the planner run behind a planning row. A reviewer run
// belongs to the plan too, but the listing does not carry its id, so only
// the planning row can offer kill and window.
func (r planRow) HasRun() bool { return r.status.Run != "" }

func (r planRow) HasPR() bool { return false }

// issueRow adapts an issue's derived status to the registry's Row.
type issueRow struct {
	status issues.Status
}

func (r issueRow) Kind() command.Kind { return command.KindIssue }

func (r issueRow) State() command.State {
	switch {
	case r.status.State == issues.StateClosed:
		return command.StateClosed
	case r.status.InProgress:
		return command.StateInProgress
	case r.status.AwaitingReview:
		return command.StateAwaitingReview
	case r.status.Blocked:
		return command.StateBlocked
	default:
		return command.StateOpen
	}
}

func (r issueRow) Launchable() bool { return r.status.Launchable }

func (r issueRow) NeedsAttention() bool { return r.status.NeedsAttention }

// NeedsInput is the needs-attention reason that is a question: the agent
// stopped to ask, and answer resumes it.
func (r issueRow) NeedsInput() bool {
	return r.status.NeedsAttention && r.status.AttentionReason == issues.AttentionAsked
}

func (r issueRow) Reviewable() bool { return false }

func (r issueRow) HasRun() bool { return r.status.Run != "" }

func (r issueRow) HasPR() bool { return r.status.PRURL != "" }

// currentRow is the row the commands act on: the selected row of the table
// on screen, or the settings screen's own row. An empty table has no row —
// nil, which only the rowless commands (new, settings, the filters) apply
// to.
func (m Model) currentRow() command.Row {
	switch m.screen {
	case screenPlans:
		if plan, ok := m.selectedPlan(); ok {
			return planRow{status: plan}
		}
	case screenIssues, screenIssue:
		if issue, ok := m.selectedIssue(); ok {
			return issueRow{status: issue}
		}
	case screenSettings:
		return settingsRow{}
	}
	return nil
}

// settingsRow is the settings screen's row. Nothing on it is a plan or an
// issue, so only the rowless commands — new, settings, the filters — and
// whatever binds the settings kind apply.
type settingsRow struct{}

func (settingsRow) Kind() command.Kind   { return command.KindSettings }
func (settingsRow) State() command.State { return command.StateOpen }
func (settingsRow) Launchable() bool     { return false }
func (settingsRow) NeedsAttention() bool { return false }
func (settingsRow) NeedsInput() bool     { return false }
func (settingsRow) Reviewable() bool     { return false }
func (settingsRow) HasRun() bool         { return false }
func (settingsRow) HasPR() bool          { return false }
