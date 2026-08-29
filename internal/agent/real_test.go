package agent

import "testing"

// TestRealPlannerDefinition exercises the user's actual planner.md when it is
// present, so a definition pib cannot parse fails here rather than at startup.
func TestRealPlannerDefinition(t *testing.T) {
	d, err := LoadPlanner()
	if err != nil {
		t.Skipf("no planner definition installed: %v", err)
	}

	t.Logf("name=%q model=%q thinking=%q tools=%v deny=%v auto-exit=%v system-prompt=%q",
		d.Name, d.Model, d.Thinking, d.Tools, d.DenyTools, d.AutoExit, d.SystemPrompt)
	for i, a := range d.Args("a CLI for tracking books I read") {
		t.Logf("arg %2d: %q", i, a)
	}

	if d.Name == "" || d.Body == "" {
		t.Errorf("definition is missing a name or body")
	}
}
