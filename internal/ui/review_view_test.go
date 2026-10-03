package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/config"
	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/protocol"
	"pib/internal/triage"
)

// reviewModel is a model parked on one issue carrying a pull request and a
// review history, with the workspace's default review cap loaded so a table
// row can say which cycle of how many it is on.
func reviewModel(t *testing.T) Model {
	t.Helper()
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.cfg = defaultCfg(t)
	m.screen = screenIssue
	m.drilled = "orders"
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{
			Number: 13, Title: "Implement Order Aggregate", State: issues.StateOpen, Type: "task",
			PRURL: "https://github.com/dan/orders/pull/44", PRState: "open",
		},
		AwaitingReview: true,
		ReviewCycle:    2,
		Run:            "review-7",
	}}
	return m
}

func settledReview(cycle int, verdict string, findings int) issues.Review {
	return issues.Review{
		Cycle:     cycle,
		PRURL:     "https://github.com/dan/orders/pull/44",
		Verdict:   verdict,
		Findings:  findings,
		StartedAt: time.Now().Add(-2 * time.Hour),
		EndedAt:   time.Now().Add(-time.Hour),
	}
}

// A row that says nothing about the review hides three passes of agent time
// and a diff the user has not looked at yet.
func TestIssuesTableNamesThePullRequestAndItsReviewCycle(t *testing.T) {
	m := reviewModel(t)
	m.screen = screenIssues

	output := m.issueTable(100, 10)
	if !strings.Contains(output, "PR #44 · review 2 of 3") {
		t.Errorf("table row does not read \"PR #44 · review 2 of 3\":\n%s", output)
	}
}

// A pull request nobody has reviewed yet is still a pull request, and must
// not claim to be on a cycle it is not.
func TestIssuesTableNamesThePullRequestBeforeAnyReview(t *testing.T) {
	m := reviewModel(t)
	m.screen = screenIssues
	m.planIssues[0].ReviewCycle = 0

	output := m.issueTable(100, 10)
	if !strings.Contains(output, "PR #44") {
		t.Errorf("table row does not name the pull request:\n%s", output)
	}
	if strings.Contains(output, "review 0") || strings.Contains(output, "review 1") {
		t.Errorf("table row claims a review cycle for a pull request with none:\n%s", output)
	}
}

