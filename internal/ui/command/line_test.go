package command

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// typing feeds a string to the line as key presses, the way a terminal sends
// one character at a time.
func typing(t *testing.T, l *Line, text string) {
	t.Helper()
	for _, r := range text {
		if r == ' ' {
			// Bubble Tea sends a lone space as KeySpace, not KeyRunes.
			l.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{r}})
			continue
		}
		msg, _ := l.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		l = msg.(*Line)
	}
}

// enter presses enter and drains the returned command.
func enter(t *testing.T, l *Line) []tea.Msg {
	t.Helper()
	_, cmd := l.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return drain(cmd)
}

// press sends a named key.
func press(t *testing.T, l *Line, name string) tea.Cmd {
	t.Helper()
	_, cmd := l.Update(keyPress(name))
	return cmd
}

// drain runs a command and collects the messages it yields, one layer deep
// for a batch.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		if c == nil {
			continue
		}
		if inner := c(); inner != nil {
			out = append(out, inner)
		}
	}
	return out
}

func messages(msgs []tea.Msg) string {
	var parts []string
	for _, m := range msgs {
		switch v := m.(type) {
		case Ran:
			parts = append(parts, "ran:"+v.Verb+" "+strings.Join(v.Args, ","))
		case UnknownVerbMsg:
			parts = append(parts, "unknown:"+v.Input)
		case NoSelectionMsg:
			parts = append(parts, "noselection:"+v.Verb)
		case InactiveMsg:
			parts = append(parts, "cancelled:"+v.Input)
		case string:
			parts = append(parts, v)
		default:
			parts = append(parts, "other")
		}
	}
	return strings.Join(parts, " ")
}

// TestPastedTextGoesInWhole covers the bracketed paste Bubble Tea delivers as
// one KeyRunes message. A reason or an answer is what people paste, and the
// arguments are the only free text in the TUI.
func TestPastedTextGoesInWhole(t *testing.T) {
	reg := New()
	var gotArgs []string
	if err := reg.Handle("close", func(_ Row, args []string) tea.Cmd {
		gotArgs = args
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "close ")
	l.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("the agent was stuck on the PR")})
	if l.Input() != "close the agent was stuck on the PR" {
		t.Fatalf("input = %q, want the paste in whole", l.Input())
	}
	enter(t, l)
	if strings.Join(gotArgs, " ") != "the agent was stuck on the PR" {
		t.Errorf("handler args = %v", gotArgs)
	}
}

func TestTypedRunesAndSpacesGoIn(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "close")
	// A space arrives as its own key type in Bubble Tea v1.3, with the rune
	// on the message.
	l.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	typing(t, l, "stuck")
	if l.Input() != "close stuck" {
		t.Errorf("input = %q", l.Input())
	}
	// Several runes in one message — fast typing, or a paste without the
	// bracketed-paste flag — all go in.
	l.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("er now")})
	if l.Input() != "close stucker now" {
		t.Errorf("input = %q", l.Input())
	}
}

func TestNamedKeysAreNotTyped(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "close")
	for _, name := range []string{"up", "down", "left", "right", "home", "f1"} {
		press(t, l, name)
	}
	if l.Input() != "close" {
		t.Errorf("input = %q, want the named keys left out of it", l.Input())
	}
}

func TestLineDispatchesByVerbWithArgs(t *testing.T) {
	var gotArgs []string
	reg := &Registry{}
	if err := reg.Register(Command{
		Verb: "close", Key: "x", Label: "Close", Args: "<reason>",
		Applies: isOpenIssue,
		Run: func(_ Row, args []string) tea.Cmd {
			gotArgs = args
			return func() tea.Msg { return "handler" }
		},
	}); err != nil {
		t.Fatal(err)
	}

	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "close the agent was stuck")
	if l.Input() != "close the agent was stuck" {
		t.Fatalf("input = %q", l.Input())
	}
	msgs := enter(t, l)
	if got := messages(msgs); got != "ran:close the,agent,was,stuck handler" {
		t.Errorf("messages = %q", got)
	}
	if strings.Join(gotArgs, ",") != "the,agent,was,stuck" {
		t.Errorf("handler args = %v", gotArgs)
	}
	if l.Active() {
		t.Error("the line stayed open after running a command")
	}
}

