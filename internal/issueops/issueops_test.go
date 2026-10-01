package issueops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/protocol"
)

// handler builds a handler over an empty store with a small type mapping.
func handler(t *testing.T) Handler {
	t.Helper()

	store, err := issues.Open(issues.DataDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	dir := t.TempDir()
	path := filepath.Join(dir, config.FileName)
	// Review off: these tests are about the operations, not the review gate,
	// and an extra issue blocking every root would rewrite every expectation.
	body := "[types]\nfeature = \"\"\ntask = \"coder\"\nresearch = \"researcher\"\n" +
		"[plan]\nreview = false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadPaths(path, "")
	if err != nil {
		t.Fatal(err)
	}

	return Handler{Store: store, Config: cfg}
}

// run carries out one operation and fails the test if it errors.
func run(t *testing.T, h Handler, op protocol.Op, params any) protocol.Response {
	t.Helper()

	var payload json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		payload = body
	}

	resp, err := h.Run(context.Background(), protocol.Request{Op: op, Payload: payload})
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	if resp.Status != protocol.StatusOK {
		t.Fatalf("%s: status = %q", op, resp.Status)
	}
	return resp
}

// into decodes a response payload.
func into[T any](t *testing.T, resp protocol.Response) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	return result
}

// document is a two-issue plan used across the tests.
func document() map[string]any {
	return map[string]any{
		"plan": map[string]any{"slug": "orders", "title": "Order placement"},
		"issues": []map[string]any{
			{"id": "feature", "type": "feature", "title": "Feature: order placement"},
			{
				"id": "schema", "type": "task", "title": "Order schema",
				"parent": "feature", "body": "## Task\n\nSchema.",
				"acceptance": []string{"Tables exist"},
			},
			{"id": "agg", "type": "task", "title": "Aggregate", "parent": "feature", "blockedBy": []string{"schema"}},
		},
	}
}

func TestPlanApplyAndView(t *testing.T) {
	h := handler(t)

	applied := into[issues.ApplyResult](t, run(t, h, protocol.OpPlanApply, document()))
	if len(applied.Created) != 3 || applied.Plan.Slug != "orders" {
		t.Fatalf("apply = %+v", applied)
	}
	if len(applied.Warnings) != 0 {
		t.Errorf("warnings = %v", applied.Warnings)
	}

	list := into[PlanList](t, run(t, h, protocol.OpPlanList, nil))
	if len(list.Plans) != 1 || list.Plans[0].Title != "Order placement" {
		t.Errorf("plans = %+v", list.Plans)
	}

	viewed := into[PlanDetail](t, run(t, h, protocol.OpPlanView, PlanViewParams{Slug: "orders"}))
	if len(viewed.Issues) != 3 || viewed.Plan.Slug != "orders" {
		t.Errorf("plan detail = %+v", viewed)
	}
}

func TestPlanApplyWarnsAboutAnUnmappedType(t *testing.T) {
	h := handler(t)

	doc := document()
	doc["issues"] = append(doc["issues"].([]map[string]any),
		map[string]any{"id": "chore", "type": "chore", "title": "Tidy"})

	applied := into[issues.ApplyResult](t, run(t, h, protocol.OpPlanApply, doc))
	if len(applied.Warnings) != 1 || !strings.Contains(applied.Warnings[0], `type "chore"`) {
		t.Errorf("warnings = %v, want the unmapped type reported", applied.Warnings)
	}
	if len(applied.Created) != 4 {
		t.Errorf("created %v; a warning must not stop the write", applied.Created)
	}
}

func TestPlanApplyNeedsADocument(t *testing.T) {
	h := handler(t)

	if _, err := h.Run(context.Background(), protocol.Request{Op: protocol.OpPlanApply}); err == nil {
		t.Error("plan.apply with no payload succeeded")
	}
	if _, err := h.Run(context.Background(), protocol.Request{
		Op: protocol.OpPlanApply, Payload: json.RawMessage(`{"plan":{"slug":"x"}}`),
	}); err == nil {
		t.Error("a document with no plan title was accepted")
	}
}

