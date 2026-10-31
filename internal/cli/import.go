package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// importSource is a worktree import has checked, and where it goes.
type importSource struct {
	top, main, branch, base string
	status                  git.Status
	name, dest              string
	inPlace                 bool
}

func (a *App) newImportCmd() *cobra.Command {
	var taskName, name string
	var withRoot bool
	cmd := &cobra.Command{
		Use:   "import PATH... [--task NAME] [--name REPO] [--with-root]",
		Short: "Adopt existing git worktrees into a task",
		Long: "import makes a worktree you created yourself, with `git worktree add`, a\n" +
			"task's: it moves it into the task's directory, with `git worktree move`,\n" +
			"and records its repository, keeping its branch. Uncommitted changes, and\n" +
			"untracked and ignored files, go with it, so a .env or node_modules is\n" +
			"still there; nothing is committed.\n\n" +
			"  tspace import ../backend-fix                  a new task named backend-fix\n" +
			"  tspace import ~/wt/api-login --task PAY-142   into an existing task\n" +
			"  tspace import ../api ../web --task PAY-142    several, all or none\n\n" +
			"It refuses a worktree with conflicted files, a detached HEAD, an unfinished\n" +
			"merge or rebase, or a lock; one that is already a task's; and a repository's\n" +
			"main checkout, which cannot move: register that with `tspace config repo`\n" +
			"and let a task make its own worktree. Run it from\n" +
			"outside the worktree. Commits not pushed, or not pulled, are reported, and\n" +
			"kept. The branch's base is its upstream if that is another branch, else the\n" +
			"repository's default; `tspace repo update` changes it.\n\n" +
			"Without --task, the task is new, named after the worktree's directory. The\n" +
			"repository is named after its source repository's directory, or --name.\n" +
			"Nothing is started: `tspace start` or `open` brings up the session.\n\n" +
			"Several paths need --task, and each repository is named after its source;\n" +
			"--name is for one path. All are checked before any moves, and a failure\n" +
			"moves back the ones already moved. As with `tspace task create`, a task\n" +
			"with no repositories gets its agent in the worktree of a single import, and\n" +
			"at the task root with several, or with --with-root, which is for a new\n" +
			"task. A task that has repositories keeps its agent where it is.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) > 1 && taskName == "" {
				return fmt.Errorf("several worktrees need a task to go into; name it with --task")
			}
			if len(args) > 1 && name != "" {
				return fmt.Errorf("--name names one repository; import that one on its own")
			}
			nameHint := "name this one with --name"
			if len(args) > 1 {
				nameHint = "import this one on its own with --name"
			}
			tasks, err := a.Store.List()
			if err != nil {
				return fmt.Errorf("check worktree ownership: %w", err)
			}
			sources := make([]*importSource, 0, len(args))
			for _, arg := range args {
				s, err := a.inspectImport(ctx, arg, tasks)
				if err != nil {
					return err
				}
				s.name = firstNonEmpty(name, filepath.Base(s.main))
				if err := task.ValidateName("repo", s.name); err != nil {
					return fmt.Errorf("%w; %s", err, nameHint)
				}
				for _, other := range sources {
					if samePath(other.main, s.main) {
						return fmt.Errorf("%s and %s are both worktrees of %s, and a task has one worktree of a repository", other.top, s.top, s.main)
					}
					// Their directories may differ only in case, which is
					// one directory on macOS.
					if strings.EqualFold(other.name, s.name) {
						return fmt.Errorf("%s and %s would both be repository %s; %s", other.top, s.top, s.name, nameHint)
					}
				}
				sources = append(sources, s)
			}

			joining := taskName != ""
			if !joining {
				taskName = filepath.Base(sources[0].top)
			}
			if err := task.ValidateName("task", taskName); err != nil {
				return fmt.Errorf("%w; name the task with --task", err)
			}
			if !joining && a.Store.Exists(taskName) {
				return fmt.Errorf("a task named %s exists already; to add the worktree to it, run: tspace import %s --task %s", taskName, args[0], taskName)
			}

			created := false
			if !a.Store.Exists(taskName) {
				create := []string{"create", taskName}
				if withRoot {
					create = append(create, "--with-root")
				}
				if err := a.runCmd(ctx, (*App).newTaskCmd, create...); err != nil {
					return err
				}
				created = true
			} else if withRoot {
				return fmt.Errorf("--with-root is for a new task, and %s exists", taskName)
			}
			// A new task is not left behind by an import that fails.
			undo := func(err error) error {
				if created {
					if t, lerr := a.Store.Load(taskName); lerr == nil {
						_ = a.workspace().Delete(t)
					}
					_ = os.Remove(a.Store.TaskDir(taskName))
				}
				return err
			}
			t, unlock, err := a.lockTask(cmd, taskName, true)
			if err != nil {
				return undo(err)
			}
			defer unlock()
			c, err := a.Store.LoadConfig()
			if err != nil {
				return undo(err)
			}
			for _, s := range sources {
				if _, ok := t.Repo(s.name); ok {
					return undo(fmt.Errorf("task %s already has a repo named %s; %s", t.Name, s.name, nameHint))
				}
				for _, other := range t.RepoNames() {
					if samePath(t.Repos[other].Path, s.main) {
						return undo(fmt.Errorf("%s is already attached to task %q as %q", s.main, t.Name, other))
					}
				}
				s.dest = a.Store.WorktreePath(t.Name, s.name)
				s.inPlace = samePath(s.top, s.dest)
				if s.inPlace {
					continue
				}
				if _, err := os.Lstat(s.dest); err == nil {
					return undo(fmt.Errorf("%s already exists; it is where %s's worktree goes", s.dest, s.name))
				} else if !os.IsNotExist(err) {
					return undo(err)
				}
			}

			// Each report is held until the record is saved, so a later
			// worktree that fails does not follow "added backend".
			out := a.Out
			var added bytes.Buffer
			a.Out = &added
			defer func() { a.Out = out }()

			// Rollback ignores cancellation so an interrupted command can restore paths.
			backCtx := context.WithoutCancel(ctx)
			var moved []*importSource
			back := func(err error) error {
				a.Out = out
				stranded := false
				for _, s := range slices.Backward(moved) {
					if merr := a.Git.MoveWorktree(backCtx, s.main, s.dest, s.top); merr != nil {
						err = fmt.Errorf("%w; and the worktree could not be moved back from %s (%v)", err, s.dest, merr)
						stranded = true
					}
				}
				if stranded {
					return err
				}
				return undo(err)
			}
			// Moved first, then recorded as any repository is: one whose
			// branch is checked out at its own place in the task.
			first := len(t.Repos) == 0
			for _, s := range sources {
				if err := os.MkdirAll(filepath.Dir(s.dest), 0o755); err != nil {
					return back(err)
				}
				if !s.inPlace {
					if err := a.Git.MoveWorktree(ctx, s.main, s.top, s.dest); err != nil {
						return back(err)
					}
					moved = append(moved, s)
				}
				if err := a.addRepo(ctx, t, c, s.main, addRepoOptions{Name: s.name, Branch: s.branch, Base: s.base}); err != nil {
					return back(err)
				}
			}
			ws := a.workspace()
			openRoot := ws.SettleLayout(ctx, t, first)
			if err := a.Store.Save(t); err != nil {
				return back(err)
			}
			a.Out = out
			// Saved by now, so an output that fails does not stop the rest.
			_, _ = added.WriteTo(a.Out)
			if openRoot {
				ws.OpenRootWindow(ctx, t)
			}
			for _, s := range sources {
				if s.inPlace {
					a.printf("adopted %s in place\n", s.top)
				} else {
					a.printf("moved %s to %s\n", s.top, s.dest)
				}
				st := s.status
				if n := st.Staged + st.Modified; n > 0 {
					a.printf("  %d uncommitted change%s kept; nothing was committed\n", n, plural(n))
				}
				if st.Upstream != "" && (st.Ahead > 0 || st.Behind > 0) {
					a.printf("  %s is %d ahead and %d behind %s; nothing was pushed or pulled\n", s.branch, st.Ahead, st.Behind, st.Upstream)
				}
			}
			a.printf("start it with: tspace open %s\n", t.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskName, "task", "", "an existing task to add it to, or the new task's name (default: the worktree's directory name)")
	cmd.Flags().StringVar(&name, "name", "", "repo name, with one path (default: the source repository's directory name)")
	cmd.Flags().BoolVar(&withRoot, "with-root", false, "give a new task a task-root window for its agent, even with one repository")
	return cmd
}

