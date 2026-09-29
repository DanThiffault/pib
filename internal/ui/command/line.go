package command

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// prompt is the character that opens the line. It is also a motion key, which
// is the point: ":" is reserved for the line and can never be a command.
const prompt = ":"

// Line is the one-line ":" prompt of ADR-006 §2. It accepts every verb in
// the registry by its CLI name, completes them with tab, remembers what was
// typed with up and down, and runs the verb the words name.
//
// It is a component and not part of Model: the screens embed it, give it the
// selected row, and leave it inactive until the user presses ":".
type Line struct {
	reg *Registry
	row Row

	active      bool
	input       []rune
	completions []string
	history     []string
	// cursor is the position in history; len(history) means not browsing.
	cursor int
}

// NewLine returns an inactive line bound to a registry and a row. The row is
// replaced with SetRow as the selection moves; the verbs on offer are the ones
// that apply to it.
func NewLine(reg *Registry, row Row) *Line {
	return &Line{reg: reg, row: row, cursor: 0}
}

// Ran reports a command the line dispatched, with the words typed after the
// verb. The line emits it whether or not the command has a handler yet, so a
// caller can show the verb it accepted.
type Ran struct {
	Verb string
	Args []string
	Row  Row
}

// UnknownVerbMsg reports that the line was given a verb no command answers to.
type UnknownVerbMsg struct{ Input string }

// NoSelectionMsg reports a verb typed with no row to act on, or with a row it
// does nothing to.
type NoSelectionMsg struct{ Verb string }

// InactiveMsg reports that the line was closed with nothing in it. It is what
// a screen uses to clear a notice left over from a bad verb.
type InactiveMsg struct{ Input string }

// Open shows the line with an empty buffer and no history browsing.
func (l *Line) Open() {
	l.active = true
	l.input = nil
	l.completions = nil
	l.cursor = len(l.history)
}

// Close hides the line and forgets what was typed.
func (l *Line) Close() {
	l.active = false
	l.input = nil
	l.completions = nil
	l.cursor = 0
}

// Active reports whether the line is showing.
func (l *Line) Active() bool { return l != nil && l.active }

// SetRow points the line at the selected row.
func (l *Line) SetRow(row Row) { l.row = row }

// Input returns what has been typed, without the prompt.
func (l *Line) Input() string { return string(l.input) }

// Init implements tea.Model. The line needs no startup command.
func (l *Line) Init() tea.Cmd { return nil }

// Update runs the line: typing, backspace, tab to complete, up and down
// through the history, enter to run and esc to cancel. Every other key is
// left to the screen — a model that swallows keys it does not understand is
// how a TUI ends up feeling stuck.
func (l *Line) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !l.Active() {
		return l, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return l, nil
	}
	switch key.String() {
	case "esc":
		input := l.Input()
		l.Close()
		if input == "" {
			return l, nil
		}
		return l, func() tea.Msg { return InactiveMsg{Input: input} }
	case "enter":
		return l, l.submit()
	case "tab":
		l.complete()
		return l, nil
	case "up":
		l.recall(-1)
		return l, nil
	case "down":
		l.recall(1)
		return l, nil
	case "backspace":
		if len(l.input) > 0 {
			l.input = l.input[:len(l.input)-1]
		}
		l.completions = nil
		return l, nil
	case "ctrl+c":
		l.Close()
		return l, tea.Quit
	case "space":
		l.input = append(l.input, ' ')
		l.completions = nil
		return l, nil
	}
	if text := key.String(); len([]rune(text)) == 1 {
		l.input = append(l.input, []rune(text)...)
		l.completions = nil
	}
	return l, nil
}

