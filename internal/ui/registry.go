package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
	"pib/internal/ui/command"
)

// Semantic messages are the contract between the registry's handlers and the
// model. A handler runs wherever the command was invoked from — a key or the
// ":" line — so it cannot touch the model; it names what the user asked for,
// and Update, holding the live model, carries it out.
type newPlanMsg struct{}
type startIssueMsg struct{ issue issues.Status }
type startAllMsg struct{ plan string }
type killMsg struct {
	run   string
	issue int64
}
type windowMsg struct {
	run   string
	issue int64
}
type settingsMsg struct{}
type toggleClosedMsg struct{}
type toggleNeedsYouMsg struct{}

// emit turns a message into the command that delivers it.
func emit(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

// wiredVerbs are the commands this build wires, in bar order. The rest of
// ADR-006's vocabulary is left unregistered on purpose: the bar, the help
// and the ":" line render from the registry, so a verb with no handler
// behind it simply does not show.
var wiredVerbs = []string{
	"new",
	"start",
	"kill",
	"window",
	"settings",
	"toggle-closed",
	"toggle-needs-you",
}

// wiredRegistry builds the command registry: the wired subset of the
// built-in vocabulary — keys, labels and predicates exactly as the command
// package defines them — with the handlers that carry each verb out.
func wiredRegistry() *command.Registry {
	builtins := command.New()
	reg := &command.Registry{}
	for _, verb := range wiredVerbs {
		c, ok := builtins.ByVerb(verb)
		if !ok {
			// A wired verb the command package does not define is a mistake
			// in this file, not a runtime condition.
			panic("ui: no built-in command named " + verb)
		}
		if err := reg.Register(c); err != nil {
			panic("ui: " + err.Error())
		}
	}

	must := func(err error) {
		if err != nil {
			panic("ui: " + err.Error())
		}
	}

	must(reg.Handle("new", func(command.Row, []string) tea.Cmd {
		return emit(newPlanMsg{})
	}))

	// start is start on an issue row, start-all on a plan row: one key, one
	// verb, the row deciding what it means — ADR-006 §1.
	must(reg.Handle("start", func(row command.Row, _ []string) tea.Cmd {
		switch r := row.(type) {
		case issueRow:
			return emit(startIssueMsg{issue: r.status})
		case planRow:
			return emit(startAllMsg{plan: r.status.Slug})
		}
		return nil
	}))

	must(reg.Handle("kill", func(row command.Row, _ []string) tea.Cmd {
		switch r := row.(type) {
		case issueRow:
			return emit(killMsg{run: r.status.Run, issue: r.status.Number})
		case planRow:
			return emit(killMsg{run: r.status.Run})
		}
		return nil
	}))

	must(reg.Handle("window", func(row command.Row, _ []string) tea.Cmd {
		switch r := row.(type) {
		case issueRow:
			return emit(windowMsg{run: r.status.Run, issue: r.status.Number})
		case planRow:
			return emit(windowMsg{run: r.status.Run})
		}
		return nil
	}))

	must(reg.Handle("settings", func(command.Row, []string) tea.Cmd {
		return emit(settingsMsg{})
	}))
	must(reg.Handle("toggle-closed", func(command.Row, []string) tea.Cmd {
		return emit(toggleClosedMsg{})
	}))
	must(reg.Handle("toggle-needs-you", func(command.Row, []string) tea.Cmd {
		return emit(toggleNeedsYouMsg{})
	}))

	return reg
}
