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

// AgentName is the definition the collector launches per unsettled
// thread: the code-reviewer again, which files its own marked finding.
const AgentName = "code-reviewer"

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
// have not been settled and spawns the code-reviewer for each, on a filing
// pass rather than a review.
type Collector struct {
	// Threads reads a pull request's review threads. Required.
	Threads ThreadReader
	// Spawn launches the agent. Required.
	Spawn Spawner
	// Report notes how a scan or a run failed. Optional.
	Report func(error)

	mu      sync.Mutex
	running map[string]bool
	// marked is what the last scan of each pull request found on it, keyed
	// by issue. Reconciliation is the only thing that may read GitHub, and
	// the interface is not allowed to look, so a scan leaves what it read
	// here and the detail pane reads it back. Nothing is stored: a restart
	// loses the threads until the next pass reads them again, which is the
	// same bargain pull request state makes.
	marked map[int64][]Marked
}

// Marked is one out-of-scope finding a scan found on an issue's pull
// request.
type Marked struct {
	// Plan and ID are what the marker said the finding would be filed as.
	Plan string
	ID   string
	// Path and Line are where on the pull request it was raised, empty when
	// GitHub did not say.
	Path string
	Line int
	// Summary is the finding's first line, kept short for a narrow pane.
	Summary string
	// Filed reports a pib:filed marker somewhere in the thread, which is
	// the only record of a filing that survives anywhere.
	Filed bool
}

// Place says where the finding was raised, or an empty string when GitHub
// did not say.
func (m Marked) Place() string {
	if m.Path == "" {
		return ""
	}
	if m.Line > 0 {
		return fmt.Sprintf("%s:%d", m.Path, m.Line)
	}
	return m.Path
}

// Marked reports the out-of-scope findings the last scan of an issue's pull
// request found, oldest first. It returns nothing when no scan has read that
// pull request, which is what the interface renders as no section at all —
// there is no state to show and nothing to wait for on this screen.
func (c *Collector) Marked(issue int64) []Marked {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.marked[issue]
}

// record leaves what a scan read behind for the interface.
func (c *Collector) record(number int64, threads []pr.Thread) {
	var found []Marked
	for _, t := range threads {
		oos := t.OutOfScope()
		if oos == nil {
			continue
		}
		found = append(found, Marked{
			Plan:    oos.Plan,
			ID:      oos.ID,
			Path:    t.Path,
			Line:    t.Line,
			Summary: firstLine(oos.Body),
			Filed:   t.Settled(),
		})
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.marked == nil {
		c.marked = map[int64][]Marked{}
	}
	if len(found) == 0 {
		// A pull request whose findings have all been filed reads as none
		// outstanding. Leaving a stale list would say the opposite.
		delete(c.marked, number)
		return
	}
	c.marked[number] = found
}

// firstLine is a finding in one line: the first non-empty line of what the
// reviewer wrote, which is a row a narrow pane can hold.
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
	c.record(p.Number, threads)
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
	fmt.Fprintf(&b, "Pull request %s (issue #%d) carries an out-of-scope finding you marked earlier.\n\n", p.URL, p.Number)
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
