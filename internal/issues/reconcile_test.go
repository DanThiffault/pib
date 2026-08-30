package issues

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub answers pull request lookups from a table, and records how
// often it was asked.
type fakeGitHub struct {
	mu       sync.Mutex
	states   map[string]string
	err      error
	calls    int
	inFlight int
	peak     int
	delay    time.Duration
}

func (f *fakeGitHub) State(ctx context.Context, url string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.inFlight++
	if f.inFlight > f.peak {
		f.peak = f.inFlight
	}
	f.mu.Unlock()

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--

	if f.err != nil {
		return "", f.err
	}
	state, ok := f.states[url]
	if !ok {
		return "open", nil
	}
	return state, nil
}

func (f *fakeGitHub) seen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// linked creates a task with a pull request already attached.
func linked(t *testing.T, s *Store, title, url string) Issue {
	t.Helper()
	issue := task(t, s, title)
	if _, err := s.LinkPR(issue.Number, url); err != nil {
		t.Fatal(err)
	}
	return issue
}

func TestReconcileClosesAMergedPullRequest(t *testing.T) {
	store := planned(t)
	blocker := linked(t, store, "Schema", "https://github.com/o/r/pull/1")

	dependent, err := store.Create(NewIssue{
		Plan: "orders", Type: "task", Title: "Aggregate", BlockedBy: []int64{blocker.Number},
	})
	if err != nil {
		t.Fatal(err)
	}

	github := &fakeGitHub{states: map[string]string{"https://github.com/o/r/pull/1": "merged"}}
	result, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !reflect.DeepEqual(result.Closed, []int64{blocker.Number}) {
		t.Errorf("closed %v, want [%d]", result.Closed, blocker.Number)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v", result.Warnings)
	}

	got, err := store.Issue(blocker.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateClosed || got.PRState != "merged" || got.ClosedAt.IsZero() {
		t.Errorf("issue = %+v, want closed and merged", got)
	}

	// The merge is recorded where the history lives.
	comments, err := store.Comments(blocker.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "merged") {
		t.Errorf("comments = %#v, want the merge recorded", comments)
	}

	// Closing the blocker frees what it was holding up.
	status, err := store.Status(dependent.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready {
		t.Errorf("#%d is still blocked after its blocker merged", dependent.Number)
	}
}

func TestReconcileLeavesAnOpenPullRequestAlone(t *testing.T) {
	store := planned(t)
	issue := linked(t, store, "Schema", "https://github.com/o/r/pull/1")

	github := &fakeGitHub{states: map[string]string{"https://github.com/o/r/pull/1": "open"}}
	result, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Closed) != 0 || len(result.Checked) != 1 {
		t.Errorf("result = %+v, want one check and no closures", result)
	}

	status, err := store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if !status.AwaitingReview || status.Ready {
		t.Errorf("status = awaiting %v ready %v, want it still waiting", status.AwaitingReview, status.Ready)
	}
}

func TestAnAbandonedPullRequestReleasesTheIssue(t *testing.T) {
	store := planned(t)
	issue := linked(t, store, "Schema", "https://github.com/o/r/pull/1")

	github := &fakeGitHub{states: map[string]string{"https://github.com/o/r/pull/1": "closed"}}
	if _, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github}); err != nil {
		t.Fatal(err)
	}

	// A pull request closed without merging means the work was abandoned:
	// the issue stays open and can be picked up again.
	status, err := store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateOpen {
		t.Errorf("state = %q, want the issue still open", status.State)
	}
	if status.AwaitingReview || !status.Ready {
		t.Errorf("status = awaiting %v ready %v, want it ready again", status.AwaitingReview, status.Ready)
	}
}

func TestTheWindowStopsRepeatedLookups(t *testing.T) {
	store := planned(t)
	linked(t, store, "Schema", "https://github.com/o/r/pull/1")

	github := &fakeGitHub{states: map[string]string{"https://github.com/o/r/pull/1": "open"}}
	opts := ReconcileOptions{Lookup: github, Window: time.Minute}

	for i := 0; i < 3; i++ {
		if _, err := store.Reconcile(context.Background(), Filter{}, opts); err != nil {
			t.Fatal(err)
		}
	}
	if github.seen() != 1 {
		t.Errorf("gh was asked %d times, want 1 inside the window", github.seen())
	}

	// Force ignores the window.
	opts.Force = true
	if _, err := store.Reconcile(context.Background(), Filter{}, opts); err != nil {
		t.Fatal(err)
	}
	if github.seen() != 2 {
		t.Errorf("gh was asked %d times, want a forced pass to check again", github.seen())
	}

	// So does a window that has passed.
	opts.Force, opts.Window = false, time.Nanosecond
	if _, err := store.Reconcile(context.Background(), Filter{}, opts); err != nil {
		t.Fatal(err)
	}
	if github.seen() != 3 {
		t.Errorf("gh was asked %d times, want a check once the window lapsed", github.seen())
	}
}