func TestIssueLifecycleThroughTheOps(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	created := into[IssueDetail](t, run(t, h, protocol.OpIssueCreate, CreateParams{
		Plan: "orders", Type: "research", Title: "Compare libraries", Body: "## Research",
	}))
	number := created.Issue.Number
	if created.Issue.Agent != "researcher" || !created.Issue.Ready {
		t.Errorf("created = %+v, want a ready researcher issue", created.Issue)
	}
	if created.Body != "## Research" {
		t.Errorf("body = %q", created.Body)
	}

	edited := into[IssueDetail](t, run(t, h, protocol.OpIssueEdit, EditParams{
		Number: number, Title: strptr("Compare event stores"),
	}))
	if edited.Issue.Title != "Compare event stores" {
		t.Errorf("title = %q", edited.Issue.Title)
	}

	commented := into[IssueDetail](t, run(t, h, protocol.OpIssueComment, CommentParams{
		Number: number, Author: "reviewer", Body: "Looks right.",
	}))
	if len(commented.Comments) != 1 || commented.Comments[0].Author != "reviewer" {
		t.Errorf("comments = %+v", commented.Comments)
	}

	viewed := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if viewed.Issue.Number != number || len(viewed.Comments) != 1 {
		t.Errorf("view = %+v", viewed)
	}

	linked := into[IssueDetail](t, run(t, h, protocol.OpIssueLinkPR, LinkPRParams{
		Number: number, URL: "https://github.com/o/r/pull/3",
	}))
	if !linked.Issue.AwaitingReview || linked.Issue.Ready {
		t.Errorf("after linking = %+v, want it awaiting review", linked.Issue)
	}

	closed := into[CloseResult](t, run(t, h, protocol.OpIssueClose, CloseParams{
		Number: number, Reason: "superseded",
	}))
	if closed.Issue.State != issues.StateClosed {
		t.Errorf("state = %q", closed.Issue.State)
	}
	if len(closed.Warnings) != 0 {
		t.Errorf("warnings = %v; research does not wait on a merge", closed.Warnings)
	}
}

func TestClosingATaskWarns(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	listed := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	number := listed.Issues[0].Number

	closed := into[CloseResult](t, run(t, h, protocol.OpIssueClose, CloseParams{Number: number}))
	if len(closed.Warnings) != 1 || !strings.Contains(closed.Warnings[0], "pull request") {
		t.Errorf("warnings = %v, want the merge rule reported", closed.Warnings)
	}
	if closed.Issue.State != issues.StateClosed {
		t.Error("the warning blocked the close; it should only report")
	}
}

func TestListAndReady(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	all := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{}))
	if len(all.Issues) != 3 {
		t.Fatalf("listed %d issues, want 3", len(all.Issues))
	}

	ready := into[StatusList](t, run(t, h, protocol.OpIssueReady, ListParams{}))
	if len(ready.Issues) != 2 {
		t.Fatalf("ready = %d issues, want the feature and the schema", len(ready.Issues))
	}

	// The reply says what would run each ready issue, so a caller can launch
	// without asking a second question.
	byType := map[string]issues.Status{}
	for _, status := range ready.Issues {
		byType[status.Type] = status
	}
	if got := byType["task"]; got.Agent != "coder" || !got.Launchable {
		t.Errorf("task = %+v, want a launchable coder", got)
	}
	if got := byType["feature"]; got.Agent != "" || got.Launchable {
		t.Errorf("feature = %+v, want a container nothing runs", got)
	}

	filtered := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "feature"}))
	if len(filtered.Issues) != 1 {
		t.Errorf("filtered = %d issues, want 1", len(filtered.Issues))
	}
}

func TestListReconcilesLinkedPullRequests(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	tasks := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	schema := tasks.Issues[0].Number

	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: schema, URL: "https://github.com/o/r/pull/1"})

	// With the pull request merged, listing is what notices and closes it.
	h.Lookup = fakeLookup{state: "merged"}
	listed := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{}))

	for _, status := range listed.Issues {
		if status.Number == schema && status.State != issues.StateClosed {
			t.Errorf("#%d is %q; a merged pull request should have closed it", schema, status.State)
		}
	}

	// And the issue it was blocking is free.
	ready := into[StatusList](t, run(t, h, protocol.OpIssueReady, ListParams{}))
	var freed bool
	for _, status := range ready.Issues {
		if status.LocalID == "agg" {
			freed = true
		}
	}
	if !freed {
		t.Error("the dependent issue did not become ready")
	}
}