// The review history is the story of how the pull request got to the state it
// is in, so every cycle is listed with what it settled on and what it found.
func TestIssueDetailListsEveryCycle(t *testing.T) {
	m := reviewModel(t)
	m.planReviews = map[int64][]issues.Review{
		13: {
			settledReview(1, issues.VerdictChanges, 2),
			{Cycle: 2, PRURL: "https://github.com/dan/orders/pull/44", Run: "review-7", StartedAt: time.Now()},
		},
	}

	view := m.issueFullScreenView()
	for _, want := range []string{"Review", "cycle 1", "changes", "2 findings", "cycle 2", "running", "review-7", "just now"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
}

// A pull request that was closed and replaced numbers its cycles from one
// again, so two cycles of the same number have to say which request each is
// on or the history reads as a cycle counted twice.
func TestIssueDetailNamesThePullRequestOfARefiledCycle(t *testing.T) {
	m := reviewModel(t)
	replaced := settledReview(1, issues.VerdictChanges, 1)
	replaced.PRURL = "https://github.com/dan/orders/pull/39"
	m.planReviews = map[int64][]issues.Review{13: {replaced}}

	view := m.issueFullScreenView()
	if !strings.Contains(view, "cycle 1 (PR #39)") {
		t.Errorf("detail view does not say which pull request a cycle is on:\n%s", view)
	}
}

// A finding the reviewer could not fix here, and whether it has been filed,
// is on the pull request and nowhere else — so it is the section that has to
// say.
func TestIssueDetailShowsOutOfScopeThreadsAndWhetherTheyAreFiled(t *testing.T) {
	m := reviewModel(t)
	m.triage = scanCollector(t, 13, "https://github.com/dan/orders/pull/44",
		marked{body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\nThe money type is a float."},
		marked{body: "<!-- pib:out-of-scope plan=orders id=unwanted-api -->\nThe API is wider than the PR needs.", filed: true},
	)

	view := m.issueFullScreenView()
	for _, want := range []string{
		"Out-of-scope comments on the PR",
		"money-type-is-float", "The money type is a float.",
		"unwanted-api", "filed",
		"not filed",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
}

// Nothing collected is nothing rendered. There is no promise to make, and a
// pane per issue waiting on a pull request is exactly the spinner the
// interface is not to have.
func TestIssueDetailRendersNoReviewSectionsBeforeAnythingIsCollected(t *testing.T) {
	m := reviewModel(t)

	view := m.issueFullScreenView()
	if strings.Contains(view, "Review") || strings.Contains(view, "Out-of-scope") {
		t.Errorf("detail view shows a section for data nothing has collected:\n%s", view)
	}
}

// Rendering happens on every frame and for every row, so it must never reach
// the store or GitHub: the model here has neither, and still renders both
// sections from what was held.
func TestReviewSectionsRenderWithNoStoreAndNoTriageReads(t *testing.T) {
	m := reviewModel(t)
	m.store = nil
	m.planReviews = map[int64][]issues.Review{13: {settledReview(1, issues.VerdictChanges, 2)}}
	m.triage = scanCollector(t, 13, "https://github.com/dan/orders/pull/44",
		marked{body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\nThe money type is a float."})

	view := m.issueFullScreenView()
	for _, want := range []string{"cycle 1", "money-type-is-float"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q without a store:\n%s", want, view)
		}
	}
}

// The detail pane shares its rows with the issues table, so anything it
// spends on review history is a row the table does not get. It shows none of
// it: the history is the full-screen view's work.
func TestDetailPaneLeavesReviewHistoryToTheFullScreenView(t *testing.T) {
	m := reviewModel(t)
	m.screen = screenIssues
	m.planReviews = map[int64][]issues.Review{13: {settledReview(1, issues.VerdictChanges, 2)}}
	m.triage = scanCollector(t, 13, "https://github.com/dan/orders/pull/44",
		marked{body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\nThe money type is a float."})

	preview := m.issuePreviewPane(45, 20)
	for _, unwanted := range []string{"cycle 1", "money-type-is-float", "Out-of-scope"} {
		if strings.Contains(preview, unwanted) {
			t.Errorf("detail pane spent its rows on %q", unwanted)
		}
	}
	if got := len(strings.Split(preview, "\n")); got != 20 {
		t.Errorf("preview rendered %d lines into 20", got)
	}
}

// A finding's summary is a paragraph written for a pull request, and the pane
// is a full screen at best. The pane's row count is the thing that has to
// stay honest, so long content is cut rather than wrapped into it.
func TestReviewRowsStayWithinThePaneAtEverySizeCovered(t *testing.T) {
	long := "This finding is deliberately far longer than any terminal is wide, and it must not be allowed to wrap itself over the rows the pane was given"

	for _, size := range []struct{ w, h int }{{120, 40}, {100, 30}, {40, 30}, {30, 20}, {20, 12}} {
		m := reviewModel(t)
		m.width, m.height = size.w, size.h
		m.planIssues[0].Title = "An issue with a title long enough to wrap on its own"
		m.planIssues[0].Acceptance = []string{long, long, long, long, long, long}
		m.planReviews = map[int64][]issues.Review{13: {settledReview(1, issues.VerdictChanges, 2)}}
		m.triage = scanCollector(t, 13, "https://github.com/dan/orders/pull/44",
			marked{body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\n" + long})

		view := m.View()
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: rendered %d lines into %d", size.w, size.h, len(lines), m.height)
		}
		if last := lines[len(lines)-1]; !strings.Contains(last, "? Help") {
			t.Errorf("%dx%d: last line is %q, want the command bar", size.w, size.h, last)
		}
	}
}

// storeWithReviewedIssue opens a store holding one plan whose task has a
// linked pull request and one settled review cycle — the state a plan is in
// by the second review pass.
func storeWithReviewedIssue(t *testing.T, store *issues.Store) issues.Issue {
	t.Helper()
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "task", Title: "Implement Order Aggregate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	linked, err := store.LinkPR(issue.Number, "https://github.com/dan/orders/pull/44")
	if err != nil {
		t.Fatalf("LinkPR: %v", err)
	}
	issue = linked
	review, err := store.OpenReview(issue.Number, "https://github.com/dan/orders/pull/44", "")
	if err != nil {
		t.Fatalf("OpenReview: %v", err)
	}
	if _, err := store.CloseReview(review.ID, issues.VerdictChanges, 2); err != nil {
		t.Fatalf("CloseReview: %v", err)
	}
	return issue
}

func testStore(t *testing.T) *issues.Store {
	t.Helper()
	store, err := issues.Open(issues.DataDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// shortScanInterval makes the scan's own tick quick enough for a test to
// watch a whole arming-and-firing cycle without waiting half an hour.
func shortScanInterval(t *testing.T) {
	t.Helper()
	previous := outOfScopeInterval
	outOfScopeInterval = 10 * time.Millisecond
	t.Cleanup(func() { outOfScopeInterval = previous })
}

// The loading command is the only thing that fills the review history in, so
// its cycles are what the Review section renders from. Testing the message
// the update handler copies would prove nothing about whether the command
// asks for them.
func TestLoadPlanIssuesCarriesTheReviewsWithTheIssues(t *testing.T) {
	store := testStore(t)
	issue := storeWithReviewedIssue(t, store)

	msg := loadPlanIssues(store, "orders", config.Config{}, 0)()

	loaded, ok := msg.(planIssuesLoadedMsg)
	if !ok {
		t.Fatalf("loadPlanIssues returned %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("loadPlanIssues: %v", loaded.err)
	}
	if len(loaded.issues) != 1 || loaded.issues[0].Number != issue.Number {
		t.Fatalf("issues = %+v, want #%d", loaded.issues, issue.Number)
	}
	cycles := loaded.reviews[issue.Number]
	if len(cycles) != 1 {
		t.Fatalf("reviews = %+v, want one cycle", loaded.reviews)
	}
	if cycles[0].Verdict != issues.VerdictChanges || cycles[0].Findings != 2 {
		t.Errorf("cycle = %+v, want changes with 2 findings", cycles[0])
	}
}

// The interface may not read GitHub, so the threads have to come from a scan
// the TUI runs itself. Without this the out-of-scope section is a section
// nothing ever fills.
func TestTheInterfaceScansOpenPullRequestsForMarkedThreads(t *testing.T) {
	store := testStore(t)
	issue := storeWithReviewedIssue(t, store)
	collector := &triage.Collector{
		Threads: staticThreads{url: issue.PRURL, threads: []marked{{
			body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\n" +
				"**File:** `internal/types/money.go:31`\n" +
				"**Issue:** The Money type uses float64, which loses precision on division.",
		}}},
		Spawn: nopSpawner{},
	}

	collectOutOfScope(store, collector, "orders")()

	found := waitForMarked(t, collector, issue.Number, 1)
	if found[0].ID != "money-type-is-float" {
		t.Errorf("finding = %+v", found[0])
	}
}

// A scan is a GraphQL call per open pull request, and every marked thread it
// finds can cost a code-reviewer run. Arming the slow tick from the store
// events — which arrive constantly — would start a scan on every one, and
// nothing ever cancels the abandoned ones. The scan re-arms from its own
// message and nowhere else.
func TestStoreEventsDoNotArmAScanOfTheirOwn(t *testing.T) {
	shortScanInterval(t)
	store := testStore(t)
	issue := storeWithReviewedIssue(t, store)
	reader := &countingThreads{inner: staticThreads{url: issue.PRURL}}
	collector := &triage.Collector{Threads: reader, Spawn: nopSpawner{}}

	m := ready(t)
	m.store, m.triage = store, collector

	// Twenty store events, at any pace, must arm nothing: the scan is not
	// their business.
	for i := 0; i < 20; i++ {
		var next tea.Model
		next, cmd := m.Update(storeEventMsg{event: issues.Event{Kind: issues.EventIssue, Plan: "orders", Issue: issue.Number}})
		m = next.(Model)
		drain(cmd)
	}
	if got := reader.reads(); got != 0 {
		t.Errorf("store events triggered %d scans, want none", got)
	}

	// One delivery of the scan's own message arms exactly one chain, and
	// further store events leave that one alone.
	next, cmd := m.Update(outOfScopeTickMsg(time.Now()))
	m = next.(Model)
	drain(cmd)
	waitForReads(t, reader, 1)
	for i := 0; i < 20; i++ {
		next, cmd := m.Update(storeEventMsg{event: issues.Event{Kind: issues.EventIssue, Plan: "orders", Issue: issue.Number}})
		m = next.(Model)
		drain(cmd)
	}
	time.Sleep(20 * outOfScopeInterval)
	if got := reader.reads(); got != 1 {
		t.Errorf("%d scans ran, want exactly 1", got)
	}
}

// drain runs a command and everything a tea.Batch fans out to, discarding
// the messages — what the bubbletea runtime does, minus the delivery.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var wg sync.WaitGroup
		for _, c := range msg {
			wg.Add(1)
			go func(c tea.Cmd) { defer wg.Done(); drain(c) }(c)
		}
		wg.Wait()
	}
}

// countingThreads counts the pulls a scan makes.
type countingThreads struct {
	inner staticThreads

	mu sync.Mutex
	n  int
}

func (c *countingThreads) Threads(ctx context.Context, url string) ([]pr.Thread, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.inner.Threads(ctx, url)
}

func (c *countingThreads) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func waitForReads(t *testing.T, c *countingThreads, want int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c.reads() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no scan ran: %d reads, want %d", c.reads(), want)
}

// The whole journey, on a real store: a plan load brings the cycles, a scan
// brings the threads, and the pane renders both without touching either
// again.
func TestTheDetailPaneFillsFromWhatTheInterfaceItselfCollected(t *testing.T) {
	store := testStore(t)
	issue := storeWithReviewedIssue(t, store)

	m := reviewModel(t)
	m.store = store
	collector := &triage.Collector{
		Threads: staticThreads{url: issue.PRURL, threads: []marked{{
			body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\n" +
				"**File:** `internal/types/money.go:31`\n" +
				"**Issue:** The money type is a float and will lose cents.",
		}}},
		Spawn: nopSpawner{},
	}
	m.triage = collector

	collectOutOfScope(store, collector, "orders")()
	msg := loadPlanIssues(store, "orders", m.cfg, 0)()
	loaded, ok := msg.(planIssuesLoadedMsg)
	if !ok {
		t.Fatalf("loadPlanIssues returned %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("loadPlanIssues: %v", loaded.err)
	}
	// Whatever the load brought is what the pane shows: no hand-built
	// statuses, no hand-set review history.
	m.planIssues = loaded.issues
	m.planReviews = loaded.reviews
	waitForMarked(t, collector, m.planIssues[0].Number, 1)

	view := m.issueFullScreenView()
	for _, want := range []string{
		"cycle 1", "changes", "2 findings",
		"money-type-is-float", "internal/types/money.go:31", "The money type is a float",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
}

func waitForMarked(t *testing.T, c *triage.Collector, issue int64, want int) []triage.Marked {
	t.Helper()
	for i := 0; i < 200; i++ {
		if got := c.Marked(issue); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no scan left %d marked findings for issue %d", want, issue)
	return nil
}

// marked is one review thread a fake pull request carries: the finding the
// reviewer marked, and whether a reply has already filed it.
type marked struct {
	body  string
	filed bool
}

// scanCollector runs the triage collector's own scan path over a fixed set of
// review threads, so what a pass leaves behind is what a real pass leaves
// behind. It returns once every thread has been read.
func scanCollector(t *testing.T, issue int64, url string, threads ...marked) *triage.Collector {
	t.Helper()
	c := &triage.Collector{Threads: staticThreads{url: url, threads: threads}, Spawn: nopSpawner{}}
	c.Collect([]issues.OpenPR{{Number: issue, URL: url}})

	// Collect returns immediately and the scan is a goroutine, so wait for
	// the findings to land rather than racing the assertion.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.Marked(issue)) == len(threads) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c
}

// nopSpawner stands in for the agent runner. The scan is what these tests are
// about, and a collector without a spawner never scans at all.
type nopSpawner struct{}

func (nopSpawner) Run(context.Context, protocol.Request) (protocol.Response, error) {
	return protocol.Response{Status: protocol.StatusOK}, nil
}

// staticThreads serves one fixed pull request, one thread per marked finding.
type staticThreads struct {
	url     string
	threads []marked
}

func (s staticThreads) Threads(_ context.Context, url string) ([]pr.Thread, error) {
	if url != s.url {
		return nil, fmt.Errorf("no such pull request: %s", url)
	}
	out := make([]pr.Thread, 0, len(s.threads))
	for i, m := range s.threads {
		comments := []pr.Comment{{Author: "code-reviewer", Body: m.body, ID: int64(100 + i)}}
		if m.filed {
			comments = append(comments, pr.Comment{
				Author: "dan", Body: "yes, file it\n\n<!-- pib:filed #42 -->", ID: int64(200 + i),
			})
		}
		out = append(out, pr.Thread{
			ID:         fmt.Sprintf("thread-%d", i),
			Path:       "internal/types/money.go",
			Line:       31,
			Comments:   comments,
			IsResolved: false,
		})
	}
	return out, nil
}
