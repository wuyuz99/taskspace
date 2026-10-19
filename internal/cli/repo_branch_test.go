package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitIdentity lets the git tspace runs commit, as a rebase does.
func gitIdentity(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "tspace")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tspace")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
}

func commitFile(t *testing.T, dir, file, text, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-m", msg)
}

// Before start, --branch and --base only change what start will do.
func TestRepoUpdateBranchAndBaseBeforeStart(t *testing.T) {
	h := newHarness(t)
	gitIn(t, h.repo, "branch", "release")
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--branch", "me/PAY-1", "--base", "release"); err != nil {
		t.Fatal(err)
	}
	r := h.load(t, "PAY-1").Repos["backend"]
	if r.Branch != "me/PAY-1" || r.Base != "release" {
		t.Fatalf("recorded %+v", r)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "release", "--rebase"); err == nil ||
		!strings.Contains(err.Error(), "no worktree yet") {
		t.Fatalf("--rebase without a worktree = %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--rebase"); err == nil {
		t.Fatal("--rebase without --base accepted")
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "no-such-ref"); err == nil {
		t.Fatal("an unknown base accepted")
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, r.Worktree, "branch", "--show-current"); got != "me/PAY-1" {
		t.Fatalf("start checked out %q", got)
	}
}

func TestRepoUpdateRenamesTheBranch(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	gitIn(t, h.repo, "branch", "taken")
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--branch", "taken"); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rename onto an existing branch = %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--branch", "me/PAY-1"); err != nil {
		t.Fatal(err)
	}
	r := h.load(t, "PAY-1").Repos["backend"]
	if got := gitIn(t, r.Worktree, "branch", "--show-current"); got != "me/PAY-1" || r.Branch != "me/PAY-1" {
		t.Fatalf("worktree on %q, record %q", got, r.Branch)
	}
	if !strings.Contains(h.out.String(), "renamed agent/PAY-1 to me/PAY-1") {
		t.Fatalf("out: %s", h.out.String())
	}
}

func TestRepoUpdateRebase(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	wt := h.load(t, "PAY-1").Repos["backend"].Worktree
	commitFile(t, wt, "feature.txt", "mine\n", "task work")
	gitIn(t, h.repo, "checkout", "-q", "-b", "release")
	commitFile(t, h.repo, "release.txt", "release\n", "release work")
	gitIn(t, h.repo, "checkout", "-q", "main")

	// Without --rebase the branch stays where it is.
	head := gitIn(t, wt, "rev-parse", "HEAD")
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "release"); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != head || h.load(t, "PAY-1").Repos["backend"].Base != "release" {
		t.Fatal("--base alone moved the branch, or did not record the base")
	}
	// Back to main, then rebase onto release for real.
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "main"); err != nil {
		t.Fatal(err)
	}
	// A dirty worktree is refused.
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "release", "--rebase"); err == nil ||
		!strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("rebase of a dirty worktree = %v", err)
	}
	if err := os.Remove(filepath.Join(wt, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "release", "--rebase"); err != nil {
		t.Fatal(err)
	}
	log := gitIn(t, wt, "log", "--format=%s")
	if !strings.Contains(log, "task work") || !strings.Contains(log, "release work") {
		t.Fatalf("after rebase:\n%s", log)
	}
	if h.load(t, "PAY-1").Repos["backend"].Base != "release" {
		t.Fatal("base not recorded after rebase")
	}
}

// A rebase that conflicts is undone, and nothing is recorded.
func TestRepoUpdateRebaseConflictChangesNothing(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	wt := h.load(t, "PAY-1").Repos["backend"].Worktree
	commitFile(t, wt, "README.md", "mine\n", "task work")
	gitIn(t, h.repo, "checkout", "-q", "-b", "release")
	commitFile(t, h.repo, "README.md", "theirs\n", "release work")
	gitIn(t, h.repo, "checkout", "-q", "main")

	head := gitIn(t, wt, "rev-parse", "HEAD")
	err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--base", "release", "--rebase")
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("conflicting rebase = %v", err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != head || gitIn(t, wt, "status", "--porcelain") != "" {
		t.Fatal("the failed rebase left the worktree changed")
	}
	if _, err := os.Stat(filepath.Join(gitIn(t, wt, "rev-parse", "--absolute-git-dir"), "rebase-merge")); !os.IsNotExist(err) {
		t.Fatal("the failed rebase is still in progress")
	}
	if got := h.load(t, "PAY-1").Repos["backend"].Base; got == "release" {
		t.Fatal("the failed rebase recorded the new base")
	}
}