func TestAFailingLookupWarnsRatherThanFailing(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	tasks := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: tasks.Issues[0].Number, URL: "https://github.com/o/r/pull/1"})

	h.Lookup = fakeLookup{err: "gh is not available"}
	listed := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{}))

	if len(listed.Warnings) != 1 || !strings.Contains(listed.Warnings[0], "gh is not available") {
		t.Errorf("warnings = %v, want the failure reported", listed.Warnings)
	}
	if len(listed.Issues) != 3 {
		t.Errorf("listing returned %d issues; a lookup failure must not lose the listing", len(listed.Issues))
	}
}

func TestReindex(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	result := into[ReindexResult](t, run(t, h, protocol.OpIssueReindex, ReindexParams{Plan: "orders"}))
	if result.Refreshed != 3 {
		t.Errorf("refreshed %d, want 3", result.Refreshed)
	}
}

func TestReviewRecordSettlesACycle(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	list := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	if len(list.Issues) == 0 {
		t.Fatal("no tasks")
	}
	number := list.Issues[0].Number

	// Link a pull request and open a review cycle.
	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: number, URL: "https://github.com/o/r/pull/1"})
	if _, err := h.Store.OpenReview(number, "https://github.com/o/r/pull/1", ""); err != nil {
		t.Fatal(err)
	}

	// Settle it.
	resp, err := h.Run(context.Background(), protocol.Request{
		Op:      protocol.OpReviewRecord,
		Payload: mustJSON(t, ReviewRecordParams{Number: number, Verdict: issues.VerdictChanges, Findings: 3}),
	})
	if err != nil {
		t.Fatalf("review.record: %v", err)
	}
	var result issues.Review
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Verdict != issues.VerdictChanges || result.Findings != 3 {
		t.Errorf("result = %+v", result)
	}

	// The cycle is no longer running.
	status, err := h.Store.Status(number, issues.StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewRunning() {
		t.Error("review still running after record")
	}
}

func TestReviewRecordNeedsAPullRequestAndOpenCycle(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	list := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	number := list.Issues[0].Number

	// No PR linked yet.
	_, err := h.Run(context.Background(), protocol.Request{
		Op:      protocol.OpReviewRecord,
		Payload: mustJSON(t, ReviewRecordParams{Number: number, Verdict: issues.VerdictApproved, Findings: 0}),
	})
	if err == nil || !strings.Contains(err.Error(), "no linked pull request") {
		t.Errorf("record without PR: %v", err)
	}

	// PR linked, but no cycle opened.
	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: number, URL: "https://github.com/o/r/pull/1"})
	_, err = h.Run(context.Background(), protocol.Request{
		Op:      protocol.OpReviewRecord,
		Payload: mustJSON(t, ReviewRecordParams{Number: number, Verdict: issues.VerdictApproved, Findings: 0}),
	})
	if err == nil || !strings.Contains(err.Error(), "no open review cycle") {
		t.Errorf("record without open cycle: %v", err)
	}
}