// View renders the line: the prompt, what is typed, the hint for the verb
// being completed, and the alternatives when a prefix is ambiguous. An
// inactive line renders nothing at all.
func (l *Line) View() string {
	if !l.Active() {
		return ""
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString(string(l.input))
	if hint := l.hint(); hint != "" {
		b.WriteString("  ")
		b.WriteString(hint)
	}
	if len(l.completions) > 0 {
		b.WriteString("  ")
		b.WriteString(strings.Join(l.completions, " "))
	}
	return b.String()
}

// submit runs the verb named in the buffer, passing the words after it as
// arguments, and closes the line.
func (l *Line) submit() tea.Cmd {
	words := strings.Fields(l.Input())
	input := l.Input()
	if len(words) == 0 {
		l.Close()
		return nil
	}
	verb, args := words[0], words[1:]
	l.remember(input)
	l.Close()

	c, ok := l.reg.ByVerb(verb)
	if !ok {
		return func() tea.Msg { return UnknownVerbMsg{Input: input} }
	}
	// A nil row is an empty table, not an error: the commands that act on
	// whatever is selected (new, settings, the filters) still apply, and the
	// ones that act on a row do not.
	if !Applies(c, l.row) {
		return func() tea.Msg { return NoSelectionMsg{Verb: verb} }
	}
	row := l.row
	return tea.Batch(
		func() tea.Msg { return Ran{Verb: verb, Args: args, Row: row} },
		Run(c, row, args),
	)
}

// complete fills in the verb from the commands that apply to the row. A unique
// prefix is completed and given a trailing space, ready for the argument; an
// ambiguous one is extended as far as the candidates agree and listed in the
// view. An empty buffer lists every verb there is.
//
// Tab only ever completes the verb. Past the first word the buffer holds the
// user's argument, and replacing "p" with "pr" in :close p is a way of
// silently changing what someone typed.
func (l *Line) complete() {
	word := string(l.input)
	if !l.completingVerb() {
		l.completions = nil
		return
	}
	candidates := l.matching(word)
	if len(candidates) == 0 {
		l.completions = nil
		return
	}
	if len(candidates) == 1 {
		l.input = []rune(candidates[0] + " ")
		l.completions = nil
		return
	}
	if prefix := commonPrefix(candidates); len(prefix) > len(word) {
		l.input = []rune(prefix)
	}
	l.completions = candidates
}

// completingVerb reports whether the buffer is still on the verb: empty, or
// one word and no space yet.
func (l *Line) completingVerb() bool {
	s := string(l.input)
	return s == "" || !strings.Contains(s, " ")
}

// matching returns the applicable verbs starting with a prefix.
func (l *Line) matching(prefix string) []string {
	var out []string
	for _, c := range l.reg.For(l.row) {
		if strings.HasPrefix(c.Verb, prefix) {
			out = append(out, c.Verb)
		}
	}
	return out
}

// hint is the Args hint of the verb, taken from the first word. It is the
// hint for what the user is about to type, so it has to survive completion:
// "close<tab>" leaves the buffer at "close ", and the hint is wanted exactly
// then.
func (l *Line) hint() string {
	if l.completions != nil {
		return ""
	}
	fields := strings.Fields(string(l.input))
	if len(fields) == 0 {
		return ""
	}
	candidates := l.matching(fields[0])
	if len(candidates) != 1 {
		return ""
	}
	c, ok := l.reg.ByVerb(candidates[0])
	if !ok || c.Args == "" {
		return ""
	}
	return c.Args
}

// recall walks the history. step is -1 for older, +1 for newer; walking past
// the newest entry leaves the buffer as it was.
func (l *Line) recall(step int) {
	if len(l.history) == 0 {
		return
	}
	next := l.cursor + step
	if next < 0 {
		next = 0
	}
	if next > len(l.history) {
		next = len(l.history)
	}
	l.cursor = next
	if next == len(l.history) {
		l.input = nil
		return
	}
	l.input = []rune(l.history[next])
}

// remember adds an entry to the history, skipping a repeat of the last one.
func (l *Line) remember(input string) {
	if n := len(l.history); n > 0 && l.history[n-1] == input {
		l.cursor = len(l.history)
		return
	}
	l.history = append(l.history, input)
	l.cursor = len(l.history)
}

func commonPrefix(candidates []string) string {
	prefix := candidates[0]
	for _, c := range candidates[1:] {
		for !strings.HasPrefix(c, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}
