package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/wuyuz99/taskspace/internal/fspath"
	"github.com/wuyuz99/taskspace/internal/task"
)

// RenameOptions controls the optional renaming of conventional local branches.
type RenameOptions struct{ Branches bool }

// renamePlan is what Rename will do, decided before anything moves.
type renamePlan struct {
	t         *task.Task
	old, name string
	// from and to are the task directories.
	from, to string
	cfg      *task.Config
	repos    []renameRepo
}

// renameRepo is one repository's part of a rename: its worktree, if present,
// moves, and its branch is renamed if rename is set.
type renameRepo struct {
	repo, from, to, branch        string
	present, rename, branchExists bool
}

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
	ctx, unlock, err := s.acquireBoth(ctx, s.command("tspace task rename"), old, name)
	if err != nil {
		return err
	}
	defer unlock()
	// The plan is made under the lock it is carried out under, so nothing
	// changes in between.
	p, err := s.planRename(ctx, old, name, opts)
	if err != nil {
		return err
	}
	return s.renameTask(ctx, p)
}

// planRename checks that old can become name and decides what moves.
func (s *Service) planRename(ctx context.Context, old, name string, opts RenameOptions) (*renamePlan, error) {
	t, err := s.Store.Load(old)
	if err != nil {
		return nil, err
	}
	p := &renamePlan{t: t, old: old, name: name, from: s.Store.TaskDir(old), to: s.Store.TaskDir(name)}
	if err := s.checkRenameable(ctx, p); err != nil {
		return nil, err
	}
	if opts.Branches {
		if p.cfg, err = s.Store.LoadConfig(); err != nil {
			return nil, err
		}
	} else {
		p.cfg = s.conversationConfig()
	}
	for _, repo := range t.RepoNames() {
		r, err := s.planRenameRepo(ctx, p, repo, opts)
		if err != nil {
			return nil, err
		}
		p.repos = append(p.repos, r)
	}
	if opts.Branches {
		if err := s.checkBranchesUnclaimed(p); err != nil {
			return nil, err
		}
	}
	if err := checkNothingUnclaimed(p); err != nil {
		return nil, err
	}
	return p, nil
}

// checkRenameable refuses a rename onto a task or path that exists, of a
// running task, or from inside the task.
func (s *Service) checkRenameable(ctx context.Context, p *renamePlan) error {
	if s.Store.Exists(p.name) {
		return fmt.Errorf("%q: %w", p.name, task.ErrExists)
	}
	for _, n := range []string{p.old, p.name} {
		running, err := s.SessionUp(ctx, n)
		if err != nil {
			return err
		}
		if running {
			return fmt.Errorf("%s is running; run tspace stop %s before renaming", n, n)
		}
	}
	for _, path := range []string{p.to, s.Store.RunDir(p.name)} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if cwd, err := os.Getwd(); err == nil && fspath.Within(cwd, p.from) {
		return fmt.Errorf("run task rename from outside %s", p.from)
	}
	return nil
}

