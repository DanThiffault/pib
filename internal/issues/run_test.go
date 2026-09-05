package issues

import (
	"reflect"
	"testing"
)

func TestStartRunPutsAnIssueInProgress(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	status, err := store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !status.InProgress || status.Ready {
		t.Errorf("status = in progress %v ready %v", status.InProgress, status.Ready)
	}
	if status.Run != "run-1" {
		t.Errorf("run = %q", status.Run)
	}

	if err := store.FinishRun("run-1", "done"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	status, err = store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.InProgress || !status.Ready {
		t.Errorf("after finishing = in progress %v ready %v", status.InProgress, status.Ready)
	}
}

func TestRunsKeepEveryAttempt(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun("run-1", "error"); err != nil {
		t.Fatal(err)
	}
	if err := store.StartRun("run-2", issue.Number, "coder", "@4"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun("run-2", "done"); err != nil {
		t.Fatal(err)
	}

	runs, err := store.Runs(issue.Number)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want the failed attempt kept alongside the good one", len(runs))
	}
	if runs[0].Status != "error" || runs[1].Status != "done" {
		t.Errorf("runs = %+v", runs)
	}
	if runs[0].Agent != "coder" || runs[0].Window != "@3" {
		t.Errorf("run = %+v", runs[0])
	}
	if runs[0].EndedAt.IsZero() {
		t.Error("a finished run has no end time")
	}
}

func TestResumingAgentReusesItsRun(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun("run-1", "needs_input"); err != nil {
		t.Fatal(err)
	}

	// A resume knows the session but not the issue; the row keeps it.
	if err := store.StartRun("run-1", 0, "coder", "@7"); err != nil {
		t.Fatalf("resuming: %v", err)
	}

	runs, err := store.Runs(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want the resumed one to be the same row", len(runs))
	}
	if !runs[0].EndedAt.IsZero() || runs[0].Status != "" {
		t.Errorf("run = %+v, want it live again", runs[0])
	}
	if runs[0].Window != "@7" {
		t.Errorf("window = %q, want the new one", runs[0].Window)
	}

	status, err := store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !status.InProgress {
		t.Error("a resumed agent should put the issue back in progress")
	}
}

func TestOpeningTheStoreClosesOrphanedRuns(t *testing.T) {
	dir := DataDir(t.TempDir())

	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CreatePlan(NewPlan{Slug: "orders", Title: "Order placement", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}
	issue := task(t, first, "Alpha")
	if err := first.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	// A crash: the run is never finished.
	first.Close()

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	runs, err := second.Runs(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "unknown" || runs[0].EndedAt.IsZero() {
		t.Fatalf("run = %+v, want it closed out as unknown", runs)
	}

	status, err := second.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.InProgress || !status.Ready {
		t.Errorf("status = in progress %v ready %v; a dead process must not hold an issue",
			status.InProgress, status.Ready)
	}
}

func TestRunRecordsAreChecked(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	if err := store.StartRun("", issue.Number, "coder", ""); err == nil {
		t.Error("a run with no id was accepted")
	}
	if err := store.StartRun("run-1", issue.Number, "", ""); err == nil {
		t.Error("a run with no agent was accepted")
	}
	if err := store.StartRun("run-1", 404, "coder", ""); err == nil {
		t.Error("a run against an issue that does not exist was accepted")
	}

	// An unrecognised outcome is recorded as unknown rather than rejected.
	if err := store.StartRun("run-1", issue.Number, "coder", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun("run-1", "exploded"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	runs, err := store.Runs(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != "unknown" {
		t.Errorf("status = %q, want unknown", runs[0].Status)
	}
}

func TestARunWithNoIssueIsStillRecorded(t *testing.T) {
	store := planned(t)

	// The planner itself is not working on an issue.
	if err := store.StartRun("run-1", 0, "planner", "@1"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := store.FinishRun("run-1", "done"); err != nil {
		t.Fatal(err)
	}

	live, err := store.liveRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Errorf("live runs = %v, want none", live)
	}
	if got, err := store.Runs(0); err != nil || len(got) != 0 {
		t.Errorf("Runs(0) = %v, %v; a run with no issue belongs to none", got, err)
	}
}

func TestOrphanCleanupLeavesFinishedRunsAlone(t *testing.T) {
	dir := DataDir(t.TempDir())

	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CreatePlan(NewPlan{Slug: "orders", Title: "Order placement", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}
	issue := task(t, first, "Alpha")
	if err := first.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	if err := first.FinishRun("run-1", "done"); err != nil {
		t.Fatal(err)
	}
	before, err := first.Runs(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	after, err := second.Runs(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a finished run changed on reopen:\n%+v\n%+v", before, after)
	}
}
