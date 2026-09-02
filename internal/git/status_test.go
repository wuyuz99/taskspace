package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeStatusClean(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	st, err := g.WorktreeStatus(ctx(), repo)
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if !st.Exists {
		t.Fatal("Exists = false, want true")
	}
	if st.Dirty() {
		t.Errorf("Dirty() = true on a fresh repo: %+v", st)
	}
	if st.Branch != "main" {
		t.Errorf("Branch = %q, want main", st.Branch)
	}
}

func TestWorktreeStatusMissingDirectoryIsNotAnError(t *testing.T) {
	g := testGit(t)
	// A worktree deleted out from under a task is a state aw must display,
	// not one it should fail on.
	st, err := g.WorktreeStatus(ctx(), filepath.Join(t.TempDir(), "gone"))
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if st.Exists {
		t.Error("Exists = true for a missing directory")
	}
}

func TestWorktreeStatusDetectsEachKindOfChange(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)

	writeFile(t, filepath.Join(repo, "README.md"), "modified\n")
	writeFile(t, filepath.Join(repo, "untracked.txt"), "new\n")
	writeFile(t, filepath.Join(repo, "staged.txt"), "staged\n")
	runGit(t, repo, "add", "staged.txt")

	st, err := g.WorktreeStatus(ctx(), repo)
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if st.Modified != 1 {
		t.Errorf("Modified = %d, want 1", st.Modified)
	}
	if st.Staged != 1 {
		t.Errorf("Staged = %d, want 1", st.Staged)
	}
	if st.Untracked != 1 {
		t.Errorf("Untracked = %d, want 1", st.Untracked)
	}
	if !st.Dirty() {
		t.Error("Dirty() = false, want true")
	}
	if st.DirtyCount() != 3 {
		t.Errorf("DirtyCount() = %d, want 3", st.DirtyCount())
	}
}

// A stash is invisible to `git status` but is still work the user would lose
// track of. It is the case most likely to be missed by a naive dirty check.
func TestWorktreeStatusCountsStashesForItsBranch(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-q", "-b", "agent/test")

	writeFile(t, filepath.Join(repo, "README.md"), "work in progress\n")
	runGit(t, repo, "stash", "push", "-m", "wip")

	st, err := g.WorktreeStatus(ctx(), repo)
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if st.DirtyCount() != 0 {
		t.Errorf("DirtyCount() = %d, want 0 (the stash cleaned the tree)", st.DirtyCount())
	}
	if st.Stashes != 1 {
		t.Errorf("Stashes = %d, want 1", st.Stashes)
	}
	if !st.Dirty() {
		t.Error("Dirty() = false with a stash present, want true")
	}
}

func TestWorktreeStatusAheadBehind(t *testing.T) {
	g := testGit(t)
	repo, _ := initRepoWithRemote(t)

	writeFile(t, filepath.Join(repo, "new.txt"), "local only\n")
	runGit(t, repo, "add", "new.txt")
	runGit(t, repo, "commit", "-m", "local commit")

	st, err := g.WorktreeStatus(ctx(), repo)
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if st.Upstream != "origin/main" {
		t.Errorf("Upstream = %q, want origin/main", st.Upstream)
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Errorf("Ahead/Behind = %d/%d, want 1/0", st.Ahead, st.Behind)
	}
}

func TestParseStatusV2(t *testing.T) {
	out := `# branch.oid abc123
# branch.head agent/checkout
# branch.upstream origin/agent/checkout
# branch.ab +3 -2
1 .M N... 100644 100644 100644 aaa bbb modified.txt
1 M. N... 100644 100644 100644 ccc ddd staged.txt
1 MM N... 100644 100644 100644 eee fff both.txt
2 R. N... 100644 100644 100644 ggg hhh R100 new.txt	old.txt
u UU N... 100644 100644 100644 100644 iii jjj kkk conflict.txt
? untracked-one.txt
? untracked-two.txt
`
	var s Status
	parseStatusV2(out, &s)

	if s.Branch != "agent/checkout" {
		t.Errorf("Branch = %q", s.Branch)
	}
	if s.Upstream != "origin/agent/checkout" {
		t.Errorf("Upstream = %q", s.Upstream)
	}
	if s.Ahead != 3 || s.Behind != 2 {
		t.Errorf("Ahead/Behind = %d/%d, want 3/2", s.Ahead, s.Behind)
	}
	// staged: "M.", "MM", "R." ; modified: ".M", "MM"
	if s.Staged != 3 {
		t.Errorf("Staged = %d, want 3", s.Staged)
	}
	if s.Modified != 2 {
		t.Errorf("Modified = %d, want 2", s.Modified)
	}
	if s.Untracked != 2 {
		t.Errorf("Untracked = %d, want 2", s.Untracked)
	}
	if s.Conflicted != 1 {
		t.Errorf("Conflicted = %d, want 1", s.Conflicted)
	}
}

func TestParseStatusV2DetachedHeadHasNoBranch(t *testing.T) {
	var s Status
	parseStatusV2("# branch.head (detached)\n", &s)
	if s.Branch != "" {
		t.Errorf("Branch = %q, want empty for detached HEAD", s.Branch)
	}
}

func TestCountStashesForBranch(t *testing.T) {
	out := `WIP on agent/checkout: 1234567 earlier commit
On agent/checkout: explicit message
WIP on other-branch: 89abcde not ours
`
	if got := countStashesForBranch(out, "agent/checkout"); got != 2 {
		t.Errorf("countStashesForBranch = %d, want 2", got)
	}
	if got := countStashesForBranch(out, "unrelated"); got != 0 {
		t.Errorf("countStashesForBranch(unrelated) = %d, want 0", got)
	}
	if got := countStashesForBranch(out, ""); got != 0 {
		t.Errorf("countStashesForBranch(\"\") = %d, want 0", got)
	}
}

func TestParseAheadBehind(t *testing.T) {
	cases := []struct {
		in            string
		ahead, behind int
	}{
		{"+0 -0", 0, 0},
		{"+5 -0", 5, 0},
		{"+0 -7", 0, 7},
		{"+2 -3", 2, 3},
		{"garbage", 0, 0},
	}
	for _, c := range cases {
		a, b := parseAheadBehind(c.in)
		if a != c.ahead || b != c.behind {
			t.Errorf("parseAheadBehind(%q) = %d/%d, want %d/%d", c.in, a, b, c.ahead, c.behind)
		}
	}
}

func TestStatusDirtyRequiresSomething(t *testing.T) {
	if (Status{Exists: true}).Dirty() {
		t.Error("empty Status reports dirty")
	}
	if !(Status{Untracked: 1}).Dirty() {
		t.Error("untracked file must count as dirty")
	}
	if !(Status{Stashes: 1}).Dirty() {
		t.Error("stash must count as dirty")
	}
}

func TestStatusOnWorktreeCreatedByEnsure(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task", "backend")
	if _, err := g.EnsureWorktree(ctx(), repo, wt, "agent/test", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	st, err := g.WorktreeStatus(ctx(), wt)
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if st.Branch != "agent/test" {
		t.Errorf("Branch = %q, want agent/test", st.Branch)
	}
	if st.Dirty() {
		t.Errorf("fresh worktree reports dirty: %+v", st)
	}

	if err := os.WriteFile(filepath.Join(wt, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err = g.WorktreeStatus(ctx(), wt)
	if err != nil {
		t.Fatalf("WorktreeStatus: %v", err)
	}
	if !st.Dirty() || st.Untracked != 1 {
		t.Errorf("after adding a file: %+v, want dirty with 1 untracked", st)
	}
}
