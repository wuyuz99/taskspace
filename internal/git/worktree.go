package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/fspath"
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
	want := fspath.Canonical(path)
	for i := range list {
		if fspath.Canonical(list[i].Path) == want {
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
	// Tracking is the remote branch a new local branch was created from and
	// set to track, such as "origin/feature/x". Empty otherwise.
	Tracking string
	// FetchErr is why refreshing the remotes failed. The worktree was still
	// created, from the refs already present; callers report it as a warning.
	FetchErr error
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
//	branch exists on a remote   create it tracking the remote branch
//	nothing exists              create branch from base, with no upstream
//
// Before concluding that a branch is new, the remotes are fetched: a branch
// pushed since the last fetch is exactly the one the user asked for, and
// forking a same-named branch from base would silently leave that work out.
//
// base is only consulted when the branch has to be created; reusing an
// existing branch never silently re-forks it.
func (g *Git) EnsureWorktree(ctx context.Context, repo, path, branch, base string) (EnsureResult, error) {
	out := EnsureResult{Path: path, Branch: branch, Base: base}

	existing, err := g.registered(ctx, repo, path, &out)
	if err != nil {
		return out, err
	}
	if existing != nil {
		if existing.Branch != "" && existing.Branch != branch {
			return out, fmt.Errorf(
				"%s already exists as a worktree on branch %q, not %q; remove it or choose another branch",
				path, existing.Branch, branch)
		}
		out.Base = "" // nothing was forked
		return out, nil
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

	_, out.FetchErr = g.FetchRemotes(ctx, repo, base)
	remote, err := g.RemoteBranch(ctx, repo, branch)
	if err != nil {
		return out, err
	}
	if remote != "" {
		if _, err := g.git(ctx, repo, "worktree", "add", "--track", "-b", branch, path, remote); err != nil {
			return out, fmt.Errorf("add worktree for %s tracking %s: %w", branch, remote, err)
		}
		out.Created = true
		out.Base = ""
		out.Tracking = remote
		return out, nil
	}

	if base == "" {
		base, err = g.ResolveBase(ctx, repo)
		if err != nil {
			return out, err
		}
		out.Base = base
	}
	// --no-track, because the branch is new work rather than a copy of base.
	// With git's default branch.autoSetupMerge, forking from origin/main makes
	// origin/main the upstream: `git push` then refuses (push.default=simple)
	// or pushes to main (push.default=upstream), and "ahead" counts commits
	// ahead of main instead of commits not yet pushed.
	if _, err := g.git(ctx, repo, "worktree", "add", "--no-track", "-b", branch, path, base); err != nil {
		return out, fmt.Errorf("create worktree %s on new branch %s from %s: %w", path, branch, base, err)
	}
	out.Created = true
	return out, nil
}

// registered is the worktree registered at path, if its directory is there
// and is that worktree. A registration whose directory is gone is pruned,
// which out records, and the path made ready for a new worktree: its parent
// created, and refused if something else is already in it.
func (g *Git) registered(ctx context.Context, repo, path string, out *EnsureResult) (*Worktree, error) {
	existing, err := g.FindWorktree(ctx, repo, path)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		info, statErr := os.Stat(path)
		if statErr == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s has a stale worktree registration: not a directory; restore the original worktree before retrying", path)
			}
			if err := g.verifyWorktree(ctx, repo, path); err != nil {
				return nil, fmt.Errorf("%s has a stale or invalid worktree registration: %w; restore the original worktree or repair its registration with git worktree repair before retrying", path, err)
			}
			return existing, nil
		}
		if !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("inspect worktree %s: %w", path, statErr)
		}
		// Registered but the directory is gone: git will refuse to reuse the
		// path until the stale record is pruned.
		if err := g.Prune(ctx, repo); err != nil {
			return nil, err
		}
		out.Pruned = true
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s already exists and is not empty; remove it or choose another location", path)
	}
	return nil, nil
}

