package session

import (
	"os"
	"path/filepath"
	"testing"
)

// write lays out a run directory the way a child agent leaves one.
func write(t *testing.T, transcript string, exit string) string {
	t.Helper()

	dir := t.TempDir()
	if transcript != "" {
		if err := os.WriteFile(filepath.Join(dir, "run.jsonl"), []byte(transcript), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if exit != "" {
		if err := os.WriteFile(filepath.Join(dir, ExitFileName), []byte(exit), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func line(role, stopReason string, blocks string) string {
	return `{"type":"message","message":{"role":"` + role + `","stopReason":"` + stopReason + `","content":[` + blocks + `]}}` + "\n"
}

func textBlock(s string) string { return `{"type":"text","text":"` + s + `"}` }

func TestCollectDone(t *testing.T) {
	dir := write(t,
		line("user", "", textBlock("go"))+
			line("assistant", "stop", `{"type":"thinking","thinking":"hmm"},`+textBlock("the answer")),
		`{"type":"done"}`)

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone {
		t.Errorf("Status = %q, want done", got.Status)
	}
	if got.Text != "the answer" {
		t.Errorf("Text = %q, want the final assistant text", got.Text)
	}
}

func TestCollectNeedsInput(t *testing.T) {
	dir := write(t, line("assistant", "stop", textBlock("thinking about it")),
		`{"type":"ask","message":"which database?"}`)

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNeedsInput {
		t.Errorf("Status = %q, want needs_input", got.Status)
	}
	if got.Text != "which database?" {
		t.Errorf("Text = %q, want the question", got.Text)
	}
}

// A crashed turn still exits zero and leaves a plausible final message, so the
// stop reason has to be what decides this, not the exit code.
func TestCollectErrorFromStopReason(t *testing.T) {
	dir := write(t, line("assistant", "error", textBlock("partial work")), "")

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusError {
		t.Errorf("Status = %q, want error", got.Status)
	}
}

func TestCollectErrorSidecarWins(t *testing.T) {
	dir := write(t, line("assistant", "stop", textBlock("stale message")),
		`{"type":"error","errorMessage":"provider overloaded","stopReason":"error"}`)

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusError {
		t.Errorf("Status = %q, want error", got.Status)
	}
	if got.Text != "provider overloaded" {
		t.Errorf("Text = %q, want the sidecar message", got.Text)
	}
}

// No sidecar and a clean stop means the window was closed by hand.
func TestCollectUnknownWithoutSidecar(t *testing.T) {
	dir := write(t, line("assistant", "stop", textBlock("half done")), "")

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnknown {
		t.Errorf("Status = %q, want unknown", got.Status)
	}
	if got.Text != "half done" {
		t.Errorf("Text = %q, want the last message", got.Text)
	}
}

func TestLastAssistantKeepsLastTextTurn(t *testing.T) {
	// A trailing tool-call turn carries no text; the previous answer stands,
	// but the stop reason must come from the final turn.
	dir := write(t,
		line("assistant", "stop", textBlock("first answer"))+
			line("assistant", "error", `{"type":"toolCall","name":"bash"}`),
		"")

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "first answer" {
		t.Errorf("Text = %q, want the last turn that spoke", got.Text)
	}
	if got.Status != StatusError {
		t.Errorf("Status = %q, want error from the final turn", got.Status)
	}
}

func TestCollectSkipsPartialLine(t *testing.T) {
	dir := write(t, line("assistant", "stop", textBlock("complete"))+`{"type":"mess`, `{"type":"done"}`)

	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "complete" {
		t.Errorf("Text = %q, want the complete entry", got.Text)
	}
}

func TestCollectNoTranscript(t *testing.T) {
	got, err := Collect(write(t, "", `{"type":"done"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.Text != "" {
		t.Errorf("got %+v, want done with no text", got)
	}
}