func TestReviewRecordRejectsAnUnknownVerdict(t *testing.T) {
	h := handler(t)
	run(t, h, protocol.OpPlanApply, document())

	list := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	number := list.Issues[0].Number

	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: number, URL: "https://github.com/o/r/pull/1"})
	if _, err := h.Store.OpenReview(number, "https://github.com/o/r/pull/1", ""); err != nil {
		t.Fatal(err)
	}

	_, err := h.Run(context.Background(), protocol.Request{
		Op:      protocol.OpReviewRecord,
		Payload: mustJSON(t, ReviewRecordParams{Number: number, Verdict: "looks-fine", Findings: 0}),
	})
	if err == nil || !strings.Contains(err.Error(), "not a review verdict") {
		t.Errorf("unknown verdict: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBadRequests(t *testing.T) {
	h := handler(t)

	if _, err := h.Run(context.Background(), protocol.Request{Op: "issue.nonsense"}); err == nil {
		t.Error("an unknown op succeeded")
	}
	if _, err := h.Run(context.Background(), protocol.Request{
		Op: protocol.OpIssueView, Payload: json.RawMessage(`{"number":`),
	}); err == nil {
		t.Error("an unreadable payload was accepted")
	}
	if _, err := h.Run(context.Background(), protocol.Request{
		Op: protocol.OpIssueView, Payload: json.RawMessage(`{"number":404}`),
	}); err == nil {
		t.Error("viewing a missing issue succeeded")
	}
	if _, err := (Handler{}).Run(context.Background(), protocol.Request{Op: protocol.OpIssueList}); err == nil {
		t.Error("a handler with no store succeeded")
	}
}

type fakeLookup struct {
	state string
	err   string
}

func (f fakeLookup) State(context.Context, string) (string, error) {
	if f.err != "" {
		return "", errString(f.err)
	}
	return f.state, nil
}

type errString string

func (e errString) Error() string { return string(e) }

func strptr(s string) *string { return &s }

// fakeSpawner records what it was asked to run, and records a run against the
// store the way the real runner does, in place of the runner and its tmux
// window.
type fakeSpawner struct {
	runs *issues.Store
	seen []protocol.Request
	// err is what a call fails with, for a runner that cannot start.
	err string

	called chan error
}

func (f *fakeSpawner) Run(_ context.Context, req protocol.Request) (protocol.Response, error) {
	f.seen = append(f.seen, req)
	if f.err != "" {
		f.signal(errString(f.err))
		return protocol.Response{}, errString(f.err)
	}
	if f.runs != nil {
		// A spawn records a new run; a resume picks the old one up again,
		// with the agent the record already names, as the runner does.
		id, agent := "run-new", req.Agent
		if req.Op == protocol.OpResume {
			id = req.Session
			agent, _ = f.runs.RunAgent(id)
		}
		if err := f.runs.StartRun(issues.RunStart{ID: id, Issue: req.Issue, Agent: agent, Window: "@3"}); err != nil {
			f.signal(err)
			return protocol.Response{}, err
		}
	}
	f.signal(nil)
	return protocol.Response{Status: protocol.StatusOK, Session: req.Session}, nil
}

func (f *fakeSpawner) signal(err error) {
	if f.called == nil {
		return
	}
	select {
	case f.called <- err:
	default:
	}
}

// wait blocks until the spawner has been called, which the answer path does in
// a goroutine. It fails the test rather than hanging if nothing arrives.
func (f *fakeSpawner) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-f.called:
		if err != nil {
			t.Fatalf("spawning: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never started")
	}
}

// firstTask applies the fixture plan and returns the first task's number.
func firstTask(t *testing.T, h Handler) int64 {
	t.Helper()
	run(t, h, protocol.OpPlanApply, document())
	list := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	if len(list.Issues) == 0 {
		t.Fatal("no tasks")
	}
	return list.Issues[0].Number
}

// started builds a spawner that records runs against the handler's store, the
// way the runner does.
func started(h Handler) *fakeSpawner {
	return &fakeSpawner{runs: h.Store, called: make(chan error, 4)}
}

func TestRetryStartsAFreshRun(t *testing.T) {
	h := handler(t)
	spawn := started(h)
	h.Spawn = spawn
	number := firstTask(t, h)

	// Something went wrong last time.
	if err := h.Store.StartRun(issues.RunStart{ID: "run-1", Issue: number, Agent: "coder", Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.FinishRun("run-1", "error"); err != nil {
		t.Fatal(err)
	}

	// The retry is worth having: without it the issue is sitting in needs
	// attention with nothing working on it.
	before := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if !before.Issue.NeedsAttention {
		t.Fatalf("a run that just failed reads as %q, want it needing attention", before.Issue.AttentionReason)
	}

	run(t, h, protocol.OpIssueRetry, RetryParams{Number: number})
	spawn.wait(t)
	if len(spawn.seen) != 1 {
		t.Fatalf("spawned %d runs, want 1", len(spawn.seen))
	}
	req := spawn.seen[0]
	if req.Op != protocol.OpSpawnBackground || req.Agent != "coder" || req.Issue != number {
		t.Errorf("request = %+v, want a coder started in the background for #%d", req, number)
	}

	// The run is recorded, so the issue is in progress rather than needing
	// attention — which is what the retry bought.
	after := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if after.Issue.NeedsAttention {
		t.Errorf("after a retry, attention = %q, want the issue back in play", after.Issue.AttentionReason)
	}
	if !after.Issue.InProgress {
		t.Error("after a retry, in progress = false, want the new run recorded")
	}
}

func TestRetryUnlinksAClosedPullRequest(t *testing.T) {
	h := handler(t)
	spawn := started(h)
	h.Spawn = spawn
	number := firstTask(t, h)

	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: number, URL: "https://github.com/o/r/pull/1"})
	// The pull request was closed without merging.
	h.Lookup = fakeLookup{state: "closed"}
	run(t, h, protocol.OpIssueList, ListParams{Type: "task"})

	run(t, h, protocol.OpIssueRetry, RetryParams{Number: number})
	spawn.wait(t)

	detail := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if detail.Issue.PRURL != "" {
		t.Errorf("pull request = %q, want the closed one unlinked", detail.Issue.PRURL)
	}
	if detail.Issue.NeedsAttention {
		t.Errorf("attention = %q, want the retry to have cleared it", detail.Issue.AttentionReason)
	}
}

