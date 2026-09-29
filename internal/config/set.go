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
		if normalizeHeader(line) == header {
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
		if isHeader(lines[i]) {
			end = i
			break
		}
	}

	for i := start + 1; i < end; i++ {
		entry, ok := keyOf(lines[i])
		if !ok || entry.key != key {
			continue
		}
		out := append([]string{}, lines...)
		rewritten := strings.TrimRight(lines[i][:entry.eq+1], " \t") + " " + value
		// A comment trailing the value belongs to the line the user wrote,
		// not to the value pib is replacing.
		if entry.comment != "" {
			rewritten += "  " + entry.comment
		}
		out[i] = rewritten
		return out, nil
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

// entry is one `key = value` line, split so a rewrite can put the value back
// without disturbing the rest of the line.
type entry struct {
	key     string
	comment string
	// eq is the index of the "=" in the line, so the key's original spacing
	// can be kept.
	eq int
}

// keyOf parses one line of a TOML block. A blank line, a comment, or a line
// with no "=" sets no key.
func keyOf(line string) (entry, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return entry{}, false
	}
	// The "=" that ends the key is the first one outside a quoted string, so
	// a key or value containing "=" is not cut in half.
	eq := indexOutsideQuotes(trimmed, "=")
	if eq < 0 {
		return entry{}, false
	}
	// Trimming does not move the "=": only leading space is dropped, and the
	// "=" sits after it.
	eq += len(line) - len(trimmed)

	key := strings.Trim(strings.TrimSpace(line[:eq]), `"'`)
	if key == "" {
		return entry{}, false
	}

	_, comment := splitComment(line[eq+1:])
	return entry{key: key, comment: comment, eq: eq}, true
}

// normalizeHeader returns the table name a line opens, or "" if it opens none.
// A hand-written header may carry a trailing comment and spaces inside the
// brackets — `[review] # depth` and `[ review ]` are the same table as
// `[review]`, and treating them as different would give the file two of them.
func normalizeHeader(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, "[") {
		return ""
	}
	if _, comment := splitComment(trimmed); comment != "" {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, comment))
	}
	inner := strings.TrimSpace(trimmed)
	if !strings.HasPrefix(inner, "[") || !strings.HasSuffix(inner, "]") {
		return ""
	}
	return "[" + strings.TrimSpace(inner[1:len(inner)-1]) + "]"
}

// isHeader reports whether a line opens a new table, ending the block above
// it.
func isHeader(line string) bool {
	return normalizeHeader(line) != ""
}

// splitComment separates a trailing comment from a value. A "#" inside a
// quoted string is part of the value, not the start of a comment.
func splitComment(value string) (string, string) {
	hash := indexOutsideQuotes(value, "#")
	if hash < 0 {
		return value, ""
	}
	return value[:hash], strings.TrimSpace(value[hash:])
}

// indexOutsideQuotes returns the index of the first byte of sub outside any
// quoted string, or -1.
func indexOutsideQuotes(s, sub string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == sub[0]:
			return i
		}
	}
	return -1
}

// Sources reports, for every key the config understands, which file supplies
// it. The value is "global", "workspace", or "default" when neither file
// mentions the key and the built-in applies.
//
// Keys are named "section.key": "types.task" for the type map, plus
// "plan.review", "plan.isolate" and "review.cycles". Every type pib knows
// about gets an entry, so the settings screen can show a source on every row
// instead of leaving the TYPES rows blank.
//
// A type's source is whichever file names it, the workspace first, exactly as
// LoadPaths merges the two maps. "default" means the built-in map supplied it,
// which happens only while there is no global file — a global file replaces
// the built-in map outright. "unmapped" means neither file names it and there
// is no built-in left to supply it, so the type has no agent at all.
func Sources(global, workspace string) (map[string]string, error) {
	base, hasGlobal, err := read(global)
	if err != nil {
		return nil, err
	}
	over, hasWorkspace, err := read(workspace)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}

	if hasGlobal {
		if base.Plan.Review != nil {
			out["plan.review"] = "global"
		}
		if base.Plan.Isolate != nil {
			out["plan.isolate"] = "global"
		}
		if base.Review.Cycles != nil {
			out["review.cycles"] = "global"
		}
	}
	if hasWorkspace {
		if over.Plan.Review != nil {
			out["plan.review"] = "workspace"
		}
		if over.Plan.Isolate != nil {
			out["plan.isolate"] = "workspace"
		}
		if over.Review.Cycles != nil {
			out["review.cycles"] = "workspace"
		}
	}
	for _, key := range []string{"plan.review", "plan.isolate", "review.cycles"} {
		if _, ok := out[key]; !ok {
			out[key] = "default"
		}
	}

	// Every type named anywhere, plus every type pib ships, so a row in the
	// settings screen always has something to show. An entry counts as named
	// even when its value is empty: `feature = ""` is how a container type is
	// declared, not an absent key.
	names := map[string]bool{}
	for name := range defaults() {
		names[name] = true
	}
	for name := range base.Types {
		names[name] = true
	}
	for name := range over.Types {
		names[name] = true
	}
	for name := range names {
		_, inWorkspace := over.Types[name]
		_, inGlobal := base.Types[name]
		switch {
		case hasWorkspace && inWorkspace:
			out["types."+name] = "workspace"
		case hasGlobal && inGlobal:
			out["types."+name] = "global"
		case !hasGlobal:
			// No global file, so the built-in map is in play and this
			// type came from it.
			out["types."+name] = "default"
		default:
			out["types."+name] = "unmapped"
		}
	}

	return out, nil
}
