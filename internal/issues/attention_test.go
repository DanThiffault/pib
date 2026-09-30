package issues

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// attention is the derived state of one issue, for a test that only cares
// about why it stopped.
func attention(t *testing.T, s *Store, number int64) Status {
	t.Helper()
	status, err := s.Status(number, agents)
	if err != nil {
		t.Fatalf("Status(%d): %v", number, err)
	}
	return status
}

func wantAttention(t *testing.T, s *Store, number int64, reason string) {
	t.Helper()
	status := attention(t, s, number)
	if !status.NeedsAttention {
		t.Errorf("#%d: needs attention = false, want it reported (%s)", number, reason)
	}
	if status.AttentionReason != reason {
		t.Errorf("#%d: reason = %q, want %q", number, status.AttentionReason, reason)
	}
	if status.Ready {
		t.Errorf("#%d: ready = true, want an issue needing attention held out of the ready set", number)
	}
}

// runOf records a finished run on an issue, after the issue was last touched,
// so the derivation sees the run as the newest thing that happened.
func runOf(t *testing.T, s *Store, number int64, id, status string) {
	t.Helper()
	if err := s.StartRun(id, number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`UPDATE runs SET ended_at = ?, status = ? WHERE id = ?`,
		format(now().Add(time.Minute)), status, id); err != nil {
		t.Fatal(err)
	}
}

func TestAFailedRunNeedsAttention(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")

	runOf(t, store, issue.Number, "run-1", "error")
	wantAttention(t, store, issue.Number, AttentionFailed)
}

func TestAQuestionNeedsAttention(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")

	runOf(t, store, issue.Number, "run-1", "needs_input")
	wantAttention(t, store, issue.Number, AttentionAsked)
}

// The three ways a run can end without saying how, all count as a failure:
// the machine stopped and only a human can restart it.
func TestAKilledWindowCountsAsAFailedAgent(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")

	// Killing the window ends the run with nothing to report.
	runOf(t, store, issue.Number, "run-1", "unknown")
	wantAttention(t, store, issue.Number, AttentionFailed)
}

func TestACrashedAgentCountsAsAFailedAgent(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")

	// The agent process died, so the run is recorded as unknown too.
	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	freeze(t, "2026-08-29T12:01:00Z")
	if err := store.FinishRun("run-1", "an outcome pib does not know"); err != nil {
		t.Fatal(err)
	}
	wantAttention(t, store, issue.Number, AttentionFailed)
}

