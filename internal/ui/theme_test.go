package ui

import (
	"strings"
	"testing"

	"pib/internal/ui/theme"
)

// planView renders the new-plan screen at a given terminal size.
func planView(t *testing.T, width, height int) string {
	t.Helper()
	m := ready(t)
	m.width, m.height = width, height
	return m.View()
}

// The art renders on the startup screen when there is room, and nowhere
// else — especially not in the new-plan lower pane, where eight lines is
// more space than the pane has.
func TestPiArtAppearsOnlyWhenThereIsRoomForIt(t *testing.T) {
	art := strings.TrimSpace(strings.Split(strings.TrimSpace(piArt), "\n")[0])

	m := ready(t)
	m.width, m.height = 120, piMinHeight
	// Put the model back into a startup phase so the startup view renders.
	m.phase = phaseCheckingAgents
	if view := m.startupView(); !strings.Contains(view, art) {
		t.Errorf("no art on a %d-line startup screen, which is the threshold:\n%s", piMinHeight, view)
	}

	m.height = piMinHeight - 1
	if view := m.startupView(); strings.Contains(view, art) {
		t.Error("art rendered one line below the threshold, where the prompt needs the space")
	}

	m.width = 40
	m.height = piMinHeight
	if view := m.startupView(); strings.Contains(view, art) {
		t.Error("art rendered on a narrow terminal, where it clips")
	}

	// The new-plan view never contains the art.
	m = ready(t)
	m.width, m.height = 120, 60
	if view := m.newPlanView(); strings.Contains(view, art) {
		t.Error("art rendered in the new-plan lower pane")
	}
}

// The art is decoration; losing it must never cost the prompt.
func TestPlanTabKeepsItsPromptAtEverySize(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{120, 60}, // room for everything
		{120, 20}, // wide but short: no art
		{40, 60},  // narrow: no art
		{40, 20},  // neither
	} {
		view := planView(t, size.w, size.h)
		for _, want := range []string{"What do you want to plan?", "[ENTER]"} {
			if !strings.Contains(view, want) {
				t.Errorf("%dx%d is missing %q:\n%s", size.w, size.h, want, view)
			}
		}
	}
}

// Every foreground in the palette was chosen against the theme background, so
// the view has to carry that background rather than borrow the terminal's.
func TestViewIsPaintedOnTheThemeBackground(t *testing.T) {
	m := ready(t)
	m.width, m.height = 100, 40

	if got := theme.Default.Base.GetBackground(); got != theme.DefaultPalette.Bg {
		t.Errorf("Base background = %v, want the palette's %v", got, theme.DefaultPalette.Bg)
	}
	// ground is what applies it; without a width there is nothing to fill.
	if plain := (Model{}).ground("x"); plain != "x" {
		t.Errorf("ground with no width = %q, want the view untouched", plain)
	}
	if grounded := m.ground("x"); !strings.Contains(grounded, "x") {
		t.Errorf("ground dropped the view: %q", grounded)
	}
}
