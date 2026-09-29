package command

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestBarShowsOnlyApplicableCommands(t *testing.T) {
	reg := New()
	openIssue := fixture{kind: KindIssue, state: StateOpen, hasRun: true}

	bar := Bar(reg, openIssue, 0)
	if !strings.Contains(bar, "x Close <reason>") {
		t.Errorf("bar = %q, want it to carry the close command with its args", bar)
	}
	for _, absent := range []string{"Archive", "Unarchive", "Blockers", "Review", "Reopen", "Update agents"} {
		if strings.Contains(bar, absent) {
			t.Errorf("bar = %q, want no %q on an open issue row", bar, absent)
		}
	}
}

func TestBarCutsToWidth(t *testing.T) {
	reg := New()
	row := fixture{kind: KindIssue, state: StateOpen, hasRun: true}
	full := Bar(reg, row, 0)
	if Bar(reg, row, lipgloss.Width(full)) != full {
		t.Fatal("a bar cut at its own width lost something")
	}
	for _, width := range []int{1, 2, 7, 20, 40} {
		got := Bar(reg, row, width)
		if w := lipgloss.Width(got); w > width {
			t.Errorf("Bar at width %d is %d cells: %q", width, w, got)
		}
	}
	if !strings.HasSuffix(Bar(reg, row, 40), ellipsis) {
		t.Errorf("a cut bar should end in %q, got %q", ellipsis, Bar(reg, row, 40))
	}
	if !strings.Contains(Bar(reg, row, 1), "n New") && !strings.HasSuffix(Bar(reg, row, 1), ellipsis) {
		t.Errorf("a bar one cell wide should still show something: %q", Bar(reg, row, 1))
	}
}

// TestBarOnlyEverCutsBetweenCommands checks the bar is made of whole
// commands: a narrow bar loses commands from the end, never half a label.
func TestBarOnlyEverCutsBetweenCommands(t *testing.T) {
	reg := New()
	row := fixture{kind: KindIssue, state: StateOpen, hasRun: true}
	full := Bar(reg, row, 0)
	known := map[string]bool{}
	for _, part := range strings.Split(full, "  ") {
		known[part] = true
	}
	for width := 1; width <= lipgloss.Width(full)+4; width++ {
		got := Bar(reg, row, width)
		if w := lipgloss.Width(got); w > width {
			t.Fatalf("width %d rendered %d cells: %q", width, w, got)
		}
		if got == full {
			continue
		}
		cut := strings.HasSuffix(got, ellipsis)
		for _, part := range strings.Split(strings.TrimSuffix(got, "  "+ellipsis), "  ") {
			if !known[part] && !cut {
				t.Fatalf("width %d invented the command %q: %q", width, part, got)
			}
		}
	}
}

func TestHelpListsTheApplicableCommandsWithVerbsAndKeys(t *testing.T) {
	reg := New()
	row := fixture{kind: KindIssue, state: StateOpen, hasPR: true}
	help := Help(reg, row)

	for _, want := range []string{"close", "x", "pr", "p"} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q:\n%s", want, help)
		}
	}
	for _, absent := range []string{"reopen", "archive", "review"} {
		if strings.Contains(help, absent) {
			t.Errorf("help lists %q, which does nothing on this row:\n%s", absent, help)
		}
	}
	if got := Help(reg, fixture{kind: KindIssue, state: StateBlocked}); !strings.Contains(got, "blockers") {
		t.Errorf("help for a blocked row is missing blockers:\n%s", got)
	}
	// Keys are a column, so they line up whatever their length.
	lines := strings.Split(help, "\n")
	if len(lines) < 2 {
		t.Fatalf("help is one line:\n%s", help)
	}
	column := strings.Index(lines[0], "new")
	for _, l := range lines[1:] {
		if strings.Index(l, strings.Fields(l)[1]) != column {
			t.Errorf("help lines do not line up:\n%s", help)
			break
		}
	}
	if strings.HasSuffix(help, "\n") {
		t.Error("help ends with a blank line")
	}
}

func TestAKeylessCommandIsListedButNotKeyed(t *testing.T) {
	reg := &Registry{}
	// A verb the ":" line can reach but no key names — the long tail that
	// would otherwise eat a letter.
	if err := reg.Register(Command{Verb: "explain", Label: "Explain", Args: "<issue>"}); err != nil {
		t.Fatal(err)
	}
	if bar := Bar(reg, fixture{}, 40); strings.Contains(bar, "Explain") {
		t.Errorf("bar = %q, want no entry for a command with no key", bar)
	}
	if help := Help(reg, fixture{}); !strings.Contains(help, "explain") {
		t.Errorf("help = %q, want the keyless command listed", help)
	}
}

func TestHelpIsEmptyWhenNothingApplies(t *testing.T) {
	reg := &Registry{}
	if got := Help(reg, fixture{}); got != "" {
		t.Errorf("Help on an empty registry = %q", got)
	}
	if got := Bar(reg, fixture{}, 40); got != "" {
		t.Errorf("Bar on an empty registry = %q", got)
	}
}
