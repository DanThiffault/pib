// Package recheck runs an agent over the remaining plan whenever an issue
// closes, so a decision or a diff that invalidates the work still queued is
// found before someone builds it as written.
package recheck

import (
	"context"
	"fmt"
	"sync"

	"pib/internal/issues"
	"pib/internal/protocol"
)

const (
	// AgentName is the definition the hook launches when an issue closes.
	AgentName = "plan-recheck"
	// ReviewerName reviews a whole plan before any of it is worked. Nothing
	// launches it on its own; `pib plan review` does.
	ReviewerName = "plan-reviewer"
)

// Spawner launches an agent. runner.Runner satisfies it.
type Spawner interface {
	Run(ctx context.Context, req protocol.Request) (protocol.Response, error)
}

// Lister reports the issues in a plan, so the hook can tell whether any work
// is still queued.
type Lister interface {
	List(filter issues.Filter) ([]issues.Issue, error)
}

// Hook launches the recheck agent when an issue closes. It satisfies
// issues.ClosedHook.
type Hook struct {
	// Agent is the definition to launch. Defaults to AgentName.
	Agent string
	// Spawn launches it.
	Spawn Spawner
	// Issues reports what is left in the plan.
	Issues Lister
	// Report notes why a recheck did not run, or how one failed. Optional.
	Report func(error)

	mu      sync.Mutex
	running map[string]bool
}

// IssueClosed launches a recheck for the plan the issue belonged to, unless
// there is nothing left to check or one is already running.
//
// It returns immediately: reconciliation calls this with a client waiting on a
// listing, and the agent it starts runs for minutes.
func (h *Hook) IssueClosed(issue issues.Issue) {
	// Neither review closing is worth reacting to. A code reviewer closing
	// means the plan is over; a plan reviewer closing means it has not begun,
	// so nothing has been produced that could contradict what is queued.
	if issue.Type == "reviewer" || issue.Type == ReviewerName {
		return
	}

	open, err := h.openInPlan(issue.Plan)
	if err != nil {
		h.report(fmt.Errorf("recheck after #%d: %w", issue.Number, err))
		return
	}
	if open == 0 {
		return
	}

	if !h.claim(issue.Plan) {
		// Reconciliation can close several issues in one pass. One recheck
		// reads the whole plan, so the others would duplicate it.
		return
	}

	go func() {
		defer h.release(issue.Plan)
		if _, err := h.Spawn.Run(context.Background(), h.request(issue)); err != nil {
			h.report(fmt.Errorf("recheck after #%d: %w", issue.Number, err))
		}
	}()
}

func (h *Hook) request(issue issues.Issue) protocol.Request {
	agent := h.Agent
	if agent == "" {
		agent = AgentName
	}
	// Deliberately not Issue: issue.Number. That column means "an agent
	// working this issue", and `pib issue followup` resumes the newest run
	// against one — so claiming the issue here would hand a followup meant
	// for the agent that did the work to the recheck that watched it finish.
	// The number reaches the agent through the briefing instead.
	return protocol.Request{
		Op:    protocol.OpSpawn,
		Agent: agent,
		Name:  fmt.Sprintf("recheck #%d", issue.Number),
		Task:  Briefing(issue),
	}
}

// Briefing tells the agent which close it is reacting to. Both the number and
// the type are in the text: the type decides what the agent looks for, and the
// number is here rather than in PIB_ISSUE because the run does not claim the
// issue.
func Briefing(issue issues.Issue) string {
	return fmt.Sprintf(
		"Issue #%d just closed in plan %q. It was of type %q: %s\n\n"+
			"Check whether what it produced contradicts any issue still open in that "+
			"plan. Most closes change nothing — say so and finish rather than looking "+
			"for something to report.",
		issue.Number, issue.Plan, issue.Type, issue.Title)
}

func (h *Hook) openInPlan(plan string) (int, error) {
	if h.Issues == nil || plan == "" {
		return 0, nil
	}
	list, err := h.Issues.List(issues.Filter{Plan: plan, State: issues.StateOpen})
	if err != nil {
		return 0, err
	}
	return len(list), nil
}

func (h *Hook) claim(plan string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running[plan] {
		return false
	}
	if h.running == nil {
		h.running = map[string]bool{}
	}
	h.running[plan] = true
	return true
}

func (h *Hook) release(plan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.running, plan)
}

func (h *Hook) report(err error) {
	if h.Report != nil {
		h.Report(err)
	}
}
