package issueops

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/server"
)

// TestOverTheSocket drives the handler the way the command line will: through
// a real listener, one request per connection.
func TestOverTheSocket(t *testing.T) {
	h := handler(t)

	srv, err := server.Listen(t.TempDir(), server.Router{Issues: h})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	send := func(op protocol.Op, params any) protocol.Response {
		t.Helper()

		var payload json.RawMessage
		if params != nil {
			body, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			payload = body
		}

		conn, err := net.Dial("unix", srv.Addr())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		if err := json.NewEncoder(conn).Encode(protocol.Request{Op: op, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		var resp protocol.Response
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	applied := send(protocol.OpPlanApply, document())
	if applied.Error != "" {
		t.Fatalf("plan.apply: %s", applied.Error)
	}
	var result issues.ApplyResult
	if err := json.Unmarshal(applied.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 3 {
		t.Errorf("created %v over the socket", result.Created)
	}

	ready := send(protocol.OpIssueReady, ListParams{Plan: "orders"})
	if ready.Error != "" {
		t.Fatalf("issue.ready: %s", ready.Error)
	}
	var list StatusList
	if err := json.Unmarshal(ready.Payload, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Issues) != 2 {
		t.Fatalf("ready = %d issues, want 2", len(list.Issues))
	}
	for _, status := range list.Issues {
		if status.Type == "task" && status.Agent != "worker" {
			t.Errorf("#%d would be run by %q, want the worker", status.Number, status.Agent)
		}
	}

	// A store error comes back as an error on the response, not a dropped
	// connection.
	missing := send(protocol.OpIssueView, ViewParams{Number: 404})
	if missing.Error == "" {
		t.Error("viewing a missing issue succeeded over the socket")
	}

	// An agent op has nowhere to go on an issues-only router.
	spawn := send(protocol.OpSpawn, nil)
	if spawn.Error == "" {
		t.Error("spawn succeeded on a router with no runner")
	}
}
