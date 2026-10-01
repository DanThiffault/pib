package issues

import (
	"time"
)

// PlanState is where a plan is in its lifecycle. It is derived from the
// plan's issues and runs, never stored — except archived, which is a column
// because the user puts a plan there by hand.
type PlanState string

// The plan states, as ADR-005 names them.
const (
	// PlanPlanning is a planner run that has not applied its plan yet: a
	// placeholder row, so the run is reachable before the plan exists.
	PlanPlanning PlanState = "planning"
	// PlanAwaitingReview is a plan whose opening review issue is open with
	// no reviewer running.
	PlanAwaitingReview PlanState = "awaiting_review"
	// PlanUnderReview is a plan with a plan-reviewer run still going.
	PlanUnderReview PlanState = "under_review"
	// PlanInProgress is a plan past its opening review — or never gated —
	// with work still open.
	PlanInProgress PlanState = "in_progress"
	// PlanAwaitingClosingReview is a plan whose issues have all closed,
	// waiting on the closing review to settle whether it did what it set
	// out to do.
	PlanAwaitingClosingReview PlanState = "awaiting_closing_review"
	// PlanComplete is a plan whose closing review passed, or that never
	// asked for one.
	PlanComplete PlanState = "complete"
	// PlanArchived is a plan the user shelved by hand. It overrides every
	// other reading.
	PlanArchived PlanState = "archived"
)

// The agents whose runs the derivation looks for. A reviewer run with no
// issue is traced to its plan by this name and runs.plan together; a run on
// the plan's reviewer issue is found through the issue, whatever its agent.
// The reviewer agent shares the review issue type's name.
const (
	plannerAgent  = "planner"
	reviewerAgent = ReviewType
)

// PlanStatus is a plan with the state pib derives for it and the counts the
// plan table shows — or a planning placeholder: a planner run that has not
// applied its plan yet.
type PlanStatus struct {
	Plan
	State PlanState `json:"state"`

	// Ready, Running and NeedsAttention count the plan's open issues in
	// those states, taken off the issue derivation itself so the plan
	// table and the issue table can never disagree.
	Ready          int `json:"ready"`
	Running        int `json:"running"`
	NeedsAttention int `json:"needsAttention"`

	// Run is the planner run behind a planning row, empty for a real plan.
	Run string `json:"run,omitempty"`
}

// PlanStatusOptions supplies what the store cannot work out on its own.
type PlanStatusOptions struct {
	// ReviewCycles is how many review passes an issue gets before it needs
	// attention. Zero or less means DefaultReviewCycles.
	ReviewCycles int
	// PlanReview is the [plan] review setting. When it is false nothing
	// will run a closing pass, so a plan whose issues have all closed is
	// complete rather than awaiting one.
	PlanReview bool
}

// planFacts is what a plan's issues say about it, gathered in one pass.
type planFacts struct {
	open           int // open issues other than the review gate
	ready          int
	running        int
	needsAttention int
	reviewerOpen   bool      // the opening review issue is still open
	lastClosed     time.Time // newest close among non-reviewer issues
}

