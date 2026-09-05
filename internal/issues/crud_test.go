package issues

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// freeze pins the store's clock for the length of a test.
func freeze(t *testing.T, iso string) {
	t.Helper()
	previous := now
	now = func() time.Time { return at(iso) }
	t.Cleanup(func() { now = previous })
}

// planned returns a store with one plan ready to hang issues off.
func planned(t *testing.T) *Store {
	t.Helper()
	store := open(t)
	if _, err := store.CreatePlan(NewPlan{Slug: "orders", Title: "Order placement", PlannerRun: ""}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	return store
}

// task creates a task issue in the orders plan.
func task(t *testing.T, s *Store, title string) Issue {
	t.Helper()
	issue, err := s.Create(NewIssue{Plan: "orders", Type: "task", Title: title})
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return issue
}

func TestCreatePlanAndLookUp(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := open(t)

	plan, err := store.CreatePlan(NewPlan{Slug: "orders", Title: "Order placement", PlannerRun: "run-abc"})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if plan.Slug != "orders" || plan.PlannerRun != "run-abc" {
		t.Errorf("plan = %+v", plan)
	}
	if !plan.CreatedAt.Equal(at("2026-08-29T12:00:00Z")) {
		t.Errorf("created at %v", plan.CreatedAt)
	}

	if _, err := store.CreatePlan(NewPlan{Slug: "orders", Title: "Duplicate", PlannerRun: ""}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate slug gave %v, want ErrExists", err)
	}
	if _, err := store.Plan("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing plan gave %v, want ErrNotFound", err)
	}

	if _, err := store.CreatePlan(NewPlan{Slug: "second", Title: "Another", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}
	plans, err := store.Plans()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 || plans[0].Slug != "second" {
		t.Errorf("Plans() = %+v, want newest first", plans)
	}
}

func TestCreateWritesTheFileAndIndexesIt(t *testing.T) {
	store := planned(t)

	issue, err := store.Create(NewIssue{
		Plan:       "orders",
		LocalID:    "order-agg",
		Type:       "task",
		Title:      "Implement Order Aggregate",
		Body:       "## Task\n\nDo it.",
		Acceptance: []string{"Handles PlaceOrder"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if issue.Number != 1 {
		t.Errorf("number = %d, want 1", issue.Number)
	}
	if want := filepath.Join(IssuesDirName, "1-implement-order-aggregate.md"); issue.Path != want {
		t.Errorf("path = %q, want %q", issue.Path, want)
	}
	if issue.State != StateOpen {
		t.Errorf("state = %q, want open", issue.State)
	}
	if issue.Plan != "orders" || issue.LocalID != "order-agg" {
		t.Errorf("issue = %+v", issue)
	}
	if !reflect.DeepEqual(issue.Acceptance, []string{"Handles PlaceOrder"}) {
		t.Errorf("acceptance = %v", issue.Acceptance)
	}

	file, err := store.Content(issue.Number)
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if file.Body != "## Task\n\nDo it." {
		t.Errorf("body = %q", file.Body)
	}

	// Numbers are allocated by the store and never reused.
	second := task(t, store, "Second")
	third := task(t, store, "Third")
	if second.Number != 2 || third.Number != 3 {
		t.Errorf("numbers = %d, %d, want 2, 3", second.Number, third.Number)
	}
}

func TestCreateRejectsWhatCannotBeWritten(t *testing.T) {
	store := planned(t)

	if _, err := store.Create(NewIssue{Plan: "missing", Type: "task", Title: "T"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plan gave %v, want ErrNotFound", err)
	}
	if _, err := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "  "}); err == nil {
		t.Error("an issue with no title was accepted")
	}
	if _, err := store.Create(NewIssue{Plan: "orders", Type: "", Title: "T"}); err == nil {
		t.Error("an issue with no type was accepted")
	}
	if _, err := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "T", BlockedBy: []int64{99}}); err == nil {
		t.Error("a blocker that does not exist was accepted")
	}

	if _, err := store.Create(NewIssue{Plan: "orders", LocalID: "dup", Type: "task", Title: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewIssue{Plan: "orders", LocalID: "dup", Type: "task", Title: "Two"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate local id gave %v, want ErrExists", err)
	}
	// The same local id in another plan is fine — ids are plan-local.
	if _, err := store.CreatePlan(NewPlan{Slug: "other", Title: "Other", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewIssue{Plan: "other", LocalID: "dup", Type: "task", Title: "Three"}); err != nil {
		t.Errorf("local id reuse across plans rejected: %v", err)
	}
}

func TestRejectedCreateLeavesNoFileBehind(t *testing.T) {
	store := planned(t)

	// The bad blocker is caught before the file is written, so the rollback
	// is all that is needed here; Create removes the file itself only when
	// the transaction fails after the write.
	if _, err := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "Doomed", BlockedBy: []int64{99}}); err == nil {
		t.Fatal("expected the create to fail")
	}

	entries, err := os.ReadDir(store.IssuesDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("issues directory holds %d files after a failed create", len(entries))
	}
}

func TestDependencies(t *testing.T) {
	store := planned(t)
	schema := task(t, store, "Schema")
	migration := task(t, store, "Migration")

	aggregate, err := store.Create(NewIssue{
		Plan: "orders", Type: "task", Title: "Aggregate",
		Parent: schema.Number, BlockedBy: []int64{schema.Number, migration.Number},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !reflect.DeepEqual(aggregate.BlockedBy, []int64{schema.Number, migration.Number}) {
		t.Errorf("blocked by %v", aggregate.BlockedBy)
	}
	if aggregate.Parent != schema.Number {
		t.Errorf("parent = %d, want %d", aggregate.Parent, schema.Number)
	}

	edited, err := store.Edit(aggregate.Number, Edit{RemoveBlockedBy: []int64{schema.Number}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !reflect.DeepEqual(edited.BlockedBy, []int64{migration.Number}) {
		t.Errorf("after removal, blocked by %v", edited.BlockedBy)
	}

	// Adding the same edge twice is not an error.
	if _, err := store.Edit(aggregate.Number, Edit{AddBlockedBy: []int64{migration.Number}}); err != nil {
		t.Errorf("re-adding an edge: %v", err)
	}
	if _, err := store.Edit(aggregate.Number, Edit{AddBlockedBy: []int64{aggregate.Number}}); err == nil {
		t.Error("an issue was allowed to block itself")
	}
}

func TestCyclesAreAllowed(t *testing.T) {
	// pib warns about cycles rather than refusing the write; they simply
	// leave nothing startable.
	store := planned(t)
	first := task(t, store, "First")
	second := task(t, store, "Second")

	if _, err := store.Edit(first.Number, Edit{AddBlockedBy: []int64{second.Number}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Edit(second.Number, Edit{AddBlockedBy: []int64{first.Number}}); err != nil {
		t.Errorf("closing a cycle was rejected: %v", err)
	}
}

func TestHandEditedTitleAppearsWithoutReindex(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")
	path := filepath.Join(store.Dir(), issue.Path)

	// Same length, so only the modification time gives the edit away.
	file, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Title = "Bravo"
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}

	got, err := store.Issue(issue.Number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got.Title != "Bravo" {
		t.Errorf("title = %q, want the hand-edited Bravo", got.Title)
	}

	listed, err := store.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if listed[0].Title != "Bravo" {
		t.Errorf("listed title = %q, want Bravo", listed[0].Title)
	}
}

func TestRetitlingKeepsThePath(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha task")
	before := issue.Path

	edited, err := store.Edit(issue.Number, Edit{Title: ptr("Completely different title")})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Path != before {
		t.Errorf("path moved to %q; a rename must not relocate the file", edited.Path)
	}
	if edited.Title != "Completely different title" {
		t.Errorf("title = %q", edited.Title)
	}

	file, err := store.Content(issue.Number)
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if file.Title != "Completely different title" {
		t.Errorf("file title = %q, frontmatter was not rewritten", file.Title)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), before)); err != nil {
		t.Errorf("the original file is gone: %v", err)
	}
}

func TestEditBodyTypeAndAcceptance(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	edited, err := store.Edit(issue.Number, Edit{
		Type:       ptr("research"),
		Body:       ptr("New body"),
		Acceptance: ptr([]string{"one", "two"}),
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Type != "research" {
		t.Errorf("type = %q", edited.Type)
	}
	if !reflect.DeepEqual(edited.Acceptance, []string{"one", "two"}) {
		t.Errorf("acceptance = %v", edited.Acceptance)
	}

	file, err := store.Content(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != "New body" {
		t.Errorf("body = %q", file.Body)
	}

	if _, err := store.Edit(issue.Number, Edit{Title: ptr(" ")}); err == nil {
		t.Error("an empty title was accepted")
	}
}

func TestListFilters(t *testing.T) {
	store := planned(t)
	if _, err := store.CreatePlan(NewPlan{Slug: "billing", Title: "Billing", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}

	first := task(t, store, "First")
	task(t, store, "Second")
	if _, err := store.Create(NewIssue{Plan: "orders", Type: "research", Title: "Compare libraries"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewIssue{Plan: "billing", Type: "task", Title: "Invoices"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CloseIssue(first.Number, ""); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"everything", Filter{}, 4},
		{"one plan", Filter{Plan: "orders"}, 3},
		{"open only", Filter{State: StateOpen}, 3},
		{"closed only", Filter{State: StateClosed}, 1},
		{"by type", Filter{Type: "research"}, 1},
		{"plan and type", Filter{Plan: "orders", Type: "task"}, 2},
	}
	for _, c := range cases {
		got, err := store.List(c.filter)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) != c.want {
			t.Errorf("%s: got %d issues, want %d", c.name, len(got), c.want)
		}
	}
}

func TestCommentsAppendInOrder(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	freeze(t, "2026-08-29T12:00:00Z")
	if err := store.Comment(issue.Number, "reviewer", "NEEDS CHANGES"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	freeze(t, "2026-08-29T13:00:00Z")
	if err := store.Comment(issue.Number, "dan", "Fixed."); err != nil {
		t.Fatalf("Comment: %v", err)
	}

	comments, err := store.Comments(issue.Number)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	want := []Comment{
		{Author: "reviewer", At: at("2026-08-29T12:00:00Z"), Body: "NEEDS CHANGES"},
		{Author: "dan", At: at("2026-08-29T13:00:00Z"), Body: "Fixed."},
	}
	if !reflect.DeepEqual(comments, want) {
		t.Errorf("comments = %#v", comments)
	}

	if err := store.Comment(issue.Number, "", "no author"); err == nil {
		t.Error("a comment with no author was accepted")
	}
}

func TestCloseWarnsForAnUnmergedTask(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	closed, warnings, err := store.CloseIssue(issue.Number, "done by hand")
	if err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if closed.State != StateClosed {
		t.Errorf("state = %q", closed.State)
	}
	if closed.ClosedAt.IsZero() {
		t.Error("closed_at was not recorded")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "pull request") {
		t.Errorf("warnings = %v, want one about the pull request", warnings)
	}

	// The reason is kept as activity rather than thrown away.
	comments, err := store.Comments(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Body != "done by hand" {
		t.Errorf("comments = %#v", comments)
	}

	// Closing again is a no-op, not a second warning.
	_, warnings, err = store.CloseIssue(issue.Number, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("closing an already closed issue warned: %v", warnings)
	}
}

func TestCloseIsQuietWhenTheRuleDoesNotApply(t *testing.T) {
	store := planned(t)

	research, err := store.Create(NewIssue{Plan: "orders", Type: "research", Title: "Compare"})
	if err != nil {
		t.Fatal(err)
	}
	if _, warnings, err := store.CloseIssue(research.Number, ""); err != nil || len(warnings) != 0 {
		t.Errorf("closing research warned %v (err %v); only tasks wait on a merge", warnings, err)
	}

	merged := task(t, store, "Merged task")
	if _, err := store.LinkPR(merged.Number, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE issues SET pr_state = 'merged' WHERE number = ?`, merged.Number); err != nil {
		t.Fatal(err)
	}
	if _, warnings, err := store.CloseIssue(merged.Number, ""); err != nil || len(warnings) != 0 {
		t.Errorf("closing a merged task warned %v (err %v)", warnings, err)
	}
}

func TestLinkPRAndReopen(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	linked, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/7")
	if err != nil {
		t.Fatalf("LinkPR: %v", err)
	}
	if linked.PRURL != "https://github.com/o/r/pull/7" || linked.PRState != "open" {
		t.Errorf("issue = %+v, want the pull request recorded as open", linked)
	}
	if _, err := store.LinkPR(issue.Number, "  "); err == nil {
		t.Error("an empty pull request url was accepted")
	}

	if _, _, err := store.CloseIssue(issue.Number, ""); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.ReopenIssue(issue.Number)
	if err != nil {
		t.Fatalf("ReopenIssue: %v", err)
	}
	if reopened.State != StateOpen || !reopened.ClosedAt.IsZero() {
		t.Errorf("issue = %+v, want open with no closed_at", reopened)
	}
}

// linkRecorder captures the issues a link notified the hook with.
type linkRecorder struct{ linked []Issue }

func (r *linkRecorder) PRLinked(issue Issue) { r.linked = append(r.linked, issue) }

func TestLinkPRNotifiesTheHook(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	// A store with no hook behaves exactly as it does today.
	if _, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatalf("LinkPR with no hook: %v", err)
	}

	var seen linkRecorder
	store.OnLinked = &seen

	if _, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/7"); err != nil {
		t.Fatal(err)
	}
	if len(seen.linked) != 1 {
		t.Fatalf("hook saw %d links, want 1: %+v", len(seen.linked), seen.linked)
	}
	// The hook sees the row as it landed, not as it was before the write.
	if got := seen.linked[0]; got.Number != issue.Number ||
		got.PRURL != "https://github.com/o/r/pull/7" || got.PRState != "open" {
		t.Errorf("hook saw %+v, want #%d with the new pull request open", got, issue.Number)
	}

	// A failed link notifies nobody.
	if _, err := store.LinkPR(issue.Number, "  "); err == nil {
		t.Fatal("an empty pull request url was accepted")
	}
	if len(seen.linked) != 1 {
		t.Errorf("hook saw %d links after a rejected url, want 1", len(seen.linked))
	}
}

func TestReindexRereadsEveryFile(t *testing.T) {
	store := planned(t)
	first := task(t, store, "Alpha")
	task(t, store, "Beta")

	// Rewrite the file behind the store's back, leaving the index stale.
	path := filepath.Join(store.Dir(), first.Path)
	if err := WriteFile(path, File{Title: "Rewritten", Type: "research", Body: "New"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		`UPDATE issues SET indexed_mtime = 0, indexed_size = 0 WHERE number = ?`, first.Number); err != nil {
		t.Fatal(err)
	}

	refreshed, err := store.Reindex("")
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if refreshed != 2 {
		t.Errorf("Reindex refreshed %d issues, want all 2", refreshed)
	}

	got, err := store.Issue(first.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Rewritten" || got.Type != "research" {
		t.Errorf("issue = %+v, want the file's title and type", got)
	}
}

func TestAMissingFileDoesNotBreakListing(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	if err := os.Remove(filepath.Join(store.Dir(), issue.Path)); err != nil {
		t.Fatal(err)
	}

	listed, err := store.List(Filter{})
	if err != nil {
		t.Fatalf("List with a missing file: %v", err)
	}
	if len(listed) != 1 || listed[0].Title != "Alpha" {
		t.Errorf("listed = %+v, want the last indexed title", listed)
	}

	// Asking for the content is where the real error belongs.
	if _, err := store.Content(issue.Number); err == nil {
		t.Error("Content succeeded for a file that is gone")
	}
}

func TestIssueNotFound(t *testing.T) {
	store := planned(t)
	if _, err := store.Issue(42); !errors.Is(err, ErrNotFound) {
		t.Errorf("Issue(42) gave %v, want ErrNotFound", err)
	}
}

func TestIssueCountsByPlan(t *testing.T) {
	store := planned(t)
	if _, err := store.CreatePlan(NewPlan{Slug: "billing", Title: "Billing", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}

	// orders: 2 open, 1 closed
	task(t, store, "Alpha")
	task(t, store, "Beta")
	gamma, _ := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "Gamma"})
	store.CloseIssue(gamma.Number, "")

	// billing: 1 open
	if _, err := store.Create(NewIssue{Plan: "billing", Type: "task", Title: "Invoice task"}); err != nil {
		t.Fatal(err)
	}

	counts, err := store.IssueCountsByPlan()
	if err != nil {
		t.Fatalf("IssueCountsByPlan: %v", err)
	}

	if got := counts["orders"]; got.Total != 3 || got.Open != 2 || got.Closed != 1 {
		t.Errorf("orders counts = %+v, want {Total:3 Open:2 Closed:1}", got)
	}
	if got := counts["billing"]; got.Total != 1 || got.Open != 1 || got.Closed != 0 {
		t.Errorf("billing counts = %+v, want {Total:1 Open:1 Closed:0}", got)
	}
}
