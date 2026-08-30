package ui

import (
	"strings"
	"testing"

	"pib/internal/ui/theme"
)

// planView renders the Plan tab at a given terminal size.
func planView(t *testing.T, width, height int) string {
	t.Helper()
	m := ready(t)
	m.width, m.height = width, height
	return m.tabPlanView()
}

// The art is the headline of the Plan tab, so it has to be there when there is
// room — and gone when there is not, in either dimension.
func TestPiArtAppearsOnlyWhenThereIsRoomForIt(t *testing.T) {
	art := strings.TrimSpace(strings.Split(strings.TrimSpace(piArt), "\n")[0])

	if view := planView(t, 120, piMinHeight); !strings.Contains(view, art) {
		t.Errorf("no art on a %d-line terminal, which is the threshold:\n%s", piMinHeight, view)
	}
	if view := planView(t, 120, piMinHeight-1); strings.Contains(view, art) {
		t.Error("art rendered one line below the threshold, where the prompt needs the space")
	}
	if view := planView(t, 40, 60); strings.Contains(view, art) {
		t.Error("art rendered on a narrow terminal, where it clips")
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
		for _, want := range []string{"What do you want to plan?", "enter plan"} {
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
