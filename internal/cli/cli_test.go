package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pib/internal/config"
	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/recheck"
	"pib/internal/server"
)

// harness runs a real pib server over a temporary socket, so the tests
// exercise the whole path: flags, the wire, the store, and the output.
type harness struct {
	socket string
	dir    string
	agents *fakeAgents
	store  *issues.Store
}

// fakeAgents stands in for the runner: it records the spawn and answers as a
// finished agent would, including writing the run row the real runner writes
// — which is what a followup later looks for.
type fakeAgents struct {
	// mu guards requests: `pib plan start` spawns every ready issue at once,
	// so several of these land in parallel.
	mu       sync.Mutex
	requests []protocol.Request
	resp     protocol.Response
	err      error
	store    *issues.Store
}

func (f *fakeAgents) Run(_ context.Context, req protocol.Request) (protocol.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.err != nil {
		return protocol.Response{}, f.err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	resp := f.resp
	if req.Op == protocol.OpSpawnBackground {
		resp = protocol.Response{Status: protocol.StatusOK, Session: f.resp.Session}
	}
	if id := resp.Session; id != "" && f.store != nil {
		agent := req.Agent
		if agent == "" {
			agent, _ = f.store.RunAgent(id)
		}
		if err := f.store.StartRun(id, req.Issue, agent, "@1"); err != nil {
			return protocol.Response{}, err
		}
		if req.Op != protocol.OpSpawnBackground {
			if err := f.store.FinishRun(id, resp.Status); err != nil {
				return protocol.Response{}, err
			}
		}
	}
	return resp, nil
}

const planDocument = `{
  "plan": { "slug": "orders", "title": "Order placement" },
  "issues": [
    { "id": "feature", "type": "feature", "title": "Feature: order placement" },
    { "id": "schema", "type": "task", "title": "Order schema",
      "parent": "feature", "body": "## Task\n\nWrite the schema.",
      "acceptance": ["Tables exist"] },
    { "id": "agg", "type": "task", "title": "Aggregate",
      "parent": "feature", "blockedBy": ["schema"] }
  ]
}`

func setup(t *testing.T) *harness {
	t.Helper()

	store, err := issues.Open(issues.DataDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.FileName)
	// Review off: these tests are about the operations, not the review gate,
	// and an extra issue blocking every root would rewrite every expectation.
	body := "[types]\nfeature = \"\"\ntask = \"worker\"\nresearch = \"researcher\"\n" +
		"[plan]\nreview = false\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadPaths(cfgPath, "")
	if err != nil {
		t.Fatal(err)
	}

	agents := &fakeAgents{
		resp:  protocol.Response{Status: "done", Text: "implemented it", Session: "run-1"},
		store: store,
	}
	srv, err := server.Listen(t.TempDir(), server.Router{
		Agents: agents,
		Issues: issueops.Handler{Store: store, Config: cfg},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })

	return &harness{socket: srv.Addr(), dir: dir, agents: agents, store: store}
}

// run invokes the command line and returns what a user would see.
func (h *harness) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return h.input(t, "", args...)
}

func (h *harness) input(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := App{
		Args:   args,
		Stdin:  strings.NewReader(stdin),
		Stdout: &stdout,
		Stderr: &stderr,
		Socket: h.socket,
	}.Run()
	return code, stdout.String(), stderr.String()
}

// ok runs a command that must succeed.
func (h *harness) ok(t *testing.T, args ...string) string {
	t.Helper()

	code, stdout, stderr := h.run(t, args...)
	if code != 0 {
		t.Fatalf("pib %s exited %d: %s", strings.Join(args, " "), code, stderr)
	}
	return stdout
}

