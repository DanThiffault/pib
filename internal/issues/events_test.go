package issues

import (
	"context"
	"testing"
	"time"
)

// watched subscribes to a store's change events and drains anything already
// queued, so a test can assert on what a single write produced.
func watched(t *testing.T, s *Store) <-chan Event {
	t.Helper()
	events, unsubscribe := s.Subscribe()
	t.Cleanup(unsubscribe)
	return events
}

// next waits for one event, failing the test if none arrives.
func next(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-events:
		if !ok {
			t.Fatal("the subscription was closed")
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event was published")
		return Event{}
	}
}

// wantEvent asserts that the next event names what the caller expects.
func wantEvent(t *testing.T, events <-chan Event, kind EventKind, plan string, issue int64) {
	t.Helper()
	e := next(t, events)
	if e.Kind != kind || e.Plan != plan || e.Issue != issue {
		t.Errorf("event = %+v, want kind %q plan %q issue %d", e, kind, plan, issue)
	}
}

// quiet asserts that a write published nothing.
func quiet(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case e := <-events:
		t.Errorf("unexpected event %+v", e)
	default:
	}
}

func TestCreatePlanPublishes(t *testing.T) {
	store := open(t)
	events := watched(t, store)

	if _, err := store.CreatePlan(NewPlan{Slug: "orders", Title: "Order placement"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	wantEvent(t, events, EventPlan, "orders", 0)
}

func TestCreateIssuePublishes(t *testing.T) {
	store := planned(t)
	events := watched(t, store)

	issue := task(t, store, "Schema")

	wantEvent(t, events, EventIssue, "orders", issue.Number)
}

func TestApplyPublishesThePlanAndItsIssues(t *testing.T) {
	store := open(t)
	events := watched(t, store)

	result := applied(t, store, doc(), ApplyOptions{})

	wantEvent(t, events, EventPlan, "orders", 0)
	published := map[int64]bool{}
	for _, number := range result.Created {
		published[number] = true
	}
	for range result.Created {
		e := next(t, events)
		if e.Kind != EventIssue || e.Plan != "orders" || !published[e.Issue] {
			t.Errorf("event = %+v, want an issue from the document", e)
		}
		delete(published, e.Issue)
	}
	if len(published) != 0 {
		t.Errorf("issues %v were never published", published)
	}
}

func TestApplyPublishesAnUpdatedIssue(t *testing.T) {
	store := open(t)
	applied(t, store, doc(), ApplyOptions{})
	events := watched(t, store)

	second := doc()
	second.Issues[0].Title = "Feature: order placement, revisited"
	result := applied(t, store, second, ApplyOptions{})

	wantEvent(t, events, EventPlan, "orders", 0)
	wantEvent(t, events, EventIssue, "orders", result.Updated[0])
}

func TestEditPublishes(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")
	events := watched(t, store)

	title := "Order schema"
	if _, err := store.Edit(issue.Number, Edit{Title: &title}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	wantEvent(t, events, EventIssue, "orders", issue.Number)
}

func TestCommentPublishes(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")
	events := watched(t, store)

	if err := store.Comment(issue.Number, "coder", "Done."); err != nil {
		t.Fatalf("Comment: %v", err)
	}

	wantEvent(t, events, EventIssue, "orders", issue.Number)
}

func TestCloseAndReopenPublish(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")

	events := watched(t, store)
	if _, _, err := store.CloseIssue(issue.Number, "not needed"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	wantEvent(t, events, EventIssue, "orders", issue.Number)

	// Closing an already closed issue writes nothing, so it says nothing.
	if _, _, err := store.CloseIssue(issue.Number, ""); err != nil {
		t.Fatalf("CloseIssue on a closed issue: %v", err)
	}
	quiet(t, events)

	if _, err := store.ReopenIssue(issue.Number); err != nil {
		t.Fatalf("ReopenIssue: %v", err)
	}
	wantEvent(t, events, EventIssue, "orders", issue.Number)
}

func TestLinkPRPublishes(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")
	events := watched(t, store)

	if _, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatalf("LinkPR: %v", err)
	}

	wantEvent(t, events, EventIssue, "orders", issue.Number)
}

func TestReviewCyclePublishes(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")
	events := watched(t, store)

	review := reviewed(t, store, issue.Number, "https://github.com/o/r/pull/1", "")
	wantEvent(t, events, EventReview, "orders", issue.Number)

	if _, err := store.CloseReview(review.ID, VerdictChanges, 2); err != nil {
		t.Fatalf("CloseReview: %v", err)
	}
	wantEvent(t, events, EventReview, "orders", issue.Number)
}

func TestRunStartAndEndPublish(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")
	events := watched(t, store)

	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	wantEvent(t, events, EventRun, "orders", issue.Number)

	if err := store.FinishRun("run-1", "done"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	wantEvent(t, events, EventRun, "orders", issue.Number)
}

func TestARunWithNoIssuePublishesStartAndEnd(t *testing.T) {
	store := planned(t)
	events := watched(t, store)

	// A planner run belongs to no issue, and it is the planning row ending
	// that a subscriber needs to hear about: a planner that quits without
	// applying leaves a placeholder to take down.
	if err := store.StartRun("run-planner", 0, "planner", "@1"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	wantEvent(t, events, EventRun, "", 0)

	if err := store.FinishRun("run-planner", "needs_input"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	wantEvent(t, events, EventRun, "", 0)
}

func TestEndingAnUnknownRunStillPublishes(t *testing.T) {
	store := planned(t)
	events := watched(t, store)

	// Nothing was written, so there is no identity to read back. The event
	// still goes out: a subscriber reloading is cheaper than one left
	// showing a run that has ended.
	if err := store.FinishRun("run-that-never-existed", "done"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	wantEvent(t, events, EventRun, "", 0)
}

func TestReindexPublishesForEachRefreshedFile(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Schema")

	// A hand edit to the file is what reindex exists to notice.
	path := store.abs(issue.Path)
	file, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Title = "Order schema, revised"
	if err := WriteFile(path, file); err != nil {
		t.Fatal(err)
	}

	events := watched(t, store)
	refreshed, err := store.Reindex("orders")
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if refreshed != 1 {
		t.Fatalf("refreshed %d issues, want 1", refreshed)
	}

	wantEvent(t, events, EventIssue, "orders", issue.Number)
}

func TestReconcilePublishesOnEveryPrStateWrite(t *testing.T) {
	store := planned(t)
	open1 := linked(t, store, "Schema", "https://github.com/o/r/pull/1")
	merged := linked(t, store, "Aggregate", "https://github.com/o/r/pull/2")

	github := &fakeGitHub{states: map[string]string{
		"https://github.com/o/r/pull/1": "open",
		"https://github.com/o/r/pull/2": "merged",
	}}

	events := watched(t, store)
	if _, err := store.Reconcile(context.Background(), Filter{}, ReconcileOptions{Lookup: github}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	published := map[int64]bool{}
	for range 2 {
		e := next(t, events)
		if e.Kind != EventIssue || e.Plan != "orders" {
			t.Errorf("event = %+v, want an issue event", e)
		}
		published[e.Issue] = true
	}
	if !published[open1.Number] || !published[merged.Number] {
		t.Errorf("published %v, want both issues", published)
	}
}

func TestAStalledSubscriberDoesNotBlockAWriter(t *testing.T) {
	store := planned(t)
	events, unsubscribe := store.Subscribe()
	defer unsubscribe()

	// More writes than the buffer holds, with nothing reading them: the
	// writer must finish, and the oldest events are the ones dropped.
	issued := make([]int64, 0, eventBuffer*2)
	for i := range eventBuffer * 2 {
		issue := task(t, store, "Task "+string(rune('a'+i%26))+string(rune('a'+i/26)))
		issued = append(issued, issue.Number)
	}

	kept := len(events)
	if kept != eventBuffer {
		t.Errorf("buffered %d events, want the buffer filled to %d", kept, eventBuffer)
	}

	// The writer is not stuck: anything after the drop still lands.
	if _, err := store.Edit(issued[0], Edit{Body: ptr("changed")}); err != nil {
		t.Fatalf("Edit after a stalled subscriber: %v", err)
	}
	e := next(t, events)
	if e.Issue != issued[0] {
		t.Errorf("event = %+v, want the write that came after the drop", e)
	}
}

func TestUnsubscribingStopsDelivery(t *testing.T) {
	store := planned(t)
	events, unsubscribe := store.Subscribe()

	task(t, store, "Schema")
	next(t, events)

	unsubscribe()
	unsubscribe() // idempotent: closing twice would panic on a live channel.

	task(t, store, "Aggregate")
	if _, ok := <-events; ok {
		t.Error("an event arrived after the subscription ended")
	}
}

func TestSubscribersAreIndependent(t *testing.T) {
	store := planned(t)
	first := watched(t, store)
	second := watched(t, store)

	issue := task(t, store, "Schema")

	wantEvent(t, first, EventIssue, "orders", issue.Number)
	wantEvent(t, second, EventIssue, "orders", issue.Number)
}
