package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	// macOS temp dirs are symlinked through /private; resolve so the path
	// matches what git reports as the top level.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	return dir
}

func TestDetectMissing(t *testing.T) {
	root := newRepo(t)

	s, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if s.GitRoot != root {
		t.Errorf("GitRoot = %q, want %q", s.GitRoot, root)
	}
	if s.Exists {
		t.Error("Exists = true, want false")
	}
}

func TestCreate(t *testing.T) {
	root := newRepo(t)

	s, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, DirName)); err != nil || !fi.IsDir() {
		t.Fatalf("workspace dir not created: %v", err)
	}

	again, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if !again.Exists {
		t.Error("after Create: Exists = false, want true")
	}
}

func TestDetectOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	// A temp dir may still sit under a repo on some machines; only assert
	// when git agrees we are outside one.
	if err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Run(); err == nil {
		t.Skip("temp dir is inside a git repository")
	}

	if _, err := Detect(); err != ErrNotRepo {
		t.Errorf("err = %v, want ErrNotRepo", err)
	}
}
