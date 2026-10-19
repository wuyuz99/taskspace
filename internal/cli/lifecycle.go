package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (a *App) newStartCmd() *cobra.Command {
	return a.newLaunchCmd(false)
}

func (a *App) newOpenCmd() *cobra.Command {
	return a.newLaunchCmd(true)
}

// Start and open share preparation so worktree and hook behavior stay identical.
func (a *App) newLaunchCmd(open bool) *cobra.Command {
	var noRuntime bool

	cmd := &cobra.Command{
		Use:   "start TASK [REPO...]",
		Short: "Create the task's worktrees and launch its agents",
		Long: "start creates any missing worktrees and brings up the task's session.\n" +
			"It is safe to re-run: existing worktrees are reused and running agents\n" +
			"are left alone.\n\n" +
			"The session is a detached session in the configured multiplexer (tmux\n" +
			"unless `tspace config multiplexer` says otherwise), with the task-root\n" +
			"window first, then one window per repository, each running its agent or a\n" +
			"shell. start checks a second later that the session and its windows are\n" +
			"still there, reporting any that exited on launch; it does not wait for the\n" +
			"agents beyond that and needs no terminal. Naming REPOs starts only those, without the\n" +
			"root window.\n\n" +
			"Without the multiplexer, start still creates the worktrees and then fails;\n" +
			"--no-runtime creates them and stops there, successfully.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			res, err := a.workspace().Start(ctx, args[0], workspace.StartOptions{Repos: args[1:], NoRuntime: noRuntime})
			if err != nil || noRuntime {
				return err
			}
			if open {
				// Start has let go of the task: nobody holds one while attached.
				a.printf("\nworkspace: %s\n", res.Task.Name)
				a.printTaskContext(res.Task)
				setTitle(a.Out, "tspace: "+res.Task.Name, a.noTitle)
				return a.Runtime.Attach(ctx, res.Session)
			}
			a.printf("\nsession %s is up; attach with: tspace attach %s\n", res.Session, res.Task.Name)
			return nil
		},
	}
	if open {
		cmd.Use = "open TASK"
		cmd.Short = "Prepare a workspace, show its context and enter its session"
		cmd.Long = "open creates missing worktrees and session windows, shows the task brief\n" +
			"and handoff, then attaches: `tspace start` and `tspace attach` in one step.\n" +
			"Existing windows are preserved, including dead agents; use restart\n" +
			"explicitly to relaunch them.\n\n" +
			"open takes over the terminal until you detach, so it needs a TTY. From a\n" +
			"script, use `tspace start TASK` and read `tspace status TASK` instead."
		cmd.Args = cobra.ExactArgs(1)
	} else {
		cmd.Flags().BoolVar(&noRuntime, "no-runtime", false,
			"create worktrees but do not start the session")
	}
	return cmd
}

// runPostCreate runs a repository's post_create hook; see
// workspace.Service.RunPostCreate.
func (a *App) runPostCreate(ctx context.Context, t *task.Task, name string, r task.Repo) error {
	return a.workspace().RunPostCreate(ctx, t, name, r)
}

func (a *App) newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop TASK",
		Short: "Stop the task's agents, leaving worktrees untouched",
		Long: "stop interrupts the agents, gives them a moment, then ends the session.\n" +
			"Worktrees and branches are not touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			running, err := a.workspace().Stop(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !running {
				a.printf("nothing running for %s\n", args[0])
				return nil
			}
			a.printf("stopped %s; worktrees left in place\n", args[0])
			return nil
		},
	}
}

func (a *App) newRestartCmd() *cobra.Command {
	var root, resume bool
	cmd := &cobra.Command{Use: "restart TASK [REPO | --root]", Short: "Relaunch a dead agent or shell",
		Long: "restart relaunches a dead repository window, or with --root the task-root\n" +
			"window, using its configured command. A window is dead when `tspace status`\n" +
			"shows it as such: its command has exited.\n\n" +
			"It refuses live panes. It replaces the old pane's output, so inspect that\n" +
			"first. It never reruns setup hooks.\n\n" +
			"--resume runs the window's resume command instead, which brings the agent\n" +
			"back with its conversation. Each agent has its own way of doing that, so\n" +
			"tspace knows none; it is set with the agent, one flag per word:\n\n" +
			"  tspace config resume claude --continue          default for new tasks\n" +
			"  tspace task update PAY-142 --resume codex --resume resume --resume=--last\n" +
			"  tspace repo update PAY-142 backend --resume aider --resume=--restore-chat-history\n\n" +
			"An agent resumed in the directory it ran in finds its conversation there.",
		Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case root && len(args) == 2:
				return fmt.Errorf("--root cannot be combined with a repository")
			case !root && len(args) == 1:
				return fmt.Errorf("name a repository, or use --root for the task-root window")
			}
			window := task.RootWindowName
			if !root {
				window = args[1]
			}
			label, err := a.workspace().Restart(cmd.Context(), args[0], window, resume)
			if err != nil {
				return err
			}
			a.printf("relaunched %s; use tspace status %s to check its state\n", label, args[0])
			return nil
		}}
	cmd.Flags().BoolVar(&root, "root", false, "restart the task-root window")
	cmd.Flags().BoolVar(&resume, "resume", false, "run the window's resume command, bringing the agent back with its conversation")
	return cmd
}

func (a *App) newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach TASK",
		Short: "Attach to the task's session",
		Long: "attach prints the task's brief and handoff, then hands the terminal to the\n" +
			"task's session; inside the same multiplexer it switches the current\n" +
			"client instead. Detach with the multiplexer's detach key (tmux: the\n" +
			"prefix, then d; zellij: Ctrl-o, then d), which leaves everything running.\n\n" +
			"The session must already be up (`tspace start TASK`), and attach needs a TTY.\n" +
			"To look at a task without one, use `tspace status TASK`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			// Label the hosting tab before handing the terminal over, so the tab
			// is identifiable even after detaching.
			setTitle(a.Out, "tspace: "+t.Name, a.noTitle)
			a.printTaskContext(t)
			return a.Runtime.Attach(cmd.Context(), a.Runtime.SessionName(t.Name))
		},
	}
}

// worktreeDir resolves TASK [REPO] to a single worktree path.
func (a *App) worktreeDir(t *task.Task, repoArg string) (task.Repo, string, error) {
	return workspace.WorktreeDir(t, repoArg)
}
