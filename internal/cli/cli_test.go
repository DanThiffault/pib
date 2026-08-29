package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pib/internal/config"
	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/server"
)

// harness runs a real pib server over a temporary socket, so the tests
// exercise the whole path: flags, the wire, the store, and the output.
type harness struct {
	socket string
	dir    string
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
	body := "[types]\nfeature = \"\"\ntask = \"worker\"\nresearch = \"researcher\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadPaths(cfgPath, "")
	if err != nil {
		t.Fatal(err)
	}

	srv, err := server.Listen(t.TempDir(), server.Router{
		Issues: issueops.Handler{Store: store, Config: cfg},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })

	return &harness{socket: srv.Addr(), dir: dir}
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
