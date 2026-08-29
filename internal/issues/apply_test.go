package issues

import (
	"strconv"
	"strings"
	"testing"
)

// doc is a small plan: a feature, a schema task, and an aggregate that waits
// on the schema. The aggregate references the schema before the document
// defines it, which the two-pass apply has to cope with.
func doc() Document {
	return Document{
		Plan: DocPlan{Slug: "orders", Title: "Order placement"},
		Issues: []DocIssue{
			{ID: "feature", Type: "feature", Title: "Feature: order placement", Body: "## Goal\n\nPlace orders."},
			{
				ID: "order-agg", Type: "task", Title: "Implement Order Aggregate",
				Parent: "feature", BlockedBy: []string{"schema"},
				Acceptance: []string{"Handles PlaceOrder"}, Body: "## Task\n\nAggregate.",
			},
			{ID: "schema", Type: "task", Title: "Order schema", Parent: "feature", Body: "## Task\n\nSchema."},
		},
	}
}

// applied applies a document and fails the test if it does not go in.
func applied(t *testing.T, s *Store, d Document, opts ApplyOptions) ApplyResult {
	t.Helper()
	result, err := s.Apply(d, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return result
}

// byLocalID finds an applied issue by the id the document used.
func byLocalID(t *testing.T, s *Store, id string) Issue {
	t.Helper()
	list, err := s.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range list {
		if issue.LocalID == id {
			return issue
		}
	}
	t.Fatalf("no issue with local id %q", id)
	return Issue{}
}

func TestApplyCreatesTheWholePlan(t *testing.T) {
	store := open(t)

	result := applied(t, store, doc(), ApplyOptions{})
	if len(result.Created) != 3 || len(result.Updated) != 0 {
		t.Errorf("created %v, updated %v, want three creates", result.Created, result.Updated)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", result.Warnings)
	}
	if result.Plan.Slug != "orders" || result.Plan.Title != "Order placement" {
		t.Errorf("plan = %+v", result.Plan)
	}

	// A forward reference resolves: the aggregate is blocked by the schema
	// even though the schema appears later in the document.
	aggregate := byLocalID(t, store, "order-agg")
	schema := byLocalID(t, store, "schema")
	feature := byLocalID(t, store, "feature")

	if len(aggregate.BlockedBy) != 1 || aggregate.BlockedBy[0] != schema.Number {
		t.Errorf("aggregate blocked by %v, want [%d]", aggregate.BlockedBy, schema.Number)
	}
	if aggregate.Parent != feature.Number || schema.Parent != feature.Number {
		t.Errorf("parents = %d and %d, want %d", aggregate.Parent, schema.Parent, feature.Number)
	}

	file, err := store.Content(aggregate.Number)
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != "## Task\n\nAggregate." {
		t.Errorf("body = %q", file.Body)
	}
	if len(file.Acceptance) != 1 {
		t.Errorf("acceptance = %v", file.Acceptance)
	}
}

func TestReapplyIsAdditive(t *testing.T) {
	store := open(t)
	first := applied(t, store, doc(), ApplyOptions{})

	// A second pass: one issue retitled, one issue added, one issue that the
	// planner dropped from the document entirely.
	second := doc()
	second.Issues = second.Issues[:2]
	second.Issues[1].Title = "Implement the Order Aggregate properly"
	second.Issues = append(second.Issues, DocIssue{
		ID: "events", Type: "task", Title: "Publish OrderPlaced", BlockedBy: []string{"order-agg"},
	})

	result := applied(t, store, second, ApplyOptions{})
	if len(result.Created) != 1 {
		t.Errorf("created %v, want just the new issue", result.Created)
	}
	if len(result.Updated) != 1 {
		t.Errorf("updated %v, want just the retitled issue", result.Updated)
	}

	// The dropped issue is untouched, not closed and not deleted.
	schema := byLocalID(t, store, "schema")
	if schema.State != StateOpen {
		t.Errorf("an issue missing from the document became %q", schema.State)
	}

	// Numbers are stable across a re-apply.
	aggregate := byLocalID(t, store, "order-agg")
	if aggregate.Number != first.Created[1] {
		t.Errorf("issue number moved from %d to %d", first.Created[1], aggregate.Number)
	}
	if aggregate.Title != "Implement the Order Aggregate properly" {
		t.Errorf("title = %q", aggregate.Title)
	}

	all, err := store.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("plan holds %d issues, want 4", len(all))
	}
}

