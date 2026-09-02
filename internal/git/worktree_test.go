package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWorktreeCreatesFromResolvedBase(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	// Park HEAD on an unrelated branch with its own commit. If EnsureWorktree
	// forks from HEAD rather than the resolved base, that commit leaks in.
	runGit(t, repo, "checkout", "-q", "-b", "unrelated")
	writeFile(t, filepath.Join(repo, "leak.txt"), "should not appear\n")
	runGit(t, repo, "add", "leak.txt")
	runGit(t, repo, "commit", "-m", "unrelated work")

	wt := filepath.Join(t.TempDir(), "task", "backend")
	res, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", "")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if res.Base != "main" {
		t.Errorf("Base = %q, want main", res.Base)
	}
	if _, err := os.Stat(filepath.Join(wt, "leak.txt")); err == nil {
		t.Error("worktree contains leak.txt: it was forked from HEAD, not the base branch")
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Errorf("worktree missing README.md from base: %v", err)
	}
}

func TestEnsureWorktreeIsIdempotent(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")

	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", ""); err != nil {
		t.Fatalf("first EnsureWorktree: %v", err)
	}
	// Uncommitted work must survive a second call.
	writeFile(t, filepath.Join(wt, "wip.txt"), "in progress\n")

	res, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", "")
	if err != nil {
		t.Fatalf("second EnsureWorktree: %v", err)
	}
	if res.Created {
		t.Error("Created = true on reuse, want false")
	}
	if _, err := os.Stat(filepath.Join(wt, "wip.txt")); err != nil {
		t.Errorf("reuse destroyed uncommitted work: %v", err)
	}
}

func TestEnsureWorktreeRecreatesAfterDirectoryDeleted(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")

	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	// The user deleted the directory by hand; git still has the registration,
	// and will refuse to reuse the path until it is pruned.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	res, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", "")
	if err != nil {
		t.Fatalf("EnsureWorktree after deletion: %v", err)
	}
	if !res.Pruned {
		t.Error("Pruned = false, want true")
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Errorf("recreated worktree is not populated: %v", err)
	}
}

func TestEnsureWorktreeRefusesBranchCheckedOutElsewhere(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-q", "-b", "agent/test")

	wt := filepath.Join(t.TempDir(), "task", "backend")
	_, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", "")
	if err == nil {
		t.Fatal("EnsureWorktree = nil, want error for branch checked out elsewhere")
	}
	if !strings.Contains(err.Error(), "already checked out") {
		t.Errorf("error = %v, want 'already checked out'", err)
	}
	// The error must name where it is checked out, or it is not actionable.
	if !strings.Contains(err.Error(), repo) {
		t.Errorf("error = %v, want it to name %s", err, repo)
	}
}

func TestEnsureWorktreeAttachesToExistingBranchWithoutReforking(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	// A branch with work on it, not currently checked out.
	runGit(t, repo, "checkout", "-q", "-b", "agent/test")
	writeFile(t, filepath.Join(repo, "prior.txt"), "earlier work\n")
	runGit(t, repo, "add", "prior.txt")
	runGit(t, repo, "commit", "-m", "prior work")
	runGit(t, repo, "checkout", "-q", "main")

	wt := filepath.Join(t.TempDir(), "task", "backend")
	res, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", "")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if _, err := os.Stat(filepath.Join(wt, "prior.txt")); err != nil {
		t.Errorf("existing branch was re-forked, losing prior work: %v", err)
	}
}

func TestEnsureWorktreeRefusesNonEmptyDirectory(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")
	writeFile(t, filepath.Join(wt, "someones-file.txt"), "do not clobber me\n")

	_, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", "")
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("EnsureWorktree error = %v, want 'not empty'", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "someones-file.txt")); err != nil {
		t.Errorf("refused operation still touched the directory: %v", err)
	}
}

func TestEnsureWorktreeRefusesPathHoldingDifferentBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")

	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/one", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	_, err := g.EnsureWorktree(ctx(), repo, wt, "agent/two", "")
	if err == nil || !strings.Contains(err.Error(), "agent/one") {
		t.Fatalf("error = %v, want it to name the occupying branch agent/one", err)
	}
}

func TestRemoveWorktree(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")

	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if err := g.RemoveWorktree(ctx(), repo, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree directory still present after removal")
	}
	// The branch must survive: removing a worktree is not deleting work.
	if ok, err := g.BranchExists(ctx(), repo, "agent/test"); err != nil || !ok {
		t.Errorf("BranchExists after RemoveWorktree = %v, %v; branch must survive", ok, err)
	}
}

func TestRemoveWorktreeRefusesDirtyWithoutForce(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")

	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	writeFile(t, filepath.Join(wt, "README.md"), "modified\n")

	if err := g.RemoveWorktree(ctx(), repo, wt, false); err == nil {
		t.Fatal("RemoveWorktree on dirty worktree = nil, want error")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("refused removal still deleted the directory: %v", err)
	}
}

func TestFindWorktreeForBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")

	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	found, err := g.FindWorktreeForBranch(ctx(), repo, "agent/test")
	if err != nil {
		t.Fatalf("FindWorktreeForBranch: %v", err)
	}
	if found == nil {
		t.Fatal("FindWorktreeForBranch = nil, want the worktree")
	}
	if canonical(found.Path) != canonical(wt) {
		t.Errorf("Path = %q, want %q", found.Path, wt)
	}

	missing, err := g.FindWorktreeForBranch(ctx(), repo, "agent/absent")
	if err != nil {
		t.Fatalf("FindWorktreeForBranch: %v", err)
	}
	if missing != nil {
		t.Errorf("FindWorktreeForBranch(absent) = %+v, want nil", missing)
	}
}