// planFile writes the sample plan document and returns its path.
func (h *harness) planFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(h.dir, "plan.json")
	if err := os.WriteFile(path, []byte(planDocument), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// applied applies the sample plan.
func (h *harness) applied(t *testing.T) {
	t.Helper()
	h.ok(t, "plan", "apply", h.planFile(t))
}

func TestPlanApplyFromAFile(t *testing.T) {
	h := setup(t)

	out := h.ok(t, "plan", "apply", h.planFile(t))
	if !strings.Contains(out, "Applied plan orders") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "3 issues created") || !strings.Contains(out, "#1, #2 and #3") {
		t.Errorf("output = %q, want the created issues named", out)
	}
}

func TestPlanApplyFromStandardInput(t *testing.T) {
	h := setup(t)

	code, out, stderr := h.input(t, planDocument, "plan", "apply", "-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "Applied plan orders") {
		t.Errorf("output = %q", out)
	}
}

func TestPlanApplyReportsWarningsOnStandardError(t *testing.T) {
	h := setup(t)

	document := `{"plan":{"slug":"p","title":"P"},
	  "issues":[{"id":"a","type":"chore","title":"A"}]}`
	code, out, stderr := h.input(t, document, "plan", "apply", "-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, `warning: no agent is mapped to type "chore"`) {
		t.Errorf("stderr = %q, want the warning", stderr)
	}
	if strings.Contains(out, "warning") {
		t.Errorf("stdout = %q, want warnings kept off it", out)
	}
}

func TestPlanApplyRejectsBadInput(t *testing.T) {
	h := setup(t)

	if code, _, stderr := h.input(t, "{not json", "plan", "apply", "-"); code == 0 {
		t.Errorf("malformed json exited 0: %s", stderr)
	}
	if code, _, _ := h.run(t, "plan", "apply", filepath.Join(h.dir, "missing.json")); code == 0 {
		t.Error("a missing file exited 0")
	}
	if code, _, _ := h.run(t, "plan", "apply"); code != 2 {
		t.Error("a missing argument should be a usage error")
	}
}

func TestPlanListAndShow(t *testing.T) {
	h := setup(t)

	if out := h.ok(t, "plan", "list"); !strings.Contains(out, "No plans yet") {
		t.Errorf("empty listing = %q", out)
	}

	h.applied(t)

	out := h.ok(t, "plan", "list")
	if !strings.Contains(out, "orders") || !strings.Contains(out, "Order placement") {
		t.Errorf("plan list = %q", out)
	}

	out = h.ok(t, "plan", "view", "orders")
	for _, want := range []string{"orders — Order placement", "Order schema", "Aggregate"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan view missing %q:\n%s", want, out)
		}
	}

	if code, _, _ := h.run(t, "plan", "view", "nope"); code != 1 {
		t.Error("showing a missing plan should fail")
	}
}

func TestIssueListShowsStateAndWhatWouldRunIt(t *testing.T) {
	h := setup(t)
	h.applied(t)

	out := h.ok(t, "issue", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected a header and three issues:\n%s", out)
	}
	if !strings.HasPrefix(lines[0], "ISSUE") {
		t.Errorf("header = %q", lines[0])
	}

	// #2 is the schema task: ready, and the worker would run it.
	if !strings.Contains(lines[2], "ready") || !strings.Contains(lines[2], "worker") {
		t.Errorf("schema row = %q", lines[2])
	}
	// #3 waits on it.
	if !strings.Contains(lines[3], "blocked") || !strings.Contains(lines[3], "waiting on #2") {
		t.Errorf("aggregate row = %q", lines[3])
	}
	// #1 is a container: ready, but nothing runs it.
	if !strings.Contains(lines[1], "no agent for this type") {
		t.Errorf("feature row = %q", lines[1])
	}
}

func TestReadyBothWays(t *testing.T) {
	h := setup(t)
	h.applied(t)

	viaCommand := h.ok(t, "issue", "ready")
	viaFlag := h.ok(t, "issue", "list", "--ready")
	if viaCommand != viaFlag {
		t.Errorf("issue ready and issue list --ready disagree:\n%s\n%s", viaCommand, viaFlag)
	}
	if strings.Contains(viaCommand, "Aggregate") {
		t.Errorf("a blocked issue appeared in the ready set:\n%s", viaCommand)
	}
}