func TestReapplyLeavesClosedIssuesClosed(t *testing.T) {
	store := open(t)
	applied(t, store, doc(), ApplyOptions{})

	schema := byLocalID(t, store, "schema")
	if _, _, err := store.CloseIssue(schema.Number, "merged"); err != nil {
		t.Fatal(err)
	}

	// The same document again, with the closed issue still in it.
	again := doc()
	again.Issues[2].Title = "Order schema, revised"
	applied(t, store, again, ApplyOptions{})

	schema = byLocalID(t, store, "schema")
	if schema.State != StateClosed {
		t.Errorf("re-applying reopened a closed issue (state %q)", schema.State)
	}
	if schema.Title != "Order schema, revised" {
		t.Errorf("title = %q; a closed issue should still take content updates", schema.Title)
	}
}

func TestApplyWarnsAboutACycle(t *testing.T) {
	store := open(t)

	d := Document{
		Plan: DocPlan{Slug: "orders", Title: "Order placement"},
		Issues: []DocIssue{
			{ID: "a", Type: "task", Title: "A", BlockedBy: []string{"b"}},
			{ID: "b", Type: "task", Title: "B", BlockedBy: []string{"a"}},
		},
	}

	result := applied(t, store, d, ApplyOptions{})
	if len(result.Created) != 2 {
		t.Errorf("the plan was not written: %v", result.Created)
	}

	var cycle, stuck bool
	for _, w := range result.Warnings {
		if strings.Contains(w, "cycle") {
			cycle = true
		}
		if strings.Contains(w, "no issue in this plan can start") {
			stuck = true
		}
	}
	if !cycle {
		t.Errorf("warnings = %v, want one naming the cycle", result.Warnings)
	}
	if !stuck {
		t.Errorf("warnings = %v, want one saying nothing can start", result.Warnings)
	}
}

func TestApplyDropsUnresolvableReferences(t *testing.T) {
	store := open(t)

	d := Document{
		Plan: DocPlan{Slug: "orders", Title: "Order placement"},
		Issues: []DocIssue{
			{ID: "a", Type: "task", Title: "A", BlockedBy: []string{"ghost", "#404"}, Parent: "nowhere"},
			{ID: "b", Type: "task", Title: "B", BlockedBy: []string{"b"}},
		},
	}

	result := applied(t, store, d, ApplyOptions{})
	if len(result.Created) != 2 {
		t.Fatalf("created %v, want both issues written anyway", result.Created)
	}
	if len(result.Warnings) != 4 {
		t.Errorf("warnings = %v, want four", result.Warnings)
	}

	a := byLocalID(t, store, "a")
	if len(a.BlockedBy) != 0 {
		t.Errorf("a is blocked by %v; unresolvable references must be dropped", a.BlockedBy)
	}
	if a.Parent != 0 {
		t.Errorf("a has parent %d, want none", a.Parent)
	}
	b := byLocalID(t, store, "b")
	if len(b.BlockedBy) != 0 {
		t.Errorf("b blocks itself: %v", b.BlockedBy)
	}
}

func TestApplyResolvesExistingIssuesByNumber(t *testing.T) {
	store := open(t)
	applied(t, store, doc(), ApplyOptions{})
	schema := byLocalID(t, store, "schema")

	// A later plan that hangs off an issue from the first one.
	second := Document{
		Plan: DocPlan{Slug: "billing", Title: "Billing"},
		Issues: []DocIssue{
			{ID: "invoices", Type: "task", Title: "Invoices", BlockedBy: []string{"#" + strconv.FormatInt(schema.Number, 10)}},
		},
	}

	result := applied(t, store, second, ApplyOptions{})
	invoices := byLocalID(t, store, "invoices")
	if len(invoices.BlockedBy) != 1 || invoices.BlockedBy[0] != schema.Number {
		t.Errorf("blocked by %v, want [%d]", invoices.BlockedBy, schema.Number)
	}

	var crossed bool
	for _, w := range result.Warnings {
		if strings.Contains(w, "another plan") {
			crossed = true
		}
	}
	if !crossed {
		t.Errorf("warnings = %v, want one about crossing plans", result.Warnings)
	}
}

func TestApplyWarnsAboutUnmappedTypes(t *testing.T) {
	store := open(t)
	known := func(t string) bool { return t == "task" || t == "feature" }

	d := doc()
	d.Issues = append(d.Issues, DocIssue{ID: "chore-1", Type: "chore", Title: "Tidy up"})
	d.Issues = append(d.Issues, DocIssue{ID: "chore-2", Type: "chore", Title: "Tidy up more"})

	result := applied(t, store, d, ApplyOptions{KnownType: known})

	var reported int
	for _, w := range result.Warnings {
		if strings.Contains(w, `type "chore"`) {
			reported++
		}
	}
	if reported != 1 {
		t.Errorf("warnings = %v, want the unmapped type reported once", result.Warnings)
	}
	if len(result.Created) != 5 {
		t.Errorf("created %v, want all five issues written", result.Created)
	}
}

