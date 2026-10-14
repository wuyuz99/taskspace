package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
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
			"The session is a detached tmux session with the task-root window first,\n" +
			"then one window per repository, each running its agent or a shell. start\n" +
			"returns as soon as they are up; it does not wait for the agents and needs\n" +
			"no terminal. Naming REPOs starts only those, without the root window.\n\n" +
			"Without tmux, start still creates the worktrees and then fails;\n" +
			"--no-runtime creates them and stops there, successfully.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			names, err := selectRepos(t, args[1:])
			if err != nil {
				return err
			}
			if len(names) == 0 {
				return fmt.Errorf("task %q has no repositories; add one with: tspace repo add %s REPO", t.Name, t.Name)
			}

			changed := false
			var windows []runtime.Window
			// The root window comes first, so a fresh session opens on the agent
			// that can see every repository. Naming repositories starts only those.
			withRoot := len(args) == 1
			if withRoot {
				windows = append(windows, a.rootWindow(t))
			}
			for _, name := range names {
				r := t.Repos[name]
				res, err := a.Git.EnsureWorktree(ctx, r.Path, r.Worktree, r.Branch, r.Base)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				if res.FetchErr != nil {
					a.warnf("%-12s could not fetch, using the refs already here: %v\n", name, res.FetchErr)
				}
				switch {
				case res.Tracking != "":
					a.printf("%-12s created worktree on %s (tracking %s)\n", name, r.Branch, res.Tracking)
				case res.Created && res.Base != "":
					a.printf("%-12s created worktree on new branch %s (from %s)\n", name, r.Branch, res.Base)
				case res.Created:
					a.printf("%-12s created worktree on existing branch %s\n", name, r.Branch)
				default:
					a.printf("%-12s worktree ready\n", name)
				}

				if res.Created {
					if err := a.runPostCreate(ctx, t, name, r); err != nil {
						// A half-provisioned worktree the user can inspect beats
						// destroying one, so this is a warning, not a rollback.
						a.warnf("%-12s post_create failed: %v\n", name, err)
					}
					if res.Base != "" && r.Base != res.Base {
						r.Base = res.Base
						t.SetRepo(name, r)
						changed = true
					}
				}

				windows = append(windows, runtime.Window{
					Name:    name,
					Dir:     r.Worktree,
					Command: r.Agent,
				})
			}

			if changed {
				if err := a.Store.Save(t); err != nil {
					return err
				}
			}

			if noRuntime {
				return nil
			}
			if withRoot {
				// tmux cannot open a window in a directory that does not exist.
				if err := os.MkdirAll(a.Store.TaskDir(t.Name), 0755); err != nil {
					return err
				}
				a.printf("%-12s %s at %s\n", "(root)", t.AgentLabel(), a.Store.TaskDir(t.Name))
			}
			if err := a.Runtime.Available(ctx); err != nil {
				a.warnf("worktrees are ready, but the runtime is unavailable: %v\n", err)
				return err
			}

			session := a.Runtime.SessionName(t.Name)
			// Signals left by a session that is gone describe agents that are
			// no longer there.
			if running, err := a.Runtime.HasSession(ctx, session); err == nil && !running {
				if err := a.Store.ClearSignals(t.Name); err != nil {
					a.warnf("tspace: %v\n", err)
				}
			}
			if err := a.Runtime.Start(ctx, session, windows); err != nil {
				return err
			}
			if open {
				a.printf("\nworkspace: %s\n", t.Name)
				a.printTaskContext(t)
				st, err := a.Runtime.State(ctx, session)
				if err != nil {
					a.warnf("could not inspect agent state: %v\n", err)
				} else {
					if pane, ok := st.Pane(a.Runtime.WindowName(task.RootWindowName)); ok && pane.Dead {
						a.warnf("the root window has exited; inspect its output, then run: tspace restart %s --root\n", t.Name)
					}
					for _, name := range names {
						if pane, ok := st.Pane(a.Runtime.WindowName(name)); ok && pane.Dead {
							a.warnf("%s has exited; inspect its output, then run: tspace restart %s %s\n", name, t.Name, name)
						}
					}
				}
				setTitle(a.Out, "tspace: "+t.Name, a.noTitle)
				return a.Runtime.Attach(ctx, session)
			}
			a.printf("\nsession %s is up; attach with: tspace attach %s\n", session, t.Name)
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

// runPostCreate runs the repo's provisioning hook in its new worktree.
//
// A fresh worktree has no .env, node_modules or venv. Without this hook the
// tool is not usable on a real project on the first try.
//
// The hook gets TSPACE_REPO_PATH pointing at the source repository. Without it the
// only way to reach the original checkout is a relative path back out of the
// worktree, whose depth depends on where TSPACE_ROOT is — so such a hook silently
// breaks for anyone who moves their state directory. Prefer:
//
//	cp "$TSPACE_REPO_PATH/.env" .
func (a *App) runPostCreate(ctx context.Context, t *task.Task, name string, r task.Repo) error {
	if r.PostCreate == "" {
		return nil
	}
	a.printf("%-12s post_create: %s\n", name, r.PostCreate)

	cmd := osexec.CommandContext(ctx, "sh", "-c", r.PostCreate)
	cmd.Dir = r.Worktree
	cmd.Stdout, cmd.Stderr = a.Out, a.Err
	cmd.Env = append(os.Environ(),
		"TSPACE_TASK="+t.Name,
		"TSPACE_REPO="+name,
		"TSPACE_REPO_PATH="+r.Path,
		"TSPACE_WORKTREE="+r.Worktree,
		"TSPACE_TASK_DIR="+a.Store.TaskDir(t.Name),
		"TSPACE_CONTEXT_DIR="+a.Store.ContextDir(t.Name),
	)
	return cmd.Run()
}

