package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEditor writes a script that replaces the file it is given with the
// contents of the script's payload file, exercising the real
// `sh -c "$EDITOR \"$@\""` invocation, arguments and all.
func fakeEditor(t *testing.T, payload string) {
	t.Helper()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload")
	if err := os.WriteFile(payloadPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-editor")
	body := "#!/bin/sh\nfor a in \"$@\"; do :; done\nprintf '%s' \"$(cat " + payloadPath + ")\" > \"$a\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)
}

func composeText(t *testing.T, tmpl Template, payload string) (string, error) {
	t.Helper()
	fakeEditor(t, payload)
	cmd, done := Compose(tmpl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("editor: %v: %s", err, out)
	}
	return done()
}

func sample() Template {
	return Template{
		Header:  "followup to #14 · Pick an event store · researcher",
		Context: []Section{{Title: "recent comments", Body: "researcher · 4m ago\nShould the projection be rebuilt?"}},
	}
}

func TestComposeKeepsTextAboveScissorsIncludingHeadings(t *testing.T) {
	payload := "# I am a heading\n\nSome text.   \n" + strings.Repeat("# ------------------------ >8 ------------------------\n", 1) +
		"# Do not modify or remove the line above. Everything below it is ignored.\n# followup to #14\n"
	got, err := composeText(t, sample(), payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "# I am a heading\n\nSome text."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestComposeDropsEverythingBelowScissors(t *testing.T) {
	payload := "hello\n" + Scissors + "\n# leaked context\n# more leaked\n"
	got, err := composeText(t, sample(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestComposeWithoutScissorsKeepsWholeText(t *testing.T) {
	payload := "# heading\nbody text\n# still context-looking line\n"
	got, err := composeText(t, sample(), payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "# heading\nbody text\n# still context-looking line"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestComposeEmptyIsAborted(t *testing.T) {
	if _, err := composeText(t, sample(), "\n"+Scissors+"\n# context\n"); err != ErrAborted {
		t.Errorf("got %v, want ErrAborted", err)
	}
}

func TestComposeRendersTemplate(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tpl")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(render(sample()))
	f.Close()
	raw, _ := os.ReadFile(f.Name())
	got := string(raw)
	for _, want := range []string{Scissors, "# followup to #14 · Pick an event store · researcher", "# ── recent comments "} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q:\n%s", want, got)
		}
	}
}

func TestComposeWrapsLongCommentBodies(t *testing.T) {
	got := render(Template{Context: []Section{{Title: "t", Body: strings.Repeat("word ", 40)}}})
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "#   word") && len(line) > 80 {
			t.Errorf("comment line not wrapped: %q", line)
		}
	}
}

func TestResolvesEditorInOrder(t *testing.T) {
	t.Setenv("EDITOR", "first-editor")
	t.Setenv("VISUAL", "second-editor")
	if cmd := start("/tmp/x"); !strings.Contains(strings.Join(cmd.Args, " "), "first-editor") {
		t.Errorf("expected EDITOR to win, got %v", cmd.Args)
	}
	unsetenv(t, "EDITOR")
	if cmd := start("/tmp/x"); !strings.Contains(strings.Join(cmd.Args, " "), "second-editor") {
		t.Errorf("expected VISUAL next, got %v", cmd.Args)
	}
	unsetenv(t, "VISUAL")
	if cmd := start("/tmp/x"); !strings.Contains(strings.Join(cmd.Args, " "), "vi") {
		t.Errorf("expected vi as the fallback, got %v", cmd.Args)
	}
}

func unsetenv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		}
	})
}

func TestEditorWithArgumentsAndQuotedPath(t *testing.T) {
	dir := t.TempDir()
	quoted := filepath.Join(dir, "a file.md")
	if err := os.WriteFile(quoted, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "ed")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'arg:%s' \"$1\" > \"$2\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script+" --wait")
	cmd, done := Open(quoted)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("editor: %v: %s", err, out)
	}
	changed, err := done()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected the file to report as changed")
	}
	raw, _ := os.ReadFile(quoted)
	if string(raw) != "arg:--wait" {
		t.Errorf("editor argument not passed through: %q", raw)
	}
}

func TestOpenReportsNoChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(path, []byte("same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "noop")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)
	cmd, done := Open(path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("editor: %v: %s", err, out)
	}
	changed, err := done()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected no change")
	}
}