func TestRetryResetsExhaustedReviewCycles(t *testing.T) {
	h := handler(t)
	spawn := started(h)
	h.Spawn = spawn
	number := firstTask(t, h)

	const url = "https://github.com/o/r/pull/1"
	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: number, URL: url})
	for i := 0; i < h.Config.ReviewCycles(); i++ {
		review, err := h.Store.OpenReview(number, url, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Store.CloseReview(review.ID, issues.VerdictChanges, 1); err != nil {
			t.Fatal(err)
		}
	}
	status := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number})).Issue
	if !status.NeedsAttention {
		t.Fatalf("attention = %q, want the review cap to have been reached", status.AttentionReason)
	}

	run(t, h, protocol.OpIssueRetry, RetryParams{Number: number})
	spawn.wait(t)

	detail := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if detail.Issue.NeedsAttention {
		t.Errorf("attention = %q, want the review cap reset", detail.Issue.AttentionReason)
	}
	if detail.Issue.ReviewBase != h.Config.ReviewCycles() {
		t.Errorf("review base = %d, want it reset from the newest cycle", detail.Issue.ReviewBase)
	}
	reviews, err := h.Store.Reviews(number)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != h.Config.ReviewCycles() {
		t.Errorf("reviews = %d, want the history kept", len(reviews))
	}
}

