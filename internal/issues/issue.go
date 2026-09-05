package issues

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is the only lifecycle pib stores. Blocked, ready, in progress and
// awaiting review are derived, so they cannot go stale.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Issue is one issue: its database row, with the frontmatter indexed from its
// markdown file.
type Issue struct {
	Number  int64  `json:"number"`
	PlanID  int64  `json:"-"`
	Plan    string `json:"plan"`
	LocalID string `json:"localId,omitempty"`
	Parent  int64  `json:"parent,omitempty"` // 0 when the issue has no parent
	// Path is the markdown file, relative to the store's data directory.
	Path string `json:"path"`

	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Acceptance []string `json:"acceptance,omitempty"`
	BlockedBy  []int64  `json:"blockedBy,omitempty"`

	State       State     `json:"state"`
	ClosedAt    time.Time `json:"closedAt,omitzero"`
	PRURL       string    `json:"prUrl,omitempty"`
	PRState     string    `json:"prState,omitempty"`
	PRCheckedAt time.Time `json:"prCheckedAt,omitzero"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NewIssue describes an issue to create.
type NewIssue struct {
	Plan       string // plan slug; must exist
	LocalID    string // id used by the apply document, for later matching
	Type       string
	Title      string
	Body       string
	Acceptance []string
	Parent     int64
	BlockedBy  []int64
}

// Edit describes a change. Nil fields are left alone.
type Edit struct {
	Title           *string
	Type            *string
	Body            *string
	Acceptance      *[]string
	Parent          *int64
	AddBlockedBy    []int64
	RemoveBlockedBy []int64
}

// Filter narrows a listing. Zero fields do not filter.
type Filter struct {
	Plan  string
	State State
	Type  string
}

// issueColumns is the select list every issue scan expects.
const issueColumns = `i.number, i.plan_id, p.slug, i.local_id, i.parent, i.path,
	i.title, i.type, i.acceptance, i.state, i.closed_at,
	i.pr_url, i.pr_state, i.pr_checked_at, i.created_at, i.updated_at`

// Create writes a new issue: a row, then the markdown file it points at.
// The number is allocated by the database, so the filename is only known
// once the row exists.
func (s *Store) Create(n NewIssue) (Issue, error) {
	plan, err := s.Plan(n.Plan)
	if err != nil {
		return Issue{}, err
	}
	if err := checkFields(n.Title, n.Type); err != nil {
		return Issue{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()

	number, rel, err := s.insert(tx, plan.ID, n)
	if err != nil {
		if isUnique(err) {
			return Issue{}, fmt.Errorf("issue %q in plan %q %w", n.LocalID, n.Plan, ErrExists)
		}
		return Issue{}, wrapRef(err)
	}

	if err := addDeps(tx, number, n.BlockedBy); err != nil {
		os.Remove(s.abs(rel))
		return Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		// The row is gone; the file it named must go with it.
		os.Remove(s.abs(rel))
		return Issue{}, err
	}

	return s.Issue(number)
}

// insert writes one issue row and its markdown file inside a transaction.
// It is shared by Create and by applying a plan document, which needs many
// issues to land or fail together.
//
// The file is written before the transaction commits. A rollback after that
// point leaves the file ahead of the index rather than behind it, which the
// reindex-on-read path corrects by itself.
func (s *Store) insert(tx *sql.Tx, planID int64, n NewIssue) (int64, string, error) {
	acceptance, err := encodeList(n.Acceptance)
	if err != nil {
		return 0, "", err
	}

	stamp := format(now())
	res, err := tx.Exec(`
		INSERT INTO issues (plan_id, local_id, parent, path, title, type, acceptance,
			indexed_mtime, indexed_size, state, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?, ?, 0, 0, 'open', ?, ?)`,
		planID, nullable(n.LocalID), nullableID(n.Parent),
		n.Title, n.Type, acceptance, stamp, stamp)
	if err != nil {
		return 0, "", err
	}

	number, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}

	rel := filepath.Join(IssuesDirName, FileName(number, n.Title))
	file := File{Title: n.Title, Type: n.Type, Acceptance: n.Acceptance, Body: n.Body}
	if err := WriteFile(s.abs(rel), file); err != nil {
		return 0, "", err
	}

	if err := setPath(tx, number, rel, s.abs(rel)); err != nil {
		os.Remove(s.abs(rel))
		return 0, "", err
	}
	return number, rel, nil
}

// checkFields rejects an issue that could never be valid on disk.
func checkFields(title, issueType string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("an issue needs a title")
	}
	if strings.TrimSpace(issueType) == "" {
		return errors.New("an issue needs a type")
	}
	return nil
}

// Issue loads one issue, refreshing the index first if its file has changed
// on disk since it was last read.
func (s *Store) Issue(number int64) (Issue, error) {
	if err := s.reindex(number); err != nil {
		return Issue{}, err
	}

	row := s.db.QueryRow(
		`SELECT `+issueColumns+` FROM issues i JOIN plans p ON p.id = i.plan_id WHERE i.number = ?`,
		number)

	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, fmt.Errorf("issue #%d: %w", number, ErrNotFound)
	}
	if err != nil {
		return Issue{}, err
	}

	issue.BlockedBy, err = s.blockers(number)
	return issue, err
}

// List returns the issues matching a filter, lowest number first. Every
// file is checked for outside edits first, so a hand-edited title shows up
// without anyone running reindex.
func (s *Store) List(f Filter) ([]Issue, error) {
	if err := s.reindexAll(f.Plan); err != nil {
		return nil, err
	}

	query := `SELECT ` + issueColumns + ` FROM issues i JOIN plans p ON p.id = i.plan_id WHERE 1 = 1`
	var args []any
	if f.Plan != "" {
		query += ` AND p.slug = ?`
		args = append(args, f.Plan)
	}
	if f.State != "" {
		query += ` AND i.state = ?`
		args = append(args, string(f.State))
	}
	if f.Type != "" {
		query += ` AND i.type = ?`
		args = append(args, f.Type)
	}
	query += ` ORDER BY i.number`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	all, _, err := s.depIndex()
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].BlockedBy = all[list[i].Number]
	}
	return list, nil
}

// Content is the parsed markdown file: body, acceptance criteria, comments.
func (s *Store) Content(number int64) (File, error) {
	issue, err := s.Issue(number)
	if err != nil {
		return File{}, err
	}
	return ReadFile(s.abs(issue.Path))
}

// Edit applies a change to an issue. Retitling rewrites the frontmatter but
// never renames the file: the path in the database is what resolves an issue,
// and a stale slug is only cosmetic.
func (s *Store) Edit(number int64, e Edit) (Issue, error) {
	issue, err := s.Issue(number)
	if err != nil {
		return Issue{}, err
	}

	file, err := ReadFile(s.abs(issue.Path))
	if err != nil {
		return Issue{}, err
	}

	rewrite := false
	if e.Title != nil && *e.Title != file.Title {
		if strings.TrimSpace(*e.Title) == "" {
			return Issue{}, errors.New("an issue needs a title")
		}
		file.Title, rewrite = *e.Title, true
	}
	if e.Type != nil && *e.Type != file.Type {
		if strings.TrimSpace(*e.Type) == "" {
			return Issue{}, errors.New("an issue needs a type")
		}
		file.Type, rewrite = *e.Type, true
	}
	if e.Body != nil && *e.Body != file.Body {
		file.Body, rewrite = *e.Body, true
	}
	if e.Acceptance != nil {
		file.Acceptance, rewrite = *e.Acceptance, true
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()

	if e.Parent != nil {
		if _, err := tx.Exec(`UPDATE issues SET parent = ? WHERE number = ?`, nullableID(*e.Parent), number); err != nil {
			return Issue{}, wrapRef(err)
		}
	}
	if err := addDeps(tx, number, e.AddBlockedBy); err != nil {
		return Issue{}, err
	}
	for _, blocker := range e.RemoveBlockedBy {
		if _, err := tx.Exec(`DELETE FROM deps WHERE blocked = ? AND blocker = ?`, number, blocker); err != nil {
			return Issue{}, err
		}
	}

	if rewrite {
		if err := WriteFile(s.abs(issue.Path), file); err != nil {
			return Issue{}, err
		}
		if err := setPath(tx, number, issue.Path, s.abs(issue.Path)); err != nil {
			return Issue{}, err
		}
		acceptance, err := encodeList(file.Acceptance)
		if err != nil {
			return Issue{}, err
		}
		if _, err := tx.Exec(`UPDATE issues SET title = ?, type = ?, acceptance = ? WHERE number = ?`,
			file.Title, file.Type, acceptance, number); err != nil {
			return Issue{}, err
		}
	}

	if _, err := tx.Exec(`UPDATE issues SET updated_at = ? WHERE number = ?`, format(now()), number); err != nil {
		return Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}

	return s.Issue(number)
}

// Comment appends to an issue's activity. It is a file append, so nothing
// the planner wrote is rewritten.
func (s *Store) Comment(number int64, author, body string) error {
	issue, err := s.Issue(number)
	if err != nil {
		return err
	}
	if strings.TrimSpace(author) == "" {
		return errors.New("a comment needs an author")
	}

	if err := AppendComment(s.abs(issue.Path), Comment{Author: author, At: now(), Body: body}); err != nil {
		return err
	}
	return s.touch(number)
}

// Comments reads an issue's activity.
func (s *Store) Comments(number int64) ([]Comment, error) {
	file, err := s.Content(number)
	if err != nil {
		return nil, err
	}
	return file.Comments, nil
}

// CloseIssue marks an issue closed. A reason, when given, is recorded as a
// comment so the history explains itself.
//
// Closing a task whose pull request has not merged is allowed and reported:
// pib warns rather than blocking, and the rule can harden later.
func (s *Store) CloseIssue(number int64, reason string) (Issue, []string, error) {
	issue, err := s.Issue(number)
	if err != nil {
		return Issue{}, nil, err
	}
	if issue.State == StateClosed {
		return issue, nil, nil
	}

	var warnings []string
	if issue.Type == "task" && issue.PRState != "merged" {
		warnings = append(warnings, fmt.Sprintf(
			"#%d is a task: it normally closes when its pull request merges", number))
	}

	if reason != "" {
		if err := AppendComment(s.abs(issue.Path), Comment{Author: "pib", At: now(), Body: reason}); err != nil {
			return Issue{}, nil, err
		}
	}

	stamp := format(now())
	if _, err := s.db.Exec(
		`UPDATE issues SET state = 'closed', closed_at = ?, updated_at = ? WHERE number = ?`,
		stamp, stamp, number); err != nil {
		return Issue{}, nil, err
	}

	issue, err = s.Issue(number)
	if err != nil {
		return Issue{}, nil, err
	}
	s.notifyClosed(number)
	return issue, warnings, nil
}

// ReopenIssue puts a closed issue back in play.
func (s *Store) ReopenIssue(number int64) (Issue, error) {
	if _, err := s.Issue(number); err != nil {
		return Issue{}, err
	}
	if _, err := s.db.Exec(
		`UPDATE issues SET state = 'open', closed_at = NULL, updated_at = ? WHERE number = ?`,
		format(now()), number); err != nil {
		return Issue{}, err
	}
	return s.Issue(number)
}

// LinkPR records the pull request that will close this issue. Whether it has
// merged is settled by reconciling with GitHub, not by this call.
func (s *Store) LinkPR(number int64, url string) (Issue, error) {
	if _, err := s.Issue(number); err != nil {
		return Issue{}, err
	}
	if strings.TrimSpace(url) == "" {
		return Issue{}, errors.New("a pull request link needs a url")
	}

	if _, err := s.db.Exec(
		`UPDATE issues SET pr_url = ?, pr_state = 'open', pr_checked_at = NULL, updated_at = ? WHERE number = ?`,
		url, format(now()), number); err != nil {
		return Issue{}, err
	}

	issue, err := s.Issue(number)
	if err != nil {
		return Issue{}, err
	}
	s.notifyLinked(issue)
	return issue, nil
}

// Blockers lists the issues an issue is waiting on.
func (s *Store) blockers(number int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT blocker FROM deps WHERE blocked = ? ORDER BY blocker`, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var blockers []int64
	for rows.Next() {
		var blocker int64
		if err := rows.Scan(&blocker); err != nil {
			return nil, err
		}
		blockers = append(blockers, blocker)
	}
	return blockers, rows.Err()
}

