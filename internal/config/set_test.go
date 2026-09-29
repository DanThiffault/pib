package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSetRewritesKeyAndKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "# a comment\n[types]\n\ntask = \"coder\"   # inline\nreviewer = \"code-reviewer\"\n\n[plan]\nreview = true\n")

	if err := Set(path, "types", "task", `"builder"`); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := readBody(t, path)
	want := "# a comment\n[types]\n\ntask = \"builder\"  # inline\nreviewer = \"code-reviewer\"\n\n[plan]\nreview = true\n"
	if got != want {
		t.Errorf("Set produced\n%q\nwant\n%q", got, want)
	}
}

func TestSetAddsMissingKeyToExistingSection(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[types]\ntask = \"coder\"\n\n[plan]\nreview = true\n")

	if err := Set(path, "plan", "isolate", "false"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	want := "[types]\ntask = \"coder\"\n\n[plan]\nreview = true\nisolate = false\n"
	if got := readBody(t, path); got != want {
		t.Errorf("Set produced\n%q\nwant\n%q", got, want)
	}
}

func TestSetCreatesFileAndSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", FileName)

	if err := Set(path, "review", "cycles", "5"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got, want := readBody(t, path), "[review]\ncycles = 5\n"; got != want {
		t.Errorf("Set produced %q, want %q", got, want)
	}

	cfg, err := LoadPaths(filepath.Join(filepath.Dir(path), "none.toml"), path)
	if err != nil {
		t.Fatalf("LoadPaths: %v", err)
	}
	if got := cfg.ReviewCycles(); got != 5 {
		t.Errorf("ReviewCycles() = %d, want 5", got)
	}
}

func TestSetOnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Set(path, "types", "task", `"coder"`); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := readBody(t, path), "[types]\ntask = \"coder\"\n"; got != want {
		t.Errorf("Set produced %q, want %q", got, want)
	}
}

func TestSetHandlesHeaderCommentAndInnerSpaces(t *testing.T) {
	for _, header := range []string{"[review]", "[review] # depth", "[ review ]", "[review]\t# depth"} {
		t.Run(header, func(t *testing.T) {
			dir := t.TempDir()
			path := write(t, dir, header+"\ncycles = 3\n")

			if err := Set(path, "review", "cycles", "2"); err != nil {
				t.Fatalf("Set: %v", err)
			}

			got := readBody(t, path)
			want := header + "\ncycles = 2\n"
			if got != want {
				t.Errorf("Set produced\n%q\nwant\n%q", got, want)
			}
			// A duplicated table would make the file unparseable, which
			// would break pib itself, not just this test.
			if _, err := LoadPaths(filepath.Join(dir, "none.toml"), path); err != nil {
				t.Errorf("rewritten config no longer loads: %v", err)
			}
		})
	}
}

func TestSetIgnoresHashInsideQuotedValue(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "[types]\nreviewer = \"#1\" # who reviews\n")

	if err := Set(path, "types", "reviewer", `"plan-reviewer"`); err != nil {
		t.Fatalf("Set: %v", err)
	}

	want := "[types]\nreviewer = \"plan-reviewer\"  # who reviews\n"
	if got := readBody(t, path); got != want {
		t.Errorf("Set produced %q, want %q", got, want)
	}
}

func TestTemplateRoundTripsByteIdentically(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(Template), 0o644); err != nil {
		t.Fatal(err)
	}

	// Each key is set to the value the template already carries, so the file
	// must come back byte for byte. The template's comments — including the
	// [review] cycles comment that sits above [plan] — are locked in here.
	for _, c := range []struct{ section, key, value string }{
		{"types", "task", `"coder"`},
		{"plan", "review", "true"},
		{"plan", "isolate", "true"},
		{"review", "cycles", "3"},
	} {
		if err := Set(path, c.section, c.key, c.value); err != nil {
			t.Fatalf("Set %s.%s: %v", c.section, c.key, err)
		}
	}

	if got := readBody(t, path); got != Template {
		t.Errorf("round trip changed the template:\n%q", got)
	}
}

func readBody(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestSourcesReportsWhereEachKeyCameFrom(t *testing.T) {
	dir := t.TempDir()
	global := write(t, dir, "[types]\ntask = \"builder\"\nchore = \"\"\n[plan]\nreview = true\n[review]\ncycles = 3\n")
	workspace := filepath.Join(dir, "ws.toml")
	if err := os.WriteFile(workspace, []byte("[types]\ntask = \"ws-coder\"\nresearch = \"scout\"\n[review]\ncycles = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Sources(global, workspace)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	want := map[string]string{
		"plan.review":   "global",
		"plan.isolate":  "default",
		"review.cycles": "workspace",
		// The global file is authoritative for the type map once it
		// exists, so the workspace does not get to reroute task.
		"types.task":          "global",
		"types.chore":         "global",
		"types.research":      "global",
		"types.feature":       "global",
		"types.prototype":     "global",
		"types.reviewer":      "global",
		"types.code-reviewer": "global",
		"types.plan-reviewer": "global",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sources() =\n%v\nwant\n%v", got, want)
	}
}

func TestSourcesWithNoGlobalFile(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "ws.toml")
	if err := os.WriteFile(workspace, []byte("[types]\ntask = \"ws-coder\"\n[plan]\nisolate = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Sources(filepath.Join(dir, "missing.toml"), workspace)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	want := map[string]string{
		"plan.review":   "default",
		"plan.isolate":  "workspace",
		"review.cycles": "default",
		// With no global file the built-in map applies, so a type the
		// workspace reroutes is the workspace's and the rest are defaults.
		"types.task":          "workspace",
		"types.research":      "default",
		"types.feature":       "default",
		"types.prototype":     "default",
		"types.reviewer":      "default",
		"types.code-reviewer": "default",
		"types.plan-reviewer": "default",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sources() =\n%v\nwant\n%v", got, want)
	}
}
