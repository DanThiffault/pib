package issues

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Run is one agent run pib started. Runs are never deleted, so an issue
// carries every attempt made at it — the coder that failed as well as the
// one that opened the pull request.
type Run struct {
	ID        string    `json:"id"`
	Issue     int64     `json:"issue,omitempty"`
	Agent     string    `json:"agent"`
	Window    string    `json:"window,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
	Status    string    `json:"status,omitempty"`
}

// runStatuses are the outcomes the schema allows. Anything else is recorded
// as unknown rather than rejected: a run that ended oddly still ended.
var runStatuses = map[string]bool{"done": true, "needs_input": true, "error": true, "unknown": true}

// The passes a plan-reviewer run makes over a plan: the opening review,
// before any of it is worked, and the closing review, after the last issue
// closes.
const (
	PassOpening = "opening"
	PassClosing = "closing"
)

// RunStart describes an agent run being recorded.
type RunStart struct {
	ID     string
	Issue  int64
	Agent  string
	Window string
	// Plan is the plan the run works on behalf of. A run on an issue gets
	// the issue's plan — the store fills it, callers need not. A run with
	// no issue — the closing-pass reviewer, `pib plan review` — is traced
	// to its plan by this, or not at all.
	Plan string
	// Pass says whether a plan-reviewer run is the opening or the closing
	// review. The store fills it for a run on the plan's reviewer issue
	// (opening); a reviewer run with no issue must say which it is.
	Pass string
}

// StartRun records an agent starting work, and is what makes an issue read
// as in progress. Resuming an agent reuses its id, so the same row is picked
// back up rather than a second one being written.
func (s *Store) StartRun(start RunStart) error {
	if start.ID == "" {
		return errors.New("a run needs an id")
	}
	if start.Agent == "" {
		return errors.New("a run needs an agent")
	}
	if start.Pass != "" && start.Pass != PassOpening && start.Pass != PassClosing {
		return fmt.Errorf("a run's pass is %q or %q, not %q", PassOpening, PassClosing, start.Pass)
	}

	plan, pass := start.Plan, start.Pass
	if start.Issue != 0 {
		// The issue decides: its plan is the run's plan whatever the caller
		// said, and a run on the plan's reviewer issue is the opening pass.
		// The lookup doubles as the existence check the foreign key gave.
		var issueType string
		err := s.db.QueryRow(`
			SELECT p.slug, i.type FROM issues i JOIN plans p ON p.id = i.plan_id
			WHERE i.number = ?`, start.Issue).Scan(&plan, &issueType)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("issue #%d: %w", start.Issue, ErrNotFound)
			}
			return err
		}
		if pass == "" && issueType == ReviewType {
			pass = PassOpening
		}
	}

	_, err := s.db.Exec(`
		INSERT INTO runs (id, issue, agent, tmux_window, started_at, plan, pass)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			started_at  = excluded.started_at,
			tmux_window = excluded.tmux_window,
			ended_at    = NULL,
			status      = NULL,
			issue       = COALESCE(excluded.issue, runs.issue),
			plan        = COALESCE(excluded.plan, runs.plan),
			pass        = COALESCE(excluded.pass, runs.pass)`,
		start.ID, nullableID(start.Issue), start.Agent, nullable(start.Window),
		format(now()), nullable(plan), nullable(pass))
	if err != nil {
		return wrapRef(err)
	}
	s.publishIssue(EventRun, start.Issue)
	return nil
}

// FinishRun records how a run ended, which releases the issue it held.
func (s *Store) FinishRun(id, status string) error {
	if !runStatuses[status] {
		status = "unknown"
	}
	_, err := s.db.Exec(
		`UPDATE runs SET ended_at = ?, status = ? WHERE id = ?`, s.runEnded(id), status, id)
	if err != nil {
		return err
	}
	// The run's issue is read back rather than carried in, because the
	// recorder that ends a run knows the run id and nothing else. A run with
	// no issue — a planner, the closing-pass reviewer — reads back as zero,
	// which is what StartRun published for it. The read-back is a lookup,
	// not a precondition: the write has landed, so it is published either
	// way, and a run pib has never heard of ends the same way.
	var issue int64
	_ = s.db.QueryRow(`SELECT COALESCE(issue, 0) FROM runs WHERE id = ?`, id).Scan(&issue)
	s.publishIssue(EventRun, issue)
	return nil
}

// runEnded reports when a run should be said to have ended.
//
// Timestamps are recorded to the second, and whether a run needs attention
// turns on ending after the last write to its issue. A coder that links its
// pull request and is killed in the same second would otherwise leave the
// issue looking untouched since the run. The tie is broken towards the run,
// which is the newer of the two events: a run ending at the same moment as
// the issue was last written is recorded a second later, so "something
// happened since" stays true.
func (s *Store) runEnded(id string) string {
	// Truncated to the second, because that is what a recorded timestamp
	// holds: an untruncated now() reads as later than itself once written.
	ended := now().Truncate(time.Second)
	var updated string
	if err := s.db.QueryRow(`
		SELECT i.updated_at FROM runs r JOIN issues i ON i.number = r.issue
		WHERE r.id = ?`, id).Scan(&updated); err == nil {
		if last := parseTime(updated); !ended.After(last) {
			ended = last.Add(time.Second)
		}
	}
	return format(ended)
}

// Run looks a run up by id. It is how a screen that knows a run and
// nothing else — a planning row's planner has no issue — finds the window
// the run is in.
func (s *Store) Run(id string) (Run, error) {
	var (
		run     Run
		number  sql.NullInt64
		window  sql.NullString
		started string
		ended   sql.NullString
		status  sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT id, issue, agent, tmux_window, started_at, ended_at, status
		FROM runs WHERE id = ?`, id).
		Scan(&run.ID, &number, &run.Agent, &window, &started, &ended, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("run %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return Run{}, err
	}
	run.Issue = number.Int64
	run.Window = window.String
	run.StartedAt = parseTime(started)
	run.EndedAt = parseTime(ended.String)
	run.Status = status.String
	return run, nil
}

