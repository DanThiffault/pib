// Package review runs a bounded reviewer → coder follow-up → reviewer loop
// whenever a pull request is linked to an issue.
package review

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pib/internal/issues"
	"pib/internal/protocol"
)

const (
	// DefaultCycles is the cap when the config does not say otherwise.
	DefaultCycles = 3
	// ReviewerAgent is the definition the hook launches.
	ReviewerAgent = "code-reviewer"
)

// Spawner launches an agent. runner.Runner satisfies it.
type Spawner interface {
	Run(ctx context.Context, req protocol.Request) (protocol.Response, error)
}

// RunStore reports the latest run for an issue, so the loop can wait for the
// linking coder to finish before following up.
type RunStore interface {
	LatestRun(issue int64) (issues.Run, bool, error)
}

// ReviewStore opens and settles review cycles.
type ReviewStore interface {
	OpenReview(issue int64, prURL, run string) (issues.Review, error)
	Reviews(issue int64) ([]issues.Review, error)
	CloseReview(id, verdict string, findings int) (issues.Review, error)
}

// Hook implements issues.LinkedHook. It starts a bounded review loop in a
// goroutine so that the caller — usually the agent that linked the pull
// request — is not blocked.
type Hook struct {
	Spawn   Spawner
	Runs    RunStore
	Reviews ReviewStore
	Cycles  int
	Report  func(error)

	mu      sync.Mutex
	running map[int64]bool
}

// PRLinked starts the review loop for the issue's pull request.
func (h *Hook) PRLinked(issue issues.Issue) {
	if issue.PRURL == "" {
		return
	}

	if !h.claim(issue.Number) {
		return
	}

	go func() {
		defer h.release(issue.Number)
		h.loop(issue)
	}()
}

func (h *Hook) loop(issue issues.Issue) {
	max := h.Cycles
	if max <= 0 {
		max = DefaultCycles
	}

	for cycle := 1; cycle <= max; cycle++ {
		review, err := h.Reviews.OpenReview(issue.Number, issue.PRURL, "")
		if err != nil {
			h.report(fmt.Errorf("open review cycle %d for #%d: %w", cycle, issue.Number, err))
			return
		}

		_, err = h.Spawn.Run(context.Background(), reviewerRequest(issue))
		if err != nil {
			h.report(fmt.Errorf("spawn reviewer for #%d cycle %d: %w", issue.Number, cycle, err))
			// Best effort: settle the cycle we opened so it does not dangle.
			_, _ = h.Reviews.CloseReview(review.ID, issues.VerdictError, 0)
			return
		}

		verdict, err := h.readVerdict(issue, review.ID)
		if err != nil {
			h.report(err)
			return
		}

		switch verdict {
		case issues.VerdictApproved, issues.VerdictError:
			return
		case issues.VerdictChanges:
			if cycle >= max {
				return // exhausted
			}
			if err := h.followup(issue); err != nil {
				h.report(fmt.Errorf("coder followup for #%d: %w", issue.Number, err))
				return
			}
		}
	}
}

// readVerdict re-reads the review cycle the reviewer was meant to settle.
// If it was never settled, we close it as error ourselves.
func (h *Hook) readVerdict(issue issues.Issue, id string) (string, error) {
	all, err := h.Reviews.Reviews(issue.Number)
	if err != nil {
		return "", fmt.Errorf("read reviews for #%d: %w", issue.Number, err)
	}

	var current *issues.Review
	for i := range all {
		if all[i].ID == id {
			current = &all[i]
			break
		}
	}
	if current == nil {
		return "", fmt.Errorf("review cycle %s for #%d disappeared", id, issue.Number)
	}

	if current.Running() {
		settled, err := h.Reviews.CloseReview(current.ID, issues.VerdictError, 0)
		if err != nil {
			return "", fmt.Errorf("close errored review for #%d: %w", issue.Number, err)
		}
		return settled.Verdict, nil
	}

	return current.Verdict, nil
}

// followup waits for the latest coder run to end and then resumes it with a
// message asking it to address the review findings.
func (h *Hook) followup(issue issues.Issue) error {
	last, err := h.waitForRun(issue.Number)
	if err != nil {
		return err
	}

	_, err = h.Spawn.Run(context.Background(), protocol.Request{
		Op:      protocol.OpResume,
		Session: last.ID,
		Answer:  followupBriefing(issue),
		Name:    fmt.Sprintf("%s #%d", last.Agent, issue.Number),
		Issue:   issue.Number,
	})
	return err
}

// waitForRun polls until the latest run for the issue has ended. The run is
// still live when PRLinked fires because the linking agent has not finished.
func (h *Hook) waitForRun(issue int64) (issues.Run, error) {
	for {
		last, found, err := h.Runs.LatestRun(issue)
		if err != nil {
			return issues.Run{}, fmt.Errorf("latest run for #%d: %w", issue, err)
		}
		if !found {
			return issues.Run{}, fmt.Errorf("no run found for #%d", issue)
		}
		if !last.EndedAt.IsZero() {
			return last, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// reviewerRequest builds the spawn request for a code-reviewer run.
// The run deliberately does not claim the issue: PIB_ISSUE is what
// makes `pib issue followup` resume the newest run against one, and
// the follow-up this loop sends is meant for the coder, not the
// reviewer. The number reaches the agent through the briefing instead.
func reviewerRequest(issue issues.Issue) protocol.Request {
	return protocol.Request{
		Op:    protocol.OpSpawn,
		Agent: ReviewerAgent,
		Name:  fmt.Sprintf("%s #%d", ReviewerAgent, issue.Number),
		Task:  reviewerBriefing(issue),
	}
}

func reviewerBriefing(issue issues.Issue) string {
	return fmt.Sprintf(
		"Review pull request %s for pib issue #%d: %s\n\n"+
			"Read it first with `pib issue view %d` — that is your specification.\n\n"+
			"Record your verdict with `pib review record %d --verdict approved|changes|error --findings <n>`.",
		issue.PRURL, issue.Number, issue.Title, issue.Number, issue.Number)
}

func followupBriefing(issue issues.Issue) string {
	return fmt.Sprintf(
		"Address the review findings on %s for pib issue #%d: %s",
		issue.PRURL, issue.Number, issue.Title)
}

func (h *Hook) claim(issue int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running[issue] {
		return false
	}
	if h.running == nil {
		h.running = map[int64]bool{}
	}
	h.running[issue] = true
	return true
}

func (h *Hook) release(issue int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.running, issue)
}

func (h *Hook) report(err error) {
	if h.Report != nil {
		h.Report(err)
	}
}
