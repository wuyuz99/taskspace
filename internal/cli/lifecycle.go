package cli

import (
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
	var noRuntime, root, fresh, interrupted bool

	cmd := &cobra.Command{
		Use:   "start TASK [REPO...] | --interrupted",
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
			"A window brought back whose agent has a resume command (see restart\n" +
			"--help) is resumed with its conversation, if its worktree, or for the root\n" +
			"window the task directory, was there before: one just created holds no\n" +
			"conversation. A resume command that exits at once is followed by the\n" +
			"window's own command. --fresh always runs the own command.\n\n" +
			"Without the multiplexer, start still creates the worktrees and then fails;\n" +
			"--no-runtime creates them and stops there, successfully.\n\n" +
			"--interrupted starts every task whose session ended without `tspace\n" +
			"stop` or clean ending it, as after a computer restart; status shows them\n" +
			"as interrupted. They start one after another, and one that fails does\n" +
			"not stop the rest.",
		Args: func(cmd *cobra.Command, args []string) error {
			if interrupted {
				if len(args) > 0 || root || noRuntime {
					return fmt.Errorf("--interrupted takes no TASK, --root or --no-runtime")
				}
				return nil
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if interrupted {
				return a.startInterrupted(cmd, fresh)
			}
			name, err := a.resolveTask(args[0])
			if err != nil {
				return err
			}
			res, err := a.workspaceFor(cmd).Start(ctx, name, workspace.StartOptions{Root: root, Repos: args[1:], NoRuntime: noRuntime, Fresh: fresh})
			if err != nil || noRuntime {
				return err
			}
			if open {
				// Start has let go of the task: nobody holds one while attached.
				a.printf("\nworkspace: %s\n", res.Task.Name)
				a.printTaskContext(res.Task)
				setTitle(a.Out, "tspace: "+res.Task.Name, a.noTitle)
				return attachFailed(a.Runtime.Attach(ctx, res.Session), res.Task.Name)
			}
			a.printf("\n%s\n", startedText(res))
			return nil
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "start every agent with a new conversation, never resuming one")
	if open {
		cmd.Use = "open TASK"
		cmd.Short = "Prepare a workspace, show its context and enter its session"
		cmd.Long = "open creates missing worktrees and session windows, shows the task brief\n" +
			"and handoff, then attaches: `tspace start` and `tspace attach` in one step.\n" +
			"Existing windows are preserved, including dead agents; use restart\n" +
			"explicitly to relaunch them. Windows it brings back resume their agents'\n" +
			"conversations as start's do, unless --fresh.\n\n" +
			"open takes over the terminal until you detach, so it needs a TTY. From a\n" +
			"script, use `tspace start TASK` and read `tspace status TASK` instead."
		cmd.Args = cobra.ExactArgs(1)
	} else {
		cmd.Flags().BoolVar(&root, "root", false, "start only the task-root window")
		cmd.Flags().BoolVar(&noRuntime, "no-runtime", false,
			"create worktrees but do not start the session")
		cmd.Flags().BoolVar(&interrupted, "interrupted", false,
			"start every task a computer restart, or anything but stop, interrupted")
	}
	return cmd
}

func (a *App) newStopCmd() *cobra.Command {
	var root bool
	cmd := &cobra.Command{
		Use:   "stop TASK [REPO | _WINDOW | --root]",
		Short: "Stop the task's agents, leaving worktrees untouched",
		Long: "stop interrupts the agents, gives them a moment, then ends the session.\n" +
			"Worktrees and branches are not touched. Name REPO or --root to stop only\n" +
			"that primary window, or an extra window by its name, such as _mon, to\n" +
			"close only that one; the other windows stay open. A saved window that is\n" +
			"closed stays saved, and start opens it again; forget it with\n" +
			"`tspace window TASK --forget NAME`. TASK must be explicit.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := explicitTask(args[0]); err != nil {
				return err
			}
			if root || len(args) == 2 {
				repo, label := "", "the root window"
				if len(args) == 2 {
					repo, label = args[1], args[1]
				}
				if err := a.workspaceFor(cmd).StopWindow(cmd.Context(), args[0], repo, root); err != nil {
					return err
				}
				a.printf("stopped %s of %s; the rest of the task keeps running\n", label, args[0])
				return nil
			}
			running, err := a.workspaceFor(cmd).Stop(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			a.printf("%s\n", stoppedText(args[0], running))
			return nil
		},
	}
	cmd.Flags().BoolVar(&root, "root", false, "stop only the task-root window")
	return cmd
}

// startInterrupted starts every interrupted task in turn, carrying on past
// one that fails.
func (a *App) startInterrupted(cmd *cobra.Command, fresh bool) error {
	ws := a.workspaceFor(cmd)
	names, err := ws.Interrupted(cmd.Context())
	if err != nil {
		return err
	}
	if len(names) == 0 {
		a.printf("no task was interrupted\n")
		return nil
	}
	var failed []string
	for i, name := range names {
		if i > 0 {
			a.printf("\n")
		}
		a.printf("== %s\n", name)
		res, err := ws.Start(cmd.Context(), name, workspace.StartOptions{Fresh: fresh})
		if err != nil {
			a.warnf("%s: %v\n", name, err)
			failed = append(failed, name)
			continue
		}
		a.printf("%s\n", startedText(res))
	}
	if len(failed) > 0 {
		return fmt.Errorf("started %d of %d interrupted tasks; %s did not start", len(names)-len(failed), len(names), joinNames(failed))
	}
	a.printf("\nstarted %d interrupted task%s\n", len(names), plural(len(names)))
	return nil
}

func (a *App) newRestartCmd() *cobra.Command {
	var root, resume, fresh bool
	cmd := &cobra.Command{Use: "restart TASK [REPO | _WINDOW | --root]", Short: "Relaunch a dead agent or shell",
		Long: "restart relaunches a dead repository window, or with --root the task-root\n" +
			"window, using its configured command. A window is dead when `tspace status`\n" +
			"shows it as such: its command has exited. A saved extra window, named with\n" +
			"its leading '_', is relaunched with its saved command.\n\n" +
			"It refuses live panes. It replaces the old pane's output, so inspect that\n" +
			"first. It never reruns setup hooks.\n\n" +
			"--resume runs the window's resume command instead, which brings the agent\n" +
			"back with its conversation. Each agent has its own way of doing that, so\n" +
			"tspace knows none; it is set with the agent, one flag per word:\n\n" +
			"  tspace config resume claude --continue          default for new tasks\n" +
			"  tspace task update PAY-142 --resume codex --resume resume --resume=--last\n" +
			"  tspace repo update PAY-142 backend --resume aider --resume=--restore-chat-history\n\n" +
			"An agent resumed in the directory it ran in finds its conversation there.\n" +
			"Without --resume, a window with a resume command is resumed too, and\n" +
			"started fresh if that exits at once; --fresh never resumes.",
		Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case root && len(args) == 2:
				return fmt.Errorf("--root cannot be combined with a repository")
			case !root && len(args) == 1:
				return fmt.Errorf("name a repository, or use --root for the task-root window")
			}
			opts := workspace.RestartOptions{Root: root, Resume: resume, Fresh: fresh}
			if !root {
				opts.Repo = args[1]
			}
			name, err := a.resolveTask(args[0])
			if err != nil {
				return err
			}
			label, resumed, err := a.workspaceFor(cmd).Restart(cmd.Context(), name, opts)
			if err != nil {
				return err
			}
			a.printf("%s\n", restartedText(label, name, resumed))
			return nil
		}}
	cmd.Flags().BoolVar(&root, "root", false, "restart the task-root window")
	cmd.Flags().BoolVar(&resume, "resume", false, "run the window's resume command, bringing the agent back with its conversation")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "run the window's own command, never its resume command")
	cmd.MarkFlagsMutuallyExclusive("resume", "fresh")
	return cmd
}

func (a *App) newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [TASK]",
		Short: "Attach to the task's session",
		Long: "attach prints the task's brief and handoff, then hands the terminal to the\n" +
			"task's session; inside the same multiplexer it switches the current\n" +
			"client instead. Detach with the multiplexer's detach key (tmux: the\n" +
			"prefix, then d; zellij: Ctrl-o, then d), which leaves everything running.\n\n" +
			"The session must already be up (`tspace start TASK`), and attach needs a TTY.\n" +
			"To look at a task without one, use `tspace status TASK`.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(taskArg(args))
			if err != nil {
				return err
			}
			// Label the hosting tab before handing the terminal over, so the tab
			// is identifiable even after detaching.
			setTitle(a.Out, "tspace: "+t.Name, a.noTitle)
			a.printTaskContext(t)
			return attachFailed(a.Runtime.Attach(cmd.Context(), a.Runtime.SessionName(t.Name)), t.Name)
		},
	}
}

// worktreeDir resolves TASK [REPO] to a single worktree path.
func (a *App) worktreeDir(t *task.Task, repoArg string) (task.Repo, string, error) {
	return workspace.WorktreeDir(t, repoArg)
}
