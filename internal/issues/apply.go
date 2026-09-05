package issues

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Document is a whole plan, as the planner hands it over: a plan and the
// issues that implement it, referring to each other by ids local to the
// document. pib allocates the real numbers, so nothing has to be created
// before it can be referred to.
type Document struct {
	Plan   DocPlan    `json:"plan"`
	Issues []DocIssue `json:"issues"`
}

// DocPlan is the plan itself: what is being built, why, and what "done"
// means for the whole of it. This is where the goal and the scope belong —
// not in a container issue that would sit open forever.
type DocPlan struct {
	Slug       string   `json:"slug"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
	PlannerRun string   `json:"plannerRun,omitempty"`
}

// DocIssue is one issue in a document. Parent and BlockedBy hold either an
// id from this document or an existing issue written as "#12".
type DocIssue struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
	Parent     string   `json:"parent,omitempty"`
	BlockedBy  []string `json:"blockedBy,omitempty"`
}

// ApplyOptions tunes the checks Apply runs.
type ApplyOptions struct {
	// KnownType reports whether pib has a type mapped to an agent. When it
	// is nil, unmapped types are not reported. The store deliberately does
	// not read the configuration itself.
	KnownType func(string) bool
	// Review adds an issue that reviews the plan before any of it is worked.
	// It applies only to a plan that has no issues yet: adding one to a plan
	// already underway would block work that has started.
	Review bool
}

const (
	// ReviewLocalID is the review issue's id within its plan. It is fixed so
	// a re-apply cannot add a second one — (plan_id, local_id) is unique.
	ReviewLocalID = "plan-review"
	// ReviewType is the type the review issue carries, and so which agent
	// runs it.
	ReviewType = "plan-reviewer"
)

const reviewBody = `## Review this plan before any of it is worked

Check every issue in this plan against the code it will change, while changing
an issue is still free.

- Does each issue name files, functions and flags that exist and mean what it
  assumes?
- Can two issues with no dependency between them be launched at once, and would
  they collide if they were?
- Can the agent behind each issue's type actually do what the issue asks?
- Is each acceptance criterion verifiable, and by what?
- Do the ADR paths continue the sequence already in the repository?

Comment findings on the issues they affect. Do not edit or close them: report,
and let the user decide what the plan does about it.

Closing this issue releases the rest of the plan, so leave it open while
anything you found is unresolved.
`

// ApplyResult is what a document did.
type ApplyResult struct {
	Plan    Plan    `json:"plan"`
	Created []int64 `json:"created,omitempty"`
	Updated []int64 `json:"updated,omitempty"`
	// Warnings are problems pib wrote anyway. A plan is applied even when
	// it is imperfect; the planner sees these and can fix them in place.
	Warnings []string `json:"warnings,omitempty"`
}

// ParseDocument reads a plan document. Unknown fields are ignored, so a
// document written by a newer pib still applies.
func ParseDocument(body []byte) (Document, error) {
	var doc Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return Document{}, fmt.Errorf("reading the plan document: %w", err)
	}
	return doc, doc.check()
}

// check rejects a document that cannot be written at all. Everything else is
// reported as a warning once the plan is in.
func (d Document) check() error {
	if strings.TrimSpace(d.Plan.Slug) == "" {
		return errors.New("the plan needs a slug")
	}
	if strings.TrimSpace(d.Plan.Title) == "" {
		return errors.New("the plan needs a title")
	}

	seen := make(map[string]bool, len(d.Issues))
	for i, issue := range d.Issues {
		if strings.TrimSpace(issue.ID) == "" {
			return fmt.Errorf("issue %d has no id; ids are how the document refers to itself", i+1)
		}
		if seen[issue.ID] {
			return fmt.Errorf("two issues share the id %q", issue.ID)
		}
		seen[issue.ID] = true

		if err := checkFields(issue.Title, issue.Type); err != nil {
			return fmt.Errorf("issue %q: %w", issue.ID, err)
		}
	}
	return nil
}

// Apply writes a document to the store in one transaction.
//
// The merge is additive. An id already in the plan updates that issue, an id
// pib has not seen creates one, and an issue missing from the document is
// left exactly as it is — never closed, never deleted. A closed issue stays
// closed. That makes a second planner pass safe to run while coders are
// still in flight.
func (s *Store) Apply(doc Document, opts ApplyOptions) (ApplyResult, error) {
	if err := doc.check(); err != nil {
		return ApplyResult{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return ApplyResult{}, err
	}
	defer tx.Rollback()

	var written []string
	defer func() {
		// Files created for a transaction that never committed have no row
		// to belong to.
		for _, path := range written {
			if _, statErr := os.Stat(path); statErr == nil && err != nil {
				os.Remove(path)
			}
		}
	}()

	result := ApplyResult{}
	var planFile string
	result.Plan, planFile, err = s.upsertPlan(tx, doc.Plan)
	if err != nil {
		return ApplyResult{}, err
	}
	if planFile != "" {
		written = append(written, planFile)
	}

	// Pass one: every issue exists and has a number, so pass two can wire
	// references in any order the document happens to use.
	local, err := existingLocalIDs(tx, result.Plan.ID)
	if err != nil {
		return ApplyResult{}, err
	}

	// A plan with issues already is one someone may be working. Only a plan
	// starting from nothing gets a review gate.
	insertReview := opts.Review && len(local) == 0

	for _, item := range doc.Issues {
		if number, ok := local[item.ID]; ok {
			changed, updateErr := s.updateFromDoc(tx, number, item)
			if updateErr != nil {
				err = updateErr
				return ApplyResult{}, err
			}
			if changed {
				result.Updated = append(result.Updated, number)
			}
			continue
		}

		number, rel, insertErr := s.insert(tx, result.Plan.ID, NewIssue{
			LocalID:    item.ID,
			Type:       item.Type,
			Title:      item.Title,
			Body:       item.Body,
			Acceptance: item.Acceptance,
		})
		if insertErr != nil {
			err = wrapRef(insertErr)
			return ApplyResult{}, err
		}
		written = append(written, s.abs(rel))
		local[item.ID] = number
		result.Created = append(result.Created, number)
	}

	// Pass two: parents and blocked-by edges, now that every id resolves.
	for _, item := range doc.Issues {
		number := local[item.ID]

		if item.Parent != "" {
			parent, warning, ok := resolveRef(tx, item.Parent, local, result.Plan.ID)
			if warning != "" {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("%q: parent %s", item.ID, warning))
			}
			switch {
			case !ok:
				// Already reported; there is nothing to point at.
			case parent == number:
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("%q cannot be its own parent; the parent was dropped", item.ID))
			default:
				if _, err = tx.Exec(`UPDATE issues SET parent = ? WHERE number = ?`, parent, number); err != nil {
					return ApplyResult{}, err
				}
			}
		}

		for _, ref := range item.BlockedBy {
			blocker, warning, ok := resolveRef(tx, ref, local, result.Plan.ID)
			if warning != "" {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("%q: blocked-by %s", item.ID, warning))
			}
			if !ok {
				continue
			}
			if blocker == number {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("%q cannot block itself; the dependency was dropped", item.ID))
				continue
			}
			if _, err = tx.Exec(
				`INSERT OR IGNORE INTO deps (blocked, blocker) VALUES (?, ?)`, number, blocker); err != nil {
				return ApplyResult{}, err
			}
		}
	}

	if insertReview {
		reviewFile, reviewErr := s.addReviewIssue(tx, result.Plan.ID, &result)
		if reviewErr != nil {
			err = reviewErr
			return ApplyResult{}, err
		}
		if reviewFile != "" {
			written = append(written, reviewFile)
		}
	}

	graphWarnings, err := inspect(tx, result.Plan.ID, doc, opts)
	if err != nil {
		return ApplyResult{}, err
	}
	result.Warnings = append(result.Warnings, graphWarnings...)

	if err = tx.Commit(); err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

// addReviewIssue puts a review at the head of the plan and blocks every root
// on it, so nothing starts until the plan has been read against the codebase.
//
// Roots only: an issue with a blocker is already waiting on something that is
// itself waiting on the review, so an edge to it would be redundant.
func (s *Store) addReviewIssue(tx *sql.Tx, planID int64, result *ApplyResult) (string, error) {
	roots, err := rootIssues(tx, planID)
	if err != nil {
		return "", err
	}

	number, rel, err := s.insert(tx, planID, NewIssue{
		LocalID: ReviewLocalID,
		Type:    ReviewType,
		Title:   "Review this plan before work starts",
		Body:    reviewBody,
		Acceptance: []string{
			"Every issue checked against the code it will change",
			"Issues that can run at once do not collide",
			"Each type's agent can do what its issue asks",
			"Findings commented on the issues they affect",
		},
	})
	if err != nil {
		return "", wrapRef(err)
	}

	for _, root := range roots {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO deps (blocked, blocker) VALUES (?, ?)`, root, number); err != nil {
			return s.abs(rel), err
		}
	}

	result.Created = append(result.Created, number)
	return s.abs(rel), nil
}

