package git

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRepositoryEnv(t *testing.T) {
	// Check against the installed Git too, so new repository-local variables
	// cannot silently escape the filter as Git evolves.
	keys := strings.Fields(runGit(t, initRepo(t), "rev-parse", "--local-env-vars"))
	keep := []string{"PATH=/bin", "GIT_SSH_COMMAND=ssh custom", "GIT_ASKPASS=ask",
		"GIT_CONFIG_GLOBAL=/config", "GIT_CONFIG_SYSTEM=/system", "HOME=/home"}
	env := append([]string{}, keep...)
	for _, key := range keys {
		env = append(env, key+"=poison")
	}
	env = append(env, "GIT_EDITOR=:", "GIT_QUARANTINE_PATH=/quarantine", "git_dir=user-variable")
	if runtime.GOOS != "windows" {
		keep = append(keep, "git_dir=user-variable")
	}
	if got := RepositoryEnv(env); !reflect.DeepEqual(got, keep) {
		t.Fatalf("environment = %v, want %v", got, keep)
	}
}

func TestInheritedRepositoryEnvironment(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	other := initRepo(t)
	wt := filepath.Join(t.TempDir(), "task")
	runGit(t, repo, "worktree", "add", "-b", "agent/demo", wt)
	writeFile(t, filepath.Join(wt, "README.md"), "task work\n")
	runGit(t, wt, "commit", "-am", "task work")
	writeFile(t, filepath.Join(wt, "README.md"), "unfinished work\n")
	runGit(t, other, "branch", "agent/demo")
	for key, value := range map[string]string{
		"GIT_DIR": filepath.Join(other, ".git"), "GIT_WORK_TREE": other,
		"GIT_COMMON_DIR":       filepath.Join(other, ".git"),
		"GIT_INDEX_FILE":       filepath.Join(other, ".git", "index"),
		"GIT_OBJECT_DIRECTORY": filepath.Join(other, ".git", "objects"),
		"GIT_CONFIG_COUNT":     "1", "GIT_CONFIG_KEY_0": "core.bare", "GIT_CONFIG_VALUE_0": "true",
	} {
		t.Setenv(key, value)
	}
	st, err := g.WorktreeStatus(ctx(), wt)
	if err != nil || st.Branch != "agent/demo" || !st.Dirty() {
		t.Fatalf("task status = %+v, %v", st, err)
	}
	d, err := g.CanDiscardBranch(ctx(), repo, "agent/demo", "main")
	if err != nil || d.Safe() {
		t.Fatalf("unmerged task must not be safe: %+v, %v", d, err)
	}
	if err := g.RemoveWorktree(ctx(), repo, wt, false); err == nil {
		t.Fatal("removed a dirty task worktree")
	}
	if err := g.RenameBranch(ctx(), repo, "agent/demo", "agent/renamed"); err != nil {
		t.Fatal(err)
	}
	if exists, err := g.BranchExists(ctx(), other, "agent/demo"); err != nil || !exists {
		t.Fatalf("changed the foreign repository: %v, %v", exists, err)
	}
	if exists, err := g.BranchExists(ctx(), repo, "agent/renamed"); err != nil || !exists {
		t.Fatalf("did not rename the task branch: %v, %v", exists, err)
	}
	var output bytes.Buffer
	dest := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone(ctx(), repo, dest, nil, &output); err != nil {
		t.Fatalf("clone: %v: %s", err, &output)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GIT_DIR") != filepath.Join(other, ".git") {
		t.Fatal("changed parent environment")
	}
}
