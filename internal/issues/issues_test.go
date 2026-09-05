package issues

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stamp = "2026-08-29T12:00:00Z"

// open makes a store on a fresh data directory.
func open(t *testing.T) *Store {
	t.Helper()
	store, err := Open(DataDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seedPlan inserts a plan and returns its id.
func seedPlan(t *testing.T, s *Store, slug string) int64 {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO plans (slug, title, created_at) VALUES (?, ?, ?)`,
		slug, "Plan "+slug, stamp)
	if err != nil {
		t.Fatalf("inserting plan: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedIssue inserts a minimal issue and returns its number.
func seedIssue(t *testing.T, s *Store, plan int64, title string) int64 {
	t.Helper()
	res, err := s.db.Exec(`
		INSERT INTO issues (plan_id, path, title, type, indexed_mtime, indexed_size, state, created_at, updated_at)
		VALUES (?, ?, ?, 'task', 0, 0, 'open', ?, ?)`,
		plan, title+".md", title, stamp, stamp)
	if err != nil {
		t.Fatalf("inserting issue: %v", err)
	}
	number, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return number
}

func TestOpenCreatesLayout(t *testing.T) {
	dir := DataDir(t.TempDir())

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(filepath.Join(dir, DBName)); err != nil {
		t.Errorf("database not created: %v", err)
	}
	info, err := os.Stat(store.IssuesDir())
	if err != nil || !info.IsDir() {
		t.Errorf("issues directory not created: %v", err)
	}
	if got, want := store.Dir(), dir; got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}

	steps, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != len(steps) {
		t.Errorf("version = %d, want %d", version, len(steps))
	}
}

func TestOpenIsRepeatableAndKeepsData(t *testing.T) {
	dir := DataDir(t.TempDir())

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	plan := seedPlan(t, first, "orders")
	seedIssue(t, first, plan, "Implement Order Aggregate")
	before, err := first.Version()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	after, err := second.Version()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("version changed on reopen: %d then %d", before, after)
	}

	var title string
	if err := second.db.QueryRow(`SELECT title FROM issues`).Scan(&title); err != nil {
		t.Fatalf("reading the issue back: %v", err)
	}
	if title != "Implement Order Aggregate" {
		t.Errorf("title = %q after reopen", title)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	store := open(t)

	before, err := store.Version()
	if err != nil {
		t.Fatal(err)
	}
	// Running the migrator again must be a no-op rather than replaying DDL
	// that would fail on tables that already exist.
	if err := migrate(store.db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	after, err := store.Version()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("version moved from %d to %d with no new migrations", before, after)
	}

	var rows int
	if err := store.db.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("schema_version holds %d rows, want 1", rows)
	}
}

func TestSchemaHasTheExpectedTables(t *testing.T) {
	store := open(t)

	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"schema_version", "plans", "issues", "deps", "runs"} {
		if !found[name] {
			t.Errorf("table %s is missing", name)
		}
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	store := open(t)
	plan := seedPlan(t, store, "orders")
	issue := seedIssue(t, store, plan, "Real issue")

	if _, err := store.db.Exec(`INSERT INTO deps (blocked, blocker) VALUES (?, ?)`, issue, 999); err == nil {
		t.Error("a dependency on a nonexistent issue was accepted")
	}
	if _, err := store.db.Exec(`
		INSERT INTO issues (plan_id, path, title, type, indexed_mtime, indexed_size, created_at, updated_at)
		VALUES (999, 'x.md', 'Orphan', 'task', 0, 0, ?, ?)`, stamp, stamp); err == nil {
		t.Error("an issue in a nonexistent plan was accepted")
	}
}

func TestConstraintsRejectNonsense(t *testing.T) {
	store := open(t)
	plan := seedPlan(t, store, "orders")
	issue := seedIssue(t, store, plan, "Real issue")

	if _, err := store.db.Exec(`UPDATE issues SET state = 'in-progress' WHERE number = ?`, issue); err == nil {
		t.Error("a state outside open/closed was accepted; derived states must not be storable")
	}
	if _, err := store.db.Exec(`INSERT INTO deps (blocked, blocker) VALUES (?, ?)`, issue, issue); err == nil {
		t.Error("an issue blocking itself was accepted")
	}
	if _, err := store.db.Exec(`
		INSERT INTO runs (id, issue, agent, started_at, status) VALUES ('r1', ?, 'coder', ?, 'finished')`,
		issue, stamp); err == nil {
		t.Error("an unknown run status was accepted")
	}
	if _, err := seedPlanErr(store, "orders"); err == nil {
		t.Error("a duplicate plan slug was accepted")
	}
}

func seedPlanErr(s *Store, slug string) (sql.Result, error) {
	return s.db.Exec(`INSERT INTO plans (slug, title, created_at) VALUES (?, ?, ?)`, slug, "dup", stamp)
}

func TestMigrationsAreContiguous(t *testing.T) {
	steps, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("no migrations are embedded")
	}
	for i, step := range steps {
		if step.version != i+1 {
			t.Errorf("step %d has version %d", i, step.version)
		}
		if strings.TrimSpace(step.body) == "" {
			t.Errorf("%s is empty", step.name)
		}
	}
}

func TestStatementsDropsCommentsAndBlanks(t *testing.T) {
	// The first comment holds a semicolon, which must not split a statement:
	// prose in a schema file is allowed punctuation.
	got := statements("-- a note; and more of it\nCREATE TABLE a (x INT);\n\n-- another\nCREATE TABLE b (y INT);\n")
	want := []string{"CREATE TABLE a (x INT)", "CREATE TABLE b (y INT)"}

	if len(got) != len(want) {
		t.Fatalf("got %d statements %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if strings.Join(strings.Fields(got[i]), " ") != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
}
