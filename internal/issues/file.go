package issues

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// CommentMarker separates what the planner authored from the comments agents
// and people append afterwards. Everything below it is activity.
const CommentMarker = "<!-- pib:comments -->"

// maxSlug caps the generated part of a filename. The database's path column
// is authoritative, so a truncated slug costs nothing but readability.
const maxSlug = 60

// File is an issue's markdown file: frontmatter pib understands, the body the
// planner wrote, and the comments appended below the marker.
type File struct {
	Title      string
	Type       string
	Acceptance []string
	// Extra holds frontmatter keys pib does not recognise. They are kept in
	// order and written back untouched, so hand-added keys survive an edit.
	Extra    []Field
	Body     string
	Comments []Comment
}

// Field is one frontmatter entry. A field is a list when List is non-nil.
type Field struct {
	Key   string
	Value string
	List  []string
}

// Comment is one entry in an issue's activity.
type Comment struct {
	Author string    `json:"author"`
	At     time.Time `json:"at"`
	Body   string    `json:"body"`
}

// commentHead matches the heading that opens a comment. The timestamp has to
// parse for the line to count, so a "### Findings" inside a comment body is
// just text.
var commentHead = regexp.MustCompile(`^###\s+(.+?)\s+·\s+(\S+)\s*$`)

// Slug turns a title into the readable half of a filename. It is generated
// once, at creation, and deliberately not kept in step with a later rename.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteRune('-')
			dash = true
		}
	}

	slug := strings.Trim(b.String(), "-")
	if runes := []rune(slug); len(runes) > maxSlug {
		slug = strings.Trim(string(runes[:maxSlug]), "-")
	}
	if slug == "" {
		return "issue"
	}
	return slug
}

// FileName is the name of an issue's markdown file.
func FileName(number int64, title string) string {
	return fmt.Sprintf("%d-%s.md", number, Slug(title))
}

// Validate reports whether the file carries the fields an issue needs. Parse
// stays lenient so a half-edited file can still be read and reported on.
func (f File) Validate() error {
	if strings.TrimSpace(f.Title) == "" {
		return errors.New("frontmatter has no title")
	}
	if strings.TrimSpace(f.Type) == "" {
		return errors.New("frontmatter has no type")
	}
	return nil
}

// ReadFile parses the issue file at path.
func ReadFile(path string) (File, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	f, err := Parse(string(body))
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// WriteFile renders the file and replaces path with it.
func WriteFile(path string, f File) error {
	return writeAtomic(path, []byte(f.Render()))
}

// AppendComment adds one comment to the end of an issue file, creating the
// marker if the file does not have one yet. It appends rather than
// re-rendering, so nothing above the marker is touched.
func AppendComment(path string, c Comment) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString(strings.TrimRight(string(body), "\n"))
	if !strings.Contains(string(body), CommentMarker) {
		b.WriteString("\n\n")
		b.WriteString(CommentMarker)
	}
	b.WriteString("\n\n")
	b.WriteString(c.render())

	return writeAtomic(path, []byte(b.String()))
}

// Parse reads an issue file. It fails only on structural damage — missing or
// unterminated frontmatter, a list item with no key — never on a missing
// field, which Validate reports instead.
func Parse(src string) (File, error) {
	src = strings.ReplaceAll(strings.TrimPrefix(src, "\ufeff"), "\r\n", "\n")

	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		return File{}, errors.New("missing frontmatter")
	}

	fields, body, err := parseFrontmatter(rest)
	if err != nil {
		return File{}, err
	}

	var f File
	for _, field := range fields {
		switch field.Key {
		case "title":
			f.Title = field.Value
		case "type":
			f.Type = field.Value
		case "acceptance":
			f.Acceptance = field.List
		default:
			f.Extra = append(f.Extra, field)
		}
	}

	prose, comments, found := strings.Cut(body, CommentMarker)
	f.Body = strings.Trim(prose, "\n")
	if found {
		f.Comments = parseComments(comments)
	}

	return f, nil
}