// RunAgent names the agent a run belongs to. A run pib has never heard of
// is not an error — the caller falls back to what it knows.
func (s *Store) RunAgent(id string) (string, error) {
	var agent string
	err := s.db.QueryRow(`SELECT agent FROM runs WHERE id = ?`, id).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return agent, err
}

// LatestRun is the most recent attempt at an issue, and the session a
// followup continues.
func (s *Store) LatestRun(issue int64) (Run, bool, error) {
	runs, err := s.Runs(issue)
	if err != nil || len(runs) == 0 {
		return Run{}, false, err
	}
	return runs[len(runs)-1], true, nil
}

// Runs lists the attempts made at an issue, oldest first.
func (s *Store) Runs(issue int64) ([]Run, error) {
	rows, err := s.db.Query(`
		SELECT id, issue, agent, tmux_window, started_at, ended_at, status
		FROM runs WHERE issue = ? ORDER BY started_at, id`, issue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Run
	for rows.Next() {
		var (
			run     Run
			number  sql.NullInt64
			window  sql.NullString
			started string
			ended   sql.NullString
			status  sql.NullString
		)
		if err := rows.Scan(&run.ID, &number, &run.Agent, &window, &started, &ended, &status); err != nil {
			return nil, err
		}
		run.Issue = number.Int64
		run.Window = window.String
		run.StartedAt = parseTime(started)
		run.EndedAt = parseTime(ended.String)
		run.Status = status.String
		list = append(list, run)
	}
	return list, rows.Err()
}

// closeOrphanRuns ends runs left open by a process that is no longer here.
// Opening the store means taking ownership of it, so nothing else can still
// be working — without this, a pib that crashed would leave its issues stuck
// in progress forever.
//
// Every orphan is closed at the same moment, which is the same tie-break as a
// run finishing: at or after the last write to its issue, so an issue whose
// agent vanished is reported rather than left looking ready.
func (s *Store) closeOrphanRuns() (int, error) {
	res, err := s.db.Exec(`
		UPDATE runs SET
		    ended_at = MAX(?, COALESCE(
		        (SELECT i.updated_at FROM issues i WHERE i.number = runs.issue), '')),
		    status = 'unknown'
		WHERE ended_at IS NULL`, format(now()))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}
