package issueops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
