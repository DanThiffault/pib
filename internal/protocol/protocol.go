// Package protocol defines the request/response pair the pi extension and the
// pib TUI exchange over the socket. One connection carries one request and
// stays open until the agent it started finishes, so there is no need to
// correlate replies.
package protocol

// SocketName is the listener inside the workspace directory.
const SocketName = "pib.sock"

// Op is the requested action.
type Op string

const (
	// OpSpawn starts a new agent.
	OpSpawn Op = "spawn"
	// OpResume continues an agent that stopped to ask a question.
	OpResume Op = "resume"
)

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
}

// Response is returned once the agent has stopped.
type Response struct {
	// Status is done, needs_input, error, or unknown.
	Status string `json:"status"`
	// Text is the agent's answer, its question, or the failure message.
	Text string `json:"text,omitempty"`
	// Session identifies this run so it can be resumed.
	Session string `json:"session,omitempty"`
	// Error is set when pib could not run the agent at all.
	Error string `json:"error,omitempty"`
}
