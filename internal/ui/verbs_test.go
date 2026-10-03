package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/recheck"
	"pib/internal/runner"
	"pib/internal/ui/command"
)

// issuesModelFrom loads the issues screen's rows straight from the store, so
// they carry the state the store derives rather than a test's say-so.
func issuesModelFrom(t *testing.T, store *issues.Store, plan string) Model {
	t.Helper()
	m := plansModel(t, []issues.Plan{{Slug: plan, Title: plan}})
	m.screen = screenIssues
	m.drilled = plan
	m.store = store
	m.cfg = defaultCfg(t)
	list, err := store.Statuses(issues.Filter{Plan: plan}, issues.StatusOptions{
		AgentFor:     m.cfg.AgentFor,
		ReviewCycles: m.cfg.ReviewCycles(),
	})
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	m.planIssues = list
	return m
}

// captureEditor makes execProcess run the editor synchronously and records
// the buffer as pib wrote it — the template of a Compose, or the file of an
// Open — before the editor touches it.
func captureEditor(t *testing.T) *string {
	t.Helper()
	old := execProcess
	opened := new(string)
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		body, _ := os.ReadFile(c.Args[len(c.Args)-1])
		*opened = string(body)
		return func() tea.Msg { return fn(c.Run()) }
	}
	t.Cleanup(func() { execProcess = old })
	return opened
}

// editorWrites points EDITOR at a script that replaces the buffer it is
// given with payload, exercising the real `sh -c` invocation.
func editorWrites(t *testing.T, payload string) {
	t.Helper()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload")
	if err := os.WriteFile(payloadPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-editor")
	body := "#!/bin/sh\nfor a in \"$@\"; do :; done\nprintf '%s' \"$(cat " + payloadPath + ")\" > \"$a\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)
}

// editorQuits points EDITOR at a script that leaves the buffer alone: the
// user wrote nothing above the scissors.
func editorQuits(t *testing.T) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "quit-editor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)
}

