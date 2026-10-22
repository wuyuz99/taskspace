package cli

import (
	"strings"
	"testing"
)

func TestAssumedBaseRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)
	gitIn(t, h.repo, "checkout", "-b", "release")
	commitFile(t, h.repo, "release.txt", "release", "release work")
	gitIn(t, h.repo, "checkout", "-b", "feature")
	commitFile(t, h.repo, "feature.txt", "feature", "feature work")
	// A branch stacked on feature contains all of it, and must not be
	// mistaken for its parent.
	gitIn(t, h.repo, "checkout", "-b", "feature-child")
	commitFile(t, h.repo, "child.txt", "child", "child work")
	gitIn(t, h.repo, "checkout", "main")
	if err := h.exec(t, h.app.newTaskCmd(), "create", "T", "backend="+h.repo+"@feature"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "T")
	r := tk.Repos["backend"]
	if r.Base != "release" || !r.BaseAssumed || tk.Version != 3 {
		t.Fatalf("repo %+v version %d", r, tk.Version)
	}
	if err := h.exec(t, h.app.newStartCmd(), "T", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	before := gitIn(t, r.Worktree, "rev-parse", "HEAD")
	if err := h.exec(t, h.app.newRepoCmd(), "update", "T", "backend", "--base", "main", "--rebase"); err == nil || !strings.Contains(err.Error(), "guessed") {
		t.Fatalf("rebase=%v", err)
	}
	if got := gitIn(t, r.Worktree, "rev-parse", "HEAD"); got != before {
		t.Fatal("refused rebase changed HEAD")
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "T", "backend", "--base", "release"); err != nil {
		t.Fatal(err)
	}
	if h.load(t, "T").Repos["backend"].BaseAssumed {
		t.Fatal("base not confirmed")
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "T", "backend", "--base", "main", "--rebase"); err != nil {
		t.Fatal(err)
	}
	log := gitIn(t, r.Worktree, "log", "--format=%s", "main..HEAD")
	if log != "feature work" {
		t.Fatalf("replayed parent commits: %s", log)
	}
}
