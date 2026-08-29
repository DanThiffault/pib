package issues

import (
	"database/sql"
	"errors"
	"time"
)

// Run is one agent run pib started. Runs are never deleted, so an issue
// carries every attempt made at it — the worker that failed as well as the
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

// StartRun records an agent starting work, and is what makes an issue read
// as in progress. Resuming an agent reuses its id, so the same row is picked
// back up rather than a second one being written.
func (s *Store) StartRun(id string, issue int64, agent, window string) error {
	if id == "" {
		return errors.New("a run needs an id")
	}
	if agent == "" {
		return errors.New("a run needs an agent")
	}

	_, err := s.db.Exec(`
		INSERT INTO runs (id, issue, agent, tmux_window, started_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			started_at  = excluded.started_at,
			tmux_window = excluded.tmux_window,
			ended_at    = NULL,
			status      = NULL,
			issue       = COALESCE(excluded.issue, runs.issue)`,
		id, nullableID(issue), agent, nullable(window), format(now()))
	return wrapRef(err)
}

// FinishRun records how a run ended, which releases the issue it held.
func (s *Store) FinishRun(id, status string) error {
	if !runStatuses[status] {
		status = "unknown"
	}
	_, err := s.db.Exec(
		`UPDATE runs SET ended_at = ?, status = ? WHERE id = ?`, format(now()), status, id)
	return err
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
func (s *Store) closeOrphanRuns() (int, error) {
	res, err := s.db.Exec(
		`UPDATE runs SET ended_at = ?, status = 'unknown' WHERE ended_at IS NULL`, format(now()))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}
