// Package issueops carries out the plan and issue operations that arrive
// over pib's socket.
//
// It is the layer where the parts meet: the store holds the data, the
// configuration says which agent implements a type, and gh settles pull
// requests. None of those know about each other, so this is where they are
// put together and turned into replies.
package issueops

import (
	"context"
	"encoding/json"
	"fmt"

	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/protocol"
)

// Handler answers issue and plan requests.
type Handler struct {
	// Store is the issue store. Required.
	Store *issues.Store
	// Config maps an issue type to the agent that implements it.
	Config config.Config
	// Lookup settles linked pull requests against GitHub. A nil Lookup
	// leaves automatic closure switched off; everything else still works.
	Lookup issues.PRLookup
}

// Parameters for the operations that take them. The payload of plan.apply is
// the plan document itself rather than one of these, since wrapping a whole
// plan in another object buys nothing.
type (
	// ListParams narrows a listing.
	ListParams struct {
		Plan  string `json:"plan,omitempty"`
		State string `json:"state,omitempty"`
		Type  string `json:"type,omitempty"`
	}

	// ViewParams identifies an issue to read in full.
	ViewParams struct {
		Number int64 `json:"number"`
	}

	// CreateParams describes an issue to create.
	CreateParams struct {
		Plan       string   `json:"plan"`
		LocalID    string   `json:"localId,omitempty"`
		Type       string   `json:"type"`
		Title      string   `json:"title"`
		Body       string   `json:"body,omitempty"`
		Acceptance []string `json:"acceptance,omitempty"`
		Parent     int64    `json:"parent,omitempty"`
		BlockedBy  []int64  `json:"blockedBy,omitempty"`
	}

	// EditParams changes an issue. Absent fields are left alone.
	EditParams struct {
		Number          int64     `json:"number"`
		Title           *string   `json:"title,omitempty"`
		Type            *string   `json:"type,omitempty"`
		Body            *string   `json:"body,omitempty"`
		Acceptance      *[]string `json:"acceptance,omitempty"`
		Parent          *int64    `json:"parent,omitempty"`
		AddBlockedBy    []int64   `json:"addBlockedBy,omitempty"`
		RemoveBlockedBy []int64   `json:"removeBlockedBy,omitempty"`
	}

	// CommentParams appends to an issue's activity.
	CommentParams struct {
		Number int64  `json:"number"`
		Author string `json:"author"`
		Body   string `json:"body"`
	}

	// LinkPRParams records the pull request that will close an issue.
	LinkPRParams struct {
		Number int64  `json:"number"`
		URL    string `json:"url"`
	}

	// CloseParams closes an issue, optionally saying why.
	CloseParams struct {
		Number int64  `json:"number"`
		Reason string `json:"reason,omitempty"`
	}

	// ReopenParams puts a closed issue back in play.
	ReopenParams struct {
		Number int64 `json:"number"`
	}

	// PlanViewParams identifies a plan.
	PlanViewParams struct {
		Slug string `json:"slug"`
	}

	// ReindexParams re-reads issue files, for one plan or all of them.
	ReindexParams struct {
		Plan string `json:"plan,omitempty"`
	}
)

// Results returned in a response payload.
type (
	// StatusList is a listing, with anything that went wrong alongside it.
	StatusList struct {
		Issues   []issues.Status `json:"issues"`
		Warnings []string        `json:"warnings,omitempty"`
	}

	// IssueDetail is one issue in full: its state, its prose, its activity,
	// and every attempt an agent has made at it.
	IssueDetail struct {
		Issue    issues.Status    `json:"issue"`
		Body     string           `json:"body,omitempty"`
		Comments []issues.Comment `json:"comments,omitempty"`
		Runs     []issues.Run     `json:"runs,omitempty"`
	}

	// PlanList is every plan pib knows.
	PlanList struct {
		Plans []issues.Plan `json:"plans"`
	}

	// PlanDetail is a plan, what it is for, and the issues in it.
	PlanDetail struct {
		Plan     issues.Plan     `json:"plan"`
		Body     string          `json:"body,omitempty"`
		Issues   []issues.Status `json:"issues"`
		Warnings []string        `json:"warnings,omitempty"`
	}

	// CloseResult reports a closure and any rule it bent.
	CloseResult struct {
		Issue    issues.Status `json:"issue"`
		Warnings []string      `json:"warnings,omitempty"`
	}

	// ReindexResult counts the files re-read.
	ReindexResult struct {
		Refreshed int `json:"refreshed"`
	}
)