func TestLineWithNoArgsRunsTheCommand(t *testing.T) {
	reg := &Registry{}
	if err := reg.Register(Command{
		Verb: "comment", Applies: isOpenIssue,
		Run: func(_ Row, args []string) tea.Cmd {
			if len(args) != 0 {
				t.Errorf("handler got args %v, want none", args)
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "comment")
	if got := messages(enter(t, l)); got != "ran:comment " {
		t.Errorf("messages = %q", got)
	}
}

func TestLineCompletesAVerbAndShowsItsArgs(t *testing.T) {
	reg := &Registry{}
	if err := reg.Register(Command{
		Verb: "close", Key: "x", Label: "Close", Args: "<reason>", Applies: isOpenIssue,
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(Command{Verb: "comment", Key: "c", Label: "Comment", Applies: isOpenIssue}); err != nil {
		t.Fatal(err)
	}
	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "cl")
	if v := l.View(); !strings.Contains(v, "<reason>") {
		t.Errorf("view = %q, want the args hint for close", v)
	}
	press(t, l, "tab")
	if l.Input() != "close " {
		t.Errorf("after tab input = %q, want %q", l.Input(), "close ")
	}
	if v := l.View(); !strings.Contains(v, "<reason>") {
		t.Errorf("view = %q, want the hint still there once the verb is complete: that is when the argument is typed", v)
	}
	typing(t, l, "stuck")
	if v := l.View(); !strings.Contains(v, "<reason>") {
		t.Errorf("view = %q, want the hint while the argument is being typed", v)
	}
	if got := messages(enter(t, l)); got != "ran:close stuck" {
		t.Errorf("messages = %q", got)
	}
}

// TestTabLeavesAnArgumentAlone is the guard on tab only ever completing a verb:
// in :close p, "p" is what the user is typing, not the start of a verb.
func TestTabLeavesAnArgumentAlone(t *testing.T) {
	reg := &Registry{}
	for _, verb := range []string{"close", "comment", "pr"} {
		if err := reg.Register(Command{Verb: verb, Applies: isOpenIssue}); err != nil {
			t.Fatal(err)
		}
	}
	row := fixture{kind: KindIssue, state: StateOpen, hasPR: true}
	l := NewLine(reg, row)
	l.Open()
	typing(t, l, "close p")
	press(t, l, "tab")
	if l.Input() != "close p" {
		t.Errorf("input = %q, want tab to leave the argument alone", l.Input())
	}
	if v := l.View(); strings.Contains(v, "pr") {
		t.Errorf("view = %q, want no verb completions offered over an argument", v)
	}
}

func TestTabOnAnEmptyLineListsEveryVerb(t *testing.T) {
	reg := &Registry{}
	if err := reg.Register(Command{Verb: "close", Applies: isOpenIssue}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(Command{Verb: "new"}); err != nil {
		t.Fatal(err)
	}
	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	press(t, l, "tab")
	if v := l.View(); !strings.Contains(v, "close") || !strings.Contains(v, "new") {
		t.Errorf("view = %q, want both verbs listed", v)
	}
}

func TestLineCompletionExtendsAnAmbiguousPrefix(t *testing.T) {
	reg := &Registry{}
	for _, verb := range []string{"close", "comment", "start"} {
		if err := reg.Register(Command{Verb: verb, Applies: isOpenIssue}); err != nil {
			t.Fatal(err)
		}
	}
	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "c")
	press(t, l, "tab")
	if l.Input() != "c" {
		t.Errorf("input = %q, want the common prefix of close and comment", l.Input())
	}
	view := l.View()
	if !strings.Contains(view, "close") || !strings.Contains(view, "comment") {
		t.Errorf("view = %q, want the candidates listed", view)
	}
	typing(t, l, "omment")
	press(t, l, "tab")
	if l.Input() != "comment " {
		t.Errorf("input = %q, want %q", l.Input(), "comment ")
	}
}

func TestLineCompletesOnlyVerbsThatApply(t *testing.T) {
	reg := New()
	l := NewLine(reg, fixture{kind: KindIssue, state: StateClosed})
	l.Open()
	typing(t, l, "clos")
	press(t, l, "tab")
	if l.Input() != "clos" {
		t.Errorf("input = %q; close does nothing on a closed issue, so there is nothing to complete", l.Input())
	}
}

func TestLineHistory(t *testing.T) {
	reg := New()
	l := NewLine(reg, fixture{kind: KindIssue, state: StateOpen})

	l.Open()
	typing(t, l, "comment")
	enter(t, l)

	l.Open()
	typing(t, l, "edit")
	enter(t, l)

	l.Open()
	press(t, l, "up")
	if l.Input() != "edit" {
		t.Errorf("up recalled %q, want the newest entry", l.Input())
	}
	press(t, l, "up")
	if l.Input() != "comment" {
		t.Errorf("up recalled %q, want the entry before it", l.Input())
	}
	press(t, l, "up")
	if l.Input() != "comment" {
		t.Errorf("walking past the oldest entry gave %q", l.Input())
	}
	press(t, l, "down")
	if l.Input() != "edit" {
		t.Errorf("down gave %q", l.Input())
	}
	press(t, l, "down")
	if l.Input() != "" {
		t.Errorf("walking past the newest entry gave %q, want an empty line", l.Input())
	}
}

func TestLineBackspaceEdits(t *testing.T) {
	l := NewLine(New(), fixture{})
	l.Open()
	typing(t, l, "closex")
	press(t, l, "backspace")
	if l.Input() != "close" {
		t.Errorf("input = %q", l.Input())
	}
	for range 20 {
		press(t, l, "backspace")
	}
	if l.Input() != "" {
		t.Errorf("backspacing past the start left %q", l.Input())
	}
}

func TestLineEscCancels(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "close")
	msgs := drain(press(t, l, "esc"))
	if got := messages(msgs); got != "cancelled:close" {
		t.Errorf("messages = %q", got)
	}
	if l.Active() {
		t.Error("esc left the line open")
	}
	if l.View() != "" {
		t.Errorf("an inactive line renders %q", l.View())
	}
	// A cancelled line runs nothing.
	if l.Input() != "" {
		t.Errorf("input = %q after cancelling", l.Input())
	}
}

func TestLineEscOnAnEmptyLineIsSilent(t *testing.T) {
	l := NewLine(New(), fixture{})
	l.Open()
	if msgs := drain(press(t, l, "esc")); len(msgs) != 0 {
		t.Errorf("messages = %v, want none", messages(msgs))
	}
}

func TestLineRejectsAVerbNothingAnswersTo(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	l.Open()
	typing(t, l, "frobnicate")
	if got := messages(enter(t, l)); got != "unknown:frobnicate" {
		t.Errorf("messages = %q", got)
	}
	if l.Active() {
		t.Error("the line stayed open after a bad verb")
	}
}

func TestLineRejectsAVerbThatDoesNotApply(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateClosed})
	l.Open()
	typing(t, l, "close")
	if got := messages(enter(t, l)); got != "noselection:close" {
		t.Errorf("messages = %q", got)
	}
}

// TestLineWithNoRow covers the empty table on first run: there is nothing
// selected, so the commands that need a row do nothing and the ones that do
// not — new, settings, the filters — still work.
func TestLineWithNoRow(t *testing.T) {
	reg := New()
	l := NewLine(reg, nil)

	l.Open()
	typing(t, l, "new")
	if got := messages(enter(t, l)); got != "ran:new " {
		t.Errorf("messages = %q, want :new to work with nothing selected", got)
	}

	l.Open()
	typing(t, l, "close")
	if got := messages(enter(t, l)); got != "noselection:close" {
		t.Errorf("messages = %q, want a command that needs a row to be refused", got)
	}
}

func TestLineCompletionWithNoRow(t *testing.T) {
	l := NewLine(New(), nil)
	l.Open()
	// Completion asks the registry what applies; with no row that is the
	// handful of commands that need none, and asking must not panic.
	typing(t, l, "n")
	press(t, l, "tab")
	if l.Input() != "new " {
		t.Errorf("input = %q", l.Input())
	}
	typing(t, l, "clos")
	press(t, l, "tab")
	if l.Input() != "new clos" {
		t.Errorf("input = %q, want close to have nothing to complete to", l.Input())
	}
}

func TestLineInit(t *testing.T) {
	// The line needs no startup command; a model that returns one it never
	// waits for is a model that stutters on open.
	if cmd := NewLine(New(), fixture{}).Init(); cmd != nil {
		t.Error("Init returned a command")
	}
}

func TestLineIgnoresNonKeyMessages(t *testing.T) {
	l := NewLine(New(), fixture{})
	l.Open()
	typing(t, l, "close")
	if _, cmd := l.Update(tea.WindowSizeMsg{Width: 80, Height: 24}); cmd != nil {
		t.Error("a window resize ran a command")
	}
	if l.Input() != "close" {
		t.Errorf("input = %q", l.Input())
	}
}

func TestLineIgnoresKeysWhenClosed(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	typing(t, l, "close")
	if l.Input() != "" {
		t.Errorf("a closed line took input: %q", l.Input())
	}
	if _, cmd := l.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("a closed line ran something on enter")
	}
}

