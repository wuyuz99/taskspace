package git

import (
	"path/filepath"
	"testing"
)

func commit(t *testing.T, repo, file string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, file), file+"\n")
	runGit(t, repo, "add", file)
	runGit(t, repo, "commit", "-m", file)
}

// The nearest fork wins; the default wins a tie, and a branch that already
// holds all of the target is its child, not its parent.
func TestInferBase(t *testing.T) {
	g := testGit(t)
	repo := initRepo(t)
	runGit(t, repo, "branch", "same-as-main")
	runGit(t, repo, "checkout", "-q", "-b", "release")
	commit(t, repo, "release.txt")
	runGit(t, repo, "checkout", "-q", "-b", "feature")
	commit(t, repo, "feature.txt")
	runGit(t, repo, "checkout", "-q", "-b", "child")
	commit(t, repo, "child.txt")
	runGit(t, repo, "checkout", "-q", "main")
	runGit(t, repo, "checkout", "-q", "-b", "topic")
	commit(t, repo, "topic.txt")
	runGit(t, repo, "checkout", "-q", "main")

	for _, c := range []struct{ branch, want string }{
		{"feature", "release"},
		{"topic", "main"}, // tied with same-as-main
	} {
		got, err := g.InferBase(ctx(), repo, c.branch, "refs/heads/"+c.branch, "main")
		if err != nil || got != c.want {
			t.Errorf("InferBase(%s) = %q, %v; want %q", c.branch, got, err, c.want)
		}
	}

	// Once merged, the branch is all on main, which is still its base.
	runGit(t, repo, "merge", "-q", "--ff-only", "topic")
	if got, err := g.InferBase(ctx(), repo, "topic", "refs/heads/topic", "main"); err != nil || got != "main" {
		t.Errorf("InferBase(merged topic) = %q, %v; want main", got, err)
	}
}
