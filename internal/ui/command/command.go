// Package command is the one place the TUI's keys are defined.
//
// ADR-006 splits every key into two kinds. Motion keys move the cursor and are
// never rebound. Commands do the work: each is a verb with a key, a label, a
// predicate over the selected row and a handler. The bottom bar, the help
// screen and the ":" line are all rendered from the same registry, so they
// cannot disagree with what a key does.
//
// A command key is a plain string, compared against tea.KeyMsg.String(), not a
// key.Binding: bindings belong to motion and the startup prompts, and keeping
// them out of here means searching for bindings finds no commands.
package command

import tea "github.com/charmbracelet/bubbletea"

// Kind is which kind of row a command is looking at.
type Kind string

const (
	// KindPlan is a row in the plans table.
	KindPlan Kind = "plan"
	// KindIssue is a row in a plan's issues table, or the issue screen.
	KindIssue Kind = "issue"
	// KindSettings is the settings screen's row.
	KindSettings Kind = "settings"
)

// State is where a row is in its lifecycle, as far as the registry is
// concerned. It is deliberately coarser than what the screens know: the
// predicates in this package need to say "open", "closed", "running" and
// nothing more.
type State string

const (
	// StateOpen is an issue that has not been closed.
	StateOpen State = "open"
	// StateClosed is a closed issue.
	StateClosed State = "closed"
	// StateInProgress is an issue with an agent on it.
	StateInProgress State = "inProgress"
	// StateAwaitingReview is an issue with a pull request awaiting merge.
	StateAwaitingReview State = "awaitingReview"
	// StateBlocked is an issue waiting on an open blocker.
	StateBlocked State = "blocked"
	// StatePlanning is a plan still being written, or being reviewed before
	// anything in it can start.
	StatePlanning State = "planning"
	// StateComplete is a plan whose issues are all closed.
	StateComplete State = "complete"
	// StateArchived is a plan out of the way.
	StateArchived State = "archived"
)

// Row is what a command's predicate looks at. The screens implement it for a
// plan row, an issue row and the settings row; the registry holds the
// predicates themselves, so the rules about which key does what live in one
// file rather than one per screen.
//
// The interface is small on purpose: every method is something a predicate in
// this package actually reads, so a screen adapts its own richer types with a
// handful of one-line methods.
type Row interface {
	// Kind is which screen's row this is.
	Kind() Kind
	// State is the row's lifecycle position.
	State() State
	// Launchable reports that the row can start work now: a ready issue
	// whose type maps to an agent, or a plan holding at least one such
	// issue.
	Launchable() bool
	// NeedsAttention reports that the row is waiting on the user.
	NeedsAttention() bool
	// NeedsInput reports that the user has been asked a question.
	NeedsInput() bool
	// Reviewable reports a plan in opening or closing review. It is false
	// for issues — review is a plan's verb.
	Reviewable() bool
	// HasRun reports an agent run that has not ended.
	HasRun() bool
	// HasPR reports a linked pull request.
	HasPR() bool
}

// Command is one verb the user can reach with a key or the ":" line.
type Command struct {
	// Verb is the CLI name: the ":" line dispatches on it, and it is what
	// the shell's vocabulary calls it too.
	Verb string
	// Key is the key that runs it, as tea.KeyMsg.String() spells it:
	// "ctrl+k", "esc", "up". It is not a key.Binding. Empty means the
	// command is reachable from the ":" line only.
	Key string
	// Label is the short form the bar and the help show.
	Label string
	// Args is an optional completion hint for the ":" line, e.g.
	// "<reason>". It is not part of the key.
	Args string
	// Applies reports whether the command does anything to a row. Commands
	// with no predicate — n, the filters — apply everywhere.
	Applies func(Row) bool
	// Run performs the command. args are the words typed after the verb on
	// the ":" line, and are empty when the key was pressed.
	Run func(Row, []string) tea.Cmd
}

// motion is the reserved navigation vocabulary of ADR-006 §1. A command can
// never take one of these keys; Register refuses them.
var motion = []string{
	"j", "k", "up", "down",
	"g", "G",
	"ctrl+d", "ctrl+u", "pgup", "pgdown",
	"l", "right", "enter", "h", "left", "esc",
	"q", "?", ":", "ctrl+c",
}

// Motion returns the reserved navigation keys, in the order the ADR lists
// them. They are never commands.
func Motion() []string {
	return append([]string(nil), motion...)
}

// IsMotion reports whether a key — as tea.KeyMsg.String() spells it — moves
// the cursor or otherwise belongs to navigation.
func IsMotion(key string) bool {
	for _, m := range motion {
		if m == key {
			return true
		}
	}
	return false
}
