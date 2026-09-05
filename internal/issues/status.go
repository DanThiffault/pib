package issues

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Status is an issue together with the state pib works out rather than
// stores. None of these flags live in a column, so none of them can be left
// stale by a crashed agent or a label nobody cleared.
type Status struct {
	Issue

	// Blocked reports an open issue waiting on a blocker that is still open.
	Blocked bool `json:"blocked"`
	// InProgress reports an agent run that has not ended.
	InProgress bool `json:"inProgress"`
	// AwaitingReview reports a linked pull request that has not merged or
	// been closed.
	AwaitingReview bool `json:"awaitingReview"`
	// Ready reports an issue that could start now: open, unblocked, with no
	// agent on it and no pull request pending.
	Ready bool `json:"ready"`
	// Launchable reports a ready issue whose type maps to an agent. A ready
	// issue of an unmapped type has nothing to run it.
	Launchable bool `json:"launchable"`

	// Agent is what would run this issue, empty when its type maps to
	// nothing — either a container type or one nobody has mapped.
	Agent string `json:"agent,omitempty"`
	// OpenBlockers are the blockers still holding this issue up.
	OpenBlockers []int64 `json:"openBlockers,omitempty"`
	// Run is the live agent run, when there is one.
	Run string `json:"run,omitempty"`

	// ReviewCycle is the newest review cycle on the issue's current pull
	// request, or zero when no review has started on it. A replacement pull
	// request starts again at one.
	ReviewCycle int `json:"reviewCycle,omitempty"`
	// ReviewVerdict is what that cycle settled on, empty while the reviewer
	// is still working. Read it with ReviewCycle: cycle 0 is "no review
	// yet", and a cycle with no verdict is a review running now.
	ReviewVerdict string `json:"reviewVerdict,omitempty"`
}

// ReviewRunning reports a review cycle that has been opened and not settled.
func (s Status) ReviewRunning() bool { return s.ReviewCycle > 0 && s.ReviewVerdict == "" }

// StatusOptions supplies what the store cannot work out on its own.
type StatusOptions struct {
	// AgentFor reports the agent that implements an issue type, and whether
	// there is one. The store does not read pib's configuration itself; the
	// caller passes the lookup in.
	AgentFor func(issueType string) (agent string, ok bool)
}

// statusQuery derives every flag in one pass. Readiness is defined once, in
// the second common table expression, so a listing and a readiness filter
// can never disagree about what it means.
const statusQuery = `
WITH flags AS (
	SELECT i.number, i.plan_id, p.slug AS plan, i.local_id, i.parent, i.path,
	       i.title, i.type, i.acceptance, i.state, i.closed_at,
	       i.pr_url, i.pr_state, i.pr_checked_at, i.created_at, i.updated_at,
	       EXISTS (
	           SELECT 1 FROM deps d JOIN issues b ON b.number = d.blocker
	           WHERE d.blocked = i.number AND b.state = 'open'
	       ) AS blocked,
	       EXISTS (
	           SELECT 1 FROM runs r WHERE r.issue = i.number AND r.ended_at IS NULL
	       ) AS in_progress,
	       (i.pr_url IS NOT NULL AND i.pr_state = 'open') AS awaiting_review,
	       COALESCE((
	           SELECT v.cycle FROM reviews v
	           WHERE v.issue = i.number AND v.pr_url = i.pr_url
	           ORDER BY v.cycle DESC LIMIT 1
	       ), 0) AS review_cycle,
	       COALESCE((
	           SELECT v.verdict FROM reviews v
	           WHERE v.issue = i.number AND v.pr_url = i.pr_url
	           ORDER BY v.cycle DESC LIMIT 1
	       ), '') AS review_verdict
	FROM issues i JOIN plans p ON p.id = i.plan_id
), status AS (
	SELECT *, (state = 'open' AND NOT blocked AND NOT in_progress AND NOT awaiting_review) AS ready
	FROM flags
)
SELECT number, plan_id, plan, local_id, parent, path, title, type, acceptance,
       state, closed_at, pr_url, pr_state, pr_checked_at, created_at, updated_at,
       blocked, in_progress, awaiting_review, ready, review_cycle, review_verdict
FROM status
WHERE 1 = 1`

