// Package session reads the transcript and exit sidecar a pi child leaves
// behind, so pib can tell a finished agent from one that stopped to ask a
// question or crashed.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Status is why a child agent stopped.
type Status string

const (
	// StatusDone means the agent reported its task complete.
	StatusDone Status = "done"
	// StatusNeedsInput means the agent stopped to ask a question.
	StatusNeedsInput Status = "needs_input"
	// StatusError means the agent's last turn failed.
	StatusError Status = "error"
	// StatusUnknown means the agent exited without saying why — usually the
	// window was closed by hand.
	StatusUnknown Status = "unknown"
)

// ExitFileName is the sidecar a child writes to declare why it stopped. Its
// path is handed to the child in PIB_EXIT_FILE.
const ExitFileName = "exit.json"

// Exit is the sidecar written by the pib_done and pib_ask tools.
type Exit struct {
	Type         string `json:"type"`
	Message      string `json:"message,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	StopReason   string `json:"stopReason,omitempty"`
}

// Result is what a child agent produced.
type Result struct {
	Status Status
	Text   string
	// StopReason is the pi stop reason of the last assistant turn.
	StopReason string
}

// ReadExit loads the sidecar. A missing file is not an error — the child may
// have been closed by hand.
func ReadExit(dir string) (Exit, bool, error) {
	body, err := os.ReadFile(filepath.Join(dir, ExitFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Exit{}, false, nil
		}
		return Exit{}, false, err
	}

	var exit Exit
	if err := json.Unmarshal(body, &exit); err != nil {
		return Exit{}, false, fmt.Errorf("parsing exit sidecar: %w", err)
	}
	return exit, true, nil
}

// Collect combines the exit sidecar with the transcript to describe how a
// child ended. A crashed turn exits zero and leaves a stale final message, so
// the stop reason is checked rather than trusted to be a success.
func Collect(dir string) (Result, error) {
	transcript, err := FindTranscript(dir)
	if err != nil {
		return Result{}, err
	}

	var result Result
	if transcript != "" {
		result, err = LastAssistant(transcript)
		if err != nil {
			return Result{}, err
		}
	}

	exit, found, err := ReadExit(dir)
	if err != nil {
		return Result{}, err
	}

	switch {
	case found && exit.Type == "error":
		result.Status = StatusError
		if exit.ErrorMessage != "" {
			result.Text = exit.ErrorMessage
		}
		if exit.StopReason != "" {
			result.StopReason = exit.StopReason
		}
	case result.StopReason == "error":
		// The turn failed even though the process exited cleanly.
		result.Status = StatusError
	case found && exit.Type == "ask":
		result.Status = StatusNeedsInput
		if exit.Message != "" {
			result.Text = exit.Message
		}
	case found && exit.Type == "done":
		result.Status = StatusDone
	default:
		result.Status = StatusUnknown
	}

	return result, nil
}

// FindTranscript returns the newest .jsonl in dir, or "" if there is none.
func FindTranscript(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	var newest string
	var newestMod int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); newest == "" || mod > newestMod {
			newest, newestMod = filepath.Join(dir, entry.Name()), mod
		}
	}

	return newest, nil
}

// entry is one line of a pi transcript.
type entry struct {
	Type    string `json:"type"`
	Message struct {
		Role       string `json:"role"`
		StopReason string `json:"stopReason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// LastAssistant returns the text of the final assistant message, which is the
// agent's answer, along with the stop reason of that turn.
func LastAssistant(path string) (Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var result Result
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			// A partially written final line is expected while a child is
			// still running; skip it rather than failing the whole read.
			continue
		}
		if e.Type != "message" || e.Message.Role != "assistant" {
			continue
		}

		var text strings.Builder
		for _, block := range e.Message.Content {
			if block.Type == "text" && block.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n\n")
				}
				text.WriteString(block.Text)
			}
		}

		// Turns that only make tool calls carry no text; keep the last one
		// that actually said something, but always track the stop reason.
		result.StopReason = e.Message.StopReason
		if text.Len() > 0 {
			result.Text = text.String()
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, err
	}

	return result, nil
}

// ErrNoTranscript reports that a child produced no transcript at all.
var ErrNoTranscript = errors.New("no transcript found")