// typeLine opens the ":" line, types input and submits it.
func typeLine(t *testing.T, m Model, input string) Model {
	t.Helper()
	m = keyPress(t, m, press(":"))
	if !m.line.Active() {
		t.Fatal(": did not open the line")
	}
	for _, r := range input {
		if r == ' ' {
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
			continue
		}
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return keyPress(t, m, press("enter"))
}

// waitFor polls cond until it holds, for the work an operation starts off
// its reply's path.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// plannedStore returns a store with one plan and one task in it.
func plannedStore(t *testing.T) (*issues.Store, issues.Issue) {
	t.Helper()
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Aggregate", Body: "Aggregate the orders."})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store, issue
}

// failedRun ends a run on the issue in error, leaving it needing attention.
func failedRun(t *testing.T, store *issues.Store, number int64) {
	t.Helper()
	if err := store.StartRun(issues.RunStart{ID: "run-1", Issue: number, Agent: "coder", Window: "@9"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := store.FinishRun("run-1", "error"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
}

// ── comment ─────────────────────────────────────────────────────────────

func TestCommentOpensTheEditorWithRecentComments(t *testing.T) {
	store, issue := plannedStore(t)
	for _, body := range []string{"first", "second", "third", "fourth"} {
		if err := store.Comment(issue.Number, "researcher", body); err != nil {
			t.Fatalf("Comment: %v", err)
		}
	}

	opened := captureEditor(t)
	editorWrites(t, "Prefer whatever keeps CGO off.")

	m := issuesModelFrom(t, store, "orders")
	m = keyPress(t, m, press("c"))

	header := fmt.Sprintf("# comment on #%d · Aggregate", issue.Number)
	if !strings.Contains(*opened, header) {
		t.Errorf("the editor buffer is missing its header %q:\n%s", header, *opened)
	}
	// The last three comments show below the scissors; the oldest one does not.
	if strings.Contains(*opened, "first") {
		t.Errorf("the buffer reaches past the last three comments:\n%s", *opened)
	}
	for _, want := range []string{"second", "third", "fourth", "researcher"} {
		if !strings.Contains(*opened, want) {
			t.Errorf("the buffer is missing recent comment %q:\n%s", want, *opened)
		}
	}

	comments, err := store.Comments(issue.Number)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	last := comments[len(comments)-1]
	if last.Body != "Prefer whatever keeps CGO off." {
		t.Errorf("the comment did not land: last = %q", last.Body)
	}
	if last.Author == "" || last.Author == "researcher" {
		t.Errorf("the comment's author = %q, want the user at the keyboard", last.Author)
	}
	if !strings.Contains(m.notice, "commented on #") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestAnEmptyCommentAbortsWithANotice(t *testing.T) {
	store, _ := plannedStore(t)

	captureEditor(t)
	editorQuits(t)

	m := issuesModelFrom(t, store, "orders")
	m = keyPress(t, m, press("c"))

	if !strings.Contains(m.notice, "comment aborted") {
		t.Errorf("notice = %q, want the abort to say so", m.notice)
	}
	comments, err := store.Comments(1)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("an aborted comment still landed: %v", comments)
	}
}

// ── followup ────────────────────────────────────────────────────────────

func TestFollowupResumesTheLastRunWithTheNote(t *testing.T) {
	store, issue := plannedStore(t)
	failedRun(t, store, issue.Number)

	opened := captureEditor(t)
	editorWrites(t, "also handle timeouts")

	agents := &fakeSpawner{}
	m := issuesModelFrom(t, store, "orders")
	// Awaiting review is a row followup applies to.
	m.planIssues[0].AwaitingReview = true
	m.planIssues[0].PRURL = "https://github.com/x/orders/pull/44"
	m.agents = agents

	m = keyPress(t, m, press("f"))

	header := fmt.Sprintf("# followup to #%d · Aggregate · coder", issue.Number)
	if !strings.Contains(*opened, header) {
		t.Errorf("the editor buffer is missing its header %q:\n%s", header, *opened)
	}

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("f sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Op != protocol.OpResume || req.Session != "run-1" || req.Issue != issue.Number {
		t.Errorf("request = %+v, want the last run resumed", req)
	}
	if req.Answer != "also handle timeouts" {
		t.Errorf("answer = %q, want the note", req.Answer)
	}
}

func TestFollowupOnALiveRunRefusesBeforeTheEditor(t *testing.T) {
	store, issue := plannedStore(t)
	if err := store.StartRun(issues.RunStart{ID: "run-1", Issue: issue.Number, Agent: "coder", Window: "@9"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	opened := captureEditor(t)
	agents := &fakeSpawner{}
	m := issuesModelFrom(t, store, "orders")
	m.agents = agents

	m = keyPress(t, m, press("f"))

	if *opened != "" {
		t.Errorf("the editor opened for a run that cannot be resumed:\n%s", *opened)
	}
	if !strings.Contains(m.notice, "already working") {
		t.Errorf("notice = %q, want the live-run refusal", m.notice)
	}
	if got := agents.seen(); len(got) != 0 {
		t.Errorf("a live run was resumed anyway: %v", got)
	}
}

func TestFollowupWithoutARunSaysToStartFirst(t *testing.T) {
	store, _ := plannedStore(t)

	opened := captureEditor(t)
	m := issuesModelFrom(t, store, "orders")
	m.planIssues[0].AwaitingReview = true
	m.planIssues[0].PRURL = "https://github.com/x/orders/pull/44"
	m.agents = &fakeSpawner{}

	m = keyPress(t, m, press("f"))

	if *opened != "" {
		t.Errorf("the editor opened for an issue never worked:\n%s", *opened)
	}
	if !strings.Contains(m.notice, "never been worked on") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestAnEmptyFollowupAbortsWithANotice(t *testing.T) {
	store, issue := plannedStore(t)
	failedRun(t, store, issue.Number)

	captureEditor(t)
	editorQuits(t)

	agents := &fakeSpawner{}
	m := issuesModelFrom(t, store, "orders")
	m.planIssues[0].AwaitingReview = true
	m.planIssues[0].PRURL = "https://github.com/x/orders/pull/44"
	m.agents = agents

	m = keyPress(t, m, press("f"))

	if !strings.Contains(m.notice, "followup aborted") {
		t.Errorf("notice = %q, want the abort to say so", m.notice)
	}
	if got := agents.seen(); len(got) != 0 {
		t.Errorf("an aborted followup still resumed the run: %v", got)
	}
}

// ── answer ──────────────────────────────────────────────────────────────

// questioningModel leaves the issue needing an answer: its run stopped to
// ask, and the question is in the run's exit sidecar under the workspace the
// model believes in.
func questioningModel(t *testing.T) (Model, *issues.Store, issues.Issue) {
	t.Helper()
	store, issue := plannedStore(t)
	if err := store.StartRun(issues.RunStart{ID: "run-1", Issue: issue.Number, Agent: "coder", Window: "@9"}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := store.FinishRun("run-1", "needs_input"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	m := issuesModelFrom(t, store, "orders")
	m.workspace.Dir = t.TempDir()
	runDir := filepath.Join(m.workspace.Dir, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "exit.json"), []byte(`{"type":"ask","message":"Which database?"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return m, store, issue
}

func TestAnswerShowsTheQuestionAndResumesThroughIssueops(t *testing.T) {
	opened := captureEditor(t)
	editorWrites(t, "Postgres, in modernc.")

	agents := &fakeSpawner{}
	m, _, issue := questioningModel(t)
	m.agents = agents

	status, ok := m.selectedIssue()
	if !ok || !status.NeedsAttention || status.AttentionReason != issues.AttentionAsked {
		t.Fatalf("setup: the issue does not read as asked: %+v", status)
	}

	m = keyPress(t, m, press("a"))

	header := fmt.Sprintf("# answer to #%d · Aggregate · coder", issue.Number)
	if !strings.Contains(*opened, header) {
		t.Errorf("the editor buffer is missing its header %q:\n%s", header, *opened)
	}
	if !strings.Contains(*opened, "Which database?") {
		t.Errorf("the editor buffer is missing the agent's question:\n%s", *opened)
	}

	if !strings.Contains(m.notice, "answered #") {
		t.Errorf("notice = %q", m.notice)
	}
	// The resume runs off the reply's path: the handler answers first and the
	// runner picks the run back up behind it.
	waitFor(t, "the resume", func() bool { return len(agents.seen()) == 1 })
	req := agents.seen()[0]
	if req.Op != protocol.OpResume || req.Session != "run-1" || req.Issue != issue.Number {
		t.Errorf("request = %+v, want the asking run resumed", req)
	}
	if req.Answer != "Postgres, in modernc." {
		t.Errorf("answer = %q, want what was written", req.Answer)
	}
}

func TestAnEmptyAnswerAbortsWithANotice(t *testing.T) {
	captureEditor(t)
	editorQuits(t)

	agents := &fakeSpawner{}
	m, _, _ := questioningModel(t)
	m.agents = agents

	m = keyPress(t, m, press("a"))

	if !strings.Contains(m.notice, "answer aborted") {
		t.Errorf("notice = %q, want the abort to say so", m.notice)
	}
	waitFor(t, "no resume to start", func() bool {
		time.Sleep(20 * time.Millisecond)
		return true
	})
	if got := agents.seen(); len(got) != 0 {
		t.Errorf("an aborted answer still resumed the run: %v", got)
	}
}

// ── retry ───────────────────────────────────────────────────────────────

func TestRetryGoesThroughIssueops(t *testing.T) {
	store, issue := plannedStore(t)
	failedRun(t, store, issue.Number)

	agents := &fakeSpawner{}
	m := issuesModelFrom(t, store, "orders")
	m.agents = agents

	status, _ := m.selectedIssue()
	if !status.NeedsAttention || status.AttentionReason != issues.AttentionFailed {
		t.Fatalf("setup: the issue does not read as failed: %+v", status)
	}

	m = keyPress(t, m, press("r"))

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("r sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Op != protocol.OpSpawnBackground || req.Agent != "coder" || req.Issue != issue.Number {
		t.Errorf("request = %+v, want a background spawn of the issue's agent", req)
	}
	if req.Task != runner.Briefing(issue.Number, "Aggregate") {
		t.Errorf("task = %q, want the shared briefing", req.Task)
	}
	if !strings.Contains(m.notice, "retrying #") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestRetryRefusesAClosedIssue(t *testing.T) {
	store, issue := plannedStore(t)
	failedRun(t, store, issue.Number)
	if _, _, err := store.CloseIssue(issue.Number, "not needed"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}

	agents := &fakeSpawner{}
	m := issuesModelFrom(t, store, "orders")
	m.showClosed = true
	m.agents = agents

	// A closed issue offers no retry key; the message still guards.
	next, cmd := m.Update(retryIssueMsg{issue: m.planIssues[0]})
	m = next.(Model)
	m = deliver(t, m, collect(cmd)...)

	if got := agents.seen(); len(got) != 0 {
		t.Errorf("a closed issue was retried: %v", got)
	}
	if !strings.Contains(m.notice, "could not retry") {
		t.Errorf("notice = %q", m.notice)
	}
}

// ── edit ────────────────────────────────────────────────────────────────

func TestEditOpensTheIssueFileAndReindexes(t *testing.T) {
	store, issue := plannedStore(t)
	failedRun(t, store, issue.Number)

	// The store's timestamps are to the second, and the tie-break records a
	// run's end a second past the issue's last write — so the edit has to
	// land in a later second than that to move updated_at past the run.
	time.Sleep(2100 * time.Millisecond)

	path := filepath.Join(store.Dir(), issue.Path)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	opened := captureEditor(t)
	editorWrites(t, string(body)+"\nMore detail: use the event store.\n")

	m := issuesModelFrom(t, store, "orders")
	m = keyPress(t, m, press("e"))

	if !strings.Contains(*opened, "Aggregate the orders.") {
		t.Errorf("the editor did not open the issue's own file:\n%s", *opened)
	}
	if !strings.Contains(m.notice, "edited #") {
		t.Errorf("notice = %q", m.notice)
	}

	// The reindex is what lifts the issue out of needs attention: the file
	// changed, so updated_at moved past the failed run.
	status, err := store.Status(issue.Number, issues.StatusOptions{
		AgentFor:     m.cfg.AgentFor,
		ReviewCycles: m.cfg.ReviewCycles(),
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.NeedsAttention {
		t.Errorf("an edited issue still needs attention: %q", status.AttentionReason)
	}
	file, err := store.Content(issue.Number)
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if !strings.Contains(file.Body, "More detail") {
		t.Errorf("the edit did not land in the file")
	}
}

func TestEditWithoutChangesSaysSo(t *testing.T) {
	store, _ := plannedStore(t)

	captureEditor(t)
	editorQuits(t)

	m := issuesModelFrom(t, store, "orders")
	m = keyPress(t, m, press("e"))

	if !strings.Contains(m.notice, "unchanged") {
		t.Errorf("notice = %q, want it to say nothing changed", m.notice)
	}
}

// ── close and reopen ─────────────────────────────────────────────────────

func TestXPrefillsTheCloseLine(t *testing.T) {
	store, issue := plannedStore(t)

	m := issuesModelFrom(t, store, "orders")
	m = keyPress(t, m, press("x"))

	if !m.line.Active() {
		t.Fatal("x did not open the line")
	}
	if got := m.line.Input(); got != "close " {
		t.Errorf("the line holds %q, want %q prefilled", got, "close ")
	}

	// The reason follows on the same line.
	m = keyPress(t, m, press("enter"))
	closed, err := store.Issue(issue.Number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if closed.State != issues.StateClosed {
		t.Errorf("submitting the prefilled line did not close the issue")
	}
}

func TestCloseWithAReasonRecordsIt(t *testing.T) {
	store, issue := plannedStore(t)

	m := issuesModelFrom(t, store, "orders")
	m = typeLine(t, m, "close the agent was stuck")

	closed, err := store.Issue(issue.Number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if closed.State != issues.StateClosed {
		t.Fatal("the issue did not close")
	}
	comments, err := store.Comments(issue.Number)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "the agent was stuck" {
		t.Errorf("the reason was not recorded: %v", comments)
	}
	if !strings.Contains(m.notice, "closed #") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestCloseWarnsAboutAnUnmergedTask(t *testing.T) {
	store, issue := plannedStore(t)

	m := issuesModelFrom(t, store, "orders")
	m = typeLine(t, m, "close")

	closed, err := store.Issue(issue.Number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if closed.State != issues.StateClosed {
		t.Fatal(":close alone did not close the issue")
	}
	if m.line.Active() {
		t.Error(":close with no reason reopened the line instead of closing")
	}
	if !strings.Contains(m.notice, "normally closes when its pull request merges") {
		t.Errorf("notice = %q, want the unmerged-task warning", m.notice)
	}
}

func TestReopenPutsAClosedIssueBack(t *testing.T) {
	store, issue := plannedStore(t)
	if _, _, err := store.CloseIssue(issue.Number, "done by hand"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}

	m := issuesModelFrom(t, store, "orders")
	m.showClosed = true

	m = keyPress(t, m, press("X"))

	reopened, err := store.Issue(issue.Number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if reopened.State != issues.StateOpen {
		t.Errorf("X did not reopen the issue")
	}
	if !strings.Contains(m.notice, "reopened #") {
		t.Errorf("notice = %q", m.notice)
	}
}

// ── pr ───────────────────────────────────────────────────────────────────

func TestPrOpensTheLinkedPullRequest(t *testing.T) {
	old := openInBrowser
	var opened string
	openInBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { openInBrowser = old })

	m := issuesModel(t, []issues.Status{{
		Issue: issues.Issue{Number: 1, Title: "Aggregate", Type: "task", State: issues.StateOpen,
			PRURL: "https://github.com/x/orders/pull/44", PRState: "open"},
	}})

	keyPress(t, m, press("p"))

	if opened != "https://github.com/x/orders/pull/44" {
		t.Errorf("opened %q, want the linked pull request", opened)
	}
}

func TestPrFailureSurfaces(t *testing.T) {
	old := openInBrowser
	openInBrowser = func(string) error { return fmt.Errorf("no browser") }
	t.Cleanup(func() { openInBrowser = old })

	m := issuesModel(t, []issues.Status{{
		Issue: issues.Issue{Number: 1, Title: "Aggregate", Type: "task", State: issues.StateOpen,
			PRURL: "https://github.com/x/orders/pull/44", PRState: "open"},
	}})

	m = keyPress(t, m, press("p"))
	if !strings.Contains(m.notice, "could not open the pull request") {
		t.Errorf("notice = %q", m.notice)
	}
}

// ── blockers ─────────────────────────────────────────────────────────────

func TestBlockersMovesTheCursorToTheFirstOpenBlocker(t *testing.T) {
	store, issue := plannedStore(t)
	blocker, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Schema first"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Edit(issue.Number, issues.Edit{AddBlockedBy: []int64{blocker.Number}}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	m := issuesModelFrom(t, store, "orders")
	// Park the cursor on the blocked issue — wherever the needs-you sort put
	// it, since its blocker is the launchable row.
	for i, row := range m.visibleIssues() {
		if row.Number == issue.Number {
			m.issueCursor = i
		}
	}
	selected, _ := m.selectedIssue()
	if selected.Number != issue.Number || len(selected.OpenBlockers) == 0 {
		t.Fatalf("setup: cursor on %+v, want the blocked #%d", selected, issue.Number)
	}

	m = keyPress(t, m, press("b"))

	selected, _ = m.selectedIssue()
	if selected.Number != blocker.Number {
		t.Errorf("b left the cursor on #%d, want the blocker #%d", selected.Number, blocker.Number)
	}
}

// ── archive ──────────────────────────────────────────────────────────────

func TestArchiveAndUnarchiveAPlan(t *testing.T) {
	store, _ := plannedStore(t)

	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.store = store
	m.cfg = defaultCfg(t)

	m = keyPress(t, m, press("z"))
	plans, err := store.PlanStatuses(true, issues.PlanStatusOptions{})
	if err != nil {
		t.Fatalf("PlanStatuses: %v", err)
	}
	if len(plans) != 1 || plans[0].State != issues.PlanArchived {
		t.Fatalf("z did not archive the plan: %+v", plans)
	}
	if !strings.Contains(m.notice, "archived orders") {
		t.Errorf("notice = %q", m.notice)
	}

	m.plans = plans
	m = keyPress(t, m, press("z"))
	plans, err = store.PlanStatuses(true, issues.PlanStatusOptions{})
	if err != nil {
		t.Fatalf("PlanStatuses: %v", err)
	}
	if len(plans) != 1 || plans[0].State == issues.PlanArchived {
		t.Fatalf("z on the archived plan did not unarchive it: %+v", plans)
	}
	if !strings.Contains(m.notice, "unarchived orders") {
		t.Errorf("notice = %q", m.notice)
	}
}

// ── review ───────────────────────────────────────────────────────────────

func TestReviewOnAPlanRowSpawnsTheReviewer(t *testing.T) {
	agents := &fakeSpawner{}
	m := plansModel(t, nil)
	m.plans = []issues.PlanStatus{{Plan: issues.Plan{Slug: "orders", Title: "Orders"}, State: issues.PlanAwaitingReview}}
	m.agents = agents

	m = keyPress(t, m, press("v"))

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("v sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Op != protocol.OpSpawn || req.Agent != recheck.ReviewerName {
		t.Errorf("request = %+v, want a plan-reviewer spawn", req)
	}
	if req.Plan != "orders" || req.Pass != issues.PassOpening {
		t.Errorf("plan/pass = %q/%q, want orders/opening", req.Plan, req.Pass)
	}
	if req.Task != recheck.OpeningBriefing("orders") {
		t.Errorf("task = %q, want the opening briefing", req.Task)
	}
	if req.Issue != 0 {
		t.Errorf("the review was tied to issue #%d; there is no reviewer issue", req.Issue)
	}
}

func TestReviewAwaitingTheClosingPassSpawnsIt(t *testing.T) {
	agents := &fakeSpawner{}
	m := plansModel(t, nil)
	m.plans = []issues.PlanStatus{{Plan: issues.Plan{Slug: "orders", Title: "Orders"}, State: issues.PlanAwaitingClosingReview}}
	m.agents = agents

	m = keyPress(t, m, press("v"))

	reqs := agents.seen()
	if len(reqs) != 1 {
		t.Fatalf("v sent %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Pass != issues.PassClosing {
		t.Errorf("pass = %q, want closing", req.Pass)
	}
	if req.Task != recheck.ClosingBriefing("orders") {
		t.Errorf("task = %q, want the closing briefing", req.Task)
	}
}

// ── the whole vocabulary ─────────────────────────────────────────────────

// Every verb ADR-006 names is wired except update, which belongs to the
// unbuilt settings screen (ADR-008): each is registered — the bar, the help
// and the ":" line all render from that — and each has a handler behind it.
func TestEveryVerbIsRegisteredAndHandled(t *testing.T) {
	reg := wiredRegistry()
	for _, c := range command.New().Commands() {
		if c.Verb == "update" {
			continue
		}
		wired, ok := reg.ByVerb(c.Verb)
		if !ok {
			t.Errorf("verb %q is not registered", c.Verb)
			continue
		}
		if wired.Run == nil {
			t.Errorf("verb %q has no handler", c.Verb)
		}
		if wired.Key == "" {
			t.Errorf("verb %q has no key", c.Verb)
		}
	}
}

// Help on a plan row lists the plan verbs; help on an issue row the issue
// ones. Both come from the same registry the keys run through.
func TestHelpListsTheVerbsThatApply(t *testing.T) {
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.plans[0] = issues.PlanStatus{Plan: issues.Plan{Slug: "orders", Title: "Orders"}, State: issues.PlanInProgress, Ready: 1}

	m = keyPress(t, m, press("?"))
	view := m.View()
	for _, want := range []string{"new", "start", "review", "archive", "settings"} {
		if !strings.Contains(view, want) {
			t.Errorf("help on a plan row missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"comment", "retry", "reopen"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("help on a plan row lists issue verb %q:\n%s", unwanted, view)
		}
	}
}

// The ":" line reaches the same handlers as the keys: :retry is r.
func TestTheColonLineRunsTheLifecycleVerbs(t *testing.T) {
	store, issue := plannedStore(t)
	failedRun(t, store, issue.Number)

	agents := &fakeSpawner{}
	m := issuesModelFrom(t, store, "orders")
	m.agents = agents

	m = typeLine(t, m, "retry")

	if got := len(agents.seen()); got != 1 {
		t.Errorf(":retry sent %d requests, want 1", got)
	}
	_ = issue
}
