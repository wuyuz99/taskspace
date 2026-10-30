package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wuyuz99/taskspace/internal/task"
)

// RenameOptions controls the optional renaming of conventional local branches.
type RenameOptions struct{ Branches bool }

// Rename changes a stopped task's identity, preserving its files and settings.
// Both names stay locked until the operation (or its rollback) is complete.
func (s *Service) Rename(ctx context.Context, old, name string, opts RenameOptions) error {
	for _, n := range []string{old, name} {
		if err := task.ValidateName("task", n); err != nil {
			return err
		}
	}
	if old == name {
		return fmt.Errorf("task is already named %s", old)
	}
	names := []string{old, name}
	sort.Strings(names)
	for _, n := range names {
		next, lock, err := s.Acquire(ctx, n, s.command("tspace task rename"))
		if err != nil {
			return err
		}
		defer lock.Unlock()
		if lock.Inherited() {
			return fmt.Errorf("task rename cannot run from a hook changing %s", n)
		}
		ctx = next
	}
	t, err := s.Store.Load(old)
	if err != nil {
		return err
	}
	if s.Store.Exists(name) {
		return fmt.Errorf("%q: %w", name, task.ErrExists)
	}
	for _, n := range []string{old, name} {
		running, err := s.SessionUp(ctx, n)
		if err != nil {
			return err
		}
		if running {
			return fmt.Errorf("%s is running; run tspace stop %s before renaming", n, n)
		}
	}
	from, to := s.Store.TaskDir(old), s.Store.TaskDir(name)
	for _, p := range []string{to, s.Store.RunDir(name)} {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("%s already exists", p)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if cwd, err := os.Getwd(); err == nil && pathInside(cwd, from) {
		return fmt.Errorf("run task rename from outside %s", from)
	}
	cfg := &task.Config{}
	if opts.Branches {
		cfg, err = s.Store.LoadConfig()
		if err != nil {
			return err
		}
	}
	type plan struct {
		repo, from, to, branch        string
		present, rename, branchExists bool
	}
	var plans []plan
	for _, repo := range t.RepoNames() {
		r := t.Repos[repo]
		if pathInside(r.Path, from) {
			return fmt.Errorf("%s's source repository is inside %s; move it outside before renaming", repo, from)
		}
		expected := s.Store.WorktreePath(old, repo)
		if filepath.Clean(r.Worktree) != filepath.Clean(expected) {
			return fmt.Errorf("%s's worktree is outside its expected path %s", repo, expected)
		}
		p := plan{repo: repo, from: r.Worktree, to: s.Store.WorktreePath(name, repo), branch: r.Branch}
		if info, err := os.Lstat(p.from); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s is not a worktree directory", p.from)
			}
			p.present = true
			wt, err := s.Git.FindWorktree(ctx, r.Path, p.from)
			if err != nil {
				return err
			}
			if wt == nil || wt.Bare || wt.Locked {
				return fmt.Errorf("%s is missing from git's worktrees or is locked", p.from)
			}
			all, err := s.Git.Worktrees(ctx, r.Path)
			if err != nil {
				return err
			}
			if len(all) > 0 && sameDirectory(all[0].Path, p.from) {
				return fmt.Errorf("%s is a main checkout", p.from)
			}
		} else if !os.IsNotExist(err) {
			return err
		} else {
			wt, err := s.Git.FindWorktree(ctx, r.Path, p.from)
			if err != nil {
				return err
			}
			if wt != nil {
				return fmt.Errorf("%s is missing but still registered with git; restore it or prune its registration before renaming", p.from)
			}
		}
		if opts.Branches && r.Branch == cfg.Branch(old) {
			p.branch = cfg.Branch(name)
			if err := s.Git.CheckBranchName(ctx, p.branch); err != nil {
				return err
			}
			p.branchExists, err = s.Git.BranchExists(ctx, r.Path, r.Branch)
			if err != nil {
				return err
			}
			remote, err := s.Git.RemoteBranch(ctx, r.Path, r.Branch)
			if err != nil {
				return err
			}
			upstream, err := s.Git.Upstream(ctx, r.Path, r.Branch)
			if err != nil {
				return err
			}
			if remote != "" || (upstream != "" && upstream != r.Base) {
				s.warnf("%s: keeping published branch %s; rename it explicitly with git branch -m if wanted", repo, r.Branch)
				p.branch = r.Branch
			} else {
				exists, err := s.Git.BranchExists(ctx, r.Path, p.branch)
				if err != nil {
					return err
				}
				if exists {
					return fmt.Errorf("%s: branch %s already exists", repo, p.branch)
				}
				p.rename = true
				wt, err := s.Git.FindWorktreeForBranch(ctx, r.Path, r.Branch)
				if err != nil {
					return err
				}
				if wt != nil && !sameDirectory(wt.Path, p.from) {
					return fmt.Errorf("%s is checked out at %s; rename without --branches", r.Branch, wt.Path)
				}
			}
		} else if opts.Branches {
			s.warnf("%s: keeping custom branch %s", repo, r.Branch)
		}
		plans = append(plans, p)
	}
	// A branch may also be recorded by a cleaned or not-yet-started task.
	// Renaming it must not silently invalidate that task's configuration.
	if opts.Branches {
		tasks, err := s.Store.List()
		if err != nil {
			return fmt.Errorf("check branch ownership: %w", err)
		}
		for _, p := range plans {
			if !p.rename {
				continue
			}
			r := t.Repos[p.repo]
			for _, other := range tasks {
				for repo, claim := range other.Repos {
					if other.Name == old && repo == p.repo {
						continue
					}
					if sameDirectory(claim.Path, r.Path) && (claim.Branch == r.Branch || claim.Base == r.Branch) {
						return fmt.Errorf("branch %s is also recorded by %s's %s; rename without --branches", r.Branch, other.Name, repo)
					}
				}
			}
		}
	}
	// An unclaimed linked worktree must not be moved by a plain directory rename.
	err = filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) && path == from {
			return nil
		}
		if err != nil {
			return err
		}
		for _, p := range plans {
			if path == p.from && p.present {
				return filepath.SkipDir
			}
		}
		if entry.Name() == ".git" {
			return fmt.Errorf("unclaimed repository at %s; import or move it first", filepath.Dir(path))
		}
		if path == from && entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("task directory %s is a symlink", from)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Migrate legacy notes at the old path before beginning the transaction.
	// Publishing the renamed record must not create files outside the undo log.
	if t.Outdated() {
		if err := s.Store.Save(t); err != nil {
			return err
		}
	}
	s.warnMovedConversations(t, from)
	var undo []func() error
	rollback := func(cause error) error {
		for i := len(undo) - 1; i >= 0; i-- {
			if err := undo[i](); err != nil {
				cause = errors.Join(cause, fmt.Errorf("rollback: %w", err))
			}
		}
		return cause
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o755)
	if info, err := os.Stat(from); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Mkdir(to, mode); err != nil {
		return err
	}
	undo = append(undo, func() error { return os.Remove(to) })
	// Rollback ignores cancellation so an interrupted command can restore paths.
	backCtx := context.WithoutCancel(ctx)
	for _, p := range plans {
		r := t.Repos[p.repo]
		oldBranch := r.Branch
		if p.present {
			if err := s.Git.MoveWorktree(ctx, r.Path, p.from, p.to); err != nil {
				return rollback(err)
			}
			undo = append(undo, func() error { return s.Git.MoveWorktree(backCtx, r.Path, p.to, p.from) })
		}
		if p.rename && p.branchExists {
			if err := s.Git.RenameBranch(ctx, r.Path, r.Branch, p.branch); err != nil {
				return rollback(err)
			}
			undo = append(undo, func() error { return s.Git.RenameBranch(backCtx, r.Path, p.branch, oldBranch) })
		}
		r.Worktree, r.Branch = p.to, p.branch
		t.Repos[p.repo] = r
	}
	entries, err := os.ReadDir(from)
	if err != nil && !os.IsNotExist(err) {
		return rollback(err)
	}
	for _, entry := range entries {
		src, dst := filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())
		if _, err := os.Lstat(dst); err == nil {
			return rollback(fmt.Errorf("%s already exists", dst))
		} else if !os.IsNotExist(err) {
			return rollback(err)
		}
		if err := os.Rename(src, dst); err != nil {
			return rollback(err)
		}
		undo = append(undo, func() error { return os.Rename(dst, src) })
	}
	oldRun, newRun := s.Store.RunDir(old), s.Store.RunDir(name)
	if _, err := os.Lstat(oldRun); err == nil {
		if err := os.Rename(oldRun, newRun); err != nil {
			return rollback(err)
		}
		undo = append(undo, func() error { return os.Rename(newRun, oldRun) })
	} else if !os.IsNotExist(err) {
		return rollback(err)
	}
	t.Name = name
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := s.Store.CommitRename(old, t); err != nil {
		return rollback(err)
	}
	if err := os.Remove(from); err != nil && !os.IsNotExist(err) {
		s.warnf("renamed task, but could not remove empty directory %s: %v", from, err)
	}
	return nil
}

func pathInside(path, root string) bool {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	if p, err := filepath.EvalSymlinks(root); err == nil {
		root = p
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sameDirectory(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

func (s *Service) warnMovedConversations(t *task.Task, root string) {
	windows := append([]string{task.RootWindowName}, t.RepoNames()...)
	for _, window := range windows {
		if len(t.WindowAgent(window)) == 0 || len(t.ResumeFor(window)) == 0 || !s.Store.Launched(t.Name, window) {
			continue
		}
		dir := root
		if window != task.RootWindowName {
			dir = t.Repos[window].Worktree
			if _, err := os.Stat(dir); err != nil {
				continue
			}
		}
		s.warnf("note: %s's %s agent resumes by directory; its conversation from %s stays there", t.Name, window, dir)
	}
}