// Run carries out one request.
func (h Handler) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	if h.Store == nil {
		return protocol.Response{}, fmt.Errorf("no issue store is open")
	}

	switch req.Op {
	case protocol.OpPlanApply:
		return h.planApply(req)
	case protocol.OpPlanList:
		return h.planList()
	case protocol.OpPlanView:
		return h.planView(ctx, req)

	case protocol.OpIssueCreate:
		return h.issueCreate(req)
	case protocol.OpIssueList:
		return h.issueList(ctx, req, false)
	case protocol.OpIssueReady:
		return h.issueList(ctx, req, true)
	case protocol.OpIssueView:
		return h.issueView(req)
	case protocol.OpIssueEdit:
		return h.issueEdit(req)
	case protocol.OpIssueComment:
		return h.issueComment(req)
	case protocol.OpIssueLinkPR:
		return h.issueLinkPR(req)
	case protocol.OpIssueClose:
		return h.issueClose(req)
	case protocol.OpIssueReopen:
		return h.issueReopen(req)
	case protocol.OpIssueReindex:
		return h.issueReindex(req)

	default:
		return protocol.Response{}, fmt.Errorf("unknown op %q", req.Op)
	}
}

func (h Handler) planApply(req protocol.Request) (protocol.Response, error) {
	if len(req.Payload) == 0 {
		return protocol.Response{}, fmt.Errorf("plan.apply needs a plan document")
	}

	doc, err := issues.ParseDocument(req.Payload)
	if err != nil {
		return protocol.Response{}, err
	}

	result, err := h.Store.Apply(doc, issues.ApplyOptions{
		KnownType: h.Config.Known,
		Review:    h.Config.PlanReview(),
	})
	if err != nil {
		return protocol.Response{}, err
	}
	return reply(result)
}

func (h Handler) planList() (protocol.Response, error) {
	plans, err := h.Store.Plans()
	if err != nil {
		return protocol.Response{}, err
	}
	return reply(PlanList{Plans: plans})
}

