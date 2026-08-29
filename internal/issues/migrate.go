package issues

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is one numbered step, applied in a transaction of its own.
type migration struct {
	version int
	name    string
	body    string
}

// migrate brings the database up to the newest schema. Steps already applied
// are skipped, so opening an up-to-date database does no work.
func migrate(db *sql.DB) error {
	steps, err := migrations()
	if err != nil {
		return err
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("creating schema_version: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_version)`); err != nil {
		return fmt.Errorf("initialising schema_version: %w", err)
	}

	current, err := currentVersion(db)
	if err != nil {
		return err
	}

	for _, step := range steps {
		if step.version <= current {
			continue
		}
		if err := apply(db, step); err != nil {
			return fmt.Errorf("migration %s: %w", step.name, err)
		}
	}

	return nil
}

// apply runs one step and records it, so a failure part way through leaves
// the database on the previous version rather than half migrated.
func apply(db *sql.DB, step migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range statements(step.body) {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%w\nin: %s", err, stmt)
		}
	}
	if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, step.version); err != nil {
		return err
	}

	return tx.Commit()
}

// currentVersion reads the applied schema version. A database with no
// schema_version table is at version zero.
func currentVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading schema_version: %w", err)
	}
	return version, nil
}

// migrations loads the embedded steps in version order.
func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}

	steps := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s is not named <version>_<description>.sql", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration %s has no usable version prefix", entry.Name())
		}

		body, err := migrationFS.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, err
		}

		steps = append(steps, migration{version: version, name: entry.Name(), body: string(body)})
	}

	sort.Slice(steps, func(i, j int) bool { return steps[i].version < steps[j].version })

	for i, step := range steps {
		if step.version != i+1 {
			return nil, fmt.Errorf("migration versions must run 1..n without gaps; found %d at position %d", step.version, i+1)
		}
	}

	return steps, nil
}

// statements splits a migration into single statements. The driver takes one
// at a time, and the schema files hold nothing more exotic than comments and
// semicolon-terminated DDL.
//
// Comment lines go first: prose is allowed a semicolon, and splitting before
// dropping them would cut a comment in half and feed the tail to the driver.
func statements(body string) []string {
	var stripped strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteString("\n")
	}

	var out []string
	for _, part := range strings.Split(stripped.String(), ";") {
		if stmt := strings.TrimSpace(part); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