func (a *App) newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop TASK",
		Short: "Stop the task's agents, leaving worktrees untouched",
		Long: "stop interrupts the agents, gives them a moment, then ends the session.\n" +
			"Worktrees and branches are not touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			session := a.Runtime.SessionName(t.Name)

			running, err := a.Runtime.HasSession(cmd.Context(), session)
			if err != nil {
				return err
			}
			if !running {
				_ = a.Store.ClearSignals(t.Name)
				a.printf("nothing running for %s\n", t.Name)
				return nil
			}
			if err := a.Runtime.Stop(cmd.Context(), session); err != nil {
				return err
			}
			if err := a.Store.ClearSignals(t.Name); err != nil {
				a.warnf("tspace: %v\n", err)
			}
			a.printf("stopped %s; worktrees left in place\n", t.Name)
			return nil
		},
	}
}

func (a *App) newRestartCmd() *cobra.Command {
	var root bool
	cmd := &cobra.Command{Use: "restart TASK [REPO | --root]", Short: "Relaunch a dead agent or shell",
		Long: "restart relaunches a dead repository window, or with --root the task-root\n" +
			"window, using its configured command. A window is dead when `tspace status`\n" +
			"shows it as such: its command has exited.\n\n" +
			"It refuses live panes. It replaces the old pane's output, so inspect that\n" +
			"first. It does not resume agent conversation history or rerun setup hooks.",
		Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case root && len(args) == 2:
				return fmt.Errorf("--root cannot be combined with a repository")
			case !root && len(args) == 1:
				return fmt.Errorf("name a repository, or use --root for the task-root window")
			}
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			var w runtime.Window
			label := t.Name
			if root {
				w = a.rootWindow(t)
				if info, err := os.Stat(w.Dir); err != nil || !info.IsDir() {
					return fmt.Errorf("task directory %s is missing; run: tspace start %s", w.Dir, t.Name)
				}
				label += " root window"
			} else {
				r, name, err := a.worktreeDir(t, args[1])
				if err != nil {
					return err
				}
				w = runtime.Window{Name: name, Dir: r.Worktree, Command: r.Agent}
				label += "/" + name
			}
			if err := a.Runtime.Restart(cmd.Context(), a.Runtime.SessionName(t.Name), w); err != nil {
				return err
			}
			// The relaunched agent has said nothing yet.
			if err := a.Store.ClearSignal(t.Name, w.Name); err != nil {
				a.warnf("tspace: %v\n", err)
			}
			a.printf("relaunched %s; use tspace status %s to check its state\n", label, t.Name)
			return nil
		}}
	cmd.Flags().BoolVar(&root, "root", false, "restart the task-root window")
	return cmd
}

func (a *App) newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach TASK",
		Short: "Attach to the task's session",
		Long: "attach prints the task's brief and handoff, then hands the terminal to the\n" +
			"task's tmux session; inside tmux it switches the current client instead.\n" +
			"Detach with the tmux prefix and d, which leaves everything running.\n\n" +
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

// selectRepos resolves optional repo-name arguments against a task.
func selectRepos(t *task.Task, args []string) ([]string, error) {
	if len(args) == 0 {
		return t.RepoNames(), nil
	}
	for _, name := range args {
		if _, ok := t.Repo(name); !ok {
			return nil, fmt.Errorf("task %q has no repo named %q (has: %s)",
				t.Name, name, joinNames(t.RepoNames()))
		}
	}
	return args, nil
}

// worktreeDir resolves TASK [REPO] to a single worktree path.
func (a *App) worktreeDir(t *task.Task, repoArg string) (task.Repo, string, error) {
	names := t.RepoNames()
	if len(names) == 0 {
		return task.Repo{}, "", fmt.Errorf("task %q has no repositories", t.Name)
	}

	name := repoArg
	if name == "" {
		if len(names) > 1 {
			return task.Repo{}, "", fmt.Errorf("task %q has several repos (%s); name one",
				t.Name, joinNames(names))
		}
		name = names[0]
	}
	r, ok := t.Repo(name)
	if !ok {
		return task.Repo{}, "", fmt.Errorf("task %q has no repo named %q (has: %s)",
			t.Name, name, joinNames(names))
	}
	if info, err := os.Stat(r.Worktree); err != nil || !info.IsDir() {
		return task.Repo{}, "", fmt.Errorf("worktree for %s is missing (%s); run: tspace start %s",
			name, r.Worktree, t.Name)
	}
	return r, name, nil
}
