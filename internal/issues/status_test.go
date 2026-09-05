package issues

import (
	"reflect"
	"strings"
	"testing"
)

// agents is the mapping a caller passes in: pib's configuration lives
// outside the store.
var agents = StatusOptions{AgentFor: func(issueType string) (string, bool) {
	switch issueType {
	case "task":
		return "coder", true
	case "research":
		return "researcher", true
	case "feature":
		// A container type: known, but nothing runs it.
		return "", false
	}
	return "", false
}}

// fixture builds a graph with a diamond, a cycle, and a container issue:
//
//	#1 base ─┬─ #2 left ─┐
//	         └─ #3 right ─┴─ #4 join      #5 ⇄ #6 (a loop)      #7 feature
//
// Arrows read "blocks": #2 cannot start until #1 closes.
func fixture(t *testing.T) *Store {
	t.Helper()
	store := planned(t)

	base := task(t, store, "Base")
	left, err := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "Left", BlockedBy: []int64{base.Number}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "Right", BlockedBy: []int64{base.Number}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewIssue{
		Plan: "orders", Type: "task", Title: "Join",
		BlockedBy: []int64{left.Number, right.Number},
	}); err != nil {
		t.Fatal(err)
	}

	loopA := task(t, store, "Loop A")
	loopB, err := store.Create(NewIssue{Plan: "orders", Type: "task", Title: "Loop B", BlockedBy: []int64{loopA.Number}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Edit(loopA.Number, Edit{AddBlockedBy: []int64{loopB.Number}}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Create(NewIssue{Plan: "orders", Type: "feature", Title: "Feature"}); err != nil {
		t.Fatal(err)
	}

	return store
}

// numbers pulls the issue numbers out of a status list.
func numbers(list []Status) []int64 {
	out := make([]int64, 0, len(list))
	for _, status := range list {
		out = append(out, status.Number)
	}
	return out
}

// ready is the ready set of the whole store.
func ready(t *testing.T, s *Store) []int64 {
	t.Helper()
	list, err := s.Ready(Filter{}, agents)
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	return numbers(list)
}

func TestReadinessFollowsTheDiamond(t *testing.T) {
	store := fixture(t)

	// #1 has no blockers; #7 is a container with none either. Everything in
	// the diamond below #1 waits, and the loop waits on itself.
	if got, want := ready(t, store), []int64{1, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("ready = %v, want %v", got, want)
	}

	if _, _, err := store.CloseIssue(1, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := ready(t, store), []int64{2, 3, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("after closing #1, ready = %v, want %v", got, want)
	}

	// Closing one arm of the diamond is not enough for the join.
	if _, _, err := store.CloseIssue(2, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := ready(t, store), []int64{3, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("after closing #2, ready = %v, want %v", got, want)
	}

	if _, _, err := store.CloseIssue(3, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := ready(t, store), []int64{4, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("after closing #3, ready = %v, want %v", got, want)
	}
}

func TestACycleIsNeverReady(t *testing.T) {
	store := fixture(t)

	for _, number := range []int64{5, 6} {
		status, err := store.Status(number, agents)
		if err != nil {
			t.Fatal(err)
		}
		if !status.Blocked || status.Ready {
			t.Errorf("#%d: blocked=%v ready=%v, want a blocked issue", number, status.Blocked, status.Ready)
		}
	}

	// Closing every other issue does not free the loop.
	for _, number := range []int64{1, 2, 3, 4, 7} {
		if _, _, err := store.CloseIssue(number, ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := ready(t, store); len(got) != 0 {
		t.Errorf("ready = %v, want nothing: only the loop is left", got)
	}

	cycles, err := store.Cycles("orders")
	if err != nil {
		t.Fatalf("Cycles: %v", err)
	}
	if len(cycles) != 1 || len(cycles[0]) != 2 {
		t.Errorf("Cycles = %v, want one loop of two", cycles)
	}
}

func TestBlockersReportOnlyWhatIsStillOpen(t *testing.T) {
	store := fixture(t)

	join, err := store.Status(4, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(join.BlockedBy, []int64{2, 3}) {
		t.Errorf("blocked by %v, want [2 3]", join.BlockedBy)
	}
	if !reflect.DeepEqual(join.OpenBlockers, []int64{2, 3}) {
		t.Errorf("open blockers %v, want [2 3]", join.OpenBlockers)
	}

	if _, _, err := store.CloseIssue(2, ""); err != nil {
		t.Fatal(err)
	}
	join, err = store.Status(4, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(join.BlockedBy, []int64{2, 3}) {
		t.Errorf("blocked by %v; the edge should survive its blocker closing", join.BlockedBy)
	}
	if !reflect.DeepEqual(join.OpenBlockers, []int64{3}) {
		t.Errorf("open blockers %v, want [3]", join.OpenBlockers)
	}
}

func TestARunningAgentTakesAnIssueOutOfTheReadySet(t *testing.T) {
	store := fixture(t)

	if _, err := store.db.Exec(
		`INSERT INTO runs (id, issue, agent, started_at) VALUES ('run-1', 1, 'coder', ?)`, stamp); err != nil {
		t.Fatal(err)
	}

	status, err := store.Status(1, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !status.InProgress || status.Ready {
		t.Errorf("#1: in progress=%v ready=%v", status.InProgress, status.Ready)
	}
	if status.Run != "run-1" {
		t.Errorf("run = %q, want run-1", status.Run)
	}
	if got, want := ready(t, store), []int64{7}; !reflect.DeepEqual(got, want) {
		t.Errorf("ready = %v, want %v", got, want)
	}

	running, err := store.InProgress(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(numbers(running), []int64{1}) {
		t.Errorf("in progress = %v, want [1]", numbers(running))
	}

	// A finished run releases the issue.
	if _, err := store.db.Exec(
		`UPDATE runs SET ended_at = ?, status = 'error' WHERE id = 'run-1'`, stamp); err != nil {
		t.Fatal(err)
	}
	if got, want := ready(t, store), []int64{1, 7}; !reflect.DeepEqual(got, want) {
		t.Errorf("after the run ended, ready = %v, want %v", got, want)
	}
}

func TestAPullRequestHoldsAnIssueUntilItIsSettled(t *testing.T) {
	store := fixture(t)

	if _, err := store.LinkPR(1, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(1, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !status.AwaitingReview || status.Ready {
		t.Errorf("#1: awaiting=%v ready=%v", status.AwaitingReview, status.Ready)
	}

	waiting, err := store.AwaitingReview(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(numbers(waiting), []int64{1}) {
		t.Errorf("awaiting review = %v, want [1]", numbers(waiting))
	}

	// A merged pull request settles it. Closing the issue is reconciliation's
	// job; readiness only cares that nothing is pending any more.
	if _, err := store.db.Exec(`UPDATE issues SET pr_state = 'merged' WHERE number = 1`); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status(1, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.AwaitingReview || !status.Ready {
		t.Errorf("#1: awaiting=%v ready=%v, want a settled and ready issue", status.AwaitingReview, status.Ready)
	}
}

func TestLaunchableNeedsAMappedType(t *testing.T) {
	store := fixture(t)

	list, err := store.Ready(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}

	for _, status := range list {
		switch status.Number {
		case 1:
			if !status.Launchable || status.Agent != "coder" {
				t.Errorf("#1: launchable=%v agent=%q, want the coder", status.Launchable, status.Agent)
			}
		case 7:
			if status.Launchable || status.Agent != "" {
				t.Errorf("#7 is a container: launchable=%v agent=%q", status.Launchable, status.Agent)
			}
		}
	}

	// Without a mapping the store says nothing about agents at all.
	bare, err := store.Ready(Filter{}, StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range bare {
		if status.Launchable || status.Agent != "" {
			t.Errorf("#%d reported an agent with no mapping supplied", status.Number)
		}
	}
}

func TestAgentExplainsWhyAnIssueCannotRun(t *testing.T) {
	store := fixture(t)

	agent, err := store.Agent(1, agents)
	if err != nil || agent != "coder" {
		t.Errorf("Agent(#1) = %q, %v; want the coder", agent, err)
	}

	if _, err := store.Agent(4, agents); err == nil || !strings.Contains(err.Error(), "blocked by #2 and #3") {
		t.Errorf("Agent(#4) = %v, want the open blockers named", err)
	}
	if _, err := store.Agent(7, agents); err == nil || !strings.Contains(err.Error(), `type "feature"`) {
		t.Errorf("Agent(#7) = %v, want the unmapped type named", err)
	}

	if _, _, err := store.CloseIssue(1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Agent(1, agents); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Agent on a closed issue = %v", err)
	}

	if _, err := store.db.Exec(
		`INSERT INTO runs (id, issue, agent, started_at) VALUES ('run-1', 2, 'coder', ?)`, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Agent(2, agents); err == nil || !strings.Contains(err.Error(), "already working") {
		t.Errorf("Agent on a running issue = %v", err)
	}
}

func TestStatusFiltersByPlanStateAndType(t *testing.T) {
	store := fixture(t)
	if _, err := store.CreatePlan(NewPlan{Slug: "billing", Title: "Billing", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewIssue{Plan: "billing", Type: "research", Title: "Compare"}); err != nil {
		t.Fatal(err)
	}

	if got := numbers(mustStatuses(t, store, Filter{Plan: "billing"})); !reflect.DeepEqual(got, []int64{8}) {
		t.Errorf("billing = %v, want [8]", got)
	}
	if got := numbers(mustStatuses(t, store, Filter{Type: "feature"})); !reflect.DeepEqual(got, []int64{7}) {
		t.Errorf("features = %v, want [7]", got)
	}

	// Readiness respects the same filters.
	list, err := store.Ready(Filter{Plan: "billing"}, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(numbers(list), []int64{8}) {
		t.Errorf("billing ready = %v, want [8]", numbers(list))
	}
	if list[0].Agent != "researcher" || !list[0].Launchable {
		t.Errorf("#8 = %+v, want a launchable researcher issue", list[0])
	}
}

func TestBlockedListing(t *testing.T) {
	store := fixture(t)

	blocked, err := store.Blocked(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := numbers(blocked), []int64{2, 3, 4, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Errorf("blocked = %v, want %v", got, want)
	}

	// A closed issue is not reported as blocked, whatever it waits on.
	if _, _, err := store.CloseIssue(2, ""); err != nil {
		t.Fatal(err)
	}
	blocked, err = store.Blocked(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range blocked {
		if status.Number == 2 {
			t.Error("a closed issue was reported as blocked")
		}
	}
}

func TestStatusNotFound(t *testing.T) {
	store := fixture(t)
	if _, err := store.Status(99, agents); err == nil {
		t.Error("Status on a missing issue succeeded")
	}
}

func mustStatuses(t *testing.T, s *Store, f Filter) []Status {
	t.Helper()
	list, err := s.Statuses(f, agents)
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	return list
}
