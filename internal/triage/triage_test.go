package triage

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/protocol"
)

type spy struct {
	mu       sync.Mutex
	requests []protocol.Request
}

func (s *spy) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	return protocol.Response{}, nil
}

func (s *spy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// waitFor gives the goroutines Collect starts a moment to get going, without
// pinning the test to a fixed sleep.
func waitFor(t *testing.T, want int, s *spy) bool {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s.count() >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type reader struct {
	threads []pr.Thread
	err     error
}

func (r reader) Threads(context.Context, string) ([]pr.Thread, error) {
	return r.threads, r.err
}

var open = []issues.OpenPR{{Number: 44, URL: "https://github.com/o/r/pull/7"}}

func marked(body string) pr.Thread {
	return pr.Thread{
		ID: "thread-1",
		Comments: []pr.Comment{
			{Author: "pib", ID: 100, Body: "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\n" + body},
			{Author: "dan", ID: 101, Body: "yeah, file that"},
		},
	}
}

func TestSpawnsForAnUnsettledMarkedThread(t *testing.T) {
	s := &spy{}
	c := &Collector{Threads: reader{threads: []pr.Thread{marked("The money type is a float.")}}, Spawn: s}

	c.Collect(open)
	if !waitFor(t, 1, s) {
		t.Fatal("no triage launched")
	}

	req := s.requests[0]
	if req.Agent != AgentName {
		t.Errorf("agent = %q, want %q", req.Agent, AgentName)
	}
	if req.Issue != 0 {
		t.Errorf("issue = %d, want the run to claim nothing", req.Issue)
	}
	for _, want := range []string{"https://github.com/o/r/pull/7", "--plan orders", "--type task", "money-type-is-float", "The money type is a float.", "id 100", "yeah, file that"} {
		if !strings.Contains(req.Task, want) {
			t.Errorf("briefing missing %q:\n%s", want, req.Task)
		}
	}
}

func TestSkipsASettledThread(t *testing.T) {
	settled := marked("The money type is a float.")
	settled.Comments = append(settled.Comments, pr.Comment{Author: "pib", ID: 102, Body: "<!-- pib:filed #42 -->\nFiled as #42."})

	s := &spy{}
	c := &Collector{Threads: reader{threads: []pr.Thread{settled}}, Spawn: s}

	c.Collect(open)
	// Nothing should ever launch; give the scan goroutine its chance.
	time.Sleep(50 * time.Millisecond)
	if s.count() != 0 {
		t.Errorf("a settled thread was triaged")
	}
}

func TestSkipsAnUnmarkedThread(t *testing.T) {
	plain := pr.Thread{ID: "thread-2", Comments: []pr.Comment{{Author: "dan", ID: 100, Body: "nice work"}}}

	s := &spy{}
	c := &Collector{Threads: reader{threads: []pr.Thread{plain}}, Spawn: s}

	c.Collect(open)
	time.Sleep(50 * time.Millisecond)
	if s.count() != 0 {
		t.Errorf("an unmarked thread was triaged")
	}
}

func TestOneAgentPerThreadAtATime(t *testing.T) {
	release := make(chan struct{})
	blocking := &spy{}
	c := &Collector{
		Threads: reader{threads: []pr.Thread{marked("The money type is a float.")}},
		Spawn:   &blockingSpawner{spy: blocking, release: release},
	}

	c.Collect(open)
	if !waitFor(t, 1, blocking) {
		t.Fatal("no triage launched")
	}

	// A second pass while the first agent is still running must not spawn
	// a duplicate.
	c.Collect(open)
	time.Sleep(50 * time.Millisecond)
	if blocking.count() != 1 {
		t.Errorf("count = %d, want the running thread left to its agent", blocking.count())
	}

	close(release)
}

type blockingSpawner struct {
	spy     *spy
	release chan struct{}
}

func (b *blockingSpawner) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	b.spy.Run(ctx, req)
	<-b.release
	return protocol.Response{}, nil
}

func TestAReadFailureIsReportedNotSpawned(t *testing.T) {
	reported := make(chan error, 1)
	c := &Collector{
		Threads: reader{err: context.DeadlineExceeded},
		Spawn:   &spy{},
		Report:  func(err error) { reported <- err },
	}

	c.Collect(open)
	select {
	case <-reported:
	case <-time.After(2 * time.Second):
		t.Fatal("the read failure was never reported")
	}
}

func TestNilPartsAreAQuietNoOp(t *testing.T) {
	c := &Collector{}
	c.Collect(open)
}

// The interface is not allowed to ask GitHub, so what a scan read is left
// where a render can find it — the same bargain pull request state makes.
func TestScanLeavesMarkedThreadsForTheInterface(t *testing.T) {
	unfiled := marked("The money type is a float.")
	unfiled.Path, unfiled.Line = "internal/types/money.go", 31
	filed := marked("The API is wider than the PR needs.")
	filed.ID = "thread-2"
	filed.Path, filed.Line = "internal/api/routes.go", 12
	filed.Comments = append(filed.Comments,
		pr.Comment{Author: "pib", ID: 102, Body: "<!-- pib:filed #42 -->\nFiled as #42."})

	s := &spy{}
	c := &Collector{Threads: reader{threads: []pr.Thread{unfiled, filed}}, Spawn: s}
	c.Collect(open)

	found := waitForMarked(t, c, 44, 2)
	if found[0].ID != "money-type-is-float" || found[0].Plan != "orders" {
		t.Errorf("first finding = %+v", found[0])
	}
	if found[0].Filed {
		t.Error("an unfiled finding reads as filed")
	}
	if got, want := found[0].Place(), "internal/types/money.go:31"; got != want {
		t.Errorf("place = %q, want %q", got, want)
	}
	if found[0].Summary != "The money type is a float." {
		t.Errorf("summary = %q", found[0].Summary)
	}
	if !found[1].Filed {
		t.Error("a thread carrying a pib:filed marker reads as unfiled")
	}
}

// A pass that finds the finding filed updates what the interface sees: the
// interface reads what was last collected, so a stale unfiled listing would
// say the opposite of what the pull request says.
func TestALaterPassUpdatesWhatTheInterfaceSees(t *testing.T) {
	s := &spy{}
	c := &Collector{Threads: reader{threads: []pr.Thread{marked("The money type is a float.")}}, Spawn: s}
	c.Collect(open)
	waitForMarked(t, c, 44, 1)

	settled := marked("The money type is a float.")
	settled.Comments = append(settled.Comments,
		pr.Comment{Author: "pib", ID: 102, Body: "<!-- pib:filed #42 -->"})
	c.Threads = reader{threads: []pr.Thread{settled}}
	c.Collect(open)

	for i := 0; i < 200; i++ {
		if got := c.Marked(44); len(got) == 1 && got[0].Filed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("Marked(44) = %+v, want the one finding, filed", c.Marked(44))
}

// Nothing has scanned a pull request, so there is nothing to render — which
// is what the interface shows, rather than a section waiting on a pass.
func TestMarkedIsEmptyForAnIssueNoScanHasRead(t *testing.T) {
	c := &Collector{Threads: reader{}, Spawn: &spy{}}
	if got := c.Marked(44); len(got) != 0 {
		t.Errorf("Marked(44) = %+v, want nothing", got)
	}
}

func waitForMarked(t *testing.T, c *Collector, issue int64, want int) []Marked {
	t.Helper()
	for i := 0; i < 200; i++ {
		if got := c.Marked(issue); len(got) >= want {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no scan left %d marked findings for issue %d", want, issue)
	return nil
}

// A finding is a paragraph; a pane is a row. The summary is its first line,
// and nothing else: a wrapped paragraph would push the pane past the rows it
// was given.
func TestMarkedSummaryIsTheFindingsFirstLineOnly(t *testing.T) {
	thread := marked("The money type is a float.\nIt will lose cents under rounding.")

	c := &Collector{Threads: reader{threads: []pr.Thread{thread}}, Spawn: &spy{}}
	c.Collect(open)

	found := waitForMarked(t, c, 44, 1)
	if got, want := found[0].Summary, "The money type is a float."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// A marked finding arrives in the shape the code-reviewer is told to write,
// and its first line is the file. Rendering that would put the file path in
// the row's summary column, next to the path the row already has.
func TestMarkedSummaryIsTheIssueLineOfAMarkedFinding(t *testing.T) {
	thread := marked("**File:** `internal/types/money.go:31`\n" +
		"**Issue:** The Money type uses float64, which loses precision on division.\n" +
		"**Suggested Fix:** Switch to a decimal type or integer cents.")

	c := &Collector{Threads: reader{threads: []pr.Thread{thread}}, Spawn: &spy{}}
	c.Collect(open)

	found := waitForMarked(t, c, 44, 1)
	want := "The Money type uses float64, which loses precision on division."
	if found[0].Summary != want {
		t.Errorf("summary = %q, want %q", found[0].Summary, want)
	}
}
