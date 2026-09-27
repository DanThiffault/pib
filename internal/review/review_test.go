package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"pib/internal/issues"
	"pib/internal/protocol"
)

type mockSpawn struct {
	mu       sync.Mutex
	requests []protocol.Request
	release  chan struct{}
	err      error
	// before is called on every Run before blocking/returning.
	before func(protocol.Request)
}

func (s *mockSpawn) Run(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if s.before != nil {
		s.before(req)
	}
	if s.release != nil {
		<-s.release
	}
	return protocol.Response{}, s.err
}

func (s *mockSpawn) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func waitFor(t *testing.T, want int, s *mockSpawn) bool {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s.count() >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type mockRunStore struct {
	run issues.Run
}

func (m *mockRunStore) LatestRun(issue int64) (issues.Run, bool, error) {
	return m.run, m.run.ID != "", nil
}

type mockReviewStore struct {
	mu      sync.Mutex
	reviews []issues.Review
	nextID  int
}

func (m *mockReviewStore) OpenReview(issue int64, prURL, run string) (issues.Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("rev-%d", m.nextID)

	// count existing cycles for this prURL
	cycle := 1
	for _, r := range m.reviews {
		if r.Issue == issue && r.PRURL == prURL {
			cycle++
		}
	}

	review := issues.Review{
		ID:        id,
		Issue:     issue,
		PRURL:     prURL,
		Cycle:     cycle,
		Run:       run,
		StartedAt: time.Now(),
	}
	m.reviews = append(m.reviews, review)
	return review, nil
}

func (m *mockReviewStore) Reviews(issue int64) ([]issues.Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []issues.Review
	for _, r := range m.reviews {
		if r.Issue == issue {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockReviewStore) CloseReview(id, verdict string, findings int) (issues.Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.reviews {
		if m.reviews[i].ID == id {
			m.reviews[i].Verdict = verdict
			m.reviews[i].Findings = findings
			m.reviews[i].EndedAt = time.Now()
			return m.reviews[i], nil
		}
	}
	return issues.Review{}, errors.New("review not found")
}

func linkedIssue(number int64) issues.Issue {
	return issues.Issue{Number: number, PRURL: "https://github.com/o/r/pull/1", Title: "Something"}
}

func TestSpawnsReviewerAndStopsOnApproval(t *testing.T) {
	spawn := &mockSpawn{}
	reviews := &mockReviewStore{}
	runs := &mockRunStore{run: endedRun("coder")}
	h := &Hook{Spawn: spawn, Runs: runs, Reviews: reviews}

	spawn.before = func(req protocol.Request) {
		if req.Agent == ReviewerAgent {
			// Simulate the reviewer recording its verdict before finishing.
			list, _ := reviews.Reviews(1)
			for _, r := range list {
				if r.Running() {
					reviews.CloseReview(r.ID, issues.VerdictApproved, 0)
					break
				}
			}
		}
	}

	h.PRLinked(linkedIssue(1))
	if !waitFor(t, 1, spawn) {
		t.Fatal("reviewer was never spawned")
	}

	req := spawn.requests[0]
	if req.Agent != ReviewerAgent {
		t.Errorf("agent = %q, want %q", req.Agent, ReviewerAgent)
	}
	if req.Issue != 0 {
		t.Errorf("issue = %d, want 0 — a reviewer watches a diff, it does not work the issue", req.Issue)
	}
	if !strings.Contains(req.Task, "#1") {
		t.Errorf("the briefing does not name the issue: %q", req.Task)
	}
	if !strings.Contains(req.Task, linkedIssue(1).PRURL) {
		t.Errorf("the briefing does not name the pull request: %q", req.Task)
	}
	if req.Op != protocol.OpSpawn {
		t.Errorf("op = %q, want spawn", req.Op)
	}
}

func TestFollowsUpCoderWhenReviewerFindsChanges(t *testing.T) {
	spawn := &mockSpawn{}
	reviews := &mockReviewStore{}
	runs := &mockRunStore{run: endedRun("coder")}
	h := &Hook{Spawn: spawn, Runs: runs, Reviews: reviews, Cycles: 3}

	spawn.before = func(req protocol.Request) {
		if req.Agent == ReviewerAgent {
			list, _ := reviews.Reviews(1)
			for _, r := range list {
				if r.Running() {
					// first cycle: changes
					if r.Cycle == 1 {
						reviews.CloseReview(r.ID, issues.VerdictChanges, 2)
					} else {
						reviews.CloseReview(r.ID, issues.VerdictApproved, 0)
					}
					break
				}
			}
		}
	}

	h.PRLinked(linkedIssue(1))
	if !waitFor(t, 3, spawn) {
		t.Fatalf("wanted 3 spawns, got %d", spawn.count())
	}

	// spawn order: reviewer (1), followup resume (2), reviewer (2)
	if spawn.requests[0].Agent != ReviewerAgent {
		t.Errorf("first spawn = %q, want reviewer", spawn.requests[0].Agent)
	}
	if spawn.requests[1].Op != protocol.OpResume {
		t.Errorf("second spawn op = %q, want resume", spawn.requests[1].Op)
	}
	if spawn.requests[1].Issue != 1 {
		t.Errorf("followup issue = %d, want 1", spawn.requests[1].Issue)
	}
	if !strings.Contains(spawn.requests[1].Answer, linkedIssue(1).PRURL) {
		t.Errorf("followup answer does not mention PR: %q", spawn.requests[1].Answer)
	}
	if spawn.requests[2].Agent != ReviewerAgent {
		t.Errorf("third spawn = %q, want reviewer", spawn.requests[2].Agent)
	}
}

func TestStopsWhenExhausted(t *testing.T) {
	spawn := &mockSpawn{}
	reviews := &mockReviewStore{}
	runs := &mockRunStore{run: endedRun("coder")}
	h := &Hook{Spawn: spawn, Runs: runs, Reviews: reviews, Cycles: 2}

	spawn.before = func(req protocol.Request) {
		if req.Agent == ReviewerAgent {
			list, _ := reviews.Reviews(1)
			for _, r := range list {
				if r.Running() {
					reviews.CloseReview(r.ID, issues.VerdictChanges, 1)
					break
				}
			}
		}
	}

	h.PRLinked(linkedIssue(1))
	if !waitFor(t, 3, spawn) {
		t.Fatalf("wanted 3 spawns, got %d", spawn.count())
	}

	// 2 reviewer cycles + 1 followup = 3 spawns, then stop
	reviewerCount := 0
	for _, req := range spawn.requests {
		if req.Agent == ReviewerAgent {
			reviewerCount++
		}
	}
	if reviewerCount != 2 {
		t.Errorf("reviewer spawns = %d, want 2", reviewerCount)
	}
}

func TestDuplicateLinkIsIgnored(t *testing.T) {
	spawn := &mockSpawn{release: make(chan struct{})}
	reviews := &mockReviewStore{}
	runs := &mockRunStore{run: endedRun("coder")}
	h := &Hook{Spawn: spawn, Runs: runs, Reviews: reviews}

	h.PRLinked(linkedIssue(1))
	if !waitFor(t, 1, spawn) {
		t.Fatal("first hook never started")
	}

	h.PRLinked(linkedIssue(1))
	time.Sleep(20 * time.Millisecond)
	if got := spawn.count(); got != 1 {
		t.Errorf("launched %d loops for one link, want 1", got)
	}

	close(spawn.release)
}

func TestSpawnFailureIsReported(t *testing.T) {
	spawn := &mockSpawn{err: errors.New("no such agent")}
	reviews := &mockReviewStore{}
	runs := &mockRunStore{run: endedRun("coder")}
	got := make(chan error, 1)
	h := &Hook{
		Spawn: spawn, Runs: runs, Reviews: reviews,
		Report: func(err error) { got <- err },
	}

	h.PRLinked(linkedIssue(1))
	select {
	case err := <-got:
		if !strings.Contains(err.Error(), "#1") {
			t.Errorf("report %q does not say which issue failed", err)
		}
	case <-time.After(time.Second):
		t.Error("a failing spawn was never reported")
	}
}

func TestUnsettledCycleIsClosedAsError(t *testing.T) {
	spawn := &mockSpawn{}
	reviews := &mockReviewStore{}
	runs := &mockRunStore{run: endedRun("coder")}
	h := &Hook{Spawn: spawn, Runs: runs, Reviews: reviews}

	// The reviewer finishes without ever recording a verdict.
	h.PRLinked(linkedIssue(1))
	if !waitFor(t, 1, spawn) {
		t.Fatal("reviewer was never spawned")
	}

	list, err := reviews.Reviews(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("wanted 1 review, got %d", len(list))
	}
	if list[0].Verdict != issues.VerdictError {
		t.Errorf("verdict = %q, want error", list[0].Verdict)
	}
}

func endedRun(agent string) issues.Run {
	return issues.Run{
		ID:        "run-1",
		Agent:     agent,
		StartedAt: time.Now().Add(-time.Hour),
		EndedAt:   time.Now(),
	}
}