// Status derives the current state of one issue.
func (s *Store) Status(number int64, opts StatusOptions) (Status, error) {
	list, err := s.statuses(statusQuery+` AND number = ?`, []any{number}, "", opts)
	if err != nil {
		return Status{}, err
	}
	if len(list) == 0 {
		return Status{}, fmt.Errorf("issue #%d: %w", number, ErrNotFound)
	}
	return list[0], nil
}

// Statuses lists issues with their derived state, lowest number first.
func (s *Store) Statuses(f Filter, opts StatusOptions) ([]Status, error) {
	query, args := filterQuery(statusQuery, f)
	return s.statuses(query, args, f.Plan, opts)
}

// Ready lists the issues that could start now. An issue is ready when it is
// open, has no open blockers, has no agent working on it, and is not waiting
// on a pull request.
//
// Whether pib can actually launch one is a separate question: check
// Launchable, which also needs a type mapped to an agent.
func (s *Store) Ready(f Filter, opts StatusOptions) ([]Status, error) {
	f.State = StateOpen
	query, args := filterQuery(statusQuery, f)
	return s.statuses(query+` AND ready`, args, f.Plan, opts)
}

// filterQuery narrows a status query.
func filterQuery(query string, f Filter) (string, []any) {
	var args []any
	if f.Plan != "" {
		query += ` AND plan = ?`
		args = append(args, f.Plan)
	}
	if f.State != "" {
		query += ` AND state = ?`
		args = append(args, string(f.State))
	}
	if f.Type != "" {
		query += ` AND type = ?`
		args = append(args, f.Type)
	}
	return query, args
}

