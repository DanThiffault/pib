package issues

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound reports a plan or issue that is not in the store.
var ErrNotFound = errors.New("not found")

// ErrExists reports a plan slug that is already taken.
var ErrExists = errors.New("already exists")

// now is the clock, replaced in tests.
var now = func() time.Time { return time.Now().UTC() }

// Plan is one planning run: a feature and the issues that implement it.
type Plan struct {
	ID         int64     `json:"-"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	CreatedAt  time.Time `json:"createdAt"`
	PlannerRun string    `json:"plannerRun,omitempty"`
}

// CreatePlan records a new plan. The slug is how everything else refers to it.
func (s *Store) CreatePlan(slug, title, plannerRun string) (Plan, error) {
	if slug == "" {
		return Plan{}, errors.New("a plan needs a slug")
	}
	if title == "" {
		return Plan{}, errors.New("a plan needs a title")
	}

	created := now()
	res, err := s.db.Exec(
		`INSERT INTO plans (slug, title, created_at, planner_run) VALUES (?, ?, ?, ?)`,
		slug, title, format(created), nullable(plannerRun))
	if err != nil {
		if isUnique(err) {
			return Plan{}, fmt.Errorf("plan %q %w", slug, ErrExists)
		}
		return Plan{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Plan{}, err
	}
	return Plan{ID: id, Slug: slug, Title: title, CreatedAt: created, PlannerRun: plannerRun}, nil
}

// Plan looks a plan up by slug.
func (s *Store) Plan(slug string) (Plan, error) {
	row := s.db.QueryRow(
		`SELECT id, slug, title, created_at, planner_run FROM plans WHERE slug = ?`, slug)

	plan, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, fmt.Errorf("plan %q: %w", slug, ErrNotFound)
	}
	return plan, err
}

// Plans lists every plan, newest first.
func (s *Store) Plans() ([]Plan, error) {
	rows, err := s.db.Query(
		`SELECT id, slug, title, created_at, planner_run FROM plans ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []Plan
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanPlan(row scanner) (Plan, error) {
	var (
		plan    Plan
		created string
		run     sql.NullString
	)
	if err := row.Scan(&plan.ID, &plan.Slug, &plan.Title, &created, &run); err != nil {
		return Plan{}, err
	}
	plan.CreatedAt = parseTime(created)
	plan.PlannerRun = run.String
	return plan, nil
}