func (h Handler) planView(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	params, err := decode[PlanViewParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	plan, err := h.Store.Plan(params.Slug)
	if err != nil {
		return protocol.Response{}, err
	}

	filter := issues.Filter{Plan: params.Slug}
	warnings, err := h.reconcile(ctx, filter)
	if err != nil {
		return protocol.Response{}, err
	}

	list, err := h.Store.Statuses(filter, h.statusOptions())
	if err != nil {
		return protocol.Response{}, err
	}

	file, err := h.Store.PlanContent(params.Slug)
	if err != nil {
		return protocol.Response{}, err
	}

	return reply(PlanDetail{Plan: plan, Body: file.Body, Issues: list, Warnings: warnings})
}

func (h Handler) issueCreate(req protocol.Request) (protocol.Response, error) {
	params, err := decode[CreateParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	issue, err := h.Store.Create(issues.NewIssue{
		Plan:       params.Plan,
		LocalID:    params.LocalID,
		Type:       params.Type,
		Title:      params.Title,
		Body:       params.Body,
		Acceptance: params.Acceptance,
		Parent:     params.Parent,
		BlockedBy:  params.BlockedBy,
	})
	if err != nil {
		return protocol.Response{}, err
	}
	return h.detail(issue.Number)
}

// issueList answers both a listing and a readiness query. Both settle any
// linked pull requests first, which is what closes a merged task and frees
// whatever it was blocking without anyone having to ask.
func (h Handler) issueList(ctx context.Context, req protocol.Request, readyOnly bool) (protocol.Response, error) {
	params, err := decode[ListParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	filter := issues.Filter{
		Plan:  params.Plan,
		State: issues.State(params.State),
		Type:  params.Type,
	}

	warnings, err := h.reconcile(ctx, issues.Filter{Plan: params.Plan})
	if err != nil {
		return protocol.Response{}, err
	}

	var list []issues.Status
	if readyOnly {
		list, err = h.Store.Ready(filter, h.statusOptions())
	} else {
		list, err = h.Store.Statuses(filter, h.statusOptions())
	}
	if err != nil {
		return protocol.Response{}, err
	}

	return reply(StatusList{Issues: list, Warnings: warnings})
}

func (h Handler) issueView(req protocol.Request) (protocol.Response, error) {
	params, err := decode[ViewParams](req)
	if err != nil {
		return protocol.Response{}, err
	}
	return h.detail(params.Number)
}

func (h Handler) issueEdit(req protocol.Request) (protocol.Response, error) {
	params, err := decode[EditParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	if _, err := h.Store.Edit(params.Number, issues.Edit{
		Title:           params.Title,
		Type:            params.Type,
		Body:            params.Body,
		Acceptance:      params.Acceptance,
		Parent:          params.Parent,
		AddBlockedBy:    params.AddBlockedBy,
		RemoveBlockedBy: params.RemoveBlockedBy,
	}); err != nil {
		return protocol.Response{}, err
	}
	return h.detail(params.Number)
}

func (h Handler) issueComment(req protocol.Request) (protocol.Response, error) {
	params, err := decode[CommentParams](req)
	if err != nil {
		return protocol.Response{}, err
	}
	if err := h.Store.Comment(params.Number, params.Author, params.Body); err != nil {
		return protocol.Response{}, err
	}
	return h.detail(params.Number)
}

func (h Handler) issueLinkPR(req protocol.Request) (protocol.Response, error) {
	params, err := decode[LinkPRParams](req)
	if err != nil {
		return protocol.Response{}, err
	}
	if _, err := h.Store.LinkPR(params.Number, params.URL); err != nil {
		return protocol.Response{}, err
	}
	return h.detail(params.Number)
}

func (h Handler) issueClose(req protocol.Request) (protocol.Response, error) {
	params, err := decode[CloseParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	_, warnings, err := h.Store.CloseIssue(params.Number, params.Reason)
	if err != nil {
		return protocol.Response{}, err
	}

	status, err := h.Store.Status(params.Number, h.statusOptions())
	if err != nil {
		return protocol.Response{}, err
	}
	return reply(CloseResult{Issue: status, Warnings: warnings})
}

// issueReopen puts a closed issue back in play, so an agent can have another
// attempt at work that was recorded as finished.
func (h Handler) issueReopen(req protocol.Request) (protocol.Response, error) {
	params, err := decode[ReopenParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	if _, err := h.Store.ReopenIssue(params.Number); err != nil {
		return protocol.Response{}, err
	}
	return h.detail(params.Number)
}

func (h Handler) issueReindex(req protocol.Request) (protocol.Response, error) {
	params, err := decode[ReindexParams](req)
	if err != nil {
		return protocol.Response{}, err
	}

	refreshed, err := h.Store.Reindex(params.Plan)
	if err != nil {
		return protocol.Response{}, err
	}
	return reply(ReindexResult{Refreshed: refreshed})
}

// detail is the reply every single-issue operation gives: the issue's state
// after the change, along with its prose and activity.
func (h Handler) detail(number int64) (protocol.Response, error) {
	status, err := h.Store.Status(number, h.statusOptions())
	if err != nil {
		return protocol.Response{}, err
	}

	file, err := h.Store.Content(number)
	if err != nil {
		return protocol.Response{}, err
	}

	runs, err := h.Store.Runs(number)
	if err != nil {
		return protocol.Response{}, err
	}

	return reply(IssueDetail{Issue: status, Body: file.Body, Comments: file.Comments, Runs: runs})
}

// reconcile settles linked pull requests before a listing. A failure here is
// reported, never fatal: pib tracks issues perfectly well with gh missing.
func (h Handler) reconcile(ctx context.Context, filter issues.Filter) ([]string, error) {
	if h.Lookup == nil {
		return nil, nil
	}
	result, err := h.Store.Reconcile(ctx, filter, issues.ReconcileOptions{Lookup: h.Lookup})
	if err != nil {
		return nil, err
	}
	return result.Warnings, nil
}

// statusOptions hands the store the type mapping it deliberately does not
// read for itself.
func (h Handler) statusOptions() issues.StatusOptions {
	return issues.StatusOptions{AgentFor: h.Config.AgentFor}
}

// decode reads an operation's parameters. An absent payload is the zero
// value, so an operation with nothing to say need not send one.
func decode[T any](req protocol.Request) (T, error) {
	var params T
	if len(req.Payload) == 0 {
		return params, nil
	}
	if err := json.Unmarshal(req.Payload, &params); err != nil {
		return params, fmt.Errorf("%s: unreadable payload: %w", req.Op, err)
	}
	return params, nil
}

// reply marshals a result into a successful response.
func reply(result any) (protocol.Response, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return protocol.Response{}, err
	}
	return protocol.Response{Status: protocol.StatusOK, Payload: body}, nil
}