// statuses runs a status query and fills in what SQL cannot: the agent for
// each type, which blockers are still open, and the live run.
func (s *Store) statuses(query string, args []any, plan string, opts StatusOptions) ([]Status, error) {
	if err := s.reindexAll(plan); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(query+` ORDER BY number`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Status
	for rows.Next() {
		var status Status
		status.Issue, err = scanIssue(rows,
			&status.Blocked, &status.InProgress, &status.AwaitingReview, &status.Ready,
			&status.ReviewCycle, &status.ReviewVerdict)
		if err != nil {
			return nil, err
		}
		list = append(list, status)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}

	all, open, err := s.depIndex()
	if err != nil {
		return nil, err
	}
	live, err := s.liveRuns()
	if err != nil {
		return nil, err
	}

	for i := range list {
		number := list[i].Number
		list[i].BlockedBy = all[number]
		list[i].OpenBlockers = open[number]
		list[i].Run = live[number]

		if opts.AgentFor != nil {
			agent, ok := opts.AgentFor(list[i].Type)
			list[i].Agent = agent
			list[i].Launchable = ok && list[i].Ready
		}
	}

	return list, nil
}

// depIndex loads every dependency edge once, split into all blockers and the
// ones still open. One query beats a lookup per issue on a listing.
func (s *Store) depIndex() (all, open map[int64][]int64, err error) {
	rows, err := s.db.Query(`
		SELECT d.blocked, d.blocker, b.state
		FROM deps d JOIN issues b ON b.number = d.blocker
		ORDER BY d.blocked, d.blocker`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	all, open = map[int64][]int64{}, map[int64][]int64{}
	for rows.Next() {
		var (
			blocked, blocker int64
			state            string
		)
		if err := rows.Scan(&blocked, &blocker, &state); err != nil {
			return nil, nil, err
		}
		all[blocked] = append(all[blocked], blocker)
		if State(state) == StateOpen {
			open[blocked] = append(open[blocked], blocker)
		}
	}
	return all, open, rows.Err()
}

// liveRuns maps issues to the agent run currently working on them. A run row
// with no end is a run still going; pib closes orphans out at startup, so a
// dead process cannot hold an issue in progress forever.
func (s *Store) liveRuns() (map[int64]string, error) {
	rows, err := s.db.Query(
		`SELECT issue, id FROM runs WHERE ended_at IS NULL AND issue IS NOT NULL ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	live := map[int64]string{}
	for rows.Next() {
		var (
			issue int64
			id    string
		)
		if err := rows.Scan(&issue, &id); err != nil {
			return nil, err
		}
		live[issue] = id
	}
	return live, rows.Err()
}

// Blocked lists the open issues waiting on something, with the blockers that
// are holding them up.
func (s *Store) Blocked(f Filter, opts StatusOptions) ([]Status, error) {
	f.State = StateOpen
	list, err := s.Statuses(f, opts)
	if err != nil {
		return nil, err
	}
	return keep(list, func(status Status) bool { return status.Blocked }), nil
}

// InProgress lists the issues an agent is working on right now.
func (s *Store) InProgress(f Filter, opts StatusOptions) ([]Status, error) {
	list, err := s.Statuses(f, opts)
	if err != nil {
		return nil, err
	}
	return keep(list, func(status Status) bool { return status.InProgress }), nil
}

// AwaitingReview lists the issues whose pull request has not been settled.
func (s *Store) AwaitingReview(f Filter, opts StatusOptions) ([]Status, error) {
	list, err := s.Statuses(f, opts)
	if err != nil {
		return nil, err
	}
	return keep(list, func(status Status) bool { return status.AwaitingReview }), nil
}

// Cycles reports dependency loops, which leave everything in them unable to
// start. Applying a plan warns about these; this is how they are found again
// later, when a loop can also be closed by a later edit.
func (s *Store) Cycles(plan string) ([][]int64, error) {
	all, _, err := s.depIndex()
	if err != nil {
		return nil, err
	}

	if plan != "" {
		list, err := s.List(Filter{Plan: plan})
		if err != nil {
			return nil, err
		}
		inPlan := make(map[int64]bool, len(list))
		for _, issue := range list {
			inPlan[issue.Number] = true
		}
		for blocked := range all {
			if !inPlan[blocked] {
				delete(all, blocked)
			}
		}
	}

	var cycles [][]int64
	for len(all) > 0 {
		cycle := findCycle(all)
		if cycle == nil {
			break
		}
		cycles = append(cycles, cycle)
		// Break the loop that was just reported, so the next pass finds a
		// different one instead of the same one forever.
		delete(all, cycle[0])
	}

	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0] < cycles[j][0] })
	return cycles, nil
}

// Agent reports which agent would run an issue, and why it might not run.
// It is the question the interface asks before offering to launch anything.
func (s *Store) Agent(number int64, opts StatusOptions) (string, error) {
	status, err := s.Status(number, opts)
	if err != nil {
		return "", err
	}
	if opts.AgentFor == nil {
		return "", errors.New("no agent mapping was supplied")
	}
	if status.Agent == "" {
		return "", fmt.Errorf("no agent is mapped to type %q", status.Type)
	}
	if !status.Ready {
		return "", fmt.Errorf("#%d is not ready: %s", number, why(status))
	}
	return status.Agent, nil
}

// why explains in one phrase what is holding an issue back.
func why(status Status) string {
	switch {
	case status.State == StateClosed:
		return "it is closed"
	case status.InProgress:
		return "an agent is already working on it"
	case status.AwaitingReview:
		return "it is waiting on a pull request"
	case status.Blocked:
		return fmt.Sprintf("it is blocked by %s", render(status.OpenBlockers))
	default:
		return "unknown"
	}
}

// render lists issue numbers the way a sentence would.
func render(numbers []int64) string {
	parts := make([]string, 0, len(numbers))
	for _, number := range numbers {
		parts = append(parts, fmt.Sprintf("#%d", number))
	}
	switch len(parts) {
	case 0:
		return "nothing"
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

func keep(list []Status, match func(Status) bool) []Status {
	var out []Status
	for _, status := range list {
		if match(status) {
			out = append(out, status)
		}
	}
	return out
}
