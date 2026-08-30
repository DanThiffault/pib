package recheck

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"pib/internal/issues"
	"pib/internal/protocol"
)

type spy struct {
	mu       sync.Mutex
	requests []protocol.Request
	release  chan struct{}
	err      error
}

func (s *spy) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if s.release != nil {
		<-s.release
	}
	return protocol.Response{}, s.err
}

func (s *spy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// waitFor gives the goroutine the hook starts a moment to get going, without
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

type lister struct {
	open int
	err  error
}

func (l lister) List(issues.Filter) ([]issues.Issue, error) {
	if l.err != nil {
		return nil, l.err
	}
	return make([]issues.Issue, l.open), nil
}

func closed(number int64, kind string) issues.Issue {
	return issues.Issue{Number: number, Plan: "orders", Type: kind, Title: "Something"}
}

func TestLaunchesWhenWorkRemains(t *testing.T) {
	s := &spy{}
	h := &Hook{Spawn: s, Issues: lister{open: 3}}

	h.IssueClosed(closed(4, "task"))
	if !waitFor(t, 1, s) {
		t.Fatal("no recheck launched")
	}

	req := s.requests[0]
	if req.Agent != AgentName {
		t.Errorf("agent = %q, want %q", req.Agent, AgentName)
	}
	// runs.issue means "an agent working this issue", and `pib issue followup`
	// resumes the newest run against one. A recheck claiming the issue would
	// take over a followup meant for the agent that did the work.
	if req.Issue != 0 {
		t.Errorf("issue = %d, want 0 — a recheck watches an issue, it does not work it", req.Issue)
	}
	if !strings.Contains(req.Task, "#4") {
		t.Errorf("the briefing does not name the closed issue: %q", req.Task)
	}
	if req.Op != protocol.OpSpawn {
		t.Errorf("op = %q, want spawn", req.Op)
	}
}

// The briefing has to carry the type: it is what decides whether the agent
// reads a diff or a decision.
func TestBriefingNamesTheTypeAndPlan(t *testing.T) {
	text := Briefing(closed(4, "prototype"))
	for _, want := range []string{"#4", "orders", "prototype"} {
		if !strings.Contains(text, want) {
			t.Errorf("briefing does not mention %q:\n%s", want, text)
		}
	}
}

// Neither review is worth reacting to: a code reviewer closing ends the plan,
// and a plan reviewer closing means the plan has not started, so nothing has
// been produced that could contradict what is queued.
func TestReviewClosesLaunchNothing(t *testing.T) {
	for _, kind := range []string{"reviewer", ReviewerName} {
		s := &spy{}
		h := &Hook{Spawn: s, Issues: lister{open: 2}}

		h.IssueClosed(closed(7, kind))
		time.Sleep(20 * time.Millisecond)
		if got := s.count(); got != 0 {
			t.Errorf("launched %d rechecks after a %s closed, want 0", got, kind)
		}
	}
}

func TestNothingOpenLaunchesNothing(t *testing.T) {
	s := &spy{}
	h := &Hook{Spawn: s, Issues: lister{open: 0}}

	h.IssueClosed(closed(4, "task"))
	time.Sleep(20 * time.Millisecond)
	if got := s.count(); got != 0 {
		t.Errorf("launched %d rechecks with nothing left open, want 0", got)
	}
}

// Reconciliation can close several issues in one pass. One recheck reads the
// whole plan, so the rest would duplicate it.
func TestConcurrentClosesCoalesceToOneRecheck(t *testing.T) {
	s := &spy{release: make(chan struct{})}
	h := &Hook{Spawn: s, Issues: lister{open: 5}}

	h.IssueClosed(closed(3, "task"))
	if !waitFor(t, 1, s) {
		t.Fatal("first recheck never started")
	}
	h.IssueClosed(closed(4, "task"))
	h.IssueClosed(closed(5, "task"))
	time.Sleep(20 * time.Millisecond)

	if got := s.count(); got != 1 {
		t.Errorf("launched %d rechecks for one plan at once, want 1", got)
	}

	// Once it finishes, the next close is free to start another.
	close(s.release)
	if !waitForRelease(h) {
		t.Fatal("the plan stayed claimed after the recheck finished")
	}
	h.IssueClosed(closed(6, "task"))
	if !waitFor(t, 2, s) {
		t.Error("a later close did not start a recheck")
	}
}

func waitForRelease(h *Hook) bool {
	for i := 0; i < 200; i++ {
		h.mu.Lock()
		held := h.running["orders"]
		h.mu.Unlock()
		if !held {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// The hook runs while a client waits on a listing, so a failing spawn must be
// reported rather than propagated.
func TestSpawnFailureIsReportedNotPanicked(t *testing.T) {
	s := &spy{err: errors.New("no such agent")}
	got := make(chan error, 1)
	h := &Hook{Spawn: s, Issues: lister{open: 1}, Report: func(err error) { got <- err }}

	h.IssueClosed(closed(4, "task"))
	select {
	case err := <-got:
		if !strings.Contains(err.Error(), "#4") {
			t.Errorf("report %q does not say which close failed", err)
		}
	case <-time.After(time.Second):
		t.Error("a failing spawn was never reported")
	}
}