// planRenameRepo decides what happens to one repository: its worktree moves
// with the task, and with --branches a conventional, unpublished branch is
// renamed too.
func (s *Service) planRenameRepo(ctx context.Context, p *renamePlan, repo string, opts RenameOptions) (renameRepo, error) {
	r := p.t.Repos[repo]
	if fspath.Within(r.Path, p.from) {
		return renameRepo{}, fmt.Errorf("%s's source repository is inside %s; move it outside before renaming", repo, p.from)
	}
	expected := s.Store.WorktreePath(p.old, repo)
	if filepath.Clean(r.Worktree) != filepath.Clean(expected) {
		return renameRepo{}, fmt.Errorf("%s's worktree is outside its expected path %s", repo, expected)
	}
	rr := renameRepo{repo: repo, from: r.Worktree, to: s.Store.WorktreePath(p.name, repo), branch: r.Branch}
	var err error
	if rr.present, err = s.movableWorktree(ctx, r.Path, rr.from); err != nil {
		return renameRepo{}, err
	}
	// A reference has no branch to rename.
	if !opts.Branches || r.Reference {
		return rr, nil
	}
	if r.Branch != p.cfg.Branch(p.old) {
		s.warnf("%s: keeping custom branch %s", repo, r.Branch)
		return rr, nil
	}
	rr.branch = p.cfg.Branch(p.name)
	if err := s.Git.CheckBranchName(ctx, rr.branch); err != nil {
		return renameRepo{}, err
	}
	if rr.branchExists, err = s.Git.BranchExists(ctx, r.Path, r.Branch); err != nil {
		return renameRepo{}, err
	}
	remote, err := s.Git.RemoteBranch(ctx, r.Path, r.Branch)
	if err != nil {
		return renameRepo{}, err
	}
	upstream, err := s.Git.Upstream(ctx, r.Path, r.Branch)
	if err != nil {
		return renameRepo{}, err
	}
	if remote != "" || (upstream != "" && upstream != r.Base) {
		s.warnf("%s: keeping published branch %s; rename it explicitly with git branch -m if wanted", repo, r.Branch)
		rr.branch = r.Branch
		return rr, nil
	}
	exists, err := s.Git.BranchExists(ctx, r.Path, rr.branch)
	if err != nil {
		return renameRepo{}, err
	}
	if exists {
		return renameRepo{}, fmt.Errorf("%s: branch %s already exists", repo, rr.branch)
	}
	rr.rename = true
	wt, err := s.Git.FindWorktreeForBranch(ctx, r.Path, r.Branch)
	if err != nil {
		return renameRepo{}, err
	}
	if wt != nil && !fspath.Same(wt.Path, rr.from) {
		return renameRepo{}, fmt.Errorf("%s is checked out at %s; rename without --branches", r.Branch, wt.Path)
	}
	return rr, nil
}

// checkBranchesUnclaimed refuses to rename a branch another task's record
// names, a cleaned or not-yet-started one included: that task's configuration
// must not silently stop matching.
func (s *Service) checkBranchesUnclaimed(p *renamePlan) error {
	tasks, err := s.Store.List()
	if err != nil {
		return fmt.Errorf("check branch ownership: %w", err)
	}
	for _, rr := range p.repos {
		if !rr.rename {
			continue
		}
		r := p.t.Repos[rr.repo]
		for _, other := range tasks {
			for repo, claim := range other.Repos {
				if other.Name == p.old && repo == rr.repo {
					continue
				}
				if fspath.Same(claim.Path, r.Path) && (claim.Branch == r.Branch || claim.Base == r.Branch) {
					return fmt.Errorf("branch %s is also recorded by %s's %s; rename without --branches", r.Branch, other.Name, repo)
				}
			}
		}
	}
	return nil
}

// checkNothingUnclaimed refuses a task directory holding a repository the
// task doesn't know: a plain directory rename would move it without git.
func checkNothingUnclaimed(p *renamePlan) error {
	return filepath.WalkDir(p.from, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) && path == p.from {
			return nil
		}
		if err != nil {
			return err
		}
		for _, rr := range p.repos {
			if path == rr.from && rr.present {
				return filepath.SkipDir
			}
		}
		if entry.Name() == ".git" {
			return fmt.Errorf("unclaimed repository at %s; import or move it first", filepath.Dir(path))
		}
		if path == p.from && entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("task directory %s is a symlink", p.from)
		}
		return nil
	})
}