func TestIssueCreateEditCommentAndView(t *testing.T) {
	h := setup(t)
	h.applied(t)

	out := h.ok(t, "issue", "create",
		"--plan", "orders", "--type", "research", "--title", "Compare event stores",
		"--body", "## Research\n\nCompare them.",
		"--acceptance", "A recommendation exists", "--acceptance", "Trade-offs written down")
	if !strings.Contains(out, "#4  Compare event stores") {
		t.Fatalf("create output = %q", out)
	}
	for _, want := range []string{"researcher", "A recommendation exists", "## Research"} {
		if !strings.Contains(out, want) {
			t.Errorf("create output missing %q:\n%s", want, out)
		}
	}

	out = h.ok(t, "issue", "edit", "4", "--title", "Compare event stores properly")
	if !strings.Contains(out, "Compare event stores properly") {
		t.Errorf("edit output = %q", out)
	}

	out = h.ok(t, "issue", "comment", "4", "--body", "Postgres wins.", "--author", "researcher")
	if !strings.Contains(out, "Activity") || !strings.Contains(out, "Postgres wins.") {
		t.Errorf("comment output = %q", out)
	}

	out = h.ok(t, "issue", "view", "4")
	if !strings.Contains(out, "researcher") || !strings.Contains(out, "Postgres wins.") {
		t.Errorf("view output = %q", out)
	}
}

func TestIssueEditWiresDependencies(t *testing.T) {
	h := setup(t)
	h.applied(t)

	h.ok(t, "issue", "edit", "2", "--add-blocked-by", "1")
	out := h.ok(t, "issue", "view", "2")
	if !strings.Contains(out, "blocked") {
		t.Errorf("view = %q, want the issue blocked", out)
	}

	h.ok(t, "issue", "edit", "2", "--remove-blocked-by", "1")
	out = h.ok(t, "issue", "view", "2")
	if !strings.Contains(out, "ready") {
		t.Errorf("view = %q, want the issue ready again", out)
	}

	if code, _, stderr := h.run(t, "issue", "edit", "2"); code != 2 ||
		!strings.Contains(stderr, "nothing to change") {
		t.Errorf("an edit with no changes gave %d: %s", code, stderr)
	}
}

func TestLinkPRAndClose(t *testing.T) {
	h := setup(t)
	h.applied(t)

	out := h.ok(t, "issue", "link-pr", "2", "https://github.com/o/r/pull/7")
	if !strings.Contains(out, "in review") || !strings.Contains(out, "pull/7") {
		t.Errorf("link-pr output = %q", out)
	}

	code, stdout, stderr := h.run(t, "issue", "close", "2", "--reason", "abandoned")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Closed #2") {
		t.Errorf("close output = %q", stdout)
	}
	// Closing a task whose pull request has not merged is allowed, and said.
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "pull request") {
		t.Errorf("stderr = %q, want the merge rule reported", stderr)
	}
}

func TestReindex(t *testing.T) {
	h := setup(t)
	h.applied(t)

	if out := h.ok(t, "issue", "reindex"); !strings.Contains(out, "Re-read 3 issue files") {
		t.Errorf("reindex output = %q", out)
	}
	if out := h.ok(t, "issue", "reindex", "--plan", "orders"); !strings.Contains(out, "3 issue files") {
		t.Errorf("reindex output = %q", out)
	}
}

