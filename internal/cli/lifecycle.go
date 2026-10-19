package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"slices"
	"time"

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
			t, unlock, err := a.lockTask(cmd, args[0], true)
			if err != nil {
				return err
			}
			defer unlock()
			ctx := cmd.Context()
			names, err := selectRepos(t, args[1:])
			if err != nil {
				return err
			}
			if len(names) == 0 {
				return fmt.Errorf("task %q has no repositories; add one with: tspace repo add %s REPO", t.Name, t.Name)
			}

			bases := map[string]string{}
			hooked := false
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
					hooked = hooked || r.PostCreate != ""
					if err := a.runPostCreate(ctx, t, name, r); err != nil {
						// A half-provisioned worktree the user can inspect beats
						// destroying one, so this is a warning, not a rollback.
						a.warnf("%-12s post_create failed: %v\n", name, err)
					}
					if res.Base != "" && r.Base != res.Base {
						r.Base = res.Base
						t.SetRepo(name, r)
						bases[name] = res.Base
					}
				}
			}

			// A post_create hook may have updated the record meanwhile, so
			// the bases resolved here go onto the latest one, not over it,
			// and the windows are made from that.
			switch {
			case len(bases) > 0:
				latest, err := a.Store.Update(ctx, t.Name, cmd.CommandPath()+" "+t.Name, func(cur *task.Task) error {
					for name, base := range bases {
						if r, ok := cur.Repos[name]; ok {
							r.Base = base
							cur.SetRepo(name, r)
						}
					}
					return nil
				})
				if err != nil {
					return err
				}
				t = latest
			case hooked:
				if t, err = a.Store.Load(t.Name); err != nil {
					return err
				}
			}
			// The root window comes first, so a fresh session opens on the agent
			// that can see every repository. Naming repositories starts only those.
			// A single-repo task has none: its agent runs in the repository.
			var windows []runtime.Window
			withRoot := len(args) == 1 && t.HasRootWindow()
			if withRoot {
				windows = append(windows, a.rootWindow(t))
			}
			for _, name := range names {
				windows = append(windows, a.repoWindow(t, name))
			}

			if noRuntime {
				return nil
			}
			if withRoot {
				// A multiplexer cannot open a window in a directory that does not exist.
				if err := os.MkdirAll(a.Store.TaskDir(t.Name), 0755); err != nil {
					return err
				}
				a.printf("%-12s %s at %s\n", "(root)", t.AgentLabel(), a.Store.TaskDir(t.Name))
			}
			if t.AgentRepo != "" && slices.Contains(names, t.AgentRepo) {
				a.printf("%-12s runs the task's agent: %s\n", t.AgentRepo, agentDisplay(task.Repo{Agent: t.RepoAgent(t.AgentRepo)}))
			}
			if err := a.Runtime.Available(ctx); err != nil {
				a.warnf("worktrees are ready, but the runtime is unavailable: %v\n", err)
				return err
			}

			session := a.Runtime.SessionName(t.Name)
			// Unreadable, the session counts as new: checkLaunched then waits,
			// and no signals are cleared on a guess.
			before, err := a.Runtime.State(ctx, session)
			// Signals left by a session that is gone describe agents that are
			// no longer there.
			if err == nil && !before.Exists {
				if err := a.Store.ClearSignals(t.Name); err != nil {
					a.warnf("tspace: %v\n", err)
				}
			}
			if err := a.ensureContextDir(t); err != nil {
				return err
			}
			if err := a.Runtime.Start(ctx, session, windows); err != nil {
				return err
			}
			if err := a.checkLaunched(ctx, t, session, before, windows, names); err != nil {
				return err
			}
			if open {
				a.printf("\nworkspace: %s\n", t.Name)
				a.printTaskContext(cmd.Context(), t)
				setTitle(a.Out, "tspace: "+t.Name, a.noTitle)
				unlock() // nobody holds a task while attached to it
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

// ensureContextDir creates the notes directory before any agent starts: a
// command may name it with {context_dir}, and an agent given a directory
// that does not exist may refuse to start.
func (a *App) ensureContextDir(t *task.Task) error {
	return os.MkdirAll(a.Store.ContextDir(t.Name), 0o755)
}

// checkLaunched reports what start launched that is already gone. Start
// returns once the windows exist, which says nothing about whether their
// commands survive the first second, and a session can go with them.
func (a *App) checkLaunched(ctx context.Context, t *task.Task, session string, before runtime.SessionState, windows []runtime.Window, names []string) error {
	launched := !before.Exists
	for _, w := range windows {
		if _, ok := before.Pane(a.Runtime.WindowName(w.Name)); !ok {
			launched = true
		}
	}
	if launched && a.settle > 0 {
		select {
		case <-time.After(a.settle):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	st, err := a.Runtime.State(ctx, session)
	if err != nil {
		a.warnf("could not inspect agent state: %v\n", err)
		return nil
	}
	if !st.Exists {
		return fmt.Errorf("session %s exited right after it started; check the agent commands with `tspace task show %s`, then run start again", session, t.Name)
	}
	if pane, ok := st.Pane(a.Runtime.WindowName(task.RootWindowName)); ok && pane.Dead {
		a.warnf("the root window has exited; inspect its output, then run: tspace restart %s --root\n", t.Name)
	}
	for _, name := range names {
		if pane, ok := st.Pane(a.Runtime.WindowName(name)); ok && pane.Dead {
			a.warnf("%s has exited; inspect its output, then run: tspace restart %s %s\n", name, t.Name, name)
		}
	}
	return nil
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
		envWindow+"=",
		task.EnvRoot+"="+a.Store.Root(),
		"TSPACE_REPO="+name,
		"TSPACE_REPO_PATH="+r.Path,
		"TSPACE_WORKTREE="+r.Worktree,
		"TSPACE_TASK_DIR="+a.Store.TaskDir(t.Name),
		"TSPACE_CONTEXT_DIR="+a.Store.ContextDir(t.Name),
	)
	// The hook runs while start holds the task, so a tspace it runs on the
	// task inherits the lock instead of waiting for start, which waits for it.
	if l := a.Store.HeldLock(ctx, t.Name); l != nil {
		cmd.Env = append(cmd.Env, l.Env())
	}
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
			t, unlock, err := a.lockTask(cmd, args[0], true)
			if err != nil {
				return err
			}
			defer unlock()
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
			t, unlock, err := a.lockTask(cmd, args[0], true)
			if err != nil {
				return err
			}
			defer unlock()
			var w runtime.Window
			label := t.Name
			if root {
				if !t.HasRootWindow() {
					return fmt.Errorf("task %s has no root window; its agent runs in %s: tspace restart %s %s", t.Name, t.AgentRepo, t.Name, t.AgentRepo)
				}
				w = a.rootWindow(t)
				if info, err := os.Stat(w.Dir); err != nil || !info.IsDir() {
					return fmt.Errorf("task directory %s is missing; run: tspace start %s", w.Dir, t.Name)
				}
				label += " root window"
			} else {
				_, name, err := a.worktreeDir(t, args[1])
				if err != nil {
					return err
				}
				w = a.repoWindow(t, name)
				label += "/" + name
			}
			if resume {
				argv := t.ResumeFor(w.Name)
				switch {
				case len(argv) > 0:
				case len(t.WindowAgent(w.Name)) == 0:
					return fmt.Errorf("%s runs a shell; there is no agent to resume", label)
				default:
					return fmt.Errorf("%s has no resume command; set one with the agent's own, e.g. %s", label, resumeHint(t, w.Name))
				}
				w.Command = a.expandArgv(t, argv)
			}
			if err := a.ensureContextDir(t); err != nil {
				return err
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
	cmd.Flags().BoolVar(&resume, "resume", false, "run the window's resume command, bringing the agent back with its conversation")
	return cmd
}

// resumeHint is the command that would set a window's resume command.
func resumeHint(t *task.Task, window string) string {
	if window == t.AgentWindow() {
		return "tspace task update " + t.Name + " --resume CMD --resume ARG..."
	}
	return "tspace repo update " + t.Name + " " + window + " --resume CMD --resume ARG..."
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
			a.printTaskContext(cmd.Context(), t)
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