// PlanStatuses lists every plan with its derived state, newest first.
// Ahead of them come the planning rows: one per planner run still going
// that has not applied a plan, newest first. Archived plans are hidden
// unless includeArchived asks for them.
func (s *Store) PlanStatuses(includeArchived bool, opts PlanStatusOptions) ([]PlanStatus, error) {
	if err := s.refreshPlans(); err != nil {
		return nil, err
	}

	// The counts come off the issue derivation itself: one definition of
	// ready, running and needs attention, read two ways.
	list, err := s.Statuses(Filter{}, StatusOptions{ReviewCycles: opts.ReviewCycles})
	if err != nil {
		return nil, err
	}

	facts := map[string]*planFacts{}
	reviewers := map[int64]string{} // reviewer issue number → its plan
	for _, issue := range list {
		f := facts[issue.Plan]
		if f == nil {
			f = &planFacts{}
			facts[issue.Plan] = f
		}
		if issue.Type == ReviewType {
			reviewers[issue.Number] = issue.Plan
			if issue.State == StateOpen {
				f.reviewerOpen = true
			}
			continue
		}
		if issue.State != StateOpen {
			if issue.ClosedAt.After(f.lastClosed) {
				f.lastClosed = issue.ClosedAt
			}
			continue
		}
		f.open++
		if issue.Ready {
			f.ready++
		}
		if issue.InProgress {
			f.running++
		}
		if issue.NeedsAttention {
			f.needsAttention++
		}
	}

	// Live runs: each unlinked planner is a planning row, and a reviewer
	// still going puts its plan under review.
	underReview := map[string]bool{}
	var planning []PlanStatus
	runs, err := s.db.Query(`
		SELECT id, agent, COALESCE(issue, 0), COALESCE(plan, ''), started_at
		FROM runs WHERE ended_at IS NULL ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer runs.Close()
	for runs.Next() {
		var (
			id, agent, plan, started string
			issue                    int64
		)
		if err := runs.Scan(&id, &agent, &issue, &plan, &started); err != nil {
			return nil, err
		}
		if agent == plannerAgent && plan == "" {
			planning = append(planning, PlanStatus{
				Plan:  Plan{Title: id, CreatedAt: parseTime(started)},
				State: PlanPlanning,
				Run:   id,
			})
			continue
		}
		if agent == reviewerAgent && plan != "" {
			underReview[plan] = true
		}
		if slug, ok := reviewers[issue]; ok {
			underReview[slug] = true
		}
	}
	if err := runs.Err(); err != nil {
		return nil, err
	}

	// The closing pass that settles a plan: the newest one that ended done,
	// per plan.
	closingDone := map[string]time.Time{}
	closing, err := s.db.Query(`
		SELECT plan, MAX(ended_at) FROM runs
		WHERE pass = ? AND status = 'done' AND plan IS NOT NULL
		GROUP BY plan`, PassClosing)
	if err != nil {
		return nil, err
	}
	defer closing.Close()
	for closing.Next() {
		var plan, ended string
		if err := closing.Scan(&plan, &ended); err != nil {
			return nil, err
		}
		closingDone[plan] = parseTime(ended)
	}
	if err := closing.Err(); err != nil {
		return nil, err
	}

	query := `SELECT ` + planColumns + ` FROM plans`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	rows, err := s.db.Query(query + ` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []PlanStatus
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		f := facts[plan.Slug]
		if f == nil {
			f = &planFacts{}
		}
		status := PlanStatus{
			Plan:           plan,
			Ready:          f.ready,
			Running:        f.running,
			NeedsAttention: f.needsAttention,
		}
		status.State = plan.state(f, underReview[plan.Slug], closingDone[plan.Slug], opts)
		plans = append(plans, status)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return append(planning, plans...), nil
}

// state reads one plan off what the listing gathered. The order is the
// precedence: archived overrides everything, a live reviewer outranks the
// gate it is working on, and open work outranks the wait for the closing
// pass.
func (p Plan) state(f *planFacts, underReview bool, closingDone time.Time, opts PlanStatusOptions) PlanState {
	if !p.ArchivedAt.IsZero() {
		return PlanArchived
	}
	if underReview {
		return PlanUnderReview
	}
	if f.reviewerOpen {
		return PlanAwaitingReview
	}
	if f.open > 0 {
		return PlanInProgress
	}
	// Nothing is open. The closing pass settles the plan: one that ended
	// done at or after the last close counts, with the tie going to the
	// run as the newer of the two events — unless the plan never asked
	// for a review at all.
	if !closingDone.IsZero() && !closingDone.Before(f.lastClosed) {
		return PlanComplete
	}
	if !opts.PlanReview {
		return PlanComplete
	}
	return PlanAwaitingClosingReview
}
