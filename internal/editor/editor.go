// Package editor opens the user's $EDITOR on a text buffer, the way git
// opens it for a commit message: a scissors line separates what the user
// wrote from the context pib offered below it. Everything above the
// scissors is taken verbatim, so Markdown headings survive.
package editor

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// ErrAborted reports that the editor exited without leaving any text above
// the scissors line, the same way an empty commit message aborts a commit.
var ErrAborted = errors.New("editor: aborted, no text entered")

// Scissors is the marker line, the same one git writes.
const Scissors = "# ------------------------ >8 ------------------------"

const scissorsNote = "# Do not modify or remove the line above. Everything below it is ignored."

// Section is one block of context shown below the scissors line.
type Section struct {
	Title string
	Body  string
}

// Template is the buffer Compose writes: a header line naming what the
// text is for, and the context the user is answering.
type Template struct {
	Header  string
	Context []Section
}

// Compose writes t to a temp file and opens the editor on it. The command
// is returned for tea.ExecProcess; the function reads the result and is
// called once the editor has exited. It returns the text above the
// scissors line with trailing whitespace trimmed, or ErrAborted when the
// user left nothing there. If the scissors line is gone, the whole file is
// kept rather than guessing what to strip.
func Compose(t Template) (*exec.Cmd, func() (string, error)) {
	f, err := os.CreateTemp("", "pib-*.md")
	if err != nil {
		return failed(), func() (string, error) { return "", err }
	}
	path := f.Name()
	if _, err := f.WriteString(render(t)); err != nil {
		f.Close()
		return failed(), func() (string, error) { return "", err }
	}
	if err := f.Close(); err != nil {
		return failed(), func() (string, error) { return "", err }
	}
	return start(path), func() (string, error) {
		text, err := cleanup(path)
		if err == nil {
			err = os.Remove(path)
		}
		if err != nil {
			return "", err
		}
		return text, nil
	}
}

// Open edits an existing file in place, with no scissors: the whole file is
// the user's. It reports whether the contents changed.
func Open(path string) (*exec.Cmd, func() (bool, error)) {
	before, _ := os.ReadFile(path)
	return start(path), func() (bool, error) {
		after, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		return string(before) != string(after), nil
	}
}

// failed returns a command that cannot run, so the caller can still hand
// something to tea.ExecProcess and read the real error from the callback.
func failed() *exec.Cmd {
	return exec.Command("false")
}

// start resolves the editor and runs it on path the way git does, so
// editors with arguments (`code --wait`) and quoted paths both work.
func start(path string) *exec.Cmd {
	name := os.Getenv("EDITOR")
	if name == "" {
		name = os.Getenv("VISUAL")
	}
	if name == "" {
		name = "vi"
	}
	return exec.Command("sh", "-c", name+` "$@"`, "sh", path)
}

func render(t Template) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(Scissors + "\n")
	b.WriteString(scissorsNote + "\n")
	if t.Header != "" {
		b.WriteString("# " + t.Header + "\n")
	}
	for _, s := range t.Context {
		b.WriteString("#\n")
		b.WriteString("# ── " + s.Title + " ")
		b.WriteString(strings.Repeat("─", 60) + "\n")
		b.WriteString("#\n")
		for _, para := range strings.Split(strings.TrimRight(s.Body, "\n"), "\n") {
			if strings.TrimSpace(para) == "" {
				b.WriteString("#\n")
				continue
			}
			for _, line := range wrap(para, commentWidth) {
				b.WriteString("#   " + line + "\n")
			}
		}
	}
	return b.String()
}

// cleanup reads path and returns what the user kept: everything above the
// scissors line, or the whole file when the scissors line is missing.
func cleanup(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(raw), "\n")
	cut := len(lines)
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == Scissors {
			cut = i
			break
		}
	}
	kept := lines[:cut]
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	text := strings.TrimRight(strings.Join(kept, "\n"), " \t\n")
	if strings.TrimSpace(text) == "" {
		return "", ErrAborted
	}
	return text, nil
}

// commentWidth is the width context bodies are wrapped at, so long
// comments stay readable in a narrow editor window.
const commentWidth = 70

// wrap breaks line on whitespace at most width columns, the way
// textproto wrap does.
func wrap(line string, width int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return nil
	}
	var out []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			out = append(out, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(out, cur)
}
