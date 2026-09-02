package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	awexec "github.com/aw-tool/aw/internal/exec"
)

// Worktree is one entry from `git worktree list --porcelain`.
type Worktree struct {
	Path       string
	Head       string
	Branch     string // short name, "" when detached or bare
	Bare       bool
	Detached   bool
	Locked     bool
	LockReason string
	Prunable   bool
}

// Worktrees lists the worktrees of a repository.
func (g *Git) Worktrees(ctx context.Context, repo string) ([]Worktree, error) {
	res, err := g.git(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees of %s: %w", repo, err)
	}
	return parseWorktreeList(res.Stdout), nil
}

// parseWorktreeList parses the porcelain worktree format: blank-line separated
// records of "key value" lines, where some keys are bare flags.
func parseWorktreeList(out string) []Worktree {
	var (
		list []Worktree
		cur  *Worktree
	)
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: filepath.Clean(value)}
		case "HEAD":
			if cur != nil {
				cur.Head = value
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
				cur.LockReason = value
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	flush()
	return list
}

// FindWorktree returns the worktree registered at path, if any. Paths are
// compared after symlink resolution because git records the resolved path and
// callers often hold an unresolved one.
func (g *Git) FindWorktree(ctx context.Context, repo, path string) (*Worktree, error) {
	list, err := g.Worktrees(ctx, repo)
	if err != nil {
		return nil, err
	}
	want := canonical(path)
	for i := range list {
		if canonical(list[i].Path) == want {
			return &list[i], nil
		}
	}
	return nil, nil
}

// FindWorktreeForBranch returns the worktree that has branch checked out.
func (g *Git) FindWorktreeForBranch(ctx context.Context, repo, branch string) (*Worktree, error) {
	list, err := g.Worktrees(ctx, repo)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Branch == branch {
			return &list[i], nil
		}
	}
	return nil, nil
}

func canonical(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// Prune drops worktree records whose directories have disappeared.
func (g *Git) Prune(ctx context.Context, repo string) error {
	if _, err := g.git(ctx, repo, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune worktrees of %s: %w", repo, err)
	}
	return nil
}

// EnsureResult describes what EnsureWorktree did.
type EnsureResult struct {
	Path    string
	Branch  string
	Base    string
	Created bool // false when an existing worktree was reused
	Pruned  bool // a stale registration was cleaned up first
}

// EnsureWorktree makes path a worktree of repo on branch, creating it if
// needed, and is safe to call repeatedly.
//
// The reconciliation order matters, and each case is a real situation:
//
//	registered and present      reuse, no-op
//	registered but gone         prune, then recreate
//	branch checked out elsewhere hard error naming the other worktree
//	branch exists, not checked out attach a worktree to it, do not re-fork
//	nothing exists              create branch from base
//
// base is only consulted when the branch has to be created; reusing an
// existing branch never silently re-forks it.
func (g *Git) EnsureWorktree(ctx context.Context, repo, path, branch, base string) (EnsureResult, error) {
	out := EnsureResult{Path: path, Branch: branch, Base: base}

	existing, err := g.FindWorktree(ctx, repo, path)
	if err != nil {
		return out, err
	}
	if existing != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			if existing.Branch != "" && existing.Branch != branch {
				return out, fmt.Errorf(
					"%s already exists as a worktree on branch %q, not %q; remove it or choose another branch",
					path, existing.Branch, branch)
			}
			out.Base = "" // nothing was forked
			return out, nil
		}
		// Registered but the directory is gone: git will refuse to reuse the
		// path until the stale record is pruned.
		if err := g.Prune(ctx, repo); err != nil {
			return out, err
		}
		out.Pruned = true
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return out, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return out, fmt.Errorf("%s already exists and is not empty; remove it or choose another location", path)
	}

	branchExists, err := g.BranchExists(ctx, repo, branch)
	if err != nil {
		return out, err
	}

	if branchExists {
		if wt, err := g.FindWorktreeForBranch(ctx, repo, branch); err != nil {
			return out, err
		} else if wt != nil {
			return out, fmt.Errorf(
				"branch %q is already checked out at %s; use that worktree or choose another branch",
				branch, wt.Path)
		}
		// Attach to the existing branch rather than re-forking it, which would
		// discard whatever work is already on it.
		if _, err := g.git(ctx, repo, "worktree", "add", path, branch); err != nil {
			return out, fmt.Errorf("add worktree for existing branch %s: %w", branch, err)
		}
		out.Created = true
		out.Base = ""
		return out, nil
	}

	if base == "" {
		base, err = g.ResolveBase(ctx, repo)
		if err != nil {
			return out, err
		}
		out.Base = base
	}
	if _, err := g.git(ctx, repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return out, fmt.Errorf("create worktree %s on new branch %s from %s: %w", path, branch, base, err)
	}
	out.Created = true
	return out, nil
}

// RemoveWorktree removes a worktree registration and its directory.
//
// force is passed through to git and will discard modifications; callers are
// responsible for having established that this is safe.
func (g *Git) RemoveWorktree(ctx context.Context, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)

	if _, err := g.git(ctx, repo, args...); err != nil {
		if awexec.IsExit(err) {
			return fmt.Errorf("remove worktree %s: %w", path, err)
		}
		return err
	}
	return nil
}
