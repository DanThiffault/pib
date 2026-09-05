package issues

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestRoundTrip(t *testing.T) {
	want := File{
		Title:      "Implement Order Aggregate",
		Type:       "task",
		Acceptance: []string{"Handles the PlaceOrder command", "Emits OrderPlaced"},
		Extra: []Field{
			{Key: "owner", Value: "dan"},
			{Key: "tags", List: []string{"orders", "domain"}},
		},
		Body: "## Task\n\nImplement the order aggregate.",
		Comments: []Comment{
			{Author: "reviewer", At: at("2026-08-29T14:02:11Z"), Body: "NEEDS CHANGES — validate first."},
			{Author: "dan", At: at("2026-08-29T15:00:00Z"), Body: "Fixed in the follow-up."},
		},
	}

	got, err := Parse(want.Render())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the file:\n got %#v\nwant %#v\n\nrendered:\n%s", got, want, want.Render())
	}
}

func TestRenderIsStable(t *testing.T) {
	f := File{Title: "A task", Type: "task", Acceptance: []string{"It works"}, Body: "Do the thing."}

	once := f.Render()
	parsed, err := Parse(once)
	if err != nil {
		t.Fatal(err)
	}
	if twice := parsed.Render(); twice != once {
		t.Errorf("rendering is not idempotent:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
}

func TestBodyKeepsHorizontalRules(t *testing.T) {
	src := "---\ntitle: Ruled\ntype: task\n---\n\nIntro\n\n---\n\nMore prose\n"

	f, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Title != "Ruled" {
		t.Errorf("title = %q", f.Title)
	}
	if want := "Intro\n\n---\n\nMore prose"; f.Body != want {
		t.Errorf("body = %q, want %q", f.Body, want)
	}
}

func TestParseToleratesAHandEditedFile(t *testing.T) {
	src := "---\n" +
		"title:   Spaced out\n" +
		"\n" +
		"type: research\n" +
		"acceptance:\n" +
		"    - first\n" +
		"    - second\n" +
		"owner: dan\n" +
		"---\n" +
		"Body with no blank line above it."

	f, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Title != "Spaced out" || f.Type != "research" {
		t.Errorf("title = %q, type = %q", f.Title, f.Type)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(f.Acceptance, want) {
		t.Errorf("acceptance = %v, want %v", f.Acceptance, want)
	}
	if len(f.Extra) != 1 || f.Extra[0].Key != "owner" || f.Extra[0].Value != "dan" {
		t.Errorf("extra = %v, want owner: dan preserved", f.Extra)
	}
	if f.Body != "Body with no blank line above it." {
		t.Errorf("body = %q", f.Body)
	}
}

func TestUnknownKeysSurviveARewrite(t *testing.T) {
	src := "---\ntitle: Keep me\ntype: task\nowner: dan\nreviewers:\n  - ana\n  - bo\n---\n\nBody\n"

	f, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	f.Title = "Renamed"

	out := f.Render()
	for _, want := range []string{"owner: dan", "reviewers:", "  - ana", "  - bo", "title: Renamed"} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten file lost %q:\n%s", want, out)
		}
	}
}

func TestQuotingSurvivesAwkwardValues(t *testing.T) {
	want := File{
		Title:      `  padded "quoted" title`,
		Type:       "task",
		Acceptance: []string{"- looks like a list item", "line one\nline two"},
		Body:       "Body",
	}

	got, err := Parse(want.Render())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Title != want.Title {
		t.Errorf("title = %q, want %q", got.Title, want.Title)
	}
	if !reflect.DeepEqual(got.Acceptance, want.Acceptance) {
		t.Errorf("acceptance = %q, want %q", got.Acceptance, want.Acceptance)
	}
}

func TestParseRejectsStructuralDamage(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":         "# Just a heading\n",
		"unterminated":           "---\ntitle: Orphan\ntype: task\n",
		"empty frontmatter":      "---\n",
		"list item with no key":  "---\n- stray\ntitle: x\n---\n",
		"list item after scalar": "---\ntitle: x\n- stray\n---\n",
		"no colon":               "---\ntitle x\n---\n",
	}

	for name, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestValidateChecksTheRequiredFields(t *testing.T) {
	if err := (File{Title: "T", Type: "task"}).Validate(); err != nil {
		t.Errorf("valid file rejected: %v", err)
	}
	if err := (File{Type: "task"}).Validate(); err == nil {
		t.Error("a file with no title passed validation")
	}
	if err := (File{Title: "T"}).Validate(); err == nil {
		t.Error("a file with no type passed validation")
	}
	// Parse itself stays lenient, so a half-edited file can still be read.
	if _, err := Parse("---\ntype: task\n---\n"); err != nil {
		t.Errorf("Parse rejected a file with no title: %v", err)
	}
}

func TestCommentBodiesKeepTheirHeadings(t *testing.T) {
	src := "---\ntitle: T\ntype: task\n---\n\nBody\n\n" + CommentMarker + "\n\n" +
		"### reviewer · 2026-08-29T14:02:11Z\n\n" +
		"### Findings\n\nSomething is wrong.\n\n### coder · not-a-timestamp\n\nStill the same comment.\n"

	f, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Comments) != 1 {
		t.Fatalf("got %d comments, want 1: %#v", len(f.Comments), f.Comments)
	}
	for _, want := range []string{"### Findings", "### coder · not-a-timestamp", "Something is wrong."} {
		if !strings.Contains(f.Comments[0].Body, want) {
			t.Errorf("comment body lost %q:\n%s", want, f.Comments[0].Body)
		}
	}
}

func TestAppendCommentAddsTheMarkerOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName(7, "Implement Order Aggregate"))

	if err := WriteFile(path, File{Title: "Implement Order Aggregate", Type: "task", Body: "## Task\n\nDo it."}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	first := Comment{Author: "reviewer", At: at("2026-08-29T14:02:11Z"), Body: "NEEDS CHANGES"}
	second := Comment{Author: "dan", At: at("2026-08-29T15:00:00Z"), Body: "Fixed."}
	for _, c := range []Comment{first, second} {
		if err := AppendComment(path, c); err != nil {
			t.Fatalf("AppendComment: %v", err)
		}
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), CommentMarker); n != 1 {
		t.Errorf("marker appears %d times, want 1:\n%s", n, body)
	}

	f, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !reflect.DeepEqual(f.Comments, []Comment{first, second}) {
		t.Errorf("comments = %#v", f.Comments)
	}
	// Appending must not disturb what the planner wrote.
	if f.Body != "## Task\n\nDo it." {
		t.Errorf("body = %q", f.Body)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Implement Order Aggregate": "implement-order-aggregate",
		"  Spaces  & Symbols!!  ":   "spaces-symbols",
		"Feature: order placement":  "feature-order-placement",
		"Café Ordering":             "café-ordering",
		"日本語のタイトル":                  "日本語のタイトル",
		"":                          "issue",
		"!!!":                       "issue",
		"already-slugged":           "already-slugged",
	}

	for title, want := range cases {
		if got := Slug(title); got != want {
			t.Errorf("Slug(%q) = %q, want %q", title, got, want)
		}
	}

	long := Slug(strings.Repeat("word ", 40))
	if runes := []rune(long); len(runes) > maxSlug {
		t.Errorf("Slug capped at %d runes, got %d", maxSlug, len(runes))
	}
	if strings.HasSuffix(long, "-") {
		t.Errorf("truncated slug ends in a dash: %q", long)
	}
}

func TestFileName(t *testing.T) {
	if got, want := FileName(7, "Implement Order Aggregate"), "7-implement-order-aggregate.md"; got != want {
		t.Errorf("FileName = %q, want %q", got, want)
	}
}

func TestWriteFileReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1-a.md")

	if err := WriteFile(path, File{Title: "A", Type: "task", Body: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, File{Title: "A", Type: "task", Body: "second"}); err != nil {
		t.Fatal(err)
	}

	f, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Body != "second" {
		t.Errorf("body = %q, want second", f.Body)
	}

	// The temporary file must not be left behind next to the issue.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory holds %v, want just the issue file", names)
	}
}
