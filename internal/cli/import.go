package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

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
			"HEAD, an unfinished merge or rebase, or a lock, and a repository's main\n" +
			"checkout, which cannot move: register that with `tspace config repo` and\n" +
			"let a task make its own worktree. Commits not pushed, or not pulled, are\n" +
			"reported, and kept. The branch's base is its upstream if it has one, else\n" +
			"the repository's default branch; `tspace repo update` changes it.\n\n" +
			"The repository is named after its source repository's directory, or\n" +
			"--name. Nothing is started: `tspace start` or `open` brings up the session.",
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
			list, err := a.Git.Worktrees(ctx, top)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("%s: git lists no worktrees", top)
			}
			if samePath(list[0].Path, top) {
				return fmt.Errorf("%s is its repository's main checkout, which cannot be moved; register it with tspace config repo NAME %s, and a task makes its own worktree", top, top)
			}
			main := filepath.Clean(filepath.FromSlash(list[0].Path))
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
			upstream, err := a.Git.Upstream(ctx, top, branch)
			if err != nil {
				return err
			}

			if taskName == "" {
				taskName = filepath.Base(top)
			}
			if err := task.ValidateName("task", taskName); err != nil {
				return fmt.Errorf("%w; name the task with --task", err)
			}
			created := false
			if !a.Store.Exists(taskName) {
				if err := a.runCmd(ctx, (*App).newTaskCmd, "create", taskName); err != nil {
					return err
				}
				created = true
			}
			t, unlock, err := a.lockTask(cmd, taskName, true)
			if err != nil {
				return err
			}
			defer unlock()
			// A new task is not left behind by an import that fails.
			undo := func(err error) error {
				if created {
					if derr := a.Store.Delete(taskName); derr == nil {
						a.warnf("note: removed the task %s this import created\n", taskName)
					}
				}
				return err
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return undo(err)
			}
			before := t.RepoNames()
			if err := a.addRepo(ctx, t, c, main, addRepoOptions{Adopt: top, Name: name, Branch: branch, Base: upstream}); err != nil {
				return undo(err)
			}
			first := len(before) == 0
			var repoName string
			for _, n := range t.RepoNames() {
				if !slices.Contains(before, n) {
					repoName = n
				}
			}
			dest := t.Repos[repoName].Worktree
			if _, err := os.Stat(dest); err == nil {
				return undo(fmt.Errorf("%s already exists; it is where %s's worktree goes", dest, repoName))
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return undo(err)
			}
			if err := a.Git.MoveWorktree(ctx, main, top, dest); err != nil {
				return undo(err)
			}
			openRoot := a.settleLayout(ctx, t, first)
			if err := a.Store.Save(t); err != nil {
				// Put it back where it was, as the record does not have it.
				if merr := a.Git.MoveWorktree(ctx, main, dest, top); merr != nil {
					return fmt.Errorf("recording the import failed (%v), and the worktree could not be moved back from %s (%v)", err, dest, merr)
				}
				return undo(err)
			}
			if openRoot {
				a.openRootWindow(ctx, t)
			}
			a.printf("moved %s to %s\n", top, dest)
			if upstream != "" && (st.Ahead > 0 || st.Behind > 0) {
				a.printf("  %s is %d ahead and %d behind %s; nothing was pushed or pulled\n", branch, st.Ahead, st.Behind, upstream)
			}
			a.printf("start it with: tspace open %s\n", t.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskName, "task", "", "the task to add it to, made if it does not exist (default: the worktree's directory name)")
	cmd.Flags().StringVar(&name, "name", "", "repo name (default: the source repository's directory name)")
	return cmd
}
