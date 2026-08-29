// Package issues stores pib's plans, issues, dependencies and agent runs.
//
// Metadata lives in a SQLite database; what the planner authored lives in one
// markdown file per issue beside it. The store is owned by the running pib
// process, which is the only writer — the CLI reaches it over pib's socket
// rather than opening the database itself.
package issues

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Names of the things the store keeps under the workspace.
const (
	// DataDirName is the workspace subdirectory holding all issue data.
	DataDirName = "data"
	// IssuesDirName holds one markdown file per issue.
	IssuesDirName = "issues"
	// PlansDirName holds one markdown file per plan.
	PlansDirName = "plans"
	// DBName is the metadata database.
	DBName = "pib.db"
)

// DataDir is the data directory for a workspace, normally <git root>/.pib/data.
func DataDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, DataDirName)
}

// Store is an open issue store.
type Store struct {
	db  *sql.DB
	dir string
}

// Open prepares the data directory and opens the database, applying any
// migrations the file has not seen yet.
func Open(dataDir string) (*Store, error) {
	for _, name := range []string{IssuesDirName, PlansDirName} {
		if err := os.MkdirAll(filepath.Join(dataDir, name), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", filepath.Join(dataDir, DBName))
	if err != nil {
		return nil, err
	}

	// One connection, held for the life of the store. pib is the single
	// writer, the data is tiny, and pinning the connection means the
	// per-connection pragmas below cannot be silently lost to a reconnect.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	store := &Store{db: db, dir: dataDir}

	// Opening the store means taking it over, so any run still marked live
	// belongs to a process that is gone.
	if _, err := store.closeOrphanRuns(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Dir is the data directory the store was opened on.
func (s *Store) Dir() string {
	return s.dir
}

// IssuesDir is the directory holding issue markdown files.
func (s *Store) IssuesDir() string {
	return filepath.Join(s.dir, IssuesDirName)
}

// PlansDir is the directory holding plan markdown files.
func (s *Store) PlansDir() string {
	return filepath.Join(s.dir, PlansDirName)
}

// Version is the schema version currently applied.
func (s *Store) Version() (int, error) {
	return currentVersion(s.db)
}