func TestRetryRefusesAnIssueThatIsAlreadyRunning(t *testing.T) {
	h := handler(t)
	spawn := started(h)
	h.Spawn = spawn
	number := firstTask(t, h)

	if err := h.Store.StartRun(issues.RunStart{ID: "run-1", Issue: number, Agent: "coder", Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(context.Background(), protocol.Request{
		Op: protocol.OpIssueRetry, Payload: mustJSON(t, RetryParams{Number: number}),
	}); err == nil {
		t.Error("retrying a live issue started a second agent")
	}
	if len(spawn.seen) != 0 {
		t.Errorf("spawned %+v, want nothing", spawn.seen)
	}
}

func TestRetryNeedsAnAgentAndARunner(t *testing.T) {
	h := handler(t)
	number := firstTask(t, h)

	if _, err := h.Run(context.Background(), protocol.Request{
		Op: protocol.OpIssueRetry, Payload: mustJSON(t, RetryParams{Number: number}),
	}); err == nil {
		t.Error("a retry with no runner succeeded")
	}

	// A spawn that fails is a retry that did not happen, and the reply says so
	// rather than leaving a cleared issue with no agent on it.
	h.Spawn = &fakeSpawner{err: "no such agent", called: make(chan error, 1)}
	if _, err := h.Run(context.Background(), protocol.Request{
		Op: protocol.OpIssueRetry, Payload: mustJSON(t, RetryParams{Number: number}),
	}); err == nil {
		t.Error("a retry whose spawn failed reported success")
	}
}

func TestAnswerResumesTheRunThatAsked(t *testing.T) {
	h := handler(t)
	spawn := started(h)
	h.Spawn = spawn
	number := firstTask(t, h)

	if err := h.Store.StartRun(issues.RunStart{ID: "run-1", Issue: number, Agent: "coder", Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.FinishRun("run-1", "needs_input"); err != nil {
		t.Fatal(err)
	}

	// The answer is worth having: the agent is waiting on one.
	before := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if before.Issue.AttentionReason != issues.AttentionAsked {
		t.Fatalf("attention = %q, want a question to answer", before.Issue.AttentionReason)
	}

	run(t, h, protocol.OpIssueAnswer, AnswerParams{Number: number, Answer: "use postgres"})
	spawn.wait(t)
	if len(spawn.seen) != 1 {
		t.Fatalf("spawned %d runs, want 1", len(spawn.seen))
	}
	req := spawn.seen[0]
	if req.Op != protocol.OpResume || req.Session != "run-1" || req.Answer != "use postgres" {
		t.Errorf("request = %+v, want run-1 resumed with the answer", req)
	}
	if req.Issue != number {
		t.Errorf("request issue = %d, want %d", req.Issue, number)
	}

	// The run is picked up again, so the issue is working rather than stuck.
	after := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number}))
	if !after.Issue.InProgress {
		t.Error("after an answer, in progress = false, want the run picked up again")
	}
	if after.Issue.NeedsAttention {
		t.Errorf("after an answer, attention = %q, want nothing to attend to", after.Issue.AttentionReason)
	}
}

// An answer is resumed off the request's context, so a resume that fails
// cannot be reported in the reply. It is reported instead, or the issue is
// left saying an agent is waiting for an answer that never arrived.
func TestAFailedResumeIsReported(t *testing.T) {
	h := handler(t)
	number := firstTask(t, h)

	if err := h.Store.StartRun(issues.RunStart{ID: "run-1", Issue: number, Agent: "coder", Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.FinishRun("run-1", "needs_input"); err != nil {
		t.Fatal(err)
	}

	reported := make(chan error, 1)
	h.Spawn = &fakeSpawner{err: "no session to resume", called: make(chan error, 1)}
	h.Report = func(err error) { reported <- err }

	run(t, h, protocol.OpIssueAnswer, AnswerParams{Number: number, Answer: "use postgres"})
	select {
	case err := <-reported:
		if err == nil {
			t.Error("a failed resume reported nothing")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a failed resume was never reported")
	}

	// And the issue is still waiting for an answer, so nothing is lost.
	status := into[IssueDetail](t, run(t, h, protocol.OpIssueView, ViewParams{Number: number})).Issue
	if !status.NeedsAttention {
		t.Errorf("attention = %q, want the question still outstanding", status.AttentionReason)
	}
}

func TestAnswerNeedsARunWaitingAndSomethingToSay(t *testing.T) {
	h := handler(t)
	h.Spawn = started(h)
	number := firstTask(t, h)

	if err := h.Store.StartRun(issues.RunStart{ID: "run-1", Issue: number, Agent: "coder", Window: "@3"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.FinishRun("run-1", "done"); err != nil {
		t.Fatal(err)
	}

	for _, params := range []AnswerParams{
		{Number: number, Answer: "hello"},
		{Number: number, Answer: "  "},
	} {
		if _, err := h.Run(context.Background(), protocol.Request{
			Op: protocol.OpIssueAnswer, Payload: mustJSON(t, params),
		}); err == nil {
			t.Errorf("answering with %+v succeeded, want an error", params)
		}
	}
}

func TestTheReviewCycleCapComesFromTheConfig(t *testing.T) {
	h := handler(t)
	number := firstTask(t, h)

	path := filepath.Join(t.TempDir(), config.FileName)
	body := "[types]\ntask = \"coder\"\n[plan]\nreview = false\n[review]\ncycles = 1\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadPaths(path, "")
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg

	const url = "https://github.com/o/r/pull/1"
	run(t, h, protocol.OpIssueLinkPR, LinkPRParams{Number: number, URL: url})
	review, err := h.Store.OpenReview(number, url, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.CloseReview(review.ID, issues.VerdictChanges, 1); err != nil {
		t.Fatal(err)
	}

	list := into[StatusList](t, run(t, h, protocol.OpIssueList, ListParams{Type: "task"}))
	for _, status := range list.Issues {
		if status.Number != number {
			continue
		}
		if !status.NeedsAttention || status.AttentionReason != issues.AttentionReview {
			t.Errorf("attention = %v %q, want the cap of one reached", status.NeedsAttention, status.AttentionReason)
		}
		return
	}
	t.Errorf("#%d is missing from the listing", number)
}
