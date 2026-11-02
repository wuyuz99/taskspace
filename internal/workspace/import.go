package workspace

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wuyuz99/taskspace/internal/fspath"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// ImportOptions say what Import takes, and where it goes.
type ImportOptions struct {
	// Paths are worktrees made outside tspace.
	Paths []string
	// Task is an existing task or a new one's name; empty, a new task named
	// after the first worktree's directory.
	Task string
	// Name names the repository, for one path.
	Name string
	// WithRoot gives a new task a root window, even with one repository.
	WithRoot bool
}

// ImportedWorktree is a worktree Import adopted.
type ImportedWorktree struct {
	// From is where it was, To where it is now, equal for one adopted in place.
	From, To string
	InPlace  bool
	Branch   string
	// Status is what it held when it was inspected.
	Status tsgit.Status
}

// ImportResult is what Import did.
type ImportResult struct {
	Task      *task.Task
	Worktrees []ImportedWorktree
}

// importSource is a worktree Import has checked, and where it goes.
type importSource struct {
	top, main, branch, base string
	status                  tsgit.Status
	name, dest              string
	inPlace                 bool
}

// Import makes worktrees created outside tspace a task's: it moves each into
// the task's directory and records its repository, keeping its branch. All
// are checked before any moves, a failure moves back the ones already moved,
// and a task made for the import does not outlive one that fails. What each
// repository reported is held until the record is saved.
func (s *Service) Import(ctx context.Context, o ImportOptions) (*ImportResult, error) {
	if len(o.Paths) > 1 && o.Task == "" {
		return nil, fmt.Errorf("several worktrees need a task to go into; name it with --task")
	}
	if len(o.Paths) > 1 && o.Name != "" {
		return nil, fmt.Errorf("--name names one repository; import that one on its own")
	}
	nameHint := "name this one with --name"
	if len(o.Paths) > 1 {
		nameHint = "import this one on its own with --name"
	}
	tasks, err := s.Store.List()
	if err != nil {
		return nil, fmt.Errorf("check worktree ownership: %w", err)
	}
	sources := make([]*importSource, 0, len(o.Paths))
	for _, arg := range o.Paths {
		src, err := s.inspectImport(ctx, arg, tasks)
		if err != nil {
			return nil, err
		}
		src.name = cmp.Or(o.Name, filepath.Base(src.main))
		if err := task.ValidateName("repo", src.name); err != nil {
			return nil, fmt.Errorf("%w; %s", err, nameHint)
		}
		for _, other := range sources {
			if fspath.Same(other.main, src.main) {
				return nil, fmt.Errorf("%s and %s are both worktrees of %s, and a task has one worktree of a repository", other.top, src.top, src.main)
			}
			// Their directories may differ only in case, which is one
			// directory on macOS.
			if strings.EqualFold(other.name, src.name) {
				return nil, fmt.Errorf("%s and %s would both be repository %s; %s", other.top, src.top, src.name, nameHint)
			}
		}
		sources = append(sources, src)
	}

	taskName, joining := o.Task, o.Task != ""
	if !joining {
		taskName = filepath.Base(sources[0].top)
	}
	if err := task.ValidateName("task", taskName); err != nil {
		return nil, fmt.Errorf("%w; name the task with --task", err)
	}
	if !joining && s.Store.Exists(taskName) {
		return nil, fmt.Errorf("a task named %s exists already; to add the worktree to it, run: tspace import %s --task %s", taskName, o.Paths[0], taskName)
	}

	created := false
	if !s.Store.Exists(taskName) {
		if _, err := s.CreateTask(ctx, CreateTaskOptions{Name: taskName, WithRoot: o.WithRoot}); err != nil {
			return nil, err
		}
		created = true
	} else if o.WithRoot {
		return nil, fmt.Errorf("--with-root is for a new task, and %s exists", taskName)
	}
	// A new task is not left behind by an import that fails.
	undo := func(err error) error {
		if created {
			if t, lerr := s.Store.Load(taskName); lerr == nil {
				_ = s.Delete(t)
			}
			_ = os.Remove(s.Store.TaskDir(taskName))
		}
		return err
	}
	t, ctx, unlock, err := s.Lock(ctx, taskName, s.command("tspace import"), true)
	if err != nil {
		return nil, undo(err)
	}
	defer unlock()
	c, err := s.Store.LoadConfig()
	if err != nil {
		return nil, undo(err)
	}
	// Inspected before the lock, so a task may have claimed one since.
	if tasks, err = s.Store.List(); err != nil {
		return nil, undo(fmt.Errorf("check worktree ownership: %w", err))
	}
	for _, src := range sources {
		if err := claimed(tasks, src.top); err != nil {
			return nil, undo(err)
		}
		if _, ok := t.Repo(src.name); ok {
			return nil, undo(fmt.Errorf("task %s already has a repo named %s; %s", t.Name, src.name, nameHint))
		}
		for _, other := range t.RepoNames() {
			if fspath.Same(t.Repos[other].Path, src.main) {
				return nil, undo(fmt.Errorf("%s is already attached to task %q as %q", src.main, t.Name, other))
			}
		}
		src.dest = s.Store.WorktreePath(t.Name, src.name)
		src.inPlace = fspath.Same(src.top, src.dest)
		if src.inPlace {
			continue
		}
		if _, err := os.Lstat(src.dest); err == nil {
			return nil, undo(fmt.Errorf("%s already exists; it is where %s's worktree goes", src.dest, src.name))
		} else if !os.IsNotExist(err) {
			return nil, undo(err)
		}
	}

	// Each report is held until the record is saved, so a later worktree that
	// fails does not follow "added backend".
	var held []Event
	adding := *s
	adding.Progress = func(e Event) {
		if _, warning := e.(Warning); warning {
			s.report(e)
			return
		}
		held = append(held, e)
	}

	// Rollback ignores cancellation so an interrupted command can restore paths.
	backCtx := context.WithoutCancel(ctx)
	var moved []*importSource
	back := func(err error) error {
		stranded := false
		for _, m := range slices.Backward(moved) {
			if merr := s.Git.MoveWorktree(backCtx, m.main, m.dest, m.top); merr != nil {
				err = fmt.Errorf("%w; and the worktree could not be moved back from %s (%v)", err, m.dest, merr)
				stranded = true
			}
		}
		if stranded {
			return err
		}
		return undo(err)
	}
	// Moved first, then recorded as any repository is: one whose branch is
	// checked out at its own place in the task.
	first := len(t.Repos) == 0
	for _, src := range sources {
		if err := os.MkdirAll(filepath.Dir(src.dest), 0o755); err != nil {
			return nil, back(err)
		}
		if !src.inPlace {
			if err := s.Git.MoveWorktree(ctx, src.main, src.top, src.dest); err != nil {
				return nil, back(err)
			}
			moved = append(moved, src)
		}
		if err := adding.AddRepo(ctx, t, c, src.main, AddRepoOptions{Name: src.name, Branch: src.branch, Base: src.base}); err != nil {
			return nil, back(err)
		}
	}
	openRoot := adding.SettleLayout(ctx, t, first)
	if err := s.Store.Save(t); err != nil {
		return nil, back(err)
	}
	for _, e := range held {
		s.report(e)
	}
	if openRoot {
		s.OpenRootWindow(ctx, t)
	}
	res := &ImportResult{Task: t}
	for _, src := range sources {
		res.Worktrees = append(res.Worktrees, ImportedWorktree{From: src.top, To: src.dest, InPlace: src.inPlace, Branch: src.branch, Status: src.status})
	}
	return res, nil
}

