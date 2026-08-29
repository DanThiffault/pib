package issues

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Defaults for reconciliation.
const (
	// DefaultPRWindow is how long a checked pull request is trusted before
	// pib asks about it again. Readiness is recomputed constantly; without
	// a window, every listing would shell out once per linked issue.
	DefaultPRWindow = 30 * time.Second
	// DefaultPRParallel caps concurrent lookups.
	DefaultPRParallel = 4
)

// PRLookup reports the state of a pull request: open, merged or closed.
// internal/pr implements it with the gh command line tool; the store does
// not depend on that package.
type PRLookup interface {
	State(ctx context.Context, url string) (string, error)
}

// ReconcileOptions tunes a reconciliation pass.
type ReconcileOptions struct {
	// Lookup asks GitHub. Reconcile does nothing when it is nil.
	Lookup PRLookup
	// Window is how long a recent check is trusted. Zero means the default.
	Window time.Duration
	// Parallel caps concurrent lookups. Zero means the default.
	Parallel int
	// Force checks every linked pull request, ignoring the window.
	Force bool
}

// ReconcileResult is what a pass found.
type ReconcileResult struct {
	// Checked are the issues whose pull request was looked up.
	Checked []int64 `json:"checked,omitempty"`
	// Closed are the issues whose pull request had merged.
	Closed []int64 `json:"closed,omitempty"`
	// Warnings are lookups that failed. A pull request pib could not reach
	// is left exactly as it was.
	Warnings []string `json:"warnings,omitempty"`
}

// pending is one issue with a pull request worth asking about.
type pending struct {
	number int64
	url    string
}

// Reconcile settles linked pull requests against GitHub and closes the
// issues whose pull request has merged.
//
// This is how a task issue closes now that GitHub's "Closes #N" automation
// is gone: the worker links its pull request, a human merges it, and the
// next reconciliation pass closes the issue and frees whatever it blocked.
// Only a merge closes a task, exactly as before.
//
// Failure is not fatal. A pull request pib cannot reach produces a warning
// and keeps its recorded state, so issue tracking works with gh missing or
// the network down — only automatic closure pauses.
func (s *Store) Reconcile(ctx context.Context, f Filter, opts ReconcileOptions) (ReconcileResult, error) {
	if opts.Lookup == nil {
		return ReconcileResult{}, nil
	}

	candidates, err := s.pendingPRs(f, opts)
	if err != nil {
		return ReconcileResult{}, err
	}
	if len(candidates) == 0 {
		return ReconcileResult{}, nil
	}

	states, failures := lookupAll(ctx, candidates, opts)

	var (
		result  ReconcileResult
		byError = map[string][]int64{}
		order   []string
	)

	for _, item := range candidates {
		if message, failed := failures[item.number]; failed {
			if _, seen := byError[message]; !seen {
				order = append(order, message)
			}
			byError[message] = append(byError[message], item.number)
			continue
		}

		result.Checked = append(result.Checked, item.number)
		closed, err := s.settle(item, states[item.number])
		if err != nil {
			return result, err
		}
		if closed {
			result.Closed = append(result.Closed, item.number)
		}
	}

	// One warning per distinct failure, naming the issues it hit. A missing
	// gh fails every lookup the same way; saying so once is enough.
	for _, message := range order {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("could not check the pull request for %s: %s", render(byError[message]), message))
	}

	return result, nil
}

// pendingPRs lists the open issues with an unsettled pull request that has
// not been checked inside the window.
func (s *Store) pendingPRs(f Filter, opts ReconcileOptions) ([]pending, error) {
	query := `
		SELECT i.number, i.pr_url, i.pr_checked_at
		FROM issues i JOIN plans p ON p.id = i.plan_id
		WHERE i.state = 'open' AND i.pr_url IS NOT NULL AND i.pr_state = 'open'`
	var args []any
	if f.Plan != "" {
		query += ` AND p.slug = ?`
		args = append(args, f.Plan)
	}
	query += ` ORDER BY i.number`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	window := opts.Window
	if window == 0 {
		window = DefaultPRWindow
	}
	cutoff := now().Add(-window)

	var candidates []pending
	for rows.Next() {
		var (
			item    pending
			checked stringOrNull
		)
		if err := rows.Scan(&item.number, &item.url, &checked); err != nil {
			return nil, err
		}
		if !opts.Force && checked.value != "" && parseTime(checked.value).After(cutoff) {
			continue
		}
		candidates = append(candidates, item)
	}
	return candidates, rows.Err()
}

// lookupAll asks about every candidate, a few at a time. A slow or hanging
// gh should not turn a listing into a serial wait.
func lookupAll(ctx context.Context, candidates []pending, opts ReconcileOptions) (map[int64]string, map[int64]string) {
	parallel := opts.Parallel
	if parallel <= 0 {
		parallel = DefaultPRParallel
	}
	if parallel > len(candidates) {
		parallel = len(candidates)
	}

	var (
		mu       sync.Mutex
		states   = make(map[int64]string, len(candidates))
		failures = make(map[int64]string, len(candidates))
		wg       sync.WaitGroup
		slots    = make(chan struct{}, parallel)
	)

	for _, item := range candidates {
		wg.Add(1)
		go func(item pending) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			state, err := opts.Lookup.State(ctx, item.url)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures[item.number] = err.Error()
				return
			}
			states[item.number] = state
		}(item)
	}
	wg.Wait()

	return states, failures
}

// settle records what GitHub said and closes the issue if it merged.
func (s *Store) settle(item pending, state string) (bool, error) {
	stamp := format(now())

	if state != "merged" {
		_, err := s.db.Exec(
			`UPDATE issues SET pr_state = ?, pr_checked_at = ?, updated_at = ? WHERE number = ?`,
			state, stamp, stamp, item.number)
		return false, err
	}

	issue, err := s.Issue(item.number)
	if err != nil {
		return false, err
	}
	if err := AppendComment(s.abs(issue.Path), Comment{
		Author: "pib",
		At:     now(),
		Body:   fmt.Sprintf("Closed by %s, merged.", item.url),
	}); err != nil {
		return false, err
	}

	if _, err := s.db.Exec(`
		UPDATE issues
		SET pr_state = 'merged', pr_checked_at = ?, state = 'closed', closed_at = ?, updated_at = ?
		WHERE number = ?`, stamp, stamp, stamp, item.number); err != nil {
		return false, err
	}
	return true, nil
}

// stringOrNull scans a column that may be NULL.
type stringOrNull struct{ value string }

func (s *stringOrNull) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		s.value = ""
	case string:
		s.value = v
	case []byte:
		s.value = string(v)
	default:
		return fmt.Errorf("unexpected value %T for a text column", src)
	}
	return nil
}
