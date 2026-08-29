package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"pib/internal/protocol"
)

// recorder answers every request with the op it was given, so a test can see
// which side of the router a request landed on.
type recorder struct {
	name string
	seen []protocol.Op
}

func (r *recorder) Run(_ context.Context, req protocol.Request) (protocol.Response, error) {
	r.seen = append(r.seen, req.Op)
	return protocol.Response{Status: protocol.StatusOK, Text: r.name, Payload: req.Payload}, nil
}

func TestRouterSplitsAgentOperationsFromIssueOperations(t *testing.T) {
	agents, issues := &recorder{name: "agents"}, &recorder{name: "issues"}
	router := Router{Agents: agents, Issues: issues}

	cases := map[protocol.Op]string{
		protocol.OpSpawn:        "agents",
		protocol.OpResume:       "agents",
		protocol.OpPlanApply:    "issues",
		protocol.OpPlanList:     "issues",
		protocol.OpPlanView:     "issues",
		protocol.OpIssueCreate:  "issues",
		protocol.OpIssueList:    "issues",
		protocol.OpIssueView:    "issues",
		protocol.OpIssueEdit:    "issues",
		protocol.OpIssueComment: "issues",
		protocol.OpIssueLinkPR:  "issues",
		protocol.OpIssueClose:   "issues",
		protocol.OpIssueReady:   "issues",
		protocol.OpIssueReindex: "issues",
	}

	for op, want := range cases {
		resp, err := router.Run(context.Background(), protocol.Request{Op: op})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if resp.Text != want {
			t.Errorf("%s went to %q, want %q", op, resp.Text, want)
		}
	}

	if len(agents.seen) != 2 {
		t.Errorf("the runner saw %v, want just spawn and resume", agents.seen)
	}
}

func TestRouterRefusesWhatItCannotServe(t *testing.T) {
	onlyAgents := Router{Agents: &recorder{name: "agents"}}
	if _, err := onlyAgents.Run(context.Background(), protocol.Request{Op: protocol.OpIssueList}); err == nil ||
		!strings.Contains(err.Error(), "no issue store") {
		t.Errorf("err = %v, want a refusal naming the missing store", err)
	}

	onlyIssues := Router{Issues: &recorder{name: "issues"}}
	if _, err := onlyIssues.Run(context.Background(), protocol.Request{Op: protocol.OpSpawn}); err == nil ||
		!strings.Contains(err.Error(), "cannot run agents") {
		t.Errorf("err = %v, want a refusal naming agents", err)
	}
}

func TestPayloadSurvivesTheWire(t *testing.T) {
	issues := &recorder{name: "issues"}
	srv := listen(t, Router{Issues: issues})

	sent := json.RawMessage(`{"plan":"orders","nested":{"n":7}}`)
	resp := call(t, srv, protocol.Request{Op: protocol.OpIssueList, Payload: sent})

	if resp.Error != "" {
		t.Fatalf("error = %q", resp.Error)
	}
	if resp.Status != protocol.StatusOK {
		t.Errorf("status = %q, want %q", resp.Status, protocol.StatusOK)
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("payload did not survive: %v", err)
	}
	if got["plan"] != "orders" {
		t.Errorf("payload = %v", got)
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok || nested["n"] != float64(7) {
		t.Errorf("nested payload = %v", got["nested"])
	}
}
