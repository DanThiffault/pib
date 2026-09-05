package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// homeAt points Dir at a temporary home for the length of a test.
func homeAt(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".pib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFreshInstallIsNotOutdated(t *testing.T) {
	homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}

	stale, err := Outdated()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("just-installed agents reported outdated: %v", stale)
	}
}

// An agent pib has never written is not outdated — installing is what handles
// those, and reporting them here would offer to "update" a file that is missing.
func TestMissingAgentIsNotOutdated(t *testing.T) {
	homeAt(t)

	stale, err := Outdated()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("uninstalled agents reported outdated: %v", stale)
	}
}

// A default that is new in the binary but absent on an existing installation
// is reported as missing, not outdated.
func TestMissingAgentIsReported(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "reviewer.md")); err != nil {
		t.Fatal(err)
	}

	missing, err := Missing()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "reviewer" {
		t.Errorf("missing = %v, want [reviewer]", missing)
	}

	stale, err := Outdated()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("outdated = %v, want none", stale)
	}
}

// A default that is present but edited is outdated, not missing.
func TestEditedAgentIsOutdatedNotMissing(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	missing, err := Missing()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}

	stale, err := Outdated()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "reviewer" {
		t.Errorf("outdated = %v, want [reviewer]", stale)
	}
}

// InstallDefaults on an existing installation writes only the files that
// are missing, leaving everything else untouched.
func TestInstallDefaultsFillsMissing(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "reviewer.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worker.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := InstallDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != "reviewer" {
		t.Errorf("written = %v, want [reviewer]", written)
	}

	got, err := os.ReadFile(filepath.Join(dir, "worker.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "edited\n" {
		t.Error("an edited agent was overwritten")
	}

	want, err := defaultBody("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	now, err := os.ReadFile(filepath.Join(dir, "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(now) != string(want) {
		t.Error("reviewer.md was not restored with the built-in definition")
	}
}

func TestChangedAgentIsOutdated(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stale, err := Outdated()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "reviewer" {
		t.Errorf("outdated = %v, want [reviewer]", stale)
	}
}

// pib cannot tell a stale default from a deliberate edit, so an update must
// never be the only copy of what was there.
func TestUpdateKeepsWhatItReplaced(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	mine := []byte("# my own reviewer\n")
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), mine, 0o644); err != nil {
		t.Fatal(err)
	}

	backup, written, err := UpdateDefaults([]string{"reviewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != "reviewer" {
		t.Errorf("written = %v, want [reviewer]", written)
	}

	saved, err := os.ReadFile(filepath.Join(backup, "reviewer.md"))
	if err != nil {
		t.Fatalf("the replaced definition was not saved: %v", err)
	}
	if string(saved) != string(mine) {
		t.Errorf("backup = %q, want the file that was replaced", saved)
	}

	now, err := os.ReadFile(filepath.Join(dir, "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := defaultBody("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if string(now) != string(want) {
		t.Error("reviewer.md was not replaced with the built-in definition")
	}
}

// Updating only what was named leaves every other definition alone, including
// ones the user has edited on purpose.
func TestUpdateTouchesOnlyTheAgentsNamed(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	mine := []byte("# my own coder\n")
	if err := os.WriteFile(filepath.Join(dir, "coder.md"), mine, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := UpdateDefaults([]string{"reviewer"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "coder.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(mine) {
		t.Error("an agent that was not named was overwritten")
	}
}

func TestUpdateWithNothingToDoWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	backup, written, err := UpdateDefaults(nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" || written != nil {
		t.Errorf("backup = %q, written = %v, want neither", backup, written)
	}
	if _, err := os.Stat(filepath.Join(home, ".pib", BackupDir)); !os.IsNotExist(err) {
		t.Error("an empty update created a backup directory")
	}
}

// Two updates in the same run group together; separate runs must not collide.
func TestEachUpdateGetsItsOwnBackup(t *testing.T) {
	dir := homeAt(t)
	if _, err := InstallDefaults(); err != nil {
		t.Fatal(err)
	}

	first := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("one\n"), 0o644)
	backupA, _, err := UpdateDefaults([]string{"reviewer"}, first)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("two\n"), 0o644)
	backupB, _, err := UpdateDefaults([]string{"reviewer"}, second)
	if err != nil {
		t.Fatal(err)
	}

	if backupA == backupB {
		t.Fatal("the second update reused the first one's backup directory")
	}
	a, _ := os.ReadFile(filepath.Join(backupA, "reviewer.md"))
	if string(a) != "one\n" {
		t.Errorf("the first backup was overwritten: %q", a)
	}
}