func TestAnOrphanClosedAtStartupCountsAsAFailedAgent(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	dir := DataDir(t.TempDir())

	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CreatePlan(NewPlan{Slug: "orders", Title: "Order placement"}); err != nil {
		t.Fatal(err)
	}
	issue := task(t, first, "Alpha")
	if err := first.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	// pib itself goes away without finishing the run.
	first.Close()
	freeze(t, "2026-08-29T13:00:00Z")

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	runs, err := second.Runs(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "unknown" {
		t.Fatalf("runs = %+v, want one closed as unknown", runs)
	}
	wantAttention(t, second, issue.Number, AttentionFailed)
}

func TestARunIsForgivenWhenSomethingChangedAfterIt(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")
	runOf(t, store, issue.Number, "run-1", "error")
	wantAttention(t, store, issue.Number, AttentionFailed)

	// Later, the user edits the issue.
	freeze(t, "2026-08-29T13:00:00Z")
	title := "Alpha, properly specified"
	if _, err := store.Edit(issue.Number, Edit{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if status := attention(t, store, issue.Number); status.NeedsAttention {
		t.Errorf("after an edit, reason = %q, want nothing to attend to", status.AttentionReason)
	}
}

func TestACommentClearsAttention(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")
	runOf(t, store, issue.Number, "run-1", "error")
	wantAttention(t, store, issue.Number, AttentionFailed)

	freeze(t, "2026-08-29T13:00:00Z")
	if err := store.Comment(issue.Number, "me", "try postgres instead"); err != nil {
		t.Fatal(err)
	}
	if status := attention(t, store, issue.Number); status.NeedsAttention {
		t.Errorf("after a comment, reason = %q, want nothing to attend to", status.AttentionReason)
	}
}

func TestEditingTheFileClearsAttentionButAnUnchangedReindexDoesNot(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")
	runOf(t, store, issue.Number, "run-1", "error")
	wantAttention(t, store, issue.Number, AttentionFailed)

	// A forced reindex of a file nobody touched changes nothing, so the
	// failed run is still the newest thing that happened.
	freeze(t, "2026-08-29T13:00:00Z")
	if _, err := store.Reindex("orders"); err != nil {
		t.Fatal(err)
	}
	wantAttention(t, store, issue.Number, AttentionFailed)

	// The file itself changes, as it does when the user edits it in $EDITOR.
	writeIssueFile(t, store, issue.Path, "Alpha, named at last")
	if _, err := store.Reindex("orders"); err != nil {
		t.Fatal(err)
	}
	if status := attention(t, store, issue.Number); status.NeedsAttention {
		t.Errorf("after editing the file, reason = %q, want nothing to attend to", status.AttentionReason)
	}
}

func TestAClosedPullRequestNeedsAttention(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")
	if _, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE issues SET pr_state = 'closed' WHERE number = ?`, issue.Number); err != nil {
		t.Fatal(err)
	}
	wantAttention(t, store, issue.Number, AttentionClosedPR)
}

// writeIssueFile rewrites an issue's file, standing in for a user editing it
// in $EDITOR. The mtime moves with it, so the index notices.
func writeIssueFile(t *testing.T, s *Store, rel, title string) {
	t.Helper()
	path := filepath.Join(s.dir, rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(body), "Alpha", title, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// exhaustedReview records cycles review cycles' worth of changes-requested
// reviews on an issue's pull request.
func exhaustedReview(t *testing.T, s *Store, number int64, cycles int) {
	t.Helper()
	const url = "https://github.com/o/r/pull/1"
	if _, err := s.LinkPR(number, url); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cycles; i++ {
		review, err := s.OpenReview(number, url, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CloseReview(review.ID, VerdictChanges, 1); err != nil {
			t.Fatal(err)
		}
	}
	// The pull request stays open, so the exhausted cap is the only reason
	// the issue is being reported.
	if _, err := s.db.Exec(`UPDATE issues SET pr_state = 'open' WHERE number = ?`, number); err != nil {
		t.Fatal(err)
	}
}

func TestExhaustedReviewCyclesNeedAttention(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")
	exhaustedReview(t, store, issue.Number, DefaultReviewCycles)
	wantAttention(t, store, issue.Number, AttentionReview)
}

func TestTheReviewCycleCapComesFromTheOptions(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")
	exhaustedReview(t, store, issue.Number, 2)

	// Two cycles are two under the default cap, and over a cap of one.
	opts := StatusOptions{AgentFor: agents.AgentFor, ReviewCycles: 1}
	status, err := store.Status(issue.Number, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !status.NeedsAttention || status.AttentionReason != AttentionReview {
		t.Errorf("with a cap of one: attention = %v %q, want the review cap reached",
			status.NeedsAttention, status.AttentionReason)
	}

	opts.ReviewCycles = 5
	status, err = store.Status(issue.Number, opts)
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsAttention {
		t.Errorf("with a cap of five: reason = %q, want the issue left alone", status.AttentionReason)
	}
}

func TestResettingReviewCyclesClearsTheCapButKeepsTheHistory(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")
	exhaustedReview(t, store, issue.Number, DefaultReviewCycles)
	wantAttention(t, store, issue.Number, AttentionReview)

	if err := store.ResetReviewCycles(issue.Number); err != nil {
		t.Fatal(err)
	}
	if status := attention(t, store, issue.Number); status.NeedsAttention {
		t.Errorf("after a reset, reason = %q, want nothing to attend to", status.AttentionReason)
	}

	reviews, err := store.Reviews(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != DefaultReviewCycles {
		t.Errorf("reviews = %d, want the %d cycles kept", len(reviews), DefaultReviewCycles)
	}
}

func TestResettingReviewCyclesOnAMissingIssueIsAnError(t *testing.T) {
	store := planned(t)
	if err := store.ResetReviewCycles(9999); err == nil {
		t.Errorf("resetting the cycles of a missing issue succeeded, want not found")
	}
}

func TestUnlinkingAPullRequestReleasesTheIssue(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")
	if _, err := store.LinkPR(issue.Number, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE issues SET pr_state = 'closed' WHERE number = ?`, issue.Number); err != nil {
		t.Fatal(err)
	}
	wantAttention(t, store, issue.Number, AttentionClosedPR)

	unlinked, err := store.UnlinkPR(issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	if unlinked.PRURL != "" || unlinked.PRState != "" {
		t.Errorf("unlinked pull request = %q %q, want it forgotten", unlinked.PRURL, unlinked.PRState)
	}
	status := attention(t, store, issue.Number)
	if status.NeedsAttention || !status.Ready {
		t.Errorf("attention = %v ready = %v, want an unlinked issue released", status.NeedsAttention, status.Ready)
	}
	if _, err := store.UnlinkPR(9999); err == nil {
		t.Errorf("unlinking the pull request of a missing issue succeeded, want not found")
	}
}

func TestALiveRunIsNotAttentionItIsProgress(t *testing.T) {
	store := planned(t)
	issue := task(t, store, "Alpha")
	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	if status := attention(t, store, issue.Number); status.NeedsAttention {
		t.Errorf("reason = %q, want a run in progress left alone", status.AttentionReason)
	}
}

// TestTheClockIsHonoured guards the comparison the derivation rests on: a run
// that ended before the last write is old news.
func TestTheClockIsHonoured(t *testing.T) {
	freeze(t, "2026-08-29T12:00:00Z")
	store := planned(t)
	issue := task(t, store, "Alpha")

	if err := store.StartRun("run-1", issue.Number, "coder", "@3"); err != nil {
		t.Fatal(err)
	}
	// A run that ended when the issue was created is not later than the
	// issue, so nothing has happened since the user last looked.
	if _, err := store.db.Exec(`UPDATE runs SET ended_at = ?, status = 'error' WHERE id = 'run-1'`, stamp); err != nil {
		t.Fatal(err)
	}
	if status := attention(t, store, issue.Number); status.NeedsAttention {
		t.Errorf("reason = %q, want a run older than the issue ignored", status.AttentionReason)
	}
}