// renameTask carries out p, undoing what it did if a step fails.
func (s *Service) renameTask(ctx context.Context, p *renamePlan) error {
	t, old, name, from, to := p.t, p.old, p.name, p.from, p.to
	// Without a carrier, say before anything moves what stays behind.
	carriers := s.carriers(p.cfg)
	resumed := s.resumedByDirectory(t, from)
	if len(carriers) == 0 {
		s.warnConversationsLeft(old, resumed, nil)
	}
	moved := []movedDir{newMovedDir(task.RootWindowName, from, to)}
	for _, rr := range p.repos {
		if rr.present {
			moved = append(moved, newMovedDir(rr.repo, rr.from, rr.to))
		}
	}
	var undo undoLog
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
	for _, rr := range p.repos {
		r := t.Repos[rr.repo]
		oldBranch := r.Branch
		if rr.present {
			if err := s.Git.MoveWorktree(ctx, r.Path, rr.from, rr.to); err != nil {
				return undo.rollback(err)
			}
			undo = append(undo, func() error { return s.Git.MoveWorktree(backCtx, r.Path, rr.to, rr.from) })
		}
		if rr.rename && rr.branchExists {
			if err := s.Git.RenameBranch(ctx, r.Path, r.Branch, rr.branch); err != nil {
				return undo.rollback(err)
			}
			undo = append(undo, func() error { return s.Git.RenameBranch(backCtx, r.Path, rr.branch, oldBranch) })
		}
		r.Worktree, r.Branch = rr.to, rr.branch
		t.Repos[rr.repo] = r
	}
	if err := s.moveTaskFiles(&undo, old, name, from, to); err != nil {
		return undo.rollback(err)
	}
	t.Name = name
	if err := ctx.Err(); err != nil {
		return undo.rollback(err)
	}
	if err := s.Store.CommitRename(old, t); err != nil {
		return undo.rollback(err)
	}
	if err := os.Remove(from); err != nil && !os.IsNotExist(err) {
		s.warnf("renamed task, but could not remove empty directory %s: %v", from, err)
	}
	// History follows only a rename that happened, and never undoes it.
	if len(carriers) > 0 {
		s.warnConversationsLeft(name, resumed, s.carryConversations(name, carriers, moved))
	}
	return nil
}

// moveTaskFiles moves what else the task directory holds, and the task's
// session memory, logging how to undo each move.
func (s *Service) moveTaskFiles(undo *undoLog, old, name, from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		src, dst := filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("%s already exists", dst)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		*undo = append(*undo, func() error { return os.Rename(dst, src) })
	}
	oldRun, newRun := s.Store.RunDir(old), s.Store.RunDir(name)
	if _, err := os.Lstat(oldRun); err == nil {
		if err := os.Rename(oldRun, newRun); err != nil {
			return err
		}
		*undo = append(*undo, func() error { return os.Rename(newRun, oldRun) })
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// acquireBoth takes two tasks' locks for a lifecycle operation, always in the
// same order, so two operations on the same pair cannot deadlock. Both stay
// held until unlock, which is what carries an operation through its rollback.
func (s *Service) acquireBoth(ctx context.Context, command, x, y string) (context.Context, func(), error) {
	names := []string{x, y}
	sort.Strings(names)
	var held []*task.Lock
	unlock := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Unlock()
		}
	}
	for _, n := range names {
		next, lock, err := s.Acquire(ctx, n, command)
		if err != nil {
			unlock()
			return ctx, nil, err
		}
		held = append(held, lock)
		if lock.Inherited() {
			unlock()
			return ctx, nil, fmt.Errorf("%s cannot run from a hook changing %s", command, n)
		}
		ctx = next
	}
	return ctx, unlock, nil
}

// movableWorktree reports whether a worktree of repo is at path, refusing
// what git would not move, or what would leave its registration behind.
func (s *Service) movableWorktree(ctx context.Context, repo, path string) (present bool, err error) {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	wt, ferr := s.Git.FindWorktree(ctx, repo, path)
	if ferr != nil {
		return false, ferr
	}
	if err != nil {
		if wt != nil {
			return false, fmt.Errorf("%s is missing but still registered with git; restore it or prune its registration first", path)
		}
		return false, nil
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s is not a worktree directory", path)
	}
	if wt == nil || wt.Bare || wt.Locked {
		return false, fmt.Errorf("%s is missing from git's worktrees or is locked", path)
	}
	all, err := s.Git.Worktrees(ctx, repo)
	if err != nil {
		return false, err
	}
	if len(all) > 0 && fspath.Same(all[0].Path, path) {
		return false, fmt.Errorf("%s is a main checkout", path)
	}
	return true, nil
}

// undoLog holds how to undo each step of an operation done so far.
type undoLog []func() error

// rollback undoes the steps, last first, and returns cause with anything that
// could not be undone.
func (u undoLog) rollback(cause error) error {
	for i := len(u) - 1; i >= 0; i-- {
		if err := u[i](); err != nil {
			cause = errors.Join(cause, fmt.Errorf("rollback: %w", err))
		}
	}
	return cause
}