// rootIssues are the issues in a plan that nothing else is holding up.
func rootIssues(tx *sql.Tx, planID int64) ([]int64, error) {
	rows, err := tx.Query(`
		SELECT number FROM issues i
		WHERE i.plan_id = ?
		  AND NOT EXISTS (SELECT 1 FROM deps d WHERE d.blocked = i.number)
		ORDER BY number`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roots []int64
	for rows.Next() {
		var number int64
		if err := rows.Scan(&number); err != nil {
			return nil, err
		}
		roots = append(roots, number)
	}
	return roots, rows.Err()
}

// upsertPlan finds the plan by slug or creates it, and writes its markdown
// file. A re-apply refreshes the title, the goal and the criteria, which is
// a change rather than a removal.
func (s *Store) upsertPlan(tx *sql.Tx, doc DocPlan) (Plan, string, error) {
	acceptance, err := encodeList(doc.Acceptance)
	if err != nil {
		return Plan{}, "", err
	}

	var (
		plan    Plan
		path    sql.NullString
		stored  sql.NullString
		created string
		run     sql.NullString
	)
	err = tx.QueryRow(
		`SELECT id, slug, title, path, acceptance, created_at, planner_run FROM plans WHERE slug = ?`,
		doc.Slug).
		Scan(&plan.ID, &plan.Slug, &plan.Title, &path, &stored, &created, &run)

	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return Plan{}, "", err
	}

	rel := filepath.Join(PlansDirName, doc.Slug+".md")
	stamp := format(now())

	if fresh {
		res, insertErr := tx.Exec(`
			INSERT INTO plans (slug, title, path, acceptance, indexed_mtime, indexed_size, created_at, planner_run)
			VALUES (?, ?, ?, ?, 0, 0, ?, ?)`,
			doc.Slug, doc.Title, rel, acceptance, stamp, nullable(doc.PlannerRun))
		if insertErr != nil {
			return Plan{}, "", insertErr
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return Plan{}, "", idErr
		}
		plan = Plan{ID: id, Slug: doc.Slug, CreatedAt: parseTime(stamp), PlannerRun: doc.PlannerRun}
	} else {
		plan.CreatedAt = parseTime(created)
		plan.PlannerRun = run.String
		if doc.PlannerRun != "" {
			plan.PlannerRun = doc.PlannerRun
		}
		if path.Valid && path.String != "" {
			rel = path.String
		}
	}

	// The file carries the prose. A body the document leaves out keeps what
	// is already written rather than blanking it.
	file := File{Title: doc.Title, Type: "plan", Acceptance: doc.Acceptance, Body: doc.Body}
	if existing, readErr := ReadFile(s.abs(rel)); readErr == nil {
		if doc.Body == "" {
			file.Body = existing.Body
		}
		if doc.Acceptance == nil {
			file.Acceptance = existing.Acceptance
		}
		file.Comments = existing.Comments
		file.Extra = existing.Extra
	}
	if err := WriteFile(s.abs(rel), file); err != nil {
		return Plan{}, "", err
	}

	info, err := os.Stat(s.abs(rel))
	if err != nil {
		return Plan{}, "", err
	}
	acceptance, err = encodeList(file.Acceptance)
	if err != nil {
		return Plan{}, "", err
	}
	if _, err := tx.Exec(`
		UPDATE plans SET title = ?, path = ?, acceptance = ?, indexed_mtime = ?, indexed_size = ?, planner_run = ?
		WHERE id = ?`,
		doc.Title, rel, acceptance, info.ModTime().UnixNano(), info.Size(),
		nullable(plan.PlannerRun), plan.ID); err != nil {
		return Plan{}, "", err
	}

	plan.Title = doc.Title
	plan.Path = rel
	plan.Acceptance = file.Acceptance

	written := ""
	if fresh {
		written = s.abs(rel)
	}
	return plan, written, nil
}

