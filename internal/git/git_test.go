package git

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

func TestValidateAcceptsRepoAndCanonicalizes(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	// A subdirectory must canonicalize to the repository top level.
	sub := filepath.Join(repo, "nested", "deeper")
	writeFile(t, filepath.Join(sub, "f.txt"), "x")

	got, err := g.Validate(ctx(), sub)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got != repo {
		t.Errorf("Validate = %q, want repo top level %q", got, repo)
	}
}

func TestValidateRejectsMissingPath(t *testing.T) {
	g := testGit(t)
	_, err := g.Validate(ctx(), filepath.Join(t.TempDir(), "nope"))
	if err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("Validate error = %v, want 'no such directory'", err)
	}
}

func TestValidateRejectsNonRepository(t *testing.T) {
	g := testGit(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file.txt"), "not a repo")

	_, err := g.Validate(ctx(), dir)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("Validate error = %v, want 'not a git repository'", err)
	}
}

func TestValidateRejectsRepoMidOperation(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	// Adding a worktree to a repo mid-merge is legal but almost never intended.
	writeFile(t, filepath.Join(repo, ".git", "MERGE_HEAD"), "deadbeef\n")

	_, err := g.Validate(ctx(), repo)
	if err == nil || !strings.Contains(err.Error(), "unfinished merge") {
		t.Fatalf("Validate error = %v, want 'unfinished merge'", err)
	}
}

func TestResolveBasePrefersOriginHead(t *testing.T) {
	g := testGit(t)
	repo, _ := initRepoWithRemote(t)
	// A local branch that must not win over origin/HEAD.
	runGit(t, repo, "branch", "master")

	got, err := g.ResolveBase(ctx(), repo)
	if err != nil {
		t.Fatalf("ResolveBase: %v", err)
	}
	if got != "origin/main" {
		t.Errorf("ResolveBase = %q, want origin/main", got)
	}
}

func TestResolveBaseFallsBackToLocalDefault(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t) // has main, no remote
	// Sit on an unrelated branch: the whole point is that we must NOT use HEAD.
	runGit(t, repo, "checkout", "-q", "-b", "some-feature")

	got, err := g.ResolveBase(ctx(), repo)
	if err != nil {
		t.Fatalf("ResolveBase: %v", err)
	}
	if got != "main" {
		t.Errorf("ResolveBase = %q, want main (not the current branch)", got)
	}
}

func TestResolveBaseFallsBackToMaster(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "branch", "-m", "main", "master")

	got, err := g.ResolveBase(ctx(), repo)
	if err != nil {
		t.Fatalf("ResolveBase: %v", err)
	}
	if got != "master" {
		t.Errorf("ResolveBase = %q, want master", got)
	}
}

// Without a default branch the base is the branch HEAD is on, or its commit
// when detached, never HEAD itself, which means something else in each
// worktree.
func TestResolveBaseFallsBackToHeadsBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "branch", "-m", "main", "trunk")

	got, err := g.ResolveBase(ctx(), repo)
	if err != nil || got != "trunk" {
		t.Fatalf("ResolveBase = %q, %v; want trunk", got, err)
	}
	commit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "-q", "--detach")
	if got, err = g.ResolveBase(ctx(), repo); err != nil || got != commit {
		t.Fatalf("detached ResolveBase = %q, %v; want %s", got, err, commit)
	}
}

func TestResolveBaseFailsOnEmptyRepository(t *testing.T) {
	g := testGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main", ".")

	_, err := g.ResolveBase(ctx(), dir)
	if err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("ResolveBase error = %v, want 'no commits'", err)
	}
}