func TestApplyRejectsADocumentItCannotWrite(t *testing.T) {
	cases := map[string]Document{
		"no plan slug":  {Plan: DocPlan{Title: "T"}},
		"no plan title": {Plan: DocPlan{Slug: "orders"}},
		"no issue id": {
			Plan:   DocPlan{Slug: "orders", Title: "T"},
			Issues: []DocIssue{{Type: "task", Title: "A"}},
		},
		"duplicate id": {
			Plan: DocPlan{Slug: "orders", Title: "T"},
			Issues: []DocIssue{
				{ID: "a", Type: "task", Title: "A"},
				{ID: "a", Type: "task", Title: "B"},
			},
		},
		"no title": {
			Plan:   DocPlan{Slug: "orders", Title: "T"},
			Issues: []DocIssue{{ID: "a", Type: "task"}},
		},
		"no type": {
			Plan:   DocPlan{Slug: "orders", Title: "T"},
			Issues: []DocIssue{{ID: "a", Title: "A"}},
		},
	}

	for name, d := range cases {
		store := open(t)
		if _, err := store.Apply(d, ApplyOptions{}); err == nil {
			t.Errorf("%s: expected a hard error", name)
			continue
		}

		// A rejected document writes nothing at all.
		plans, err := store.Plans()
		if err != nil {
			t.Fatal(err)
		}
		if len(plans) != 0 {
			t.Errorf("%s: %d plans were written by a rejected document", name, len(plans))
		}
		list, err := store.List(Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 0 {
			t.Errorf("%s: %d issues were written by a rejected document", name, len(list))
		}
	}
}

func TestParseDocument(t *testing.T) {
	body := []byte(`{
	  "plan": { "slug": "orders", "title": "Order placement", "plannerRun": "run-1" },
	  "issues": [
	    { "id": "feature", "type": "feature", "title": "Feature: order placement", "body": "## Goal" },
	    { "id": "agg", "type": "task", "title": "Aggregate", "parent": "feature",
	      "blockedBy": ["feature"], "acceptance": ["one", "two"], "unknownField": 1 }
	  ]
	}`)

	d, err := ParseDocument(body)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if d.Plan.Slug != "orders" || d.Plan.PlannerRun != "run-1" {
		t.Errorf("plan = %+v", d.Plan)
	}
	if len(d.Issues) != 2 {
		t.Fatalf("got %d issues", len(d.Issues))
	}
	if d.Issues[1].Parent != "feature" || len(d.Issues[1].Acceptance) != 2 {
		t.Errorf("issue = %+v", d.Issues[1])
	}

	if _, err := ParseDocument([]byte(`{"plan":`)); err == nil {
		t.Error("malformed json was accepted")
	}
	if _, err := ParseDocument([]byte(`{"plan": {"slug": "x"}}`)); err == nil {
		t.Error("a document with no plan title was accepted")
	}
}

func TestApplyRecordsThePlannerRunAndRefreshesTheTitle(t *testing.T) {
	store := open(t)

	d := doc()
	d.Plan.PlannerRun = "run-1"
	applied(t, store, d, ApplyOptions{})

	d.Plan.Title = "Order placement, revised"
	result := applied(t, store, d, ApplyOptions{})
	if result.Plan.Title != "Order placement, revised" {
		t.Errorf("plan title = %q", result.Plan.Title)
	}

	plan, err := store.Plan("orders")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlannerRun != "run-1" || plan.Title != "Order placement, revised" {
		t.Errorf("plan = %+v", plan)
	}

	plans, err := store.Plans()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Errorf("re-applying created %d plans, want 1", len(plans))
	}
}

func TestFindCycle(t *testing.T) {
	if cycle := findCycle(map[int64][]int64{1: {2}, 2: {3}}); cycle != nil {
		t.Errorf("findCycle on a chain returned %v", cycle)
	}
	// A diamond is not a cycle: two paths converging is normal in a plan.
	if cycle := findCycle(map[int64][]int64{4: {2, 3}, 2: {1}, 3: {1}}); cycle != nil {
		t.Errorf("findCycle on a diamond returned %v", cycle)
	}
	if cycle := findCycle(map[int64][]int64{1: {2}, 2: {3}, 3: {1}}); len(cycle) != 3 {
		t.Errorf("findCycle on a loop returned %v, want three nodes", cycle)
	}
	if cycle := findCycle(map[int64][]int64{5: {6}, 6: {5}, 1: {2}}); len(cycle) != 2 {
		t.Errorf("findCycle returned %v, want the two-node loop", cycle)
	}
}