// existingLocalIDs maps the document ids a plan already knows to numbers.
func existingLocalIDs(tx *sql.Tx, planID int64) (map[string]int64, error) {
	rows, err := tx.Query(
		`SELECT local_id, number FROM issues WHERE plan_id = ? AND local_id IS NOT NULL`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	local := map[string]int64{}
	for rows.Next() {
		var (
			id     string
			number int64
		)
		if err := rows.Scan(&id, &number); err != nil {
			return nil, err
		}
		local[id] = number
	}
	return local, rows.Err()
}

// updateFromDoc refreshes an issue the document already knows about. A closed
// issue keeps its state: applying a plan again never reopens work.
func (s *Store) updateFromDoc(tx *sql.Tx, number int64, item DocIssue) (bool, error) {
	var rel string
	if err := tx.QueryRow(`SELECT path FROM issues WHERE number = ?`, number).Scan(&rel); err != nil {
		return false, err
	}

	file, err := ReadFile(s.abs(rel))
	if err != nil {
		return false, err
	}

	changed := false
	if file.Title != item.Title {
		file.Title, changed = item.Title, true
	}
	if file.Type != item.Type {
		file.Type, changed = item.Type, true
	}
	if item.Body != "" && file.Body != item.Body {
		file.Body, changed = item.Body, true
	}
	if item.Acceptance != nil && !equalLists(file.Acceptance, item.Acceptance) {
		file.Acceptance, changed = item.Acceptance, true
	}
	if !changed {
		return false, nil
	}

	if err := WriteFile(s.abs(rel), file); err != nil {
		return false, err
	}
	acceptance, err := encodeList(file.Acceptance)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`UPDATE issues SET title = ?, type = ?, acceptance = ?, updated_at = ? WHERE number = ?`,
		file.Title, file.Type, acceptance, format(now()), number); err != nil {
		return false, err
	}
	return true, setPath(tx, number, rel, s.abs(rel))
}