func TestAFailedLookupWarnsAndChangesNothing(t *testing.T) {
	store := planned(t)
	first := linked(t, store, "One", "https://github.com/o/r/pull/1")
	second := linked(t, store, "Two", "https://github.com/o/r/pull/2")

	github := &fakeGitHub{err: errors.New("gh is not available")}
	result, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github})
	if err != nil {
		t.Fatalf("Reconcile returned an error rather than degrading: %v", err)
	}

	// One warning for the shared failure, naming both issues.
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "#1 and #2") {
		t.Errorf("warning = %q, want both issues named", result.Warnings[0])
	}
	if len(result.Checked) != 0 || len(result.Closed) != 0 {
		t.Errorf("result = %+v, want nothing settled", result)
	}

	for _, number := range []int64{first.Number, second.Number} {
		issue, err := store.Issue(number)
		if err != nil {
			t.Fatal(err)
		}
		if issue.State != StateOpen || issue.PRState != "open" || !issue.PRCheckedAt.IsZero() {
			t.Errorf("#%d = %+v, want it untouched", number, issue)
		}
	}
}

func TestDistinctFailuresAreReportedSeparately(t *testing.T) {
	store := planned(t)
	linked(t, store, "One", "https://github.com/o/r/pull/1")
	linked(t, store, "Two", "https://github.com/o/r/pull/2")

	github := &failByURL{errs: map[string]string{
		"https://github.com/o/r/pull/1": "no such pull request",
		"https://github.com/o/r/pull/2": "not authenticated",
	}}
	result, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 2 {
		t.Errorf("warnings = %v, want two distinct failures", result.Warnings)
	}
}

type failByURL struct{ errs map[string]string }

func (f *failByURL) State(_ context.Context, url string) (string, error) {
	return "", errors.New(f.errs[url])
}

func TestLookupsAreBounded(t *testing.T) {
	store := planned(t)
	for i := 0; i < 8; i++ {
		linked(t, store, "Task", "https://github.com/o/r/pull/"+string(rune('a'+i)))
	}

	github := &fakeGitHub{delay: 20 * time.Millisecond}
	if _, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{
		Lookup: github, Parallel: 3,
	}); err != nil {
		t.Fatal(err)
	}

	if github.seen() != 8 {
		t.Errorf("gh was asked %d times, want 8", github.seen())
	}
	if github.peak > 3 {
		t.Errorf("%d lookups ran at once, want at most 3", github.peak)
	}
	if github.peak < 2 {
		t.Errorf("lookups did not overlap at all (peak %d); they should run in parallel", github.peak)
	}
}

func TestReconcileWithoutALookupDoesNothing(t *testing.T) {
	store := planned(t)
	linked(t, store, "Schema", "https://github.com/o/r/pull/1")

	result, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{})
	if err != nil {
		t.Fatalf("Reconcile with no lookup: %v", err)
	}
	if len(result.Checked) != 0 {
		t.Errorf("result = %+v, want nothing done", result)
	}
}

func TestReconcileFiltersByPlan(t *testing.T) {
	store := planned(t)
	if _, err := store.CreatePlan(NewPlan{Slug: "billing", Title: "Billing", PlannerRun: ""}); err != nil {
		t.Fatal(err)
	}
	linked(t, store, "Orders task", "https://github.com/o/r/pull/1")

	other, err := store.Create(NewIssue{Plan: "billing", Type: "task", Title: "Billing task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPR(other.Number, "https://github.com/o/r/pull/2"); err != nil {
		t.Fatal(err)
	}

	github := &fakeGitHub{states: map[string]string{
		"https://github.com/o/r/pull/1": "merged",
		"https://github.com/o/r/pull/2": "merged",
	}}
	result, err := store.Reconcile(context.Background(), Filter{Plan: "billing"}, ReconcileOptions{Lookup: github})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Closed, []int64{other.Number}) {
		t.Errorf("closed %v, want only the billing issue", result.Closed)
	}
}

func TestClosedIssuesAreNotReconciled(t *testing.T) {
	store := planned(t)
	issue := linked(t, store, "Schema", "https://github.com/o/r/pull/1")
	if _, _, err := store.CloseIssue(issue.Number, ""); err != nil {
		t.Fatal(err)
	}

	github := &fakeGitHub{}
	if _, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github}); err != nil {
		t.Fatal(err)
	}
	if github.seen() != 0 {
		t.Errorf("gh was asked about a closed issue %d times", github.seen())
	}
}

// recorder captures closes for the hook test below.
type recorder struct{ closed []Issue }

func (r *recorder) IssueClosed(issue Issue) { r.closed = append(r.closed, issue) }

// The two close paths do not share code: one is CloseIssue, the other a raw
// update inside reconciliation. A hook wired to only one would miss every
// task, since a task closes on a merged pull request.
func TestBothClosePathsNotifyTheHook(t *testing.T) {
	store := planned(t)
	merged := linked(t, store, "Build", "https://github.com/o/r/pull/1")

	explicit, err := store.Create(NewIssue{Plan: "orders", Type: "research", Title: "Decide"})
	if err != nil {
		t.Fatal(err)
	}

	var seen recorder
	store.OnClosed = &seen

	if _, _, err := store.CloseIssue(explicit.Number, "done"); err != nil {
		t.Fatal(err)
	}
	github := &fakeGitHub{states: map[string]string{"https://github.com/o/r/pull/1": "merged"}}
	if _, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github}); err != nil {
		t.Fatal(err)
	}

	if len(seen.closed) != 2 {
		t.Fatalf("hook saw %d closes, want one from each path: %+v", len(seen.closed), seen.closed)
	}
	for i, want := range []int64{explicit.Number, merged.Number} {
		if seen.closed[i].Number != want {
			t.Errorf("close %d was #%d, want #%d", i, seen.closed[i].Number, want)
		}
		if seen.closed[i].State != StateClosed {
			t.Errorf("#%d reached the hook as %q, want it already closed",
				seen.closed[i].Number, seen.closed[i].State)
		}
	}
}
