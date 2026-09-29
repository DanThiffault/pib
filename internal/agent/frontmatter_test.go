package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fmSample = `---
name: coder
description: Implements a single pib issue
tools: read, bash
model: stealth/space-bunny-alpha
---

# Body
`

func writeAgent(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coder.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetFrontmatterRewritesKeyKeepsTheRest(t *testing.T) {
	path := writeAgent(t, fmSample)

	if err := SetFrontmatter(path, "model", "anthropic/claude"); err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}

	got := readFile(t, path)
	want := `---
name: coder
description: Implements a single pib issue
tools: read, bash
model: anthropic/claude
---

# Body
`
	if got != want {
		t.Errorf("SetFrontmatter produced\n%q\nwant\n%q", got, want)
	}
}

func TestSetFrontmatterAddsMissingKey(t *testing.T) {
	path := writeAgent(t, fmSample)

	if err := SetFrontmatter(path, "thinking", "high"); err != nil {
		t.Fatalf("SetFrontmatter: %v", err)
	}

	got := readFile(t, path)
	want := `---
name: coder
description: Implements a single pib issue
tools: read, bash
model: stealth/space-bunny-alpha
thinking: high
---

# Body
`
	if got != want {
		t.Errorf("SetFrontmatter produced\n%q\nwant\n%q", got, want)
	}
}

func TestSetFrontmatterRejectsFileWithoutFrontmatter(t *testing.T) {
	path := writeAgent(t, "# Just a body\n")

	if err := SetFrontmatter(path, "model", "anthropic/claude"); err == nil {
		t.Fatal("SetFrontmatter on a file with no frontmatter: want error, got nil")
	}
}

func TestDefaultAgentsRoundTripByteIdentically(t *testing.T) {
	for _, name := range DefaultNames() {
		want, err := defaultBody(name)
		if err != nil {
			t.Fatalf("defaultBody(%s): %v", name, err)
		}
		path := writeAgent(t, string(want))

		if err := SetFrontmatter(path, "model", modelOf(t, name)); err != nil {
			t.Fatalf("SetFrontmatter(%s): %v", name, err)
		}
		if err := SetFrontmatter(path, "tools", strings.Join(toolsOf(t, name), ", ")); err != nil {
			t.Fatalf("SetFrontmatter(%s): %v", name, err)
		}

		if got := readFile(t, path); got != string(want) {
			t.Errorf("%s.md did not round trip:\n%s", name, got)
		}
	}
}

// modelOf and toolsOf return the values the definition already carries, so
// setting them again must change nothing.
func modelOf(t *testing.T, name string) string {
	t.Helper()
	return parseDefault(t, name).Model
}

func toolsOf(t *testing.T, name string) []string {
	t.Helper()
	return parseDefault(t, name).Tools
}

func parseDefault(t *testing.T, name string) Definition {
	t.Helper()
	body, err := defaultBody(name)
	if err != nil {
		t.Fatal(err)
	}
	d, err := parse(string(body))
	if err != nil {
		t.Fatalf("parse(%s): %v", name, err)
	}
	return d
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