// parseFrontmatter consumes up to the closing fence and returns the fields
// and whatever followed. Only the first bare "---" closes it, so a horizontal
// rule in the body is not mistaken for a fence.
func parseFrontmatter(src string) ([]Field, string, error) {
	var fields []Field

	for {
		line, rest, found := strings.Cut(src, "\n")
		if !found && line == "" {
			return nil, "", errors.New("unterminated frontmatter")
		}
		src = rest

		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "---":
			return fields, src, nil
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "- "):
			if len(fields) == 0 || fields[len(fields)-1].Value != "" {
				return nil, "", fmt.Errorf("list item with no key: %q", trimmed)
			}
			item, err := unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			if err != nil {
				return nil, "", err
			}
			last := &fields[len(fields)-1]
			last.List = append(last.List, item)
		default:
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				return nil, "", fmt.Errorf("malformed frontmatter line: %q", trimmed)
			}
			parsed, err := unquote(strings.TrimSpace(value))
			if err != nil {
				return nil, "", err
			}
			fields = append(fields, Field{Key: strings.TrimSpace(key), Value: parsed})
		}

		if !found {
			return nil, "", errors.New("unterminated frontmatter")
		}
	}
}

// parseComments splits the activity section into comments. Text before the
// first heading is dropped: there is nothing it could belong to.
func parseComments(src string) []Comment {
	var comments []Comment
	var current *Comment
	var body strings.Builder

	flush := func() {
		if current == nil {
			return
		}
		current.Body = strings.Trim(body.String(), "\n")
		comments = append(comments, *current)
		body.Reset()
	}

	for _, line := range strings.Split(src, "\n") {
		if m := commentHead.FindStringSubmatch(line); m != nil {
			if at, err := time.Parse(time.RFC3339, m[2]); err == nil {
				flush()
				current = &Comment{Author: m[1], At: at}
				continue
			}
		}
		if current != nil {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}
	flush()

	return comments
}

// Render writes the file back out. The marker is emitted only when there are
// comments, so an empty activity section is normalised away.
func (f File) Render() string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString(renderField(Field{Key: "title", Value: f.Title}))
	b.WriteString(renderField(Field{Key: "type", Value: f.Type}))
	if len(f.Acceptance) > 0 {
		b.WriteString(renderField(Field{Key: "acceptance", List: f.Acceptance}))
	}
	for _, field := range f.Extra {
		b.WriteString(renderField(field))
	}
	b.WriteString("---\n")

	if body := strings.Trim(f.Body, "\n"); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}

	if len(f.Comments) > 0 {
		b.WriteString("\n")
		b.WriteString(CommentMarker)
		b.WriteString("\n")
		for _, c := range f.Comments {
			b.WriteString("\n")
			b.WriteString(c.render())
		}
	}

	return b.String()
}

func (c Comment) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s · %s\n", c.Author, c.At.UTC().Format(time.RFC3339))
	if body := strings.Trim(c.Body, "\n"); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func renderField(f Field) string {
	if f.List != nil {
		var b strings.Builder
		b.WriteString(f.Key + ":\n")
		for _, item := range f.List {
			b.WriteString("  - " + quote(item) + "\n")
		}
		return b.String()
	}
	return f.Key + ": " + quote(f.Value) + "\n"
}

// quote protects a value whose plain form would not survive a round trip.
func quote(value string) string {
	if value == "" {
		return value
	}
	if value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, "\n\"") ||
		strings.HasPrefix(value, "- ") {
		return strconv.Quote(value)
	}
	return value
}

func unquote(value string) (string, error) {
	if len(value) < 2 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
		return value, nil
	}
	out, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("malformed quoted value %s: %w", value, err)
	}
	return out, nil
}

// writeAtomic replaces a file in one step, so a crash mid-write cannot leave
// an issue truncated.
func writeAtomic(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