func TestJSONOutputIsCleanAndParsable(t *testing.T) {
	h := setup(t)
	h.applied(t)

	out := h.ok(t, "issue", "list", "--json")
	var listing issueops.StatusList
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("issue list --json is not parsable: %v\n%s", err, out)
	}
	if len(listing.Issues) != 3 {
		t.Errorf("listing = %d issues", len(listing.Issues))
	}
	if listing.Issues[1].Agent != "worker" {
		t.Errorf("the agent is missing from json output: %+v", listing.Issues[1])
	}

	// A warning belongs in the payload here, not printed alongside it.
	code, stdout, stderr := h.input(t, `{"plan":{"slug":"p","title":"P"},
	  "issues":[{"id":"a","type":"chore","title":"A"}]}`, "plan", "apply", "-", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want json mode to print nothing but json", stderr)
	}
	var applied issues.ApplyResult
	if err := json.Unmarshal([]byte(stdout), &applied); err != nil {
		t.Fatalf("not parsable: %v\n%s", err, stdout)
	}
	if len(applied.Warnings) != 1 {
		t.Errorf("warnings = %v, want them in the payload", applied.Warnings)
	}
}

func TestFlagsAndPositionalsInEitherOrder(t *testing.T) {
	h := setup(t)
	h.applied(t)

	before := h.ok(t, "issue", "view", "--json", "2")
	after := h.ok(t, "issue", "view", "2", "--json")
	if before != after {
		t.Errorf("argument order changed the result:\n%s\n%s", before, after)
	}
}

func TestIssueNumbersMayCarryAHash(t *testing.T) {
	h := setup(t)
	h.applied(t)

	if out := h.ok(t, "issue", "view", "#2"); !strings.Contains(out, "#2  Order schema") {
		t.Errorf("view #2 = %q", out)
	}
	if code, _, stderr := h.run(t, "issue", "view", "banana"); code != 2 ||
		!strings.Contains(stderr, "not an issue number") {
		t.Errorf("a bad number gave %d: %s", code, stderr)
	}
}

func TestErrorsFromTheStoreExitNonZero(t *testing.T) {
	h := setup(t)
	h.applied(t)

	code, _, stderr := h.run(t, "issue", "view", "404")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("stderr = %q, want the store's message", stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	h := setup(t)

	cases := [][]string{
		{},
		{"nonsense"},
		{"issue"},
		{"issue", "nonsense"},
		{"plan"},
		{"issue", "create", "--plan", "orders"},
		{"issue", "view"},
		{"issue", "link-pr", "2"},
	}
	for _, args := range cases {
		if code, _, _ := h.run(t, args...); code != 2 {
			t.Errorf("pib %s exited %d, want a usage error", strings.Join(args, " "), code)
		}
	}

	if code, _, stderr := h.run(t, "help"); code != 0 || !strings.Contains(stderr, "pib issue ready") {
		t.Errorf("help exited %d: %s", code, stderr)
	}
}

func TestNoListenerSaysHowToFixIt(t *testing.T) {
	h := &harness{socket: filepath.Join(t.TempDir(), "absent.sock"), dir: t.TempDir()}

	code, _, stderr := h.run(t, "issue", "list")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "pib is not running") || !strings.Contains(stderr, "Start pib in this repository") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestIssueStartRunsTheMappedAgent(t *testing.T) {
	h := setup(t)
	h.applied(t)

	code, stdout, stderr := h.run(t, "issue", "start", "2")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "implemented it") {
		t.Errorf("stdout = %q, want the agent's answer", stdout)
	}
	if !strings.Contains(stderr, "Starting worker on #2") {
		t.Errorf("stderr = %q, want it to say what it started", stderr)
	}

	if len(h.agents.seen()) != 1 {
		t.Fatalf("spawned %d agents", len(h.agents.seen()))
	}
	req := h.agents.seen()[0]
	if req.Op != protocol.OpSpawn || req.Agent != "worker" {
		t.Errorf("request = %+v, want a worker spawn", req)
	}
	if req.Issue != 2 {
		t.Errorf("Issue = %d, want 2 so the run is recorded against it", req.Issue)
	}
	if !strings.Contains(req.Task, "pib issue view 2") || !strings.Contains(req.Task, "PIB_ISSUE") {
		t.Errorf("task = %q, want it pointed at the issue", req.Task)
	}
}

