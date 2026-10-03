// Package protocol defines the request/response pair the pi extension and the
// pib TUI exchange over the socket. One connection carries one request and
// stays open until the agent it started finishes, so there is no need to
// correlate replies.
package protocol

import "encoding/json"

// SocketName is the listener inside the workspace directory.
const SocketName = "pib.sock"

// Op is the requested action.
type Op string

const (
	// OpSpawn starts a new agent.
	OpSpawn Op = "spawn"
	// OpResume continues an agent that stopped to ask a question.
	OpResume Op = "resume"
	// OpSpawnBackground starts a new agent and returns immediately,
	// leaving it running in the background.
	OpSpawnBackground Op = "spawn_background"
)

// Issue and plan operations. These carry their arguments and results in
// Payload rather than in the agent fields above, and reply immediately
// instead of holding the connection open.
const (
	OpPlanApply Op = "plan.apply"
	OpPlanList  Op = "plan.list"
	OpPlanView  Op = "plan.view"

	OpIssueCreate  Op = "issue.create"
	OpIssueList    Op = "issue.list"
	OpIssueView    Op = "issue.view"
	OpIssueEdit    Op = "issue.edit"
	OpIssueComment Op = "issue.comment"
	OpIssueLinkPR  Op = "issue.link_pr"
	OpIssueClose   Op = "issue.close"
	OpIssueReopen  Op = "issue.reopen"
	OpIssueReady   Op = "issue.ready"
	OpIssueReindex Op = "issue.reindex"
	// OpIssueRetry starts a new run of an issue's agent, clearing whatever
	// stopped the last one.
	OpIssueRetry Op = "issue.retry"
	// OpIssueAnswer resumes a run that stopped to ask a question.
	OpIssueAnswer Op = "issue.answer"

	OpReviewRecord Op = "review.record"
)

// StatusOK is the status of a successful issue or plan operation. Agent
// operations report why the agent stopped instead.
const StatusOK = "ok"

// IsAgent reports whether an operation runs an agent. Those are the ones
// that hold the connection open until the agent stops; everything else
// answers straight away.
func (o Op) IsAgent() bool {
	return o == OpSpawn || o == OpResume || o == OpSpawnBackground
}

// Request is sent by the extension.
type Request struct {
	Op Op `json:"op"`
	// Agent is the definition name, e.g. "scout".
	Agent string `json:"agent,omitempty"`
	// Name is the display name for the window.
	Name string `json:"name,omitempty"`
	// Task is the prompt for a spawn.
	Task string `json:"task,omitempty"`
	// Session identifies the run to resume.
	Session string `json:"session,omitempty"`
	// Answer replies to a question from a resumed agent.
	Answer string `json:"answer,omitempty"`
	// Caller is the agent making the request, used to refuse self-spawning.
	Caller string `json:"caller,omitempty"`
	// Issue is the issue this agent is being spawned for, if any. It is
	// what makes that issue read as in progress while the agent works.
	Issue int64 `json:"issue,omitempty"`
	// Plan is the plan a run works on behalf of. A run on an issue is
	// traced through the issue instead; this is for a run with no issue —
	// the closing-pass reviewer, `pib plan review`.
	Plan string `json:"plan,omitempty"`
	// Pass says whether a plan-reviewer run is the opening or the closing
	// review of its plan.
	Pass string `json:"pass,omitempty"`
	// Foreground selects the agent's window rather than leaving it behind
	// pib's. It is for a spawn the user is meant to watch — the planner a
	// `new` command opens; everything pib starts on its own stays in the
	// background.
	Foreground bool `json:"foreground,omitempty"`

	// Payload carries the arguments of an issue or plan operation. Its
	// shape depends on Op; internal/issueops defines them.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is returned once the agent has stopped.
type Response struct {
	// Status is done, needs_input, error, or unknown.
	Status string `json:"status"`
	// Text is the agent's answer, its question, or the failure message.
	Text string `json:"text,omitempty"`
	// Session identifies this run so it can be resumed.
	Session string `json:"session,omitempty"`
	// Error is set when pib could not carry the request out at all.
	Error string `json:"error,omitempty"`

	// Payload carries the result of an issue or plan operation.
	Payload json.RawMessage `json:"payload,omitempty"`
}
