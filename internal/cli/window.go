package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// extraWindowPrefix starts the name of every window `tspace window` opens.
// Repository names must start with a letter or digit, so an extra window can
// never take a repository's window name, and "_root" is already reserved.
const extraWindowPrefix = "_"

func (a *App) newWindowCmd() *cobra.Command {
	var root, useAgent, detach bool
	var name string
	cmd := &cobra.Command{
		Use:   "window TASK [REPO | --root] [--agent | -- COMMAND [ARGS...]]",
		Short: "Open another window in a task's running session",
		Long: "window adds a window to the task's session, in a repository's worktree or\n" +
			"at the task root, without touching the windows already there:\n\n" +
			"  tspace window PAY-142 backend --agent     the task's agent, in backend only\n" +
			"  tspace window PAY-142 --name staging      a spare shell at the task root\n" +
			"  tspace window PAY-142 infra -- kubectl port-forward svc/api 8080:80\n\n" +
			"Without a command the window runs your shell and closes when it exits.\n" +
			"A command's window stays open after it exits, so its output can be read.\n" +
			"Without REPO, a task with one repository opens that worktree; any other\n" +
			"task opens its root. Inside the multiplexer you are switched to it.\n\n" +
			"Extra windows are named with a leading '_' and are not recreated by\n" +
			"start or open; stop ends them with the rest of the session.",
		Args: func(cmd *cobra.Command, args []string) error {
			n := len(args)
			if d := cmd.ArgsLenAtDash(); d >= 0 {
				n = d
			}
			if n < 1 || n > 2 {
				return fmt.Errorf("expected TASK [REPO], optionally followed by -- COMMAND [ARGS...]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, argv := args, []string(nil)
			if d := cmd.ArgsLenAtDash(); d >= 0 {
				pos, argv = args[:d], args[d:]
				if len(argv) == 0 {
					return fmt.Errorf("no command after --")
				}
			}
			if useAgent && len(argv) > 0 {
				return fmt.Errorf("choose either --agent or a command after --")
			}
			// Opening a window changes the session, so it waits for whatever
			// is changing the task, such as a clean removing the worktree.
			t, unlock, err := a.lockTask(cmd, pos[0], true)
			if err != nil {
				return err
			}
			defer unlock()
			repoArg := ""
			if len(pos) == 2 {
				repoArg = pos[1]
			}
			if root && repoArg != "" {
				return fmt.Errorf("--root cannot be combined with a repository")
			}

			place, dir := "root", a.Store.TaskDir(t.Name)
			if !root && !defaultsToRoot(t, repoArg) {
				r, repoName, err := a.worktreeDir(t, repoArg)
				if err != nil {
					return err
				}
				place, dir = repoName, r.Worktree
			} else if !isDir(dir) {
				return fmt.Errorf("task directory %s is missing; run: tspace start %s", dir, t.Name)
			}

			if useAgent {
				if argv, err = a.taskAgent(t); err != nil {
					return err
				}
				argv = a.expandArgv(t, argv)
			}

			ctx := cmd.Context()
			session := a.Runtime.SessionName(t.Name)
			st, err := a.Runtime.State(ctx, session)
			if err != nil {
				return err
			}
			if !st.Exists {
				return fmt.Errorf("task %s has no running session; start it with: tspace open %s", t.Name, t.Name)
			}
			taken := func(n string) bool {
				_, ok := st.Pane(a.Runtime.WindowName(n))
				return ok
			}

			var window string
			if name != "" {
				if err := task.ValidateName("window", name); err != nil {
					return err
				}
				window = extraWindowPrefix + name
				if taken(window) {
					return fmt.Errorf("window %s already exists in %s's session", window, t.Name)
				}
			} else {
				label := "shell"
				if len(argv) > 0 {
					label = filepath.Base(argv[0])
				}
				base := extraWindowPrefix + place + "-" + label
				window = base
				for i := 2; taken(window); i++ {
					window = fmt.Sprintf("%s-%d", base, i)
				}
			}

			// An extra window has no signal of its own, so its agent falls back to the directory.
			w := runtime.Window{Name: window, Dir: dir, Command: argv, Transient: len(argv) == 0, Env: a.windowEnv(t.Name, "")}
			if err := a.Runtime.OpenWindow(ctx, session, w); err != nil {
				return err
			}
			what := "shell"
			if len(argv) > 0 {
				what = shellJoin(argv)
			}
			a.printf("opened %s in %s: %s at %s\n", window, t.Name, what, dir)
			if detach {
				return nil
			}
			focused, err := a.Runtime.Focus(ctx, session, window)
			if err != nil {
				return err
			}
			if !focused {
				a.printf("attach with: tspace attach %s\n", t.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&root, "root", false, "open the window at the task root")
	cmd.Flags().BoolVar(&useAgent, "agent", false, "run the task's agent (its root agent, else the default from tspace config)")
	cmd.Flags().StringVar(&name, "name", "", "window name, shown with a leading '_' (default: from the place and command)")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "open the window without switching to it")
	return cmd
}

// taskAgent is the agent `window --agent` runs: the one the task's root
// window runs, or for a task created with a shell root, the user's default.
func (a *App) taskAgent(t *task.Task) ([]string, error) {
	if len(t.Agent) > 0 {
		return t.Agent, nil
	}
	c, err := a.Store.LoadConfig()
	if err != nil {
		return nil, err
	}
	if len(c.Agent) == 0 {
		return nil, fmt.Errorf("task %s has no agent and no default is set; give a command after --, or set one with: tspace config agent CMD", t.Name)
	}
	return c.Agent, nil
}