func TestLineDoesNotRepeatAHistoryEntry(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	for range 2 {
		l.Open()
		typing(t, l, "comment")
		enter(t, l)
	}
	l.Open()
	press(t, l, "up")
	press(t, l, "up")
	if l.Input() != "comment" {
		t.Errorf("input = %q, want the entry held once", l.Input())
	}
	press(t, l, "down")
	if l.Input() != "" {
		t.Errorf("input = %q, want no second entry to walk to", l.Input())
	}
}

func TestLineRemembersARepeatAfterSomethingElse(t *testing.T) {
	l := NewLine(New(), fixture{kind: KindIssue, state: StateOpen})
	for _, verb := range []string{"comment", "edit", "comment"} {
		l.Open()
		typing(t, l, verb)
		enter(t, l)
	}
	l.Open()
	press(t, l, "up")
	if l.Input() != "comment" {
		t.Errorf("up gave %q", l.Input())
	}
	press(t, l, "up")
	if l.Input() != "edit" {
		t.Errorf("up gave %q, want the middle entry kept", l.Input())
	}
}

func TestLineViewIsThePromptAndWhatIsTyped(t *testing.T) {
	l := NewLine(New(), fixture{})
	l.Open()
	typing(t, l, "cl")
	if got := l.View(); !strings.HasPrefix(got, ":cl") {
		t.Errorf("view = %q", got)
	}
}

func TestLineQuitsOnCtrlC(t *testing.T) {
	l := NewLine(New(), fixture{})
	l.Open()
	cmd := press(t, l, "ctrl+c")
	if cmd == nil {
		t.Fatal("ctrl+c in the line did nothing")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T", cmd())
	}
	if l.Active() {
		t.Error("ctrl+c left the line open")
	}
}

func TestLineKeepsUpWithTheSelectedRow(t *testing.T) {
	reg := New()
	open := fixture{kind: KindIssue, state: StateOpen}
	closed := fixture{kind: KindIssue, state: StateClosed}
	l := NewLine(reg, open)
	l.Open()
	typing(t, l, "close")
	l.SetRow(closed)
	if got := messages(enter(t, l)); got != "noselection:close" {
		t.Errorf("messages = %q, want the line to have followed the selection", got)
	}
}
