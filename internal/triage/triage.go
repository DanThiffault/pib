// Package triage files out-of-scope review findings as issues when the user
// asks for it on the pull request.
//
// The code-reviewer marks a finding it believes is real but outside its pull
// request with a pib:out-of-scope comment. When someone replies asking for it
// to be filed, pib files it — and deciding whether "yeah, good catch, though
// maybe later" is that request is a judgment, so an agent makes it, one
// thread at a time. Idempotency lives on the pull request: a thread carrying
// a pib:filed marker is never triaged again, and nothing is stored locally.
package triage

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"pib/internal/issues"
	"pib/internal/pr"
	"pib/internal/protocol"
)

// AgentName is the definition the collector launches per unsettled thread.
const AgentName = "pr-triage"

// ThreadReader reads the review threads on a pull request. pr.CLI
// satisfies it.
type ThreadReader interface {
	Threads(ctx context.Context, url string) ([]pr.Thread, error)
}

// Spawner launches an agent. runner.Runner satisfies it.
type Spawner interface {
	Run(ctx context.Context, req protocol.Request) (protocol.Response, error)
}

// Collector scans open linked pull requests for out-of-scope threads that
// have not been settled and spawns pr-triage for each.
type Collector struct {
	// Threads reads a pull request's review threads. Required.
	Threads ThreadReader
	// Spawn launches the agent. Required.
	Spawn Spawner
	// Report notes how a scan or a run failed. Optional.
	Report func(error)

	mu      sync.Mutex
	running map[string]bool
}

// Collect scans each pull request for unsettled out-of-scope threads. It
// returns immediately: reconciliation calls it while a client is waiting on
// a listing, and neither gh nor the agent it starts may be waited on there.
func (c *Collector) Collect(prs []issues.OpenPR) {
	if c.Threads == nil || c.Spawn == nil {
		return
	}
	for _, p := range prs {
		go c.scan(p)
	}
}

// scan reads one pull request's threads and launches triage for each marked
// thread that has not been settled.
func (c *Collector) scan(p issues.OpenPR) {
	threads, err := c.Threads.Threads(context.Background(), p.URL)
	if err != nil {
		c.report(fmt.Errorf("triage scan of %s: %w", p.URL, err))
		return
	}
	for _, t := range threads {
		oos := t.OutOfScope()
		if oos == nil || t.Settled() {
			continue
		}
		if !c.claim(t.ID) {
			// One agent per thread; a second pass while it runs would
			// file the same finding twice.
			continue
		}
		go func(t pr.Thread, oos *pr.OutOfScope) {
			defer c.release(t.ID)
			if _, err := c.Spawn.Run(context.Background(), c.request(p, t, oos)); err != nil {
				c.report(fmt.Errorf("triage of %s: %w", p.URL, err))
			}
		}(t, oos)
	}
}

func (c *Collector) request(p issues.OpenPR, t pr.Thread, oos *pr.OutOfScope) protocol.Request {
	return protocol.Request{
		Op:    protocol.OpSpawn,
		Agent: AgentName,
		Name:  fmt.Sprintf("triage #%d", p.Number),
		Task:  Briefing(p, t, oos),
	}
}

// Briefing hands the agent the one thread it is to judge: the pull request,
// the comment to reply to, the filing the marker describes, and the replies
// beneath it. Deliberately no Issue field — the run does not work an issue,
// it may file one.
func Briefing(p issues.OpenPR, t pr.Thread, oos *pr.OutOfScope) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request %s (issue #%d) carries an out-of-scope finding from pib's code-reviewer.\n\n", p.URL, p.Number)
	fmt.Fprintf(&b, "The marker says it would be filed as: pib issue create --plan %s --type task (id %s).\n\n", oos.Plan, oos.ID)
	fmt.Fprintf(&b, "The finding, in the reviewer's own words:\n\n%s\n\n", oos.Body)
	if len(t.Comments) > 0 {
		fmt.Fprintf(&b, "The comment to reply to has id %d. The thread so far:\n\n", t.Comments[0].ID)
		for _, c := range t.Comments {
			fmt.Fprintf(&b, "--- %s:\n%s\n\n", c.Author, c.Body)
		}
	}
	b.WriteString("Read the replies and decide whether anyone has asked for the finding to be filed. " +
		"If they have, file it and post the pib:filed marker. If they have not, do nothing.")
	return b.String()
}

func (c *Collector) claim(thread string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running[thread] {
		return false
	}
	if c.running == nil {
		c.running = map[string]bool{}
	}
	c.running[thread] = true
	return true
}

func (c *Collector) release(thread string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.running, thread)
}

func (c *Collector) report(err error) {
	if c.Report != nil {
		c.Report(err)
	}
}