// resolveRef turns a document reference into an issue number. A reference is
// either an id from this document or an existing issue as "#12". A reference
// that cannot be resolved is reported and dropped: there is no row to point
// a dependency at.
func resolveRef(tx *sql.Tx, ref string, local map[string]int64, planID int64) (int64, string, bool) {
	ref = strings.TrimSpace(ref)

	if number, ok := local[ref]; ok {
		return number, "", true
	}

	if !strings.HasPrefix(ref, "#") {
		return 0, fmt.Sprintf("%q is not an id in this document; the reference was dropped", ref), false
	}

	number, err := strconv.ParseInt(strings.TrimPrefix(ref, "#"), 10, 64)
	if err != nil {
		return 0, fmt.Sprintf("%q is not an issue number; the reference was dropped", ref), false
	}

	var owner int64
	switch err := tx.QueryRow(`SELECT plan_id FROM issues WHERE number = ?`, number).Scan(&owner); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Sprintf("issue #%d does not exist; the reference was dropped", number), false
	case err != nil:
		return 0, fmt.Sprintf("issue #%d could not be read; the reference was dropped", number), false
	case owner != planID:
		return number, fmt.Sprintf("issue #%d belongs to another plan", number), true
	}
	return number, "", true
}

// inspect reports what is wrong with the plan that was just written. None of
// it blocks the write; each of these can graduate to a hard error later
// without changing the shape of anything.
func inspect(tx *sql.Tx, planID int64, doc Document, opts ApplyOptions) ([]string, error) {
	var warnings []string

	if opts.KnownType != nil {
		reported := map[string]bool{}
		for _, item := range doc.Issues {
			if opts.KnownType(item.Type) || reported[item.Type] {
				continue
			}
			reported[item.Type] = true
			warnings = append(warnings,
				fmt.Sprintf("no agent is mapped to type %q; those issues cannot be launched", item.Type))
		}
	}

	blockers, states, err := graph(tx, planID)
	if err != nil {
		return nil, err
	}

	if cycle := findCycle(blockers); len(cycle) > 0 {
		warnings = append(warnings,
			fmt.Sprintf("the dependency graph has a cycle (%s); nothing in it can ever start", renderCycle(cycle)))
	}

	open, startable := 0, 0
	for number, state := range states {
		if state != StateOpen {
			continue
		}
		open++
		blocked := false
		for _, blocker := range blockers[number] {
			if states[blocker] == StateOpen {
				blocked = true
				break
			}
		}
		if !blocked {
			startable++
		}
	}
	if open > 0 && startable == 0 {
		warnings = append(warnings, "no issue in this plan can start; every open issue is blocked")
	}

	return warnings, nil
}