// claimed refuses a worktree that is one of tasks' repositories.
func claimed(tasks []*task.Task, top string) error {
	for _, owner := range tasks {
		for _, repo := range owner.RepoNames() {
			if wt := owner.Repos[repo].Worktree; wt != "" && fspath.Same(wt, top) {
				return fmt.Errorf("%s is %s's %s; run tspace repo rm %s %s --keep-worktree before importing it", top, owner.Name, repo, owner.Name, repo)
			}
		}
	}
	return nil
}

// inspectImport finds the worktree at arg and refuses one that import cannot
// take, changing nothing. tasks are the tasks that might claim it.
func (s *Service) inspectImport(ctx context.Context, arg string, tasks []*task.Task) (*importSource, error) {
	path, err := filepath.Abs(ExpandHome(arg))
	if err != nil {
		return nil, err
	}
	// The worktree's top, and nothing unfinished in it.
	top, err := s.Git.Validate(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := claimed(tasks, top); err != nil {
		return nil, err
	}
	// A directory in use cannot be moved on Windows, and elsewhere the
	// shell would be left in one that is gone.
	if cwd, err := os.Getwd(); err == nil && fspath.Within(cwd, top) {
		return nil, fmt.Errorf("run import from outside %s, which it moves", top)
	}
	list, err := s.Git.Worktrees(ctx, top)
	if err != nil {
		return nil, err
	}
	switch {
	case len(list) == 0:
		return nil, fmt.Errorf("%s: git lists no worktrees", top)
	case list[0].Bare:
		return nil, fmt.Errorf("%s belongs to the bare repository %s; tspace needs a repository with a checkout of its own", top, list[0].Path)
	case fspath.Same(list[0].Path, top):
		return nil, fmt.Errorf("%s is its repository's main checkout, which cannot be moved; register it with tspace config repo NAME %s, and a task makes its own worktree", top, top)
	}
	main, err := s.Git.Validate(ctx, filepath.FromSlash(list[0].Path))
	if err != nil {
		return nil, err
	}
	branch, found := "", false
	for _, w := range list {
		if !fspath.Same(w.Path, top) {
			continue
		}
		switch {
		case w.Detached || w.Branch == "":
			return nil, fmt.Errorf("%s has a detached HEAD; check out a branch there first, so the task has one", top)
		case w.Locked:
			return nil, fmt.Errorf("%s is locked (%s); unlock it with git worktree unlock first", top, cmp.Or(w.LockReason, "-"))
		}
		branch, found = w.Branch, true
	}
	if !found {
		return nil, fmt.Errorf("%s is not one of %s's worktrees", top, main)
	}
	st, err := s.Git.WorktreeStatus(ctx, top)
	if err != nil {
		return nil, err
	}
	if n := st.Conflicted; n > 0 {
		return nil, fmt.Errorf("%s has %d conflicted file%s; resolve them first", top, n, Plural(n))
	}
	// Its own remote copy, as after git push -u, is where the work is
	// published, not where it started.
	base := st.Upstream
	if strings.HasSuffix(base, "/"+branch) {
		base = ""
	}
	return &importSource{top: top, main: main, branch: branch, base: base, status: st}, nil
}
