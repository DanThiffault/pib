package ui

import (
	"strings"

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

// The lifecycle verbs of ADR-006 §1. close splits in two: a key press asks
// for the reason first (closeAskMsg opens the ":" line prefilled), and the
// line's dispatch carries it (closeIssueMsg).
type reviewPlanMsg struct{ plan issues.PlanStatus }
type retryIssueMsg struct{ issue issues.Status }
type answerIssueMsg struct{ issue issues.Status }
type followupIssueMsg struct{ issue issues.Status }
type commentIssueMsg struct{ issue issues.Status }
type editIssueMsg struct{ issue issues.Status }
type closeAskMsg struct{}
type closeIssueMsg struct {
	issue  issues.Status
	reason string
}
type reopenIssueMsg struct{ issue issues.Status }
type openPRMsg struct{ url string }
type blockersMsg struct{ issue issues.Status }
type archivePlanMsg struct{ slug string }
type unarchivePlanMsg struct{ slug string }

// emit turns a message into the command that delivers it.
func emit(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

// wiredVerbs are the commands this build wires, in bar order. update stays
// unregistered: it belongs to the settings screen (ADR-008), which is not
// built yet. The bar, the help and the ":" line render from the registry,
// so a verb with no handler behind it simply does not show.
var wiredVerbs = []string{
	"new",
	"start",
	"review",
	"retry",
	"answer",
	"followup",
	"kill",
	"window",
	"comment",
	"edit",
	"close",
	"reopen",
	"pr",
	"blockers",
	"archive",
	"unarchive",
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

	must(reg.Handle("review", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(planRow); ok {
			return emit(reviewPlanMsg{plan: r.status})
		}
		return nil
	}))

	must(reg.Handle("retry", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(retryIssueMsg{issue: r.status})
		}
		return nil
	}))
	must(reg.Handle("answer", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(answerIssueMsg{issue: r.status})
		}
		return nil
	}))
	must(reg.Handle("followup", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(followupIssueMsg{issue: r.status})
		}
		return nil
	}))

	must(reg.Handle("comment", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(commentIssueMsg{issue: r.status})
		}
		return nil
	}))
	must(reg.Handle("edit", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(editIssueMsg{issue: r.status})
		}
		return nil
	}))

	// close takes its reason from the ":" line's arguments. A key press
	// carries none — Press passes nil — so x asks for the reason first by
	// opening the line with the verb typed. ":close" alone closes without
	// one.
	must(reg.Handle("close", func(row command.Row, args []string) tea.Cmd {
		r, ok := row.(issueRow)
		if !ok {
			return nil
		}
		if args == nil {
			return emit(closeAskMsg{})
		}
		return emit(closeIssueMsg{issue: r.status, reason: strings.Join(args, " ")})
	}))
	must(reg.Handle("reopen", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(reopenIssueMsg{issue: r.status})
		}
		return nil
	}))

	must(reg.Handle("pr", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(openPRMsg{url: r.status.PRURL})
		}
		return nil
	}))
	must(reg.Handle("blockers", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(issueRow); ok {
			return emit(blockersMsg{issue: r.status})
		}
		return nil
	}))

	must(reg.Handle("archive", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(planRow); ok {
			return emit(archivePlanMsg{slug: r.status.Slug})
		}
		return nil
	}))
	must(reg.Handle("unarchive", func(row command.Row, _ []string) tea.Cmd {
		if r, ok := row.(planRow); ok {
			return emit(unarchivePlanMsg{slug: r.status.Slug})
		}
		return nil
	}))

	must(reg.Handle("toggle-closed", func(command.Row, []string) tea.Cmd {
		return emit(toggleClosedMsg{})
	}))
	must(reg.Handle("toggle-needs-you", func(command.Row, []string) tea.Cmd {
		return emit(toggleNeedsYouMsg{})
	}))

	return reg
}
