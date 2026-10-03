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
	// AgentName is the definition the hook launches when work remains.
	AgentName = "plan-recheck"
	// ReviewerName reviews a whole plan before any of it is worked. `pib plan
	// review` and the closing pass launch it.
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
	// Agent is the recheck definition to launch when work remains.
	// Defaults to AgentName.
	Agent string
	// ReviewAgent is the definition to launch for the closing pass,
	// when a close leaves nothing open. Empty means the closing pass
	// is disabled.
	ReviewAgent string
	// Spawn launches it.
	Spawn Spawner
	// Issues reports what is left in the plan.
	Issues Lister
	// Report notes why a recheck did not run, or how one failed. Optional.
	Report func(error)

	mu      sync.Mutex
	running map[string]bool
	done    map[string]bool // plans that have had their closing pass
}

// IssueClosed launches a recheck for the plan the issue belonged to, unless
// there is nothing left to check or one is already running.
//
// It returns immediately: reconciliation calls this with a client waiting on a
// listing, and the agent it starts runs for minutes.
func (h *Hook) IssueClosed(issue issues.Issue) {
	// A plan reviewer closing means the plan has not begun (opening pass) or
	// has just finished (closing pass). Either way, it is not worth reacting
	// to here.
	if issue.Type == ReviewerName {
		return
	}

	open, err := h.openInPlan(issue.Plan)
	if err != nil {
		h.report(fmt.Errorf("recheck after #%d: %w", issue.Number, err))
		return
	}
	if open == 0 {
		if h.ReviewAgent == "" {
			return
		}
		if h.hasDone(issue.Plan) {
			return
		}
		if !h.claim(issue.Plan) {
			return
		}
		h.markDone(issue.Plan)
		go func() {
			defer h.release(issue.Plan)
			if _, err := h.Spawn.Run(context.Background(), h.closingRequest(issue)); err != nil {
				h.report(fmt.Errorf("closing review after #%d: %w", issue.Number, err))
			}
		}()
		return
	}

	if !h.claim(issue.Plan) {
		// Reconciliation can close several issues in one pass. One recheck
		// reads the whole plan, so the others would duplicate it.
		return
	}

	go func() {
		defer h.release(issue.Plan)
		if _, err := h.Spawn.Run(context.Background(), h.recheckRequest(issue)); err != nil {
			h.report(fmt.Errorf("recheck after #%d: %w", issue.Number, err))
		}
	}()
}

func (h *Hook) recheckAgent() string {
	if h.Agent != "" {
		return h.Agent
	}
	return AgentName
}

func (h *Hook) recheckRequest(issue issues.Issue) protocol.Request {
	// Deliberately not Issue: issue.Number. That column means "an agent
	// working this issue", and `pib issue followup` resumes the newest run
	// against one — so claiming the issue here would hand a followup meant
	// for the agent that did the work to the recheck that watched it finish.
	// The number reaches the agent through the briefing instead.
	return protocol.Request{
		Op:    protocol.OpSpawn,
		Agent: h.recheckAgent(),
		Name:  fmt.Sprintf("recheck #%d", issue.Number),
		Task:  Briefing(issue),
	}
}

func (h *Hook) closingRequest(issue issues.Issue) protocol.Request {
	return protocol.Request{
		Op:    protocol.OpSpawn,
		Agent: h.ReviewAgent,
		Name:  fmt.Sprintf("closing review %s", issue.Plan),
		Task:  ClosingBriefing(issue.Plan),
		// No issue claims this run, so the plan and the pass are what trace
		// it: they are how the plan reads as under review while it runs, and
		// how its ending settles the plan.
		Plan: issue.Plan,
		Pass: issues.PassClosing,
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

// OpeningBriefing tells the plan reviewer this is the opening pass: the
// plan is checked against the codebase before any of it is worked. It is
// what `pib plan review` sends.
func OpeningBriefing(plan string) string {
	return fmt.Sprintf(
		"Review the plan %q before any of it is worked. Read it with "+
			"`pib plan view %s` and `pib issue list --plan %s`, then check every "+
			"issue against the codebase it will change.",
		plan, plan, plan)
}

// ClosingBriefing tells the plan reviewer this is the closing pass: every
// issue has been worked and the review is against the plan's own acceptance
// criteria.
func ClosingBriefing(plan string) string {
	return fmt.Sprintf(
		"This is the closing pass for plan %q. Every issue in the plan has been "+
			"worked and closed. Read the plan with `pib plan view %s`, then check "+
			"whether the plan achieved what it set out to do — goals that were dropped, "+
			"acceptance criteria nothing satisfies, scope that drifted across pull "+
			"requests nobody read end to end. What you find, file as new issues in the "+
			"plan. There is no open pull request left to comment on.",
		plan, plan)
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

func (h *Hook) hasDone(plan string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.done[plan]
}

func (h *Hook) markDone(plan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done == nil {
		h.done = map[string]bool{}
	}
	h.done[plan] = true
}

func (h *Hook) report(err error) {
	if h.Report != nil {
		h.Report(err)
	}
}