func TestIssueStartRefusesWhatCannotRun(t *testing.T) {
	h := setup(t)
	h.applied(t)

	// #3 waits on #2.
	code, _, stderr := h.run(t, "issue", "start", "3")
	if code != 1 || !strings.Contains(stderr, "waiting on #2") {
		t.Errorf("starting a blocked issue gave %d: %s", code, stderr)
	}
	if len(h.agents.seen()) != 0 {
		t.Error("a blocked issue was started anyway")
	}

	// #1 is a container, so nothing is mapped to run it.
	code, _, stderr = h.run(t, "issue", "start", "1")
	if code != 1 || !strings.Contains(stderr, `no agent is mapped to type "feature"`) {
		t.Errorf("starting a container gave %d: %s", code, stderr)
	}
}

func TestIssueStartCanBeForcedAndOverridden(t *testing.T) {
	h := setup(t)
	h.applied(t)

	if code, _, stderr := h.run(t, "issue", "start", "3", "--force"); code != 0 {
		t.Fatalf("--force exit %d: %s", code, stderr)
	}
	if h.agents.seen()[0].Issue != 3 {
		t.Errorf("request = %+v", h.agents.seen()[0])
	}

	if code, _, stderr := h.run(t, "issue", "start", "1", "--agent", "scout"); code != 0 {
		t.Fatalf("--agent exit %d: %s", code, stderr)
	}
	if got := h.agents.seen()[1].Agent; got != "scout" {
		t.Errorf("agent = %q, want the override", got)
	}
}

func TestIssueStartReportsAnAgentThatStopped(t *testing.T) {
	h := setup(t)
	h.applied(t)
	h.agents.resp = protocol.Response{Status: "needs_input", Text: "Postgres or SQLite?", Session: "run-9"}

	code, stdout, stderr := h.run(t, "issue", "start", "2")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "needs an answer") || !strings.Contains(stdout, "Postgres or SQLite?") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "run-9") {
		t.Errorf("stderr = %q, want the session to resume from", stderr)
	}
}

// worked runs an agent against an issue so there is a session to follow up.
func (h *harness) worked(t *testing.T, number string) {
	t.Helper()
	h.ok(t, "issue", "start", number)
}

func TestFollowupResumesTheLastRun(t *testing.T) {
	h := setup(t)
	h.applied(t)
	h.worked(t, "2")

	h.agents.resp = protocol.Response{Status: "done", Text: "addressed the comments"}
	code, stdout, stderr := h.run(t, "issue", "followup", "2",
		"--message", "address the comments I left on the PR")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "addressed the comments") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "Following up with worker on #2") {
		t.Errorf("stderr = %q", stderr)
	}

	if len(h.agents.seen()) != 2 {
		t.Fatalf("%d requests, want the start and the followup", len(h.agents.seen()))
	}
	req := h.agents.seen()[1]
	if req.Op != protocol.OpResume {
		t.Errorf("op = %q, want a resume", req.Op)
	}
	if req.Session != "run-1" {
		t.Errorf("session = %q, want the id of the run being continued", req.Session)
	}
	if req.Answer != "address the comments I left on the PR" {
		t.Errorf("answer = %q", req.Answer)
	}
	if req.Issue != 2 || req.Name != "worker #2" {
		t.Errorf("request = %+v", req)
	}
}

func TestFollowupTakesAMessageFile(t *testing.T) {
	h := setup(t)
	h.applied(t)
	h.worked(t, "2")

	code, _, stderr := h.input(t, "the review is on the PR", "issue", "followup", "2", "--message-file", "-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := h.agents.seen()[1].Answer; got != "the review is on the PR" {
		t.Errorf("answer = %q", got)
	}
}

