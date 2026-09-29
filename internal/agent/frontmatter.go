package agent

import (
	"fmt"
	"os"
	"strings"
)

// SetFrontmatter rewrites one key in the frontmatter of the agent file at
// path, leaving the rest of the file — comments included — byte for byte as it
// was. A key that is absent is added to the end of the frontmatter; a file
// with no frontmatter is an error, because there is nowhere to put it and
// pib cannot invent a system prompt.
//
// value is written as it is given, matching how the definitions on disk are
// already written: read, bash rather than "read, bash".
func SetFrontmatter(path, key, value string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(src), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fmt.Errorf("%s: missing frontmatter", path)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return fmt.Errorf("%s: unterminated frontmatter", path)
	}

	// A key that is not there already goes at the end of the block, where a
	// reader expects an addition rather than a reordering.
	prefix := key + ":"
	line := prefix + " " + value
	at := -1
	for i := 1; i < end; i++ {
		if t := strings.TrimSpace(lines[i]); t == prefix || strings.HasPrefix(t, prefix+" ") {
			at = i
			break
		}
	}
	switch {
	case at >= 0:
		lines[at] = line
	default:
		added := make([]string, 0, len(lines)+1)
		added = append(added, lines[:end]...)
		added = append(added, line)
		lines = append(added, lines[end:]...)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