// EnsureReference makes path a worktree of repo checked out at base,
// detached, for a repository the task only reads, creating it if needed. An
// existing one is reused where it is: it moves to a newer base only when
// asked, with CheckoutDetached. A missing base is fetched once.
func (g *Git) EnsureReference(ctx context.Context, repo, path, base string) (EnsureResult, error) {
	out := EnsureResult{Path: path, Base: base}
	existing, err := g.registered(ctx, repo, path, &out)
	if err != nil {
		return out, err
	}
	if existing != nil {
		if existing.Branch != "" {
			return out, fmt.Errorf("%s already exists as a worktree on branch %q, and a reference has none; remove it, or make the repository a working one with tspace repo update --branch", path, existing.Branch)
		}
		return out, nil
	}
	if base == "" {
		if base, err = g.ResolveBase(ctx, repo); err != nil {
			return out, err
		}
		out.Base = base
	}
	if ok, err := g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		return out, err
	} else if !ok {
		_, out.FetchErr = g.FetchRemotes(ctx, repo, base)
	}
	if _, err := g.git(ctx, repo, "worktree", "add", "--detach", path, base); err != nil {
		return out, fmt.Errorf("create worktree %s at %s: %w", path, base, err)
	}
	out.Created = true
	return out, nil
}

// CheckoutDetached checks out ref in worktree, detached.
func (g *Git) CheckoutDetached(ctx context.Context, worktree, ref string) error {
	if _, err := g.git(ctx, worktree, "checkout", "--quiet", "--detach", ref); err != nil {
		return fmt.Errorf("check out %s in %s: %w", ref, worktree, err)
	}
	return nil
}

// CreateBranchHere creates branch at worktree's HEAD and checks it out, with
// no upstream, keeping any changes in the worktree.
func (g *Git) CreateBranchHere(ctx context.Context, worktree, branch string) error {
	if _, err := g.git(ctx, worktree, "switch", "--quiet", "--no-track", "-c", branch); err != nil {
		return fmt.Errorf("create branch %s in %s: %w", branch, worktree, err)
	}
	return nil
}

// verifyWorktree checks both directions of a linked worktree's identity.
// A matching top level and common directory alone could still be another
// worktree of the same repository moved over this registration.
func (g *Git) verifyWorktree(ctx context.Context, repo, path string) error {
	// One rev-parse per directory: process spawns dominate on Windows.
	resolve := func(dir string, flags ...string) ([]string, error) {
		res, err := g.git(ctx, dir, append([]string{"rev-parse", "--path-format=absolute"}, flags...)...)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(res.Out(), "\n")
		if len(lines) != len(flags) {
			return nil, fmt.Errorf("git rev-parse returned %d paths for %v", len(lines), flags)
		}
		for i, l := range lines {
			if l = strings.TrimSpace(l); l == "" {
				return nil, fmt.Errorf("git returned no %s", flags[i])
			}
			lines[i] = fspath.Canonical(l)
		}
		return lines, nil
	}
	got, err := resolve(path, "--show-toplevel", "--git-common-dir", "--absolute-git-dir")
	if err != nil {
		return err
	}
	top, common, gitDir := got[0], got[1], got[2]
	if top != fspath.Canonical(path) {
		return fmt.Errorf("directory belongs to worktree %s", top)
	}
	want, err := resolve(repo, "--git-common-dir")
	if err != nil {
		return err
	}
	if common != want[0] {
		return fmt.Errorf("directory belongs to a different repository")
	}
	if gitDir == common {
		// The main worktree has no backlink, but must be the main entry.
		list, err := g.Worktrees(ctx, repo)
		if err != nil {
			return err
		}
		if len(list) == 0 || fspath.Canonical(list[0].Path) != fspath.Canonical(path) {
			return fmt.Errorf("directory uses the main worktree's Git metadata")
		}
		return nil
	}
	backlink, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return fmt.Errorf("read worktree backlink: %w", err)
	}
	if fspath.Canonical(strings.TrimSpace(string(backlink))) != fspath.Canonical(filepath.Join(path, ".git")) {
		return fmt.Errorf("Git metadata points to a different worktree location")
	}
	return nil
}

// RemoveWorktree removes a worktree registration and its directory.
//
// force is passed through to git and will discard modifications, and remove
// a locked worktree; callers are responsible for having established that this
// is safe.
func (g *Git) RemoveWorktree(ctx context.Context, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		// Once discards modifications; a locked worktree takes it twice.
		args = append(args, "--force", "--force")
	}
	args = append(args, path)

	if _, err := g.git(ctx, repo, args...); err != nil {
		if tsexec.IsExit(err) {
			return fmt.Errorf("remove worktree %s: %w", path, err)
		}
		return err
	}
	return nil
}

// MoveWorktree moves a linked worktree of repo, untracked and ignored files
// along with it, and tells git where it went.
func (g *Git) MoveWorktree(ctx context.Context, repo, from, to string) error {
	if _, err := g.git(ctx, repo, "worktree", "move", from, to); err != nil {
		return fmt.Errorf("move worktree %s to %s: %w", from, to, err)
	}
	return nil
}