func TestFollowupRefusesWhatItCannotContinue(t *testing.T) {
	h := setup(t)
	h.applied(t)

	// Never worked on.
	code, _, stderr := h.run(t, "issue", "followup", "2", "--message", "hello")
	if code != 1 || !strings.Contains(stderr, "never been worked on") {
		t.Errorf("followup on an unworked issue gave %d: %s", code, stderr)
	}
	if len(h.agents.seen()) != 0 {
		t.Error("it resumed something anyway")
	}

	// No message.
	h.worked(t, "2")
	if code, _, _ := h.run(t, "issue", "followup", "2"); code != 2 {
		t.Errorf("a followup with no message gave %d, want a usage error", code)
	}
}

func TestFollowupRefusesAClosedIssueUnlessForced(t *testing.T) {
	h := setup(t)
	h.applied(t)
	h.worked(t, "2")
	h.ok(t, "issue", "close", "2")

	code, _, stderr := h.run(t, "issue", "followup", "2", "--message", "one more thing")
	if code != 1 || !strings.Contains(stderr, "is closed") {
		t.Errorf("followup on a closed issue gave %d: %s", code, stderr)
	}

	before := len(h.agents.seen())
	if code, _, stderr := h.run(t, "issue", "followup", "2", "--message", "one more thing", "--force"); code != 0 {
		t.Fatalf("--force exit %d: %s", code, stderr)
	}
	if len(h.agents.seen()) != before+1 {
		t.Error("--force did not resume the session")
	}
}

func TestFollowupWaitsForALiveRun(t *testing.T) {
	h := setup(t)
	h.applied(t)
	h.worked(t, "2")

	// A run that started and has not ended.
	if err := h.store.StartRun("run-live", 2, "worker", "@7"); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := h.run(t, "issue", "followup", "2", "--message", "hurry up")
	if code != 1 || !strings.Contains(stderr, "already working on #2") {
		t.Errorf("followup during a live run gave %d: %s", code, stderr)
	}
}

func TestPlanReviewSpawnsTheReviewer(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))

	h.ok(t, "plan", "review", "orders")

	if len(h.agents.seen()) != 1 {
		t.Fatalf("spawned %d agents, want 1", len(h.agents.seen()))
	}
	req := h.agents.seen()[0]
	if req.Agent != recheck.ReviewerName {
		t.Errorf("agent = %q, want %q", req.Agent, recheck.ReviewerName)
	}
	if req.Issue != 0 {
		t.Errorf("issue = %d, want 0 — the reviewer reviews the plan, not an issue", req.Issue)
	}
	if !strings.Contains(req.Task, "orders") {
		t.Errorf("task does not name the plan: %q", req.Task)
	}
}

// A typo in the slug should fail before an agent opens a window.
func TestPlanReviewOnAnUnknownPlanSpawnsNothing(t *testing.T) {
	h := setup(t)

	if code, _, _ := h.run(t, "plan", "review", "nope"); code == 0 {
		t.Error("reviewing a plan that does not exist succeeded")
	}
	if len(h.agents.seen()) != 0 {
		t.Errorf("spawned %d agents for an unknown plan, want 0", len(h.agents.seen()))
	}
}

func TestIssueReopenPutsAClosedIssueBackInPlay(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))
	h.ok(t, "issue", "close", "2", "--reason", "done")

	if out := h.ok(t, "issue", "view", "2"); !strings.Contains(out, "closed") {
		t.Fatalf("setup failed, #2 is not closed: %q", out)
	}

	out := h.ok(t, "issue", "reopen", "2")
	if strings.Contains(out, "closed") {
		t.Errorf("reopen output still says closed: %q", out)
	}
	if !strings.Contains(out, "#2") {
		t.Errorf("reopen output does not show the issue: %q", out)
	}

	// Whatever depended on it is blocked again.
	if out := h.ok(t, "issue", "view", "3"); !strings.Contains(out, "waiting on #2") {
		t.Errorf("#3 did not go back to waiting on #2: %q", out)
	}
}