// graph loads a plan's dependency edges and issue states.
func graph(tx *sql.Tx, planID int64) (map[int64][]int64, map[int64]State, error) {
	states := map[int64]State{}
	rows, err := tx.Query(`SELECT number, state FROM issues WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var (
			number int64
			state  string
		)
		if err := rows.Scan(&number, &state); err != nil {
			rows.Close()
			return nil, nil, err
		}
		states[number] = State(state)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	blockers := map[int64][]int64{}
	rows, err = tx.Query(
		`SELECT blocked, blocker FROM deps WHERE blocked IN (SELECT number FROM issues WHERE plan_id = ?)`,
		planID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var blocked, blocker int64
		if err := rows.Scan(&blocked, &blocker); err != nil {
			return nil, nil, err
		}
		blockers[blocked] = append(blockers[blocked], blocker)
	}
	for number := range blockers {
		sort.Slice(blockers[number], func(i, j int) bool { return blockers[number][i] < blockers[number][j] })
	}

	return blockers, states, rows.Err()
}

// findCycle returns one cycle in the blocked-by graph, or nil. Reporting a
// single concrete loop is more use than counting them.
func findCycle(blockers map[int64][]int64) []int64 {
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	mark := map[int64]int{}
	var stack []int64

	var walk func(int64) []int64
	walk = func(node int64) []int64 {
		mark[node] = active
		stack = append(stack, node)

		for _, next := range blockers[node] {
			switch mark[next] {
			case active:
				for i, seen := range stack {
					if seen == next {
						return append([]int64(nil), stack[i:]...)
					}
				}
			case unvisited:
				if cycle := walk(next); cycle != nil {
					return cycle
				}
			}
		}

		stack = stack[:len(stack)-1]
		mark[node] = done
		return nil
	}

	nodes := make([]int64, 0, len(blockers))
	for node := range blockers {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })

	for _, node := range nodes {
		if mark[node] == unvisited {
			if cycle := walk(node); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

func renderCycle(cycle []int64) string {
	parts := make([]string, 0, len(cycle)+1)
	for _, number := range cycle {
		parts = append(parts, fmt.Sprintf("#%d", number))
	}
	return strings.Join(append(parts, parts[0]), " → ")
}

func equalLists(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
