package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func repo(t *testing.T) Manager {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.test"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	return Manager{GitRoot: dir, StateDir: filepath.Join(dir, ".pib")}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The whole point: a branch made in one issue's checkout is invisible in
// another's, so two agents branching at once cannot move each other's tree.
func TestBranchesInOneWorktreeDoNotMoveAnother(t *testing.T) {
	m := repo(t)

	first, err := m.For(11)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.For(12)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two issues were given the same checkout")
	}

	git(t, first, "checkout", "-b", "coder/11-one")
	git(t, second, "checkout", "-b", "coder/12-two")

	if got := git(t, first, "rev-parse", "--abbrev-ref", "HEAD"); got != "coder/11-one" {
		t.Errorf("#11's checkout is on %q, want coder/11-one", got)
	}
	if got := git(t, second, "rev-parse", "--abbrev-ref", "HEAD"); got != "coder/12-two" {
		t.Errorf("#12's checkout is on %q, want coder/12-two", got)
	}
	// And the repository the user is sitting in was not dragged along.
	if got := git(t, m.GitRoot, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the main checkout moved to %q", got)
	}
}

// A followup resumes an agent mid-work. It has to come back to the branch and
// the uncommitted files its earlier run left.
func TestSameIssueComesBackToTheSameCheckout(t *testing.T) {
	m := repo(t)

	first, err := m.For(11)
	if err != nil {
		t.Fatal(err)
	}
	git(t, first, "checkout", "-b", "coder/11-one")
	if err := os.WriteFile(filepath.Join(first, "wip.txt"), []byte("half done"), 0o644); err != nil {
		t.Fatal(err)
	}

	again, err := m.For(11)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatalf("followup got %s, want the earlier %s", again, first)
	}
	if got := git(t, again, "rev-parse", "--abbrev-ref", "HEAD"); got != "coder/11-one" {
		t.Errorf("followup landed on %q, not the branch its run created", got)
	}
	if _, err := os.Stat(filepath.Join(again, "wip.txt")); err != nil {
		t.Errorf("the earlier run's uncommitted work is gone: %v", err)
	}
}

// Removing the checkout must not take the work with it.
func TestRemoveKeepsTheBranch(t *testing.T) {
	m := repo(t)

	dir, err := m.For(11)
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "checkout", "-b", "coder/11-one")
	git(t, dir, "commit", "--allow-empty", "-m", "work")

	if err := m.Remove(11); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the checkout is still there: %v", err)
	}
	if branches := git(t, m.GitRoot, "branch", "--list", "coder/11-one"); branches == "" {
		t.Error("removing the checkout deleted the branch with the work on it")
	}

	// Removing twice, or one that was never made, is not an error.
	if err := m.Remove(11); err != nil {
		t.Errorf("second Remove: %v", err)
	}
	if err := m.Remove(99); err != nil {
		t.Errorf("Remove of an issue that never ran: %v", err)
	}
}

// An agent with no issue works where the user is, as it always has.
func TestNoIssueUsesTheRepositoryItself(t *testing.T) {
	m := repo(t)
	dir, err := m.For(0)
	if err != nil {
		t.Fatal(err)
	}
	if dir != m.GitRoot {
		t.Errorf("dir = %q, want the repository root %q", dir, m.GitRoot)
	}
}

// A directory left behind by a crash, or by someone deleting a worktree by
// hand, must not wedge the next run of that issue.
func TestWreckageIsReplacedRatherThanRefused(t *testing.T) {
	m := repo(t)

	dir, err := m.For(11)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Prune(); err != nil {
		t.Fatal(err)
	}

	again, err := m.For(11)
	if err != nil {
		t.Fatalf("a stale directory wedged the issue: %v", err)
	}
	if got := git(t, again, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Errorf("the replacement is on %q, want a detached head", got)
	}
}

func TestSweepRemovesFinishedIssuesOnly(t *testing.T) {
	m := repo(t)
	for _, issue := range []int64{11, 12, 13} {
		if _, err := m.For(issue); err != nil {
			t.Fatal(err)
		}
	}

	closed := map[int64]bool{11: true, 13: true}
	removed, err := m.Sweep(func(issue int64) bool { return closed[issue] })
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("removed %d checkouts, want 2", removed)
	}

	left, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0] != 12 {
		t.Errorf("left %v, want only the open issue #12", left)
	}
}

func TestListIgnoresWhatIsNotAnIssue(t *testing.T) {
	m := repo(t)
	if _, err := m.For(11); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(m.StateDir, DirName, "notanumber"), 0o755); err != nil {
		t.Fatal(err)
	}

	issues, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0] != 11 {
		t.Errorf("List() = %v, want just [11]", issues)
	}
}

func TestListOnAWorkspaceWithNoCheckouts(t *testing.T) {
	m := repo(t)
	issues, err := m.List()
	if err != nil {
		t.Errorf("List() before anything ran: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("List() = %v, want none", issues)
	}
}

// `pib plan start` launches every ready issue at once, so the first thing the
// checkouts have to survive is being asked for simultaneously.
func TestCheckoutsCanBeAskedForAtOnce(t *testing.T) {
	m := repo(t)

	var wg sync.WaitGroup
	dirs := make([]string, 8)
	errs := make([]error, 8)
	for i := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dirs[i], errs[i] = m.For(int64(i + 1))
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Errorf("issue %d: %v", i+1, err)
			continue
		}
		if seen[dirs[i]] {
			t.Errorf("issue %d was given a checkout another issue already has: %s", i+1, dirs[i])
		}
		seen[dirs[i]] = true
	}
}
