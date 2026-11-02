package cli

import (
	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

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
			res, err := a.workspaceFor(cmd).Import(cmd.Context(), workspace.ImportOptions{Paths: args, Task: taskName, Name: name, WithRoot: withRoot})
			if err != nil {
				return err
			}
			for _, w := range res.Worktrees {
				if w.InPlace {
					a.printf("adopted %s in place\n", w.From)
				} else {
					a.printf("moved %s to %s\n", w.From, w.To)
				}
				st := w.Status
				if n := st.Staged + st.Modified; n > 0 {
					a.printf("  %d uncommitted change%s kept; nothing was committed\n", n, workspace.Plural(n))
				}
				if st.Upstream != "" && (st.Ahead > 0 || st.Behind > 0) {
					a.printf("  %s is %d ahead and %d behind %s; nothing was pushed or pulled\n", w.Branch, st.Ahead, st.Behind, st.Upstream)
				}
			}
			a.printf("start it with: tspace open %s\n", res.Task.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskName, "task", "", "an existing task to add it to, or the new task's name (default: the worktree's directory name)")
	cmd.Flags().StringVar(&name, "name", "", "repo name, with one path (default: the source repository's directory name)")
	cmd.Flags().BoolVar(&withRoot, "with-root", false, "give a new task a task-root window for its agent, even with one repository")
	return cmd
}