// inspectImport finds the worktree at arg and refuses one that import cannot
// take, changing nothing. tasks are the tasks that might claim it.
func (a *App) inspectImport(ctx context.Context, arg string, tasks []*task.Task) (*importSource, error) {
	path, err := filepath.Abs(expandHome(arg))
	if err != nil {
		return nil, err
	}
	// The worktree's top, and nothing unfinished in it.
	top, err := a.Git.Validate(ctx, path)
	if err != nil {
		return nil, err
	}
	for _, owner := range tasks {
		for _, repo := range owner.RepoNames() {
			if wt := owner.Repos[repo].Worktree; wt != "" && samePath(wt, top) {
				return nil, fmt.Errorf("%s is %s's %s; run tspace repo rm %s %s --keep-worktree before importing it", top, owner.Name, repo, owner.Name, repo)
			}
		}
	}
	// A directory in use cannot be moved on Windows, and elsewhere the
	// shell would be left in one that is gone.
	if cwd, err := os.Getwd(); err == nil && within(cwd, top) {
		return nil, fmt.Errorf("run import from outside %s, which it moves", top)
	}
	list, err := a.Git.Worktrees(ctx, top)
	if err != nil {
		return nil, err
	}
	switch {
	case len(list) == 0:
		return nil, fmt.Errorf("%s: git lists no worktrees", top)
	case list[0].Bare:
		return nil, fmt.Errorf("%s belongs to the bare repository %s; tspace needs a repository with a checkout of its own", top, list[0].Path)
	case samePath(list[0].Path, top):
		return nil, fmt.Errorf("%s is its repository's main checkout, which cannot be moved; register it with tspace config repo NAME %s, and a task makes its own worktree", top, top)
	}
	main, err := a.Git.Validate(ctx, filepath.FromSlash(list[0].Path))
	if err != nil {
		return nil, err
	}
	branch, found := "", false
	for _, w := range list {
		if !samePath(w.Path, top) {
			continue
		}
		switch {
		case w.Detached || w.Branch == "":
			return nil, fmt.Errorf("%s has a detached HEAD; check out a branch there first, so the task has one", top)
		case w.Locked:
			return nil, fmt.Errorf("%s is locked (%s); unlock it with git worktree unlock first", top, dash(w.LockReason))
		}
		branch, found = w.Branch, true
	}
	if !found {
		return nil, fmt.Errorf("%s is not one of %s's worktrees", top, main)
	}
	st, err := a.Git.WorktreeStatus(ctx, top)
	if err != nil {
		return nil, err
	}
	if n := st.Conflicted; n > 0 {
		return nil, fmt.Errorf("%s has %d conflicted file%s; resolve them first", top, n, plural(n))
	}
	// Its own remote copy, as after git push -u, is where the work
	// is published, not where it started.
	base := st.Upstream
	if strings.HasSuffix(base, "/"+branch) {
		base = ""
	}
	return &importSource{top: top, main: main, branch: branch, base: base, status: st}, nil
}
