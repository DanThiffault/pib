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
