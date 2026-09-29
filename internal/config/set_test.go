package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	global := write(t, dir, "[types]\ntask = \"builder\"\n[plan]\nreview = true\n[review]\ncycles = 3\n")
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
		// The workspace overrides the global file key by key, the way
		// LoadPaths merges the two maps, so it wins for task too.
		"types.task":     "workspace",
		"types.research": "workspace",
		// This global file replaces the built-in map and names only task,
		// so the rest of the built-ins are not in play at all.
		"types.feature":       "unmapped",
		"types.prototype":     "unmapped",
		"types.reviewer":      "unmapped",
		"types.code-reviewer": "unmapped",
		"types.plan-reviewer": "unmapped",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sources() =\n%v\nwant\n%v", got, want)
	}
}

func TestSourcesSaysUnmappedWhenAGlobalFileReplacesTheDefaults(t *testing.T) {
	dir := t.TempDir()
	// A global file replaces the built-in map outright, so a type it does
	// not name has no agent left, and the workspace cannot bring the
	// built-in back either.
	global := write(t, dir, "[types]\ntask = \"builder\"\n")

	got, err := Sources(global, filepath.Join(dir, "missing.toml"))
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	want := map[string]string{
		"plan.review":         "default",
		"plan.isolate":        "default",
		"review.cycles":       "default",
		"types.task":          "global",
		"types.research":      "unmapped",
		"types.feature":       "unmapped",
		"types.prototype":     "unmapped",
		"types.reviewer":      "unmapped",
		"types.code-reviewer": "unmapped",
		"types.plan-reviewer": "unmapped",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sources() =\n%v\nwant\n%v", got, want)
	}
}

func TestSourcesTreatsAnEmptyValueAsNamed(t *testing.T) {
	dir := t.TempDir()
	// `feature = ""` declares a container type; it is not an absent key.
	global := write(t, dir, "[types]\nfeature = \"\"\n")

	got, err := Sources(global, "")
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if got["types.feature"] != "global" {
		t.Errorf("types.feature = %q, want global: an empty value still names the type", got["types.feature"])
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
		"plan.review":         "default",
		"plan.isolate":        "workspace",
		"review.cycles":       "default",
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

// TestSourcesAgreesWithLoadPaths pins the precedence Sources reports to the
// precedence LoadPaths applies: a type Sources calls "unmapped" must be one
// AgentFor cannot resolve, and every other type must resolve.
func TestSourcesAgreesWithLoadPaths(t *testing.T) {
	cases := []struct{ global, workspace string }{
		{"[types]\ntask = \"builder\"\n", ""},
		{"[types]\n", "[types]\ntask = \"ws-coder\"\n"},
		{"", "[types]\nresearch = \"scout\"\nfeature = \"\"\n"},
		{"[types]\nchore = \"\"\n", "[types]\nchore = \"janitor\"\n"},
		{"", ""},
		{"[plan]\nreview = false\n[review]\ncycles = 1\n", "[plan]\nreview = true\n"},
	}

	for _, c := range cases {
		dir := t.TempDir()
		global := filepath.Join(dir, "global.toml")
		workspace := filepath.Join(dir, "ws.toml")
		if err := os.WriteFile(global, []byte(c.global), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(workspace, []byte(c.workspace), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadPaths(global, workspace)
		if err != nil {
			t.Fatalf("LoadPaths: %v", err)
		}
		sources, err := Sources(global, workspace)
		if err != nil {
			t.Fatalf("Sources: %v", err)
		}

		for _, key := range sources {
			typ, ok := strings.CutPrefix(key, "types.")
			if !ok {
				continue
			}
			_, resolved := cfg.AgentFor(typ)
			if (sources[key] == "unmapped") == resolved {
				t.Errorf("global=%q workspace=%q: %s is %q but AgentFor(%q) resolved=%v",
					c.global, c.workspace, key, sources[key], typ, resolved)
			}
		}

		// The scalar knobs must resolve the way the sources say they do.
		if want := sources["plan.review"]; want != "default" && cfg.PlanReview() != (want == "workspace") {
			t.Errorf("plan.review from %s but PlanReview() = %v", want, cfg.PlanReview())
		}
		if want := sources["review.cycles"]; want != "default" && cfg.ReviewCycles() != 1 {
			t.Errorf("review.cycles from %s but ReviewCycles() = %d", want, cfg.ReviewCycles())
		}
	}
}