func TestIssueReopenOnAnUnknownIssueFails(t *testing.T) {
	h := setup(t)
	if code, _, _ := h.run(t, "issue", "reopen", "99"); code == 0 {
		t.Error("reopening an issue that does not exist succeeded")
	}
}

// planStart takes the ready set once. An issue unblocked by something it
// started belongs to the next invocation, or one command would run a whole
// plan with no moment to look at what came back.
// seen copies the recorded spawns under the lock.
func (f *fakeAgents) seen() []protocol.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Request(nil), f.requests...)
}

func TestPlanStartLaunchesOnlyWhatWasReady(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))

	// #1 feature (no agent), #2 schema (ready), #3 aggregate (waits on #2).
	out := h.ok(t, "plan", "start", "orders")

	var started []int64
	for _, req := range h.agents.seen() {
		started = append(started, req.Issue)
	}
	if len(started) != 1 || started[0] != 2 {
		t.Fatalf("started %v, want only #2 — the one issue that was ready", started)
	}
	if !strings.Contains(out, "#2") {
		t.Errorf("output does not mention the issue it started: %q", out)
	}

	// #3 became ready as a result, and must not have been picked up.
	for _, req := range h.agents.seen() {
		if req.Issue == 3 {
			t.Error("#3 was started; it only became ready because #2 did")
		}
	}
}

func TestPlanStartRunsReadyIssuesTogether(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))
	// Free the aggregate so two issues are ready at once.
	h.ok(t, "issue", "edit", "3", "--remove-blocked-by", "2")

	h.ok(t, "plan", "start", "orders")

	started := map[int64]bool{}
	for _, req := range h.agents.seen() {
		started[req.Issue] = true
		if req.Agent != "worker" {
			t.Errorf("#%d started with agent %q, want the mapped worker", req.Issue, req.Agent)
		}
		if !strings.Contains(req.Task, "pib issue view") {
			t.Errorf("#%d got a briefing the CLI does not share with issue start: %q", req.Issue, req.Task)
		}
	}
	if !started[2] || !started[3] {
		t.Errorf("started %v, want both ready issues", started)
	}
}

// The feature issue is ready but its type maps to no agent, so nothing can run
// it. That is worth saying rather than silently skipping.
func TestPlanStartReportsWhatItCannotRun(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))
	h.ok(t, "issue", "close", "2")
	h.ok(t, "issue", "close", "3")

	code, _, stderr := h.run(t, "plan", "start", "orders")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(h.agents.seen()) != 0 {
		t.Errorf("started %d agents, want none — only the unmappable feature was ready", len(h.agents.seen()))
	}
	if !strings.Contains(stderr, "#1") {
		t.Errorf("stderr does not mention the issue it could not run: %q", stderr)
	}
}

func TestPlanStartIsNonBlockingByDefault(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))

	out := h.ok(t, "plan", "start", "orders")

	reqs := h.agents.seen()
	if len(reqs) != 1 || reqs[0].Op != protocol.OpSpawnBackground {
		t.Fatalf("expected OpSpawnBackground, got %+v", reqs)
	}
	if !strings.Contains(out, "started") {
		t.Errorf("output does not say the agent started: %q", out)
	}
}

func TestPlanStartBlocksWithWaitFlag(t *testing.T) {
	h := setup(t)
	h.ok(t, "plan", "apply", h.planFile(t))

	out := h.ok(t, "plan", "start", "--wait", "orders")

	reqs := h.agents.seen()
	if len(reqs) != 1 || reqs[0].Op != protocol.OpSpawn {
		t.Fatalf("expected OpSpawn with --wait, got %+v", reqs)
	}
	if !strings.Contains(out, "implemented it") {
		t.Errorf("output does not contain the agent result: %q", out)
	}
}
