package command

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Registry is the set of commands, in the order the bar shows them.
type Registry struct {
	commands []Command
}

// New returns a registry holding the built-in commands of ADR-006 §1. Their
// handlers are nil: the screens install the ones that touch the world, and a
// command with no handler does nothing rather than panicking, so a partially
// wired registry is still usable.
func New() *Registry {
	return &Registry{commands: builtins()}
}

// Register adds a command.
//
// A motion key is refused: navigation is never a command. A verb that is
// already registered is refused too, because the ":" line dispatches by verb
// and two commands under one name would make the key and the ":" line
// disagree.
//
// A key may be shared, and the registry does not stop it: z is both archive
// and unarchive, and the settings row can take a key the issue rows use. What
// it requires is that no row makes both apply, which is a property of the
// predicates rather than of the registration — see TestSharedKeysAreDisjoint.
func (r *Registry) Register(c Command) error {
	if c.Verb == "" {
		return errors.New("a command needs a verb")
	}
	if c.Key != "" && IsMotion(c.Key) {
		return fmt.Errorf("%q is a motion key and cannot be a command", c.Key)
	}
	for _, existing := range r.commands {
		if existing.Verb == c.Verb {
			return fmt.Errorf("verb %q is already registered", c.Verb)
		}
	}
	r.commands = append(r.commands, c)
	return nil
}

// Commands returns every command, in bar order.
func (r *Registry) Commands() []Command {
	return append([]Command(nil), r.commands...)
}

// ByVerb returns the command the ":" line dispatches to.
func (r *Registry) ByVerb(verb string) (Command, bool) {
	for _, c := range r.commands {
		if c.Verb == verb {
			return c, true
		}
	}
	return Command{}, false
}

// WithKey returns every command bound to a key, in bar order.
func (r *Registry) WithKey(key string) []Command {
	var out []Command
	for _, c := range r.commands {
		if c.Key == key {
			out = append(out, c)
		}
	}
	return out
}

// Applies reports whether a command does anything to a row. A command with no
// predicate applies everywhere.
func Applies(c Command, row Row) bool {
	return c.Applies == nil || c.Applies(row)
}

// For returns the commands that apply to a row, in bar order. It is what the
// bar, the help screen and the ":" line all render from.
func (r *Registry) For(row Row) []Command {
	var out []Command
	for _, c := range r.commands {
		if Applies(c, row) {
			out = append(out, c)
		}
	}
	return out
}

// Press runs the command a key press names on a row, if there is one. args is
// empty — a key press carries no arguments. It reports whether anything ran,
// so a caller can leave a key unhandled rather than swallow it silently.
func (r *Registry) Press(row Row, key string) (tea.Cmd, bool) {
	for _, c := range r.WithKey(key) {
		if Applies(c, row) {
			return Run(c, row, nil), true
		}
	}
	return nil, false
}

// Run invokes a command's handler with the arguments typed after its verb.
// A command with no handler is a no-op, not a crash: the registry is built
// before the screens have installed every action.
func Run(c Command, row Row, args []string) tea.Cmd {
	if c.Run == nil {
		return nil
	}
	return c.Run(row, args)
}

func isIssue(r Row) bool     { return r.Kind() == KindIssue }
func isPlan(r Row) bool      { return r.Kind() == KindPlan }
func isOpenIssue(r Row) bool { return isIssue(r) && r.State() == StateOpen }

// builtins is the vocabulary of ADR-006 §1, in the order the bar shows it.
func builtins() []Command {
	return []Command{
		{Verb: "new", Key: "n", Label: "New"},
		{Verb: "start", Key: "s", Label: "Start", Applies: func(r Row) bool { return r.Launchable() }},
		{Verb: "review", Key: "v", Label: "Review", Applies: func(r Row) bool { return isPlan(r) && r.Reviewable() }},
		{Verb: "retry", Key: "r", Label: "Retry", Applies: func(r Row) bool { return r.NeedsAttention() }},
		{Verb: "answer", Key: "a", Label: "Answer", Args: "<question>", Applies: func(r Row) bool { return r.NeedsInput() }},
		{Verb: "followup", Key: "f", Label: "Follow up", Args: "<note>", Applies: func(r Row) bool {
			return isIssue(r) && (r.State() == StateInProgress || r.State() == StateAwaitingReview)
		}},
		{Verb: "kill", Key: "ctrl+k", Label: "Kill", Applies: func(r Row) bool { return r.HasRun() }},
		{Verb: "window", Key: "w", Label: "Window", Applies: func(r Row) bool { return r.HasRun() }},
		{Verb: "comment", Key: "c", Label: "Comment", Applies: isOpenIssue},
		{Verb: "edit", Key: "e", Label: "Edit", Applies: isOpenIssue},
		{Verb: "close", Key: "x", Label: "Close", Args: "<reason>", Applies: isOpenIssue},
		{Verb: "reopen", Key: "X", Label: "Reopen", Applies: func(r Row) bool { return isIssue(r) && r.State() == StateClosed }},
		{Verb: "pr", Key: "p", Label: "PR", Applies: func(r Row) bool { return isIssue(r) && r.HasPR() }},
		{Verb: "blockers", Key: "b", Label: "Blockers", Applies: func(r Row) bool { return isIssue(r) && r.State() == StateBlocked }},
		{Verb: "archive", Key: "z", Label: "Archive", Applies: func(r Row) bool { return isPlan(r) && r.State() != StateArchived }},
		{Verb: "unarchive", Key: "z", Label: "Unarchive", Applies: func(r Row) bool { return isPlan(r) && r.State() == StateArchived }},
		{Verb: "update", Key: "u", Label: "Update agents", Applies: func(r Row) bool { return r.Kind() == KindSettings }},
		{Verb: "settings", Key: ",", Label: "Settings"},
		// The two filters are keys like any other; they are not commands
		// about a row, they change which rows there are.
		{Verb: "toggle-closed", Key: "Z", Label: "Closed"},
		{Verb: "toggle-needs-you", Key: "!", Label: "Needs you"},
	}
}
