package git

import (
	"path/filepath"
	"strings"
	"testing"
)

// The two halves of the safe-to-discard test exist for opposite reasons:
// without the merge test a merged local-only branch could never be cleaned,
// and without the upstream test a pushed-but-unmerged branch looks unique
// forever. These tests pin both.

func TestCanDiscardMergedBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "agent/test")
	writeFile(t, filepath.Join(repo, "feature.txt"), "work\n")
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "feature work")
	runGit(t, repo, "checkout", "-q", "main")
	runGit(t, repo, "merge", "--no-ff", "-m", "merge feature", "agent/test")

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if !d.Merged {
		t.Error("Merged = false, want true")
	}
	if !d.Safe() {
		t.Errorf("Safe() = false for a merged branch: %s", d.Reason())
	}
	if !strings.Contains(d.Reason(), "merged into main") {
		t.Errorf("Reason() = %q", d.Reason())
	}
}

// A squash merge leaves none of the branch's commits on base, and the hosting
// service usually deletes the remote branch, so only the squash test sees it.
func TestCanDiscardSquashMergedBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "agent/test")
	for _, f := range []string{"one.txt", "two.txt"} {
		writeFile(t, filepath.Join(repo, f), f+"\n")
		runGit(t, repo, "add", f)
		runGit(t, repo, "commit", "-m", "add "+f)
	}
	runGit(t, repo, "checkout", "-q", "main")
	// Unrelated work lands on main first, as it does on a busy base branch.
	writeFile(t, filepath.Join(repo, "other.txt"), "someone else\n")
	runGit(t, repo, "add", "other.txt")
	runGit(t, repo, "commit", "-m", "other work")

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if d.Safe() {
		t.Fatalf("Safe() before the squash merge: %s", d.Reason())
	}

	runGit(t, repo, "merge", "--squash", "agent/test")
	runGit(t, repo, "commit", "-m", "Squashed: agent/test")
	d, err = g.CanDiscardBranch(ctx(), repo, "agent/test", "main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if d.Merged || !d.Squashed || !d.Safe() {
		t.Errorf("Merged, Squashed = %v, %v; want false, true (%s)", d.Merged, d.Squashed, d.Reason())
	}
	if !strings.Contains(d.Reason(), "squash-merged into main") {
		t.Errorf("Reason() = %q", d.Reason())
	}

	// More work after the squash is unique again.
	runGit(t, repo, "checkout", "-q", "agent/test")
	writeFile(t, filepath.Join(repo, "three.txt"), "later\n")
	runGit(t, repo, "add", "three.txt")
	runGit(t, repo, "commit", "-m", "add three.txt")
	runGit(t, repo, "checkout", "-q", "main")
	if d, err = g.CanDiscardBranch(ctx(), repo, "agent/test", "main"); err != nil || d.Safe() {
		t.Errorf("work added after the squash counted as merged: %v, %s", err, d.Reason())
	}
}

func TestCanDiscardPushedButUnmergedBranch(t *testing.T) {
	g := testGit(t)
	repo, _ := initRepoWithRemote(t)

	runGit(t, repo, "checkout", "-q", "-b", "agent/test")
	writeFile(t, filepath.Join(repo, "feature.txt"), "work\n")
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "feature work")
	runGit(t, repo, "push", "-u", "origin", "agent/test")
	runGit(t, repo, "checkout", "-q", "main")

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "origin/main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if d.Merged {
		t.Error("Merged = true, want false (it was never merged)")
	}
	if !d.Pushed {
		t.Error("Pushed = false, want true")
	}
	if !d.Safe() {
		t.Errorf("Safe() = false for a fully pushed branch: %s", d.Reason())
	}
}

func TestCannotDiscardNeverPushedUnmergedBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	runGit(t, repo, "checkout", "-q", "-b", "agent/test")
	writeFile(t, filepath.Join(repo, "feature.txt"), "unique work\n")
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "unique work")
	runGit(t, repo, "checkout", "-q", "main")

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if d.Safe() {
		t.Fatal("Safe() = true for unmerged, never-pushed work")
	}
	if !strings.Contains(d.Reason(), "never pushed") {
		t.Errorf("Reason() = %q, want it to mention never pushed", d.Reason())
	}
}

func TestCannotDiscardBranchWithUnpushedCommits(t *testing.T) {
	g := testGit(t)
	repo, _ := initRepoWithRemote(t)

	runGit(t, repo, "checkout", "-q", "-b", "agent/test")
	writeFile(t, filepath.Join(repo, "a.txt"), "pushed\n")
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-m", "pushed work")
	runGit(t, repo, "push", "-u", "origin", "agent/test")

	// One more commit that never left the machine.
	writeFile(t, filepath.Join(repo, "b.txt"), "not pushed\n")
	runGit(t, repo, "add", "b.txt")
	runGit(t, repo, "commit", "-m", "unpushed work")
	runGit(t, repo, "checkout", "-q", "main")

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "origin/main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if d.Safe() {
		t.Fatal("Safe() = true with an unpushed commit")
	}
	if d.UnpushedCommits != 1 {
		t.Errorf("UnpushedCommits = %d, want 1", d.UnpushedCommits)
	}
	if !strings.Contains(d.Reason(), "not pushed") {
		t.Errorf("Reason() = %q", d.Reason())
	}
}

func TestCanDiscardMissingBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/never-existed", "main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if !d.Safe() {
		t.Error("Safe() = false for a branch that does not exist")
	}
}

func TestCanDiscardBranchWithNoCommitsOfItsOwn(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	// Created but never committed to: it is an ancestor of main by definition.
	runGit(t, repo, "branch", "agent/test")

	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "main")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if !d.Safe() {
		t.Errorf("Safe() = false for a branch with no unique commits: %s", d.Reason())
	}
}

func TestCanDiscardHandlesMissingBaseRef(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "branch", "agent/test")

	// A base that does not resolve must not be treated as "merged".
	d, err := g.CanDiscardBranch(ctx(), repo, "agent/test", "origin/does-not-exist")
	if err != nil {
		t.Fatalf("CanDiscardBranch: %v", err)
	}
	if d.Merged {
		t.Error("Merged = true against an unresolvable base ref")
	}
	if d.Safe() {
		t.Error("Safe() = true when the base ref cannot be checked")
	}
}
