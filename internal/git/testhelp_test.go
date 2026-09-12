package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	awexec "github.com/wuyuz99/taskspace/internal/exec"
)

// These tests drive real git against real temporary repositories. Mocking git
// would only test our idea of git, and the behaviours that matter here —
// worktree reconciliation, dirty detection, ancestry — are exactly the ones a
// mock would get wrong.

func testGit(t *testing.T) *Git {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	return New(awexec.Real())
}

func ctx() context.Context { return context.Background() }

// runGit runs git in dir and fails the test if it errors.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=aw test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=aw test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo creates a repository on branch main with one commit.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks up front: macOS /var -> /private/var would otherwise
	// make path comparisons in worktree tests fail for uninteresting reasons.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	runGit(t, dir, "init", "-b", "main", ".")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

// initRepoWithRemote creates a repo with a bare origin and main pushed to it.
func initRepoWithRemote(t *testing.T) (repo, remote string) {
	t.Helper()
	remote = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(remote); err == nil {
		remote = resolved
	}
	runGit(t, remote, "init", "--bare", "-b", "main", ".")

	repo = initRepo(t)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
	runGit(t, repo, "remote", "set-head", "origin", "-a")
	return repo, remote
}

// pushFromElsewhere commits file on a new branch in a separate clone of remote
// and pushes it, the way a colleague would: repo has never fetched it.
func pushFromElsewhere(t *testing.T, remote, branch, file string) {
	t.Helper()
	other := t.TempDir()
	runGit(t, other, "clone", "-q", remote, ".")
	runGit(t, other, "checkout", "-q", "-b", branch)
	writeFile(t, filepath.Join(other, file), "their work\n")
	runGit(t, other, "add", file)
	runGit(t, other, "commit", "-m", "work on "+branch)
	runGit(t, other, "push", "-q", "origin", branch)
}

// upstream returns the branch's upstream, or "" when it has none.
func upstream(t *testing.T, repo, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
