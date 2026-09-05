package issues

import (
	"errors"
	"testing"
)

// reviewed opens a cycle on an issue's linked pull request, failing the test
// if the store will not.
func reviewed(t *testing.T, s *Store, issue int64, prURL, run string) Review {
	t.Helper()
	review, err := s.OpenReview(issue, prURL, run)
	if err != nil {
		t.Fatalf("OpenReview(%d, %q): %v", issue, prURL, err)
	}
	return review
}

func TestOpenReviewNumbersCyclesPerPullRequest(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	const first = "https://github.com/o/r/pull/1"
	for cycle := 1; cycle <= 3; cycle++ {
		review := reviewed(t, store, issue.Number, first, "")
		if review.Cycle != cycle {
			t.Errorf("cycle %d of %q = %d", cycle, first, review.Cycle)
		}
		if _, err := store.CloseReview(review.ID, VerdictChanges, 2); err != nil {
			t.Fatalf("CloseReview: %v", err)
		}
	}

	// A replacement pull request is a fresh diff, so the cap starts over.
	const replacement = "https://github.com/o/r/pull/2"
	review := reviewed(t, store, issue.Number, replacement, "")
	if review.Cycle != 1 {
		t.Errorf("first cycle of the replacement = %d, want 1", review.Cycle)
	}
}

func TestOpenReviewNeedsAPullRequest(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	if _, err := store.OpenReview(issue.Number, "  ", ""); err == nil {
		t.Error("a review with no pull request url was accepted")
	}
	if _, err := store.OpenReview(9999, "https://github.com/o/r/pull/1", ""); err == nil {
		t.Error("a review on an issue that does not exist was accepted")
	}
}

func TestCloseReviewSettlesACycle(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")
	review := reviewed(t, store, issue.Number, "https://github.com/o/r/pull/1", "")

	if !review.Running() {
		t.Error("a freshly opened cycle is not running")
	}

	settled, err := store.CloseReview(review.ID, VerdictApproved, 0)
	if err != nil {
		t.Fatalf("CloseReview: %v", err)
	}
	if settled.Verdict != VerdictApproved || settled.Running() {
		t.Errorf("settled = %+v", settled)
	}

	if _, err := store.CloseReview(review.ID, "looks fine", 0); err == nil {
		t.Error("an unrecognised verdict was accepted")
	}
	if _, err := store.CloseReview("nope", VerdictApproved, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("closing an unknown review gave %v, want ErrNotFound", err)
	}
}

func TestReviewsListEveryCycleOfEveryPullRequest(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	const first = "https://github.com/o/r/pull/1"
	one := reviewed(t, store, issue.Number, first, "")
	if _, err := store.CloseReview(one.ID, VerdictChanges, 4); err != nil {
		t.Fatal(err)
	}
	two := reviewed(t, store, issue.Number, first, "")
	if _, err := store.CloseReview(two.ID, VerdictApproved, 0); err != nil {
		t.Fatal(err)
	}
	reviewed(t, store, issue.Number, "https://github.com/o/r/pull/2", "")

	list, err := store.Reviews(issue.Number)
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("listed %d cycles, want 3", len(list))
	}
	if list[0].Cycle != 1 || list[0].Findings != 4 || list[0].Verdict != VerdictChanges {
		t.Errorf("cycle one = %+v", list[0])
	}
	if list[1].Cycle != 2 || list[1].Verdict != VerdictApproved {
		t.Errorf("cycle two = %+v", list[1])
	}
	if list[2].Cycle != 1 || !list[2].Running() {
		t.Errorf("the replacement's cycle = %+v", list[2])
	}

	empty, err := store.Reviews(issue.Number + 1)
	if err != nil || empty != nil {
		t.Errorf("an issue with no reviews gave %v, %v", empty, err)
	}
}

func TestStatusDerivesTheCurrentPullRequestsCycle(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	status, err := store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewCycle != 0 || status.ReviewVerdict != "" || status.ReviewRunning() {
		t.Errorf("before any review: %+v", status)
	}

	const first = "https://github.com/o/r/pull/1"
	if _, err := store.LinkPR(issue.Number, first); err != nil {
		t.Fatal(err)
	}
	one := reviewed(t, store, issue.Number, first, "")

	status, err = store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewCycle != 1 || status.ReviewVerdict != "" || !status.ReviewRunning() {
		t.Errorf("while cycle one runs: cycle %d verdict %q", status.ReviewCycle, status.ReviewVerdict)
	}

	if _, err := store.CloseReview(one.ID, VerdictChanges, 3); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewCycle != 1 || status.ReviewVerdict != VerdictChanges || status.ReviewRunning() {
		t.Errorf("after cycle one: cycle %d verdict %q", status.ReviewCycle, status.ReviewVerdict)
	}

	// Linking a replacement pull request leaves the old cycles behind: the
	// status reads the newest cycle of the pull request the issue is on now.
	const replacement = "https://github.com/o/r/pull/2"
	if _, err := store.LinkPR(issue.Number, replacement); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status(issue.Number, agents)
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewCycle != 0 || status.ReviewVerdict != "" {
		t.Errorf("on a replacement with no review yet: cycle %d verdict %q", status.ReviewCycle, status.ReviewVerdict)
	}
}

// A review under way must not change what readiness means: an issue with an
// open pull request is already held back by awaiting_review.
func TestReviewsDoNotRedefineReadiness(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")

	before, err := store.Ready(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	reviewed(t, store, issue.Number, "https://github.com/o/r/pull/1", "")

	after, err := store.Ready(Filter{}, agents)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)-1 {
		t.Errorf("ready went from %d to %d; the pull request alone should hold the issue back", len(before), len(after))
	}
	for _, status := range after {
		if status.Number == issue.Number {
			t.Error("an issue with an open pull request is ready")
		}
	}
}

func TestOpenReviewRecordsItsRun(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")
	if err := store.StartRun("run-1", issue.Number, "code-reviewer", "@3"); err != nil {
		t.Fatal(err)
	}

	review := reviewed(t, store, issue.Number, "https://github.com/o/r/pull/1", "run-1")
	if review.Run != "run-1" {
		t.Errorf("run = %q", review.Run)
	}

	// The run column is a foreign key: a review cannot name a run pib has
	// not recorded.
	if _, err := store.OpenReview(issue.Number, "https://github.com/o/r/pull/2", "ghost"); err == nil {
		t.Error("a review naming an unknown run was accepted")
	}
}
