package issues

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrNotFound reports a plan or issue that is not in the store.
var ErrNotFound = errors.New("not found")

// ErrExists reports a plan slug that is already taken.
var ErrExists = errors.New("already exists")

// now is the clock, replaced in tests.
var now = func() time.Time { return time.Now().UTC() }

// Plan is one planning run: what is being built, and the issues that build
// it. The goal, the scope and the feature-level acceptance criteria live
// here, in a markdown file of the plan's own — not in a container issue that
// would never close.
type Plan struct {
	ID    int64  `json:"-"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// Path is the markdown file, relative to the store's data directory.
	Path       string    `json:"path,omitempty"`
	Acceptance []string  `json:"acceptance,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	PlannerRun string    `json:"plannerRun,omitempty"`
}

// NewPlan describes a plan to create.
type NewPlan struct {
	Slug       string
	Title      string
	Body       string
	Acceptance []string
	PlannerRun string
}

// planColumns is the select list every plan scan expects.
const planColumns = `id, slug, title, path, acceptance, created_at, planner_run`

// CreatePlan records a new plan and writes its markdown file.
func (s *Store) CreatePlan(n NewPlan) (Plan, error) {
	if n.Slug == "" {
		return Plan{}, errors.New("a plan needs a slug")
	}
	if n.Title == "" {
		return Plan{}, errors.New("a plan needs a title")
	}

	acceptance, err := encodeList(n.Acceptance)
	if err != nil {
		return Plan{}, err
	}

	rel := filepath.Join(PlansDirName, n.Slug+".md")
	created := format(now())

	res, err := s.db.Exec(`
		INSERT INTO plans (slug, title, path, acceptance, indexed_mtime, indexed_size, created_at, planner_run)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?)`,
		n.Slug, n.Title, rel, acceptance, created, nullable(n.PlannerRun))
	if err != nil {
		if isUnique(err) {
			return Plan{}, fmt.Errorf("plan %q %w", n.Slug, ErrExists)
		}
		return Plan{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Plan{}, err
	}

	file := File{Title: n.Title, Type: "plan", Acceptance: n.Acceptance, Body: n.Body}
	if err := WriteFile(s.abs(rel), file); err != nil {
		return Plan{}, err
	}
	if err := s.indexPlan(id, rel); err != nil {
		return Plan{}, err
	}

	return s.Plan(n.Slug)
}

// indexPlan records the state of a plan's file on disk.
func (s *Store) indexPlan(id int64, rel string) error {
	info, err := os.Stat(s.abs(rel))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE plans SET path = ?, indexed_mtime = ?, indexed_size = ? WHERE id = ?`,
		rel, info.ModTime().UnixNano(), info.Size(), id)
	return err
}

// refreshPlan re-reads a plan's file when it no longer matches the index, so
// a hand-edited goal shows up without anyone asking.
func (s *Store) refreshPlan(id int64) error {
	var (
		rel   string
		mtime int64
		size  int64
	)
	err := s.db.QueryRow(
		`SELECT path, indexed_mtime, indexed_size FROM plans WHERE id = ?`, id).
		Scan(&rel, &mtime, &size)
	if err != nil || rel == "" {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	info, err := os.Stat(s.abs(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.ModTime().UnixNano() == mtime && info.Size() == size {
		return nil
	}

	file, err := ReadFile(s.abs(rel))
	if err != nil {
		return err
	}
	acceptance, err := encodeList(file.Acceptance)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		UPDATE plans SET title = ?, acceptance = ?, indexed_mtime = ?, indexed_size = ?
		WHERE id = ?`,
		file.Title, acceptance, info.ModTime().UnixNano(), info.Size(), id)
	return err
}

// PlanContent is a plan's parsed markdown file: its goal, its scope, and the
// criteria the whole plan is judged on.
func (s *Store) PlanContent(slug string) (File, error) {
	plan, err := s.Plan(slug)
	if err != nil {
		return File{}, err
	}
	if plan.Path == "" {
		return File{}, nil
	}
	return ReadFile(s.abs(plan.Path))
}

// Plan looks a plan up by slug.
func (s *Store) Plan(slug string) (Plan, error) {
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM plans WHERE slug = ?`, slug).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Plan{}, fmt.Errorf("plan %q: %w", slug, ErrNotFound)
		}
		return Plan{}, err
	}
	if err := s.refreshPlan(id); err != nil {
		return Plan{}, err
	}

	return scanPlan(s.db.QueryRow(`SELECT `+planColumns+` FROM plans WHERE id = ?`, id))
}

// PlanCounts holds aggregated issue counts for a plan.
type PlanCounts struct {
	Total  int
	Open   int
	Closed int
}

// IssueCountsByPlan returns the number of total, open and closed issues
// grouped by plan slug.
func (s *Store) IssueCountsByPlan() (map[string]PlanCounts, error) {
	rows, err := s.db.Query(`
		SELECT p.slug, COUNT(i.number) as total,
			COUNT(CASE WHEN i.state = 'open' THEN 1 END) as open_count,
			COUNT(CASE WHEN i.state = 'closed' THEN 1 END) as closed_count
		FROM plans p
		LEFT JOIN issues i ON i.plan_id = p.id
		GROUP BY p.slug
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]PlanCounts)
	for rows.Next() {
		var slug string
		var total, open, closed int
		if err := rows.Scan(&slug, &total, &open, &closed); err != nil {
			return nil, err
		}
		counts[slug] = PlanCounts{Total: total, Open: open, Closed: closed}
	}
	return counts, rows.Err()
}

// Plans lists every plan, newest first.
func (s *Store) Plans() ([]Plan, error) {
	if err := s.refreshPlans(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT ` + planColumns + ` FROM plans ORDER BY id DESC`)
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

// refreshPlans re-reads every plan file that has changed on disk.
func (s *Store) refreshPlans() error {
	rows, err := s.db.Query(`SELECT id FROM plans`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		if err := s.refreshPlan(id); err != nil {
			return err
		}
	}
	return nil
}

func scanPlan(row scanner) (Plan, error) {
	var (
		plan       Plan
		path       sql.NullString
		acceptance sql.NullString
		created    string
		run        sql.NullString
	)
	if err := row.Scan(&plan.ID, &plan.Slug, &plan.Title, &path, &acceptance, &created, &run); err != nil {
		return Plan{}, err
	}
	plan.Path = path.String
	plan.CreatedAt = parseTime(created)
	plan.PlannerRun = run.String

	if acceptance.Valid && acceptance.String != "" {
		if err := json.Unmarshal([]byte(acceptance.String), &plan.Acceptance); err != nil {
			return Plan{}, fmt.Errorf("plan %q: reading acceptance criteria: %w", plan.Slug, err)
		}
	}
	return plan, nil
}
