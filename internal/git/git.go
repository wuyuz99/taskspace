// Package git wraps the git commands aw needs.
//
// It never formats output for humans and never knows about tmux or the CLI.
// Errors carry git's own stderr and the argv that produced it, because a tool
// that paraphrases git makes problems harder to diagnose, not easier.
package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	awexec "github.com/aw-tool/aw/internal/exec"
)

// Git runs git commands through a Runner.
type Git struct {
	run awexec.Runner
}

// New returns a Git using the given runner.
func New(r awexec.Runner) *Git {
	if r == nil {
		r = awexec.Real()
	}
	return &Git{run: r}
}

func (g *Git) git(ctx context.Context, dir string, args ...string) (awexec.Result, error) {
	return g.run.Run(ctx, dir, append([]string{"git"}, args...)...)
}

// ok runs a git command and reports whether it exited zero, discarding the
// distinction between "ran and failed" and "output we do not care about".
// A failure to start the process is still returned as an error.
func (g *Git) ok(ctx context.Context, dir string, args ...string) (bool, error) {
	_, err := g.git(ctx, dir, args...)
	if err == nil {
		return true, nil
	}
	if awexec.IsExit(err) {
		return false, nil
	}
	return false, err
}

// Available reports whether git is usable.
func (g *Git) Available(ctx context.Context) error {
	if _, err := g.git(ctx, "", "--version"); err != nil {
		return fmt.Errorf("git is required but not usable: %w", err)
	}
	return nil
}

// Validate checks that path is a usable git repository and returns the
// canonical absolute path of its top level.
//
// "Usable" excludes a repository sitting in the middle of a rebase, merge,
// cherry-pick, revert or bisect: adding a worktree to one is legal but the
// user almost certainly did not mean to, and the resulting confusion is worse
// than an upfront refusal.
func (g *Git) Validate(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("%s: no such directory", abs)
	}
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: not a directory", abs)
	}

	res, err := g.git(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		if awexec.IsExit(err) {
			return "", fmt.Errorf("%s: not a git repository", abs)
		}
		return "", err
	}
	top := res.Out()
	if top == "" {
		return "", fmt.Errorf("%s: not a git repository", abs)
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	top = filepath.Clean(top)

	if op, err := g.InProgress(ctx, top); err != nil {
		return "", err
	} else if op != "" {
		return "", fmt.Errorf("%s: repository has an unfinished %s; resolve it first", top, op)
	}
	return top, nil
}

// InProgress returns the name of an unfinished git operation, or "".
func (g *Git) InProgress(ctx context.Context, repo string) (string, error) {
	res, err := g.git(ctx, repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		if awexec.IsExit(err) {
			return "", nil
		}
		return "", err
	}
	gitDir := res.Out()
	if gitDir == "" {
		return "", nil
	}

	// Ordered so the more specific operations win over plain "rebase".
	markers := []struct{ path, name string }{
		{"rebase-merge/interactive", "interactive rebase"},
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(gitDir, m.path)); err == nil {
			return m.name, nil
		}
	}
	return "", nil
}

// ResolveBase determines the ref new task branches should fork from.
//
// Order: origin/HEAD, then a conventional local default branch, then HEAD.
// This exists because branching from whatever HEAD happens to be silently
// inherits an unrelated feature branch — a correctness bug, not a UX wrinkle.
// Callers are expected to report the chosen ref to the user.
func (g *Git) ResolveBase(ctx context.Context, repo string) (string, error) {
	res, err := g.git(ctx, repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil && !awexec.IsExit(err) {
		return "", err
	}
	if err == nil {
		if ref := strings.TrimPrefix(res.Out(), "refs/remotes/"); ref != "" && ref != res.Out() {
			return ref, nil
		}
	}

	for _, candidate := range []string{"main", "master"} {
		found, err := g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate)
		if err != nil {
			return "", err
		}
		if found {
			return candidate, nil
		}
	}

	// A repository with no commits has no usable HEAD to fork from.
	hasHead, err := g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", err
	}
	if !hasHead {
		return "", fmt.Errorf("%s: repository has no commits to branch from", repo)
	}
	return "HEAD", nil
}

// RefExists reports whether a ref resolves.
func (g *Git) RefExists(ctx context.Context, repo, ref string) (bool, error) {
	return g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", ref)
}

// BranchExists reports whether a local branch exists.
func (g *Git) BranchExists(ctx context.Context, repo, branch string) (bool, error) {
	return g.ok(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
}

// DeleteBranch deletes a local branch. force uses -D, which discards the
// merge-safety check git would otherwise apply; callers must have run their own
// safety check first.
func (g *Git) DeleteBranch(ctx context.Context, repo, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	if _, err := g.git(ctx, repo, "branch", flag, branch); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	return nil
}

// CurrentBranch returns the checked-out branch of a worktree, or "" if detached.
func (g *Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	res, err := g.git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		if awexec.IsExit(err) {
			return "", nil
		}
		return "", err
	}
	return res.Out(), nil
}