func TestBranchExists(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	if ok, err := g.BranchExists(ctx(), repo, "main"); err != nil || !ok {
		t.Fatalf("BranchExists(main) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := g.BranchExists(ctx(), repo, "nope"); err != nil || ok {
		t.Fatalf("BranchExists(nope) = %v, %v; want false, nil", ok, err)
	}
}

func TestParseWorktreeList(t *testing.T) {
	out := strings.Join([]string{
		"worktree /src/backend",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /wt/checkout/backend",
		"HEAD def456",
		"branch refs/heads/agent/checkout",
		"locked agent still running",
		"",
		"worktree /wt/detached",
		"HEAD 999999",
		"detached",
		"",
	}, "\n")

	got := parseWorktreeList(out)
	if len(got) != 3 {
		t.Fatalf("parsed %d worktrees, want 3: %+v", len(got), got)
	}
	if got[0].Branch != "main" {
		t.Errorf("[0].Branch = %q, want main", got[0].Branch)
	}
	if got[1].Branch != "agent/checkout" {
		t.Errorf("[1].Branch = %q, want agent/checkout", got[1].Branch)
	}
	if !got[1].Locked || got[1].LockReason != "agent still running" {
		t.Errorf("[1] lock = %v/%q, want true/'agent still running'", got[1].Locked, got[1].LockReason)
	}
	if !got[2].Detached || got[2].Branch != "" {
		t.Errorf("[2] = %+v, want detached with no branch", got[2])
	}
}

func TestAvailable(t *testing.T) {
	if err := testGit(t).Available(ctx()); err != nil {
		t.Fatalf("Available with git on PATH: %v", err)
	}
	f := tsexec.NewFake()
	f.Fail("git --version", "not found", 127)
	if err := New(f).Available(ctx()); err == nil || !strings.Contains(err.Error(), "git is required") {
		t.Fatalf("Available without git = %v", err)
	}
}

// DeleteBranchAt deletes only the commit it was told was safe: a branch that
// has moved on, or one checked out, stays.
func TestDeleteBranchAt(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	checked, err := g.Commit(ctx(), repo, "feature")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "branch.feature.description", "x")

	runGit(t, repo, "checkout", "-q", "feature")
	writeFile(t, filepath.Join(repo, "late.txt"), "late\n")
	runGit(t, repo, "add", "late.txt")
	runGit(t, repo, "commit", "-m", "late")
	err = g.DeleteBranchAt(ctx(), repo, "feature", checked)
	if err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Fatalf("DeleteBranchAt(checked out) = %v", err)
	}
	runGit(t, repo, "checkout", "-q", "main")
	err = g.DeleteBranchAt(ctx(), repo, "feature", checked)
	if err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("DeleteBranchAt(moved) = %v", err)
	}
	if ok, _ := g.BranchExists(ctx(), repo, "feature"); !ok {
		t.Fatal("a branch that moved was deleted")
	}

	tip, err := g.Commit(ctx(), repo, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteBranchAt(ctx(), repo, "feature", tip); err != nil {
		t.Fatalf("DeleteBranchAt: %v", err)
	}
	if ok, _ := g.BranchExists(ctx(), repo, "feature"); ok {
		t.Fatal("branch survived")
	}
	if _, err := g.git(ctx(), repo, "config", "--get-regexp", `^branch\.feature\.`); err == nil {
		t.Fatal("the branch's configuration survived")
	}
	if err := g.DeleteBranchAt(ctx(), repo, "feature", ""); err == nil {
		t.Fatal("deleting a missing branch succeeded")
	}
}

// A worktree stopped in a rebase of the branch is detached, but the branch
// is still in use there: git branch -D refuses it, and so does DeleteBranchAt.
func TestDeleteBranchAtRefusesARebase(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(repo, "README.md"), "feature\n")
	runGit(t, repo, "commit", "-q", "-am", "feature")
	runGit(t, repo, "checkout", "-q", "main")
	writeFile(t, filepath.Join(repo, "README.md"), "main\n")
	runGit(t, repo, "commit", "-q", "-am", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", wt, "feature")
	tip, err := g.Commit(ctx(), repo, "feature")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "rebase", "main")
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("rebase did not stop on its conflict:\n%s", out)
	}

	err = g.DeleteBranchAt(ctx(), repo, "feature", tip)
	if err == nil || !strings.Contains(err.Error(), "being rebased") {
		t.Fatalf("DeleteBranchAt(rebasing) = %v", err)
	}
	if ok, _ := g.BranchExists(ctx(), repo, "feature"); !ok {
		t.Fatal("a branch being rebased was deleted")
	}
}
