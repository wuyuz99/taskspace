package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newImportCmd() *cobra.Command {
	var taskName, name string
	cmd := &cobra.Command{
		Use:   "import PATH [--task NAME] [--name REPO]",
		Short: "Adopt an existing git worktree into a task",
		Long: "import makes a worktree you created yourself, with `git worktree add`, a\n" +
			"task's: it moves it into the task's directory, with `git worktree move`,\n" +
			"and records its repository, keeping its branch. Untracked and ignored\n" +
			"files go with it, so a .env or node_modules is still there.\n\n" +
			"  tspace import ../backend-fix                  a new task named backend-fix\n" +
			"  tspace import ~/wt/api-login --task PAY-142   into an existing task\n\n" +
			"It refuses a worktree with uncommitted changes to tracked files, a detached\n" +
			"HEAD, an unfinished merge or rebase, or a lock; one that is already a\n" +
			"task's; and a repository's main checkout, which cannot move: register that\n" +
			"with `tspace config repo` and let a task make its own worktree. Run it from\n" +
			"outside the worktree. Commits not pushed, or not pulled, are reported, and\n" +
			"kept. The branch's base is its upstream if that is another branch, else the\n" +
			"repository's default; `tspace repo update` changes it.\n\n" +
			"Without --task, the task is new, named after the worktree's directory. The\n" +
			"repository is named after its source repository's directory, or --name.\n" +
			"Nothing is started: `tspace start` or `open` brings up the session.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := filepath.Abs(expandHome(args[0]))
			if err != nil {
				return err
			}
			// The worktree's top, and nothing unfinished in it.
			top, err := a.Git.Validate(ctx, path)
			if err != nil {
				return err
			}
			if within(top, a.Store.WorktreesDir()) {
				return fmt.Errorf("%s is already a task's worktree", top)
			}
			// A directory in use cannot be moved on Windows, and elsewhere the
			// shell would be left in one that is gone.
			if cwd, err := os.Getwd(); err == nil && within(cwd, top) {
				return fmt.Errorf("run import from outside %s, which it moves", top)
			}
			list, err := a.Git.Worktrees(ctx, top)
			if err != nil {
				return err
			}
			switch {
			case len(list) == 0:
				return fmt.Errorf("%s: git lists no worktrees", top)
			case list[0].Bare:
				return fmt.Errorf("%s belongs to the bare repository %s; tspace needs a repository with a checkout of its own", top, list[0].Path)
			case samePath(list[0].Path, top):
				return fmt.Errorf("%s is its repository's main checkout, which cannot be moved; register it with tspace config repo NAME %s, and a task makes its own worktree", top, top)
			}
			main, err := a.Git.Validate(ctx, filepath.FromSlash(list[0].Path))
			if err != nil {
				return err
			}
			branch, found := "", false
			for _, w := range list {
				if !samePath(w.Path, top) {
					continue
				}
				switch {
				case w.Detached || w.Branch == "":
					return fmt.Errorf("%s has a detached HEAD; check out a branch there first, so the task has one", top)
				case w.Locked:
					return fmt.Errorf("%s is locked (%s); unlock it with git worktree unlock first", top, dash(w.LockReason))
				}
				branch, found = w.Branch, true
			}
			if !found {
				return fmt.Errorf("%s is not one of %s's worktrees", top, main)
			}
			st, err := a.Git.WorktreeStatus(ctx, top)
			if err != nil {
				return err
			}
			if n := st.Staged + st.Modified + st.Conflicted; n > 0 {
				return fmt.Errorf("%s has %d uncommitted change%s to tracked files; commit or stash them first", top, n, plural(n))
			}
			// Its own remote copy, as after git push -u, is where the work
			// is published, not where it started.
			base := st.Upstream
			if strings.HasSuffix(base, "/"+branch) {
				base = ""
			}

			joining := taskName != ""
			if !joining {
				taskName = filepath.Base(top)
			}
			if err := task.ValidateName("task", taskName); err != nil {
				return fmt.Errorf("%w; name the task with --task", err)
			}
			if !joining && a.Store.Exists(taskName) {
				return fmt.Errorf("a task named %s exists already; to add the worktree to it, run: tspace import %s --task %s", taskName, args[0], taskName)
			}
			if name == "" {
				name = filepath.Base(main)
			}
			if err := task.ValidateName("repo", name); err != nil {
				return fmt.Errorf("%w; name the repository with --name", err)
			}

			created := false
			if !a.Store.Exists(taskName) {
				if err := a.runCmd(ctx, (*App).newTaskCmd, "create", taskName); err != nil {
					return err
				}
				created = true
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
			if _, ok := t.Repo(name); ok {
				return undo(fmt.Errorf("task %s already has a repo named %s; name this one with --name", t.Name, name))
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return undo(err)
			}

			// Moved first, then recorded as any repository is: one whose
			// branch is checked out at its own place in the task.
			dest := a.Store.WorktreePath(t.Name, name)
			if _, err := os.Stat(dest); err == nil {
				return undo(fmt.Errorf("%s already exists; it is where %s's worktree goes", dest, name))
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return undo(err)
			}
			if err := a.Git.MoveWorktree(ctx, main, top, dest); err != nil {
				return undo(err)
			}
			back := func(err error) error {
				if merr := a.Git.MoveWorktree(ctx, main, dest, top); merr != nil {
					return fmt.Errorf("%w; and the worktree could not be moved back from %s (%v)", err, dest, merr)
				}
				return undo(err)
			}
			first := len(t.Repos) == 0
			if err := a.addRepo(ctx, t, c, main, addRepoOptions{Name: name, Branch: branch, Base: base}); err != nil {
				return back(err)
			}
			openRoot := a.settleLayout(ctx, t, first)
			if err := a.Store.Save(t); err != nil {
				return back(err)
			}
			if openRoot {
				a.openRootWindow(ctx, t)
			}
			a.printf("moved %s to %s\n", top, dest)
			if st.Upstream != "" && (st.Ahead > 0 || st.Behind > 0) {
				a.printf("  %s is %d ahead and %d behind %s; nothing was pushed or pulled\n", branch, st.Ahead, st.Behind, st.Upstream)
			}
			a.printf("start it with: tspace open %s\n", t.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskName, "task", "", "an existing task to add it to, or the new task's name (default: the worktree's directory name)")
	cmd.Flags().StringVar(&name, "name", "", "repo name (default: the source repository's directory name)")
	return cmd
}
