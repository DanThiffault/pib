package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Set rewrites one key in the config file at path, creating the file and the
// section if they are missing. value is the literal TOML value — "coder" with
// its quotes, or true, or 3 — because the caller knows the key's type.
//
// The rewrite is line-oriented: a line that already sets the key is replaced,
// and every other line, comment included, is copied through byte for byte.
// That is the point. These files are written by hand and read by people, so a
// settings change must not reformat the file around it.
func Set(path, section, key, value string) error {
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := strings.Split(string(body), "\n")
	// A file written by Set ends with a newline, which Split leaves as a
	// trailing empty element. There is no line there to replace.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	rewritten, err := setLine(lines, "["+section+"]", key, value)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(rewritten, "\n")+"\n"), 0o644)
}

// setLine sets key to value in the block opened by header, creating that block
// at the end of the file if it is absent. A replaced key keeps the spacing the
// line already had, so a column-aligned table stays aligned. It returns the new
// lines.
func setLine(lines []string, header, key, value string) ([]string, error) {
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}

	if start < 0 {
		// No such section. Add one rather than guess where the user meant
		// it to go; a config with an extra section still loads.
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		return append(out, header, key+" = "+value), nil
	}

	// The block runs to the next section header.
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			end = i
			break
		}
	}

	for i := start + 1; i < end; i++ {
		if left, ok := keyOf(lines[i]); ok && left == key {
			out := append([]string{}, lines...)
			eq := strings.Index(lines[i], "=")
			rewritten := strings.TrimRight(lines[i][:eq+1], " \t") + " " + value
			// A comment trailing the value belongs to the line the user
			// wrote, not to the value pib is replacing.
			if hash := strings.Index(lines[i][eq+1:], "#"); hash >= 0 {
				rewritten += "  " + strings.TrimSpace(lines[i][eq+1+hash:])
			}
			out[i] = rewritten
			return out, nil
		}
	}

	// Append after the section's last non-blank line, so the new key lands
	// inside the block and the blank line that ends it stays at the end.
	insert := end
	for insert > start+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insert]...)
	out = append(out, key+" = "+value)
	return append(out, lines[insert:]...), nil
}

// keyOf returns the key a line sets, if it sets one. A comment sets nothing.
func keyOf(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	eq := strings.Index(trimmed, "=")
	if eq < 0 {
		return "", false
	}
	name := strings.Trim(strings.TrimSpace(trimmed[:eq]), `"'`)
	return name, name != ""
}

// Sources reports, for every key the config understands, which file supplies
// it. The value is "global", "workspace", or "default" when neither file
// mentions the key and the built-in applies.
//
// It exists so the settings screen can show where a value came from without
// the user having to know which of the two files to open.
func Sources(global, workspace string) (map[string]string, error) {
	keys := []string{"plan.review", "plan.isolate", "review.cycles"}

	out := make(map[string]string, len(keys))
	for _, path := range []struct {
		name string
		path string
	}{{"global", global}, {"workspace", workspace}} {
		if path.path == "" {
			continue
		}
		f, found, err := read(path.path)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if f.Plan.Review != nil {
			out["plan.review"] = path.name
		}
		if f.Plan.Isolate != nil {
			out["plan.isolate"] = path.name
		}
		if f.Review.Cycles != nil {
			out["review.cycles"] = path.name
		}
	}

	for _, key := range keys {
		if _, ok := out[key]; !ok {
			out[key] = "default"
		}
	}

	return out, nil
}
