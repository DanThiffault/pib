package command

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMotionCoversTheADRLists(t *testing.T) {
	// The ADR's table, corrected where Bubble Tea spells the key differently:
	// pgdn is pgdown, and there is a ctrl+c and a ":" in there too.
	want := []string{
		"j", "k", "up", "down", "g", "G",
		"ctrl+d", "ctrl+u", "pgup", "pgdown",
		"l", "right", "enter", "h", "left", "esc",
		"q", "?", ":", "ctrl+c",
	}
	got := Motion()
	if len(got) != len(want) {
		t.Fatalf("Motion() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Motion()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if IsMotion("pgdn") {
		t.Error("pgdn is not a key Bubble Tea produces")
	}
	if IsMotion("s") {
		t.Error("s is a command key, not motion")
	}
}

func TestRegisterRefusesMotionKeys(t *testing.T) {
	for _, key := range Motion() {
		reg := New()
		err := reg.Register(Command{Verb: "start", Key: key, Label: "Start"})
		if err == nil {
			t.Fatalf("registering %q as a command key was allowed", key)
		}
		if len(reg.WithKey(key)) != 0 {
			t.Fatalf("%q was registered despite the refusal", key)
		}
	}
}

func TestRegisterRefusesDuplicateVerbs(t *testing.T) {
	reg := New()
	err := reg.Register(Command{Verb: "start", Key: "S", Label: "Start"})
	if err == nil {
		t.Fatal("a second command under one verb was allowed")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %v, want it to name the duplicate verb", err)
	}
}

func TestRegisterRefusesAnEmptyVerb(t *testing.T) {
	if err := New().Register(Command{Key: "n", Label: "New"}); err == nil {
		t.Fatal("a command with no verb was allowed")
	}
}

// TestCommandKeysArePlainStrings checks the field is a string, and that every
// built-in key is spelled the way a real key press spells it, so a command
// key and a key press cannot drift apart by a capital or an abbreviation.
func TestCommandKeysArePlainStrings(t *testing.T) {
	var _ string = Command{}.Key // the key is not a key.Binding

	for _, c := range New().Commands() {
		if c.Key == "" {
			t.Fatalf("command %q has no key", c.Verb)
		}
		if got := keyPress(c.Key).String(); got != c.Key {
			t.Errorf("command %q has key %q, which a key press spells %q", c.Verb, c.Key, got)
		}
	}
	if got := (tea.KeyMsg{Type: tea.KeyCtrlK}).String(); got != "ctrl+k" {
		t.Errorf("ctrl+k is spelled %q", got)
	}
}

// keyPress builds the message a terminal would send for a key name.
func keyPress(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// TestSharedKeysAreDisjoint walks every fixture row and checks that no key is
// ever ambiguous. z is archive and unarchive, and a screen may add a command
// on a key the issue rows use; neither may be reachable with the same row.
func TestSharedKeysAreDisjoint(t *testing.T) {
	reg := New()
	// The settings screen takes `e` on its own rows for an edit of its own,
	// the same key the issue rows use for editing an issue.
	if err := reg.Register(Command{
		Verb: "edit-setting", Key: "e", Label: "Edit setting",
		Applies: func(r Row) bool { return r.Kind() == KindSettings },
	}); err != nil {
		t.Fatalf("registering a settings command on e: %v", err)
	}

	rows := everyRow()
	for _, c := range reg.Commands() {
		if c.Key == "" || len(reg.WithKey(c.Key)) < 2 {
			continue
		}
		for _, row := range rows {
			var on []string
			for _, other := range reg.WithKey(c.Key) {
				if Applies(other, row) {
					on = append(on, other.Verb)
				}
			}
			if len(on) > 1 {
				t.Fatalf("row %+v: key %q applies to %v at once", row, c.Key, on)
			}
		}
	}
}

func TestOnlyZIsASharedKey(t *testing.T) {
	counts := map[string]int{}
	for _, c := range New().Commands() {
		counts[c.Key]++
	}
	for key, n := range counts {
		if n > 1 && key != "z" {
			t.Errorf("key %q is bound to %d commands; only z is meant to be shared", key, n)
		}
	}
}

func TestForReturnsOnlyApplicableCommands(t *testing.T) {
	reg := New()
	for _, c := range reg.For(fixture{kind: KindIssue, state: StateOpen}) {
		if c.Key == "z" {
			t.Errorf("%q applies to an issue row", c.Verb)
		}
	}
	var labels []string
	for _, c := range reg.For(fixture{kind: KindPlan, state: StateArchived}) {
		labels = append(labels, c.Verb)
	}
	want := []string{"new", "unarchive", "settings", "toggle-closed", "toggle-needs-you"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("For(archived plan) = %v, want %v", labels, want)
	}
}

func TestPredicatesReadTheRowState(t *testing.T) {
	tests := []struct {
		verb string
		row  fixture
		want bool
	}{
		{"start", fixture{kind: KindIssue, state: StateOpen, launchable: true}, true},
		{"start", fixture{kind: KindIssue, state: StateOpen}, false},
		{"start", fixture{kind: KindPlan, state: StatePlanning, launchable: true}, true},
		{"review", fixture{kind: KindPlan, state: StatePlanning, reviewable: true}, true},
		{"review", fixture{kind: KindIssue, state: StateOpen, reviewable: true}, false},
		{"retry", fixture{kind: KindIssue, state: StateOpen, needsAttention: true}, true},
		{"answer", fixture{kind: KindIssue, state: StateOpen, needsInput: true}, true},
		{"followup", fixture{kind: KindIssue, state: StateInProgress}, true},
		{"followup", fixture{kind: KindIssue, state: StateAwaitingReview}, true},
		{"followup", fixture{kind: KindIssue, state: StateOpen}, false},
		{"kill", fixture{kind: KindIssue, state: StateInProgress, hasRun: true}, true},
		{"kill", fixture{kind: KindIssue, state: StateOpen, hasRun: true}, true},
		{"window", fixture{kind: KindIssue, state: StateInProgress, hasRun: true}, true},
		{"window", fixture{kind: KindIssue, state: StateOpen}, false},
		{"comment", fixture{kind: KindIssue, state: StateOpen}, true},
		{"comment", fixture{kind: KindIssue, state: StateClosed}, false},
		{"edit", fixture{kind: KindIssue, state: StateOpen}, true},
		{"close", fixture{kind: KindIssue, state: StateOpen}, true},
		{"close", fixture{kind: KindIssue, state: StateClosed}, false},
		{"reopen", fixture{kind: KindIssue, state: StateClosed}, true},
		{"reopen", fixture{kind: KindIssue, state: StateOpen}, false},
		{"pr", fixture{kind: KindIssue, state: StateAwaitingReview, hasPR: true}, true},
		{"pr", fixture{kind: KindIssue, state: StateOpen}, false},
		{"blockers", fixture{kind: KindIssue, state: StateBlocked}, true},
		{"blockers", fixture{kind: KindIssue, state: StateOpen}, false},
		{"archive", fixture{kind: KindPlan, state: StateComplete}, true},
		{"archive", fixture{kind: KindPlan, state: StateArchived}, false},
		{"unarchive", fixture{kind: KindPlan, state: StateArchived}, true},
		{"unarchive", fixture{kind: KindPlan, state: StateComplete}, false},
		{"update", fixture{kind: KindSettings}, true},
		{"update", fixture{kind: KindPlan}, false},
		{"new", fixture{kind: KindPlan, state: StateComplete}, true},
		{"settings", fixture{kind: KindSettings}, true},
		{"toggle-closed", fixture{kind: KindIssue, state: StateOpen}, true},
	}
	reg := New()
	for _, tt := range tests {
		c, ok := reg.ByVerb(tt.verb)
		if !ok {
			t.Fatalf("verb %q is not registered", tt.verb)
		}
		if got := Applies(c, tt.row); got != tt.want {
			t.Errorf("%q on %+v = %v, want %v", tt.verb, tt.row, got, tt.want)
		}
	}
}

func TestPressRunsTheCommandForAKey(t *testing.T) {
	row := fixture{kind: KindIssue, state: StateOpen}
	var gotArgs []string
	var gotRow Row
	reg := &Registry{}
	if err := reg.Register(Command{
		Verb: "close", Key: "x", Label: "Close", Args: "<reason>",
		Applies: isOpenIssue,
		Run: func(r Row, args []string) tea.Cmd {
			gotRow, gotArgs = r, args
			return func() tea.Msg { return "closed" }
		},
	}); err != nil {
		t.Fatalf("registering close: %v", err)
	}

	cmd, ok := reg.Press(row, "x")
	if !ok {
		t.Fatal("x on an open issue did nothing")
	}
	if gotArgs != nil {
		t.Errorf("a key press passed args %v; it should pass none", gotArgs)
	}
	if gotRow != Row(row) {
		t.Errorf("handler got row %v, want %v", gotRow, row)
	}
	if msg := cmd(); msg != "closed" {
		t.Errorf("command returned %v", msg)
	}
}

func TestPressOnAKeyWithNoApplicableCommand(t *testing.T) {
	reg := New()
	row := fixture{kind: KindIssue, state: StateClosed}
	for _, key := range []string{"x", "e", "c", "z", "v", "b"} {
		if _, ok := reg.Press(row, key); ok {
			t.Errorf("%q on a closed issue ran something", key)
		}
	}
	if _, ok := reg.Press(row, "j"); ok {
		t.Error("a motion key ran a command")
	}
}

func TestPressPicksTheSharedCommandThatApplies(t *testing.T) {
	// z is archive on a live plan and unarchive on an archived one, so one
	// key press runs a different command depending on the row.
	var ran []string
	reg := &Registry{}
	for _, verb := range []string{"archive", "unarchive"} {
		v := verb
		if err := reg.Register(Command{
			Verb: v, Key: "z", Label: v,
			Applies: func(r Row) bool {
				if r.Kind() != KindPlan {
					return false
				}
				archived := r.State() == StateArchived
				return archived == (v == "unarchive")
			},
			Run: func(Row, []string) tea.Cmd {
				ran = append(ran, v)
				return nil
			},
		}); err != nil {
			t.Fatalf("registering %s: %v", verb, err)
		}
	}

	if _, ok := reg.Press(fixture{kind: KindPlan, state: StateComplete}, "z"); !ok {
		t.Fatal("z on a live plan did nothing")
	}
	if _, ok := reg.Press(fixture{kind: KindPlan, state: StateArchived}, "z"); !ok {
		t.Fatal("z on an archived plan did nothing")
	}
	if _, ok := reg.Press(fixture{kind: KindIssue, state: StateOpen}, "z"); ok {
		t.Error("z on an issue row ran something")
	}
	if strings.Join(ran, ",") != "archive,unarchive" {
		t.Errorf("ran = %v, want archive then unarchive", ran)
	}
}

// TestRunPassesOnlyWhatItIsGiven checks the handler contract the ":" line
// depends on: the arguments come from the caller, not from the handler.
// TestNilRowRunsOnlyTheCommandsThatNeedNoRow covers the empty table on first
// run: a predicate cannot be asked about a row that is not there, so a nil row
// gets the commands with no predicate and nothing else.
func TestNilRowRunsOnlyTheCommandsThatNeedNoRow(t *testing.T) {
	var ran []string
	reg := &Registry{}
	for _, verb := range []string{"new", "start", "close"} {
		v := verb
		c := Command{
			Verb: v, Key: keyFor(v), Label: v,
			Run: func(Row, []string) tea.Cmd {
				ran = append(ran, v)
				return nil
			},
		}
		if v != "new" {
			c.Applies = isOpenIssue
		}
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok := reg.Press(nil, "s"); ok {
		t.Error("a command with a predicate ran against no row")
	}
	if _, ok := reg.Press(nil, "x"); ok {
		t.Error("a command with a predicate ran against no row")
	}
	if _, ok := reg.Press(nil, "n"); !ok {
		t.Error("a command with no predicate did nothing on an empty table")
	}
	if strings.Join(ran, ",") != "new" {
		t.Errorf("ran = %v, want only new", ran)
	}
	for _, c := range reg.For(nil) {
		if c.Verb != "new" {
			t.Errorf("For(nil) offered %q", c.Verb)
		}
	}
}

func keyFor(verb string) string {
	if verb == "start" {
		return "s"
	}
	return verb[:1]
}

func TestAppliesIsFalseForAPredicateOnNoRow(t *testing.T) {
	if Applies(Command{Verb: "close", Applies: isOpenIssue}, nil) {
		t.Error("a command with a predicate applied to no row")
	}
	if !Applies(Command{Verb: "new"}, nil) {
		t.Error("a command with no predicate did not apply to no row")
	}
}

func TestBarAndHelpOnNoRow(t *testing.T) {
	bar := Bar(New(), nil, 0)
	if !strings.Contains(bar, "n New") {
		t.Errorf("bar = %q, want the commands that need no row", bar)
	}
	for _, absent := range []string{"x Close", "s Start", "b Blockers"} {
		if strings.Contains(bar, absent) {
			t.Errorf("bar = %q, want no %q with nothing selected", bar, absent)
		}
	}
	for _, l := range strings.Split(Help(New(), nil), "\n") {
		switch strings.Fields(l)[1] {
		case "close", "start", "blockers", "reopen":
			t.Errorf("help lists %q with nothing selected:\n%s", l, l)
		}
	}
}

func TestRunPassesOnlyWhatItIsGiven(t *testing.T) {
	var got []string
	c := Command{Verb: "close", Run: func(_ Row, args []string) tea.Cmd {
		got = args
		return nil
	}}
	Run(c, fixture{}, []string{"the", "agent", "was", "stuck"})
	if strings.Join(got, " ") != "the agent was stuck" {
		t.Errorf("args = %v", got)
	}
	Run(c, fixture{}, nil)
	if got != nil {
		t.Errorf("args = %v, want none", got)
	}
}

func TestRunOnACommandWithNoHandlerIsNotACrash(t *testing.T) {
	if cmd := Run(Command{Verb: "new"}, fixture{}, nil); cmd != nil {
		t.Error("a command with no handler returned a command")
	}
	if cmd, ok := New().Press(fixture{}, "n"); !ok || cmd != nil {
		t.Errorf("Press on an unwired command = (%v, %v), want (nil, true)", cmd, ok)
	}
}
