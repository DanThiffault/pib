package issues

import (
	"errors"
	"testing"
)

// reviewer creates the plan's opening review issue.
func reviewer(t *testing.T, s *Store) Issue {
	t.Helper()
	issue, err := s.Create(NewIssue{Plan: "orders", Type: ReviewType, Title: "Review the plan"})
	if err != nil {
		t.Fatalf("Create reviewer: %v", err)
	}
	return issue
}

func closeIssue(t *testing.T, s *Store, number int64) {
	t.Helper()
	if _, _, err := s.CloseIssue(number, "done"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
}

// closingPass records a closing review of the plan that has ended.
func closingPass(t *testing.T, s *Store, status string) {
	t.Helper()
	if err := s.StartRun(RunStart{ID: "run-closing", Agent: reviewerAgent, Plan: "orders", Pass: PassClosing}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun("run-closing", status); err != nil {
		t.Fatal(err)
	}
}

// planState reads the orders plan's derived state out of the listing.
func planState(t *testing.T, s *Store, opts PlanStatusOptions) PlanState {
	t.Helper()
	list, err := s.PlanStatuses(true, opts)
	if err != nil {
		t.Fatalf("PlanStatuses: %v", err)
	}
	for _, status := range list {
		if status.Slug == "orders" {
			return status.State
		}
	}
	t.Fatalf("no plan row for orders in %+v", list)
	return ""
}

// The derivation in ADR-005 §1, one case per state it names. Every case
// starts from the same orders plan; setup puts it in the state wanted.
func TestPlanStateDerivation(t *testing.T) {
	cases := []struct {
		name string
		// review is the [plan] review setting the derivation runs with.
		review bool
		setup  func(t *testing.T, s *Store)
		want   PlanState
	}{
		{
			name:   "awaiting review while the gate issue is open",
			review: true,
			setup: func(t *testing.T, s *Store) {
				reviewer(t, s)
			},
			want: PlanAwaitingReview,
		},
		{
			name:   "under review by a run on the reviewer issue",
			review: true,
			setup: func(t *testing.T, s *Store) {
				gate := reviewer(t, s)
				if err := s.StartRun(RunStart{ID: "run-r", Issue: gate.Number, Agent: reviewerAgent, Window: "@3"}); err != nil {
					t.Fatal(err)
				}
			},
			want: PlanUnderReview,
		},
		{
			name:   "under review by a reviewer run with no issue",
			review: true,
			setup: func(t *testing.T, s *Store) {
				if err := s.StartRun(RunStart{ID: "run-r", Agent: reviewerAgent, Plan: "orders", Pass: PassOpening}); err != nil {
					t.Fatal(err)
				}
			},
			want: PlanUnderReview,
		},
		{
			name:   "in progress once the opening review closes",
			review: true,
			setup: func(t *testing.T, s *Store) {
				gate := reviewer(t, s)
				closeIssue(t, s, gate.Number)
				task(t, s, "Alpha")
			},
			want: PlanInProgress,
		},
		{
			name:   "in progress for a plan with no reviewer issue",
			review: true,
			setup: func(t *testing.T, s *Store) {
				task(t, s, "Alpha")
			},
			want: PlanInProgress,
		},
		{
			name:   "in progress when the plan asked for no review",
			review: false,
			setup: func(t *testing.T, s *Store) {
				task(t, s, "Alpha")
			},
			want: PlanInProgress,
		},
		{
			name:   "awaiting closing review once everything has closed",
			review: true,
			setup: func(t *testing.T, s *Store) {
				gate := reviewer(t, s)
				closeIssue(t, s, gate.Number)
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
			},
			want: PlanAwaitingClosingReview,
		},
		{
			name:   "a closing pass older than the last close settles nothing",
			review: true,
			setup: func(t *testing.T, s *Store) {
				freeze(t, "2026-08-29T12:00:00Z")
				closingPass(t, s, "done")
				freeze(t, "2026-08-29T13:00:00Z")
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
			},
			want: PlanAwaitingClosingReview,
		},
		{
			name:   "a closing pass that fails settles nothing",
			review: true,
			setup: func(t *testing.T, s *Store) {
				freeze(t, "2026-08-29T12:00:00Z")
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
				freeze(t, "2026-08-29T13:00:00Z")
				closingPass(t, s, "error")
			},
			want: PlanAwaitingClosingReview,
		},
		{
			name:   "complete once the closing pass ends done",
			review: true,
			setup: func(t *testing.T, s *Store) {
				freeze(t, "2026-08-29T12:00:00Z")
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
				freeze(t, "2026-08-29T13:00:00Z")
				closingPass(t, s, "done")
			},
			want: PlanComplete,
		},
		{
			name:   "the tie between the last close and the closing pass goes to the pass",
			review: true,
			setup: func(t *testing.T, s *Store) {
				freeze(t, "2026-08-29T12:00:00Z")
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
				closingPass(t, s, "done")
			},
			want: PlanComplete,
		},
		{
			name:   "complete when the plan asked for no review",
			review: false,
			setup: func(t *testing.T, s *Store) {
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
			},
			want: PlanComplete,
		},
		{
			name:   "back in progress when the closing pass files new issues",
			review: true,
			setup: func(t *testing.T, s *Store) {
				freeze(t, "2026-08-29T12:00:00Z")
				issue := task(t, s, "Alpha")
				closeIssue(t, s, issue.Number)
				freeze(t, "2026-08-29T13:00:00Z")
				closingPass(t, s, "done")
				task(t, s, "Follow-up")
			},
			want: PlanInProgress,
		},
		{
			name:   "archived overrides work still open",
			review: true,
			setup: func(t *testing.T, s *Store) {
				task(t, s, "Alpha")
				if err := s.ArchivePlan("orders"); err != nil {
					t.Fatal(err)
				}
			},
			want: PlanArchived,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := planned(t)
			tc.setup(t, store)
			if got := planState(t, store, PlanStatusOptions{PlanReview: tc.review}); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

// A planner that has not applied its plan yet is a placeholder row, so the
// run is reachable before the plan exists.
func TestAPlannerStillRunningIsAPlanningRow(t *testing.T) {
	store := planned(t)

	if err := store.StartRun(RunStart{ID: "run-plan", Agent: plannerAgent, Window: "@1"}); err != nil {
		t.Fatal(err)
	}
	// A planner that already applied — or already ended — is no placeholder.
	if err := store.StartRun(RunStart{ID: "run-applied", Agent: plannerAgent, Plan: "orders", Window: "@2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartRun(RunStart{ID: "run-ended", Agent: plannerAgent, Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun("run-ended", "done"); err != nil {
		t.Fatal(err)
	}

	list, err := store.PlanStatuses(false, PlanStatusOptions{PlanReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d rows, want the planning row ahead of the one plan: %+v", len(list), list)
	}
	row := list[0]
	if row.State != PlanPlanning {
		t.Errorf("first row = %q, want the planning placeholder", row.State)
	}
	if row.Run != "run-plan" || row.Title != "run-plan" {
		t.Errorf("row = %+v, want it titled by the run id", row)
	}
}

// Applying the plan a planner wrote links the run to the plan, and the
// placeholder disappears even though the planner is still open.
func TestApplyingLinksThePlannerRun(t *testing.T) {
	store := open(t)

	if err := store.StartRun(RunStart{ID: "run-plan", Agent: plannerAgent, Window: "@1"}); err != nil {
		t.Fatal(err)
	}

	result, err := store.Apply(Document{
		Plan:   DocPlan{Slug: "orders", Title: "Order placement", PlannerRun: "run-plan"},
		Issues: []DocIssue{{ID: "schema", Type: "task", Title: "Order schema"}},
	}, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Plan.PlannerRun != "run-plan" {
		t.Errorf("planner run = %q, want the run that applied", result.Plan.PlannerRun)
	}

	var plan string
	if err := store.db.QueryRow(`SELECT COALESCE(plan, '') FROM runs WHERE id = 'run-plan'`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan != "orders" {
		t.Errorf("runs.plan = %q, want orders", plan)
	}

	list, err := store.PlanStatuses(false, PlanStatusOptions{PlanReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].State != PlanInProgress {
		t.Errorf("list = %+v, want just the plan, in progress", list)
	}
}

// The counts come off the issue derivation itself, so the plan table and the
// issue table cannot disagree.
func TestPlanStatusCounts(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)

	task(t, store, "Ready")

	running := task(t, store, "Running")
	if err := store.StartRun(RunStart{ID: "run-1", Issue: running.Number, Agent: "coder", Window: "@3"}); err != nil {
		t.Fatal(err)
	}

	failed := task(t, store, "Failed")
	runOf(t, store, failed.Number, "run-2", "error")

	done := task(t, store, "Done")
	closeIssue(t, store, done.Number)

	list, err := store.PlanStatuses(false, PlanStatusOptions{PlanReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d rows, want 1", len(list))
	}
	status := list[0]
	if status.Ready != 1 || status.Running != 1 || status.NeedsAttention != 1 {
		t.Errorf("counts = ready %d running %d needs attention %d, want one of each",
			status.Ready, status.Running, status.NeedsAttention)
	}
	if status.State != PlanInProgress {
		t.Errorf("state = %q, want in progress", status.State)
	}
}

func TestArchivedPlansAreHiddenUnlessAskedFor(t *testing.T) {
	store := planned(t)
	task(t, store, "Alpha")

	events, stop := store.Subscribe()
	defer stop()

	if err := store.ArchivePlan("orders"); err != nil {
		t.Fatalf("ArchivePlan: %v", err)
	}

	list, err := store.PlanStatuses(false, PlanStatusOptions{PlanReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("list = %+v, want the archived plan hidden", list)
	}

	list, err = store.PlanStatuses(true, PlanStatusOptions{PlanReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].State != PlanArchived {
		t.Errorf("list = %+v, want the plan archived when asked for", list)
	}
	if list[0].ArchivedAt.IsZero() {
		t.Error("an archived plan carries when")
	}

	select {
	case e := <-events:
		if e.Kind != EventPlan || e.Plan != "orders" {
			t.Errorf("event = %+v, want the plan change named", e)
		}
	default:
		t.Error("archiving published nothing")
	}

	if err := store.UnarchivePlan("orders"); err != nil {
		t.Fatalf("UnarchivePlan: %v", err)
	}
	plan, err := store.Plan("orders")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ArchivedAt.IsZero() {
		t.Error("unarchiving left the timestamp behind")
	}

	if err := store.ArchivePlan("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("archiving a plan that does not exist gave %v, want ErrNotFound", err)
	}
}

// A run on an issue is traced to the issue's plan whatever the caller said;
// a run on the reviewer issue is the opening pass.
func TestARunOnAnIssueTakesTheIssuesPlan(t *testing.T) {
	store := planned(t)

	issue := task(t, store, "Alpha")
	if err := store.StartRun(RunStart{ID: "run-1", Issue: issue.Number, Agent: "coder", Plan: "wrong", Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err := store.db.QueryRow(`SELECT COALESCE(plan, '') FROM runs WHERE id = 'run-1'`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan != "orders" {
		t.Errorf("runs.plan = %q, want the issue's plan", plan)
	}

	gate := reviewer(t, store)
	if err := store.StartRun(RunStart{ID: "run-2", Issue: gate.Number, Agent: reviewerAgent, Window: "@4"}); err != nil {
		t.Fatal(err)
	}
	var pass string
	if err := store.db.QueryRow(`SELECT COALESCE(pass, '') FROM runs WHERE id = 'run-2'`).Scan(&pass); err != nil {
		t.Fatal(err)
	}
	if pass != PassOpening {
		t.Errorf("runs.pass = %q, want the opening pass", pass)
	}

	if err := store.StartRun(RunStart{ID: "run-3", Agent: reviewerAgent, Plan: "orders", Pass: "sideways"}); err == nil {
		t.Error("a pass that is neither opening nor closing was accepted")
	}
}