// Reindex re-reads issue files whose size or modification time no longer
// match what was indexed. Pass an empty plan for the whole store.
func (s *Store) Reindex(plan string) (int, error) {
	return s.reindexPlan(plan, true)
}

// reindexAll refreshes stale files before a listing.
func (s *Store) reindexAll(plan string) error {
	_, err := s.reindexPlan(plan, false)
	return err
}

// reindexPlan walks issues and refreshes the ones whose file has moved on.
// force re-reads every file, which is what an explicit reindex is for.
func (s *Store) reindexPlan(plan string, force bool) (int, error) {
	query := `SELECT i.number FROM issues i JOIN plans p ON p.id = i.plan_id`
	var args []any
	if plan != "" {
		query += ` WHERE p.slug = ?`
		args = append(args, plan)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return 0, err
	}
	numbers := []int64{}
	for rows.Next() {
		var number int64
		if err := rows.Scan(&number); err != nil {
			rows.Close()
			return 0, err
		}
		numbers = append(numbers, number)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	refreshed := 0
	for _, number := range numbers {
		changed, err := s.refresh(number, force)
		if err != nil {
			return refreshed, err
		}
		if changed {
			refreshed++
		}
	}
	return refreshed, nil
}

// reindex refreshes one issue if its file has changed.
func (s *Store) reindex(number int64) error {
	_, err := s.refresh(number, false)
	return err
}

// refresh re-reads an issue's file when it no longer matches the index. The
// file is the truth for everything it holds, so a hand edit wins.
//
// A file that has gone missing is left indexed as it was: a listing should
// still work, and Content reports the real error when someone asks for it.
func (s *Store) refresh(number int64, force bool) (bool, error) {
	var (
		rel   string
		mtime int64
		size  int64
	)
	err := s.db.QueryRow(
		`SELECT path, indexed_mtime, indexed_size FROM issues WHERE number = ?`, number).
		Scan(&rel, &mtime, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if rel == "" {
		return false, nil
	}

	info, err := os.Stat(s.abs(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !force && info.ModTime().UnixNano() == mtime && info.Size() == size {
		return false, nil
	}

	file, err := ReadFile(s.abs(rel))
	if err != nil {
		return false, err
	}
	acceptance, err := encodeList(file.Acceptance)
	if err != nil {
		return false, err
	}

	_, err = s.db.Exec(`
		UPDATE issues SET title = ?, type = ?, acceptance = ?, indexed_mtime = ?, indexed_size = ?
		WHERE number = ?`,
		file.Title, file.Type, acceptance, info.ModTime().UnixNano(), info.Size(), number)
	return err == nil, err
}

// touch records that an issue changed, and reindexes its file.
func (s *Store) touch(number int64) error {
	if _, err := s.db.Exec(`UPDATE issues SET updated_at = ? WHERE number = ?`, format(now()), number); err != nil {
		return err
	}
	return s.reindex(number)
}

// abs resolves a stored path against the data directory.
func (s *Store) abs(rel string) string {
	return filepath.Join(s.dir, rel)
}

// setPath records an issue's file and the state of that file on disk.
func setPath(tx *sql.Tx, number int64, rel, abs string) error {
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE issues SET path = ?, indexed_mtime = ?, indexed_size = ? WHERE number = ?`,
		rel, info.ModTime().UnixNano(), info.Size(), number)
	return err
}

// addDeps wires blocked-by edges. A duplicate edge is not an error; a cycle
// is allowed, because pib warns about those rather than refusing the write.
func addDeps(tx *sql.Tx, number int64, blockers []int64) error {
	for _, blocker := range blockers {
		if blocker == number {
			return fmt.Errorf("#%d cannot block itself", number)
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO deps (blocked, blocker) VALUES (?, ?)`, number, blocker); err != nil {
			return wrapRef(err)
		}
	}
	return nil
}

// scanIssue reads the issue columns, plus any trailing destinations the
// caller adds — a status query selects the derived flags after them.
func scanIssue(row scanner, extra ...any) (Issue, error) {
	var (
		issue      Issue
		localID    sql.NullString
		parent     sql.NullInt64
		acceptance sql.NullString
		state      string
		closedAt   sql.NullString
		prURL      sql.NullString
		prState    sql.NullString
		prChecked  sql.NullString
		created    string
		updated    string
	)

	dest := []any{&issue.Number, &issue.PlanID, &issue.Plan, &localID, &parent, &issue.Path,
		&issue.Title, &issue.Type, &acceptance, &state, &closedAt,
		&prURL, &prState, &prChecked, &created, &updated}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Issue{}, err
	}

	issue.LocalID = localID.String
	issue.Parent = parent.Int64
	issue.State = State(state)
	issue.PRURL = prURL.String
	issue.PRState = prState.String
	issue.ClosedAt = parseTime(closedAt.String)
	issue.PRCheckedAt = parseTime(prChecked.String)
	issue.CreatedAt = parseTime(created)
	issue.UpdatedAt = parseTime(updated)

	if acceptance.Valid && acceptance.String != "" {
		if err := json.Unmarshal([]byte(acceptance.String), &issue.Acceptance); err != nil {
			return Issue{}, fmt.Errorf("issue #%d: reading acceptance criteria: %w", issue.Number, err)
		}
	}

	return issue, nil
}

func encodeList(list []string) (any, error) {
	if len(list) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	return string(body), nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func format(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// wrapRef turns a foreign key rejection into something that names the cause.
func wrapRef(err error) error {
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		return fmt.Errorf("%w: it refers to an issue or plan that does not exist", err)
	}
	return err
}
