package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// extraWindowPrefix starts the name of every window `tspace window` opens.
const extraWindowPrefix = task.ExtraWindowPrefix

func (a *App) newWindowCmd() *cobra.Command {
	var root, useAgent, detach, save bool
	var name, forget, rename string
	cmd := &cobra.Command{
		Use:   "window TASK [REPO | --root] [--agent | -- COMMAND [ARGS...]] | TASK --save NAME | TASK --forget NAME | TASK --rename OLD NEW",
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
			"Extra windows are named with a leading '_', and stop ends them with the\n" +
			"rest of the session. --save, with --name, keeps the window with the task,\n" +
			"and in project recipes saved from it: start and open recreate it whenever\n" +
			"it is not there. Without a running session, --save only saves it.\n\n" +
			"tspace remembers what an unsaved window runs while its session lasts, so\n" +
			"restart can relaunch it and --save NAME can keep it once it is open.\n\n" +
			"  tspace window PAY-142 backend --name git --save -- lazygit\n" +
			"  tspace window PAY-142 --save _main-lazygit  keep a window already open\n" +
			"  tspace window PAY-142 --forget git         start no longer opens it\n" +
			"  tspace window PAY-142 --rename git lg      open and saved alike",
		Args: func(cmd *cobra.Command, args []string) error {
			n := len(args)
			if d := cmd.ArgsLenAtDash(); d >= 0 {
				n = d
			}
			switch {
			case cmd.Flags().Changed("forget") && (n != 1 || len(args) != 1):
				return fmt.Errorf("expected TASK --forget NAME")
			case cmd.Flags().Changed("rename") && (n != 2 || len(args) != 2):
				return fmt.Errorf("expected TASK --rename OLD NEW")
			case n < 1 || n > 2:
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
			managing := cmd.Flags().Changed("forget") || cmd.Flags().Changed("rename")
			if (cmd.Flags().Changed("forget") && strings.TrimSpace(forget) == "") || (cmd.Flags().Changed("rename") && strings.TrimSpace(rename) == "") {
				return fmt.Errorf("--forget and --rename need a window name")
			}
			if managing && (root || useAgent || save || detach || name != "") {
				return fmt.Errorf("--forget and --rename take only the task and window names")
			}
			if forget != "" && rename != "" {
				return fmt.Errorf("choose either --forget or --rename")
			}
			// With nothing to open, the name after TASK is a window that is.
			saveOpen := save && name == "" && len(pos) == 2 && cmd.ArgsLenAtDash() < 0 && !root && !useAgent && !detach
			if save && name == "" && !saveOpen {
				return fmt.Errorf("--save needs --name, which the window is saved and recreated by; to keep a window already open: tspace window TASK --save NAME")
			}
			// Opening a window changes the session, so it waits for whatever
			// is changing the task, such as a clean removing the worktree.
			// Forgetting one only edits the record.
			t, unlock, err := a.lockTask(cmd, pos[0], forget == "")
			if err != nil {
				return err
			}
			defer unlock()
			switch {
			case forget != "":
				return a.forgetWindow(t, forget)
			case rename != "":
				return a.renameWindow(cmd.Context(), t, rename, pos[1])
			case saveOpen:
				return a.saveOpenWindow(cmd.Context(), t, pos[1])
			}
			repoArg := ""
			if len(pos) == 2 {
				repoArg = pos[1]
			}
			if root && repoArg != "" {
				return fmt.Errorf("--root cannot be combined with a repository")
			}

			ctx := cmd.Context()
			session := a.Runtime.SessionName(t.Name)
			st, stateErr := a.Runtime.State(ctx, session)
			running := stateErr == nil && st.Exists
			if !running && !save {
				if stateErr != nil {
					return stateErr
				}
				return fmt.Errorf("task %s has no running session; start it with: tspace open %s", t.Name, t.Name)
			}
			// A window saved for later, with nothing running to open it in,
			// only needs its repository to be the task's: start makes the
			// worktree.
			later := !running
			place, repo, dir := "root", "", a.Store.TaskDir(t.Name)
			if !root && !defaultsToRoot(t, repoArg) {
				resolve := a.worktreeDir
				if later {
					resolve = workspace.ResolveRepo
				}
				r, repoName, err := resolve(t, repoArg)
				if err != nil {
					return err
				}
				place, repo, dir = repoName, repoName, r.Worktree
			} else if !isDir(dir) && !later {
				return fmt.Errorf("task directory %s is missing; run: tspace start %s", dir, t.Name)
			}

			if useAgent {
				if argv, err = a.taskAgent(t); err != nil {
					return err
				}
			}
			// Saved as given, placeholders and all, so a recipe carries it to
			// other tasks; run expanded, as start runs it.
			raw := append([]string(nil), argv...)
			argv = a.expandArgv(t, argv)

			var saved task.Window
			if save {
				if err := task.ValidateName("window", name); err != nil {
					return err
				}
				if _, ok := t.SavedWindow(name); ok {
					return fmt.Errorf("%s already has a saved window %s%s; forget it first: tspace window %s --forget %s", t.Name, extraWindowPrefix, name, t.Name, name)
				}
				if extraWindowPrefix+name == task.RootWindowName {
					return fmt.Errorf("%q names the task's root window; choose another name", name)
				}
				saved = task.Window{Name: name, Repo: repo, Command: raw}
			}
			saveWindow := func() error {
				t.Windows = append(t.Windows, saved)
				return a.Store.Save(t)
			}

			if later {
				if err := saveWindow(); err != nil {
					return err
				}
				a.printf("saved window %s for %s; start opens it\n", saved.SessionName(), t.Name)
				return nil
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
				window = task.Window{Name: name}.SessionName()
				if taken(window) {
					return fmt.Errorf("window %s already exists in %s's session", window, t.Name)
				}
			} else {
				label := "shell"
				if len(argv) > 0 {
					label = windowLabel(filepath.Base(argv[0]))
				}
				base := extraWindowPrefix + place + "-" + label
				window = base
				for i := 2; taken(window); i++ {
					window = fmt.Sprintf("%s-%d", base, i)
				}
			}

			// Opened exactly as restart and start will open it again.
			opened := task.Window{Name: strings.TrimPrefix(window, extraWindowPrefix), Repo: repo, Command: raw}
			if err := a.Runtime.OpenWindow(ctx, session, workspace.ExtraWindow(a.Store, t, opened)); err != nil {
				return err
			}
			if !save {
				if err := a.Store.RememberWindow(t.Name, opened); err != nil {
					a.warnf("tspace: %v\n", err)
				}
			}
			what := "shell"
			if len(argv) > 0 {
				what = shellJoin(argv)
			}
			a.printf("opened %s in %s: %s at %s\n", window, t.Name, what, dir)
			if save {
				if err := saveWindow(); err != nil {
					return err
				}
				a.printf("saved it: start opens it again whenever it is not there\n")
			}
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
	cmd.Flags().BoolVar(&save, "save", false, "keep the window with the task, for start and open to recreate")
	cmd.Flags().StringVar(&forget, "forget", "", "stop recreating the saved window NAME")
	cmd.Flags().StringVar(&rename, "rename", "", "rename the extra window OLD, open or saved, to the name after TASK")
	return cmd
}

// windowLabel makes a command's name fit in a window name, which status
// and dash list only when it is one tspace could give: g++ becomes g--.
func windowLabel(command string) string {
	label := strings.Map(func(r rune) rune {
		if r < 0x80 && (r == '.' || r == '_' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')) {
			return r
		}
		return '-'
	}, command)
	if label == "" {
		return "cmd"
	}
	return label
}

// saveOpenWindow keeps with the task an unsaved window that is open, as it
// was opened: the window itself is not touched.
func (a *App) saveOpenWindow(ctx context.Context, t *task.Task, name string) error {
	name = strings.TrimPrefix(name, extraWindowPrefix)
	window := extraWindowPrefix + name
	if !task.IsExtraWindow(window) {
		return fmt.Errorf("%q is not a window name tspace gives", window)
	}
	if _, ok := t.SavedWindow(name); ok {
		return fmt.Errorf("%s is already saved with %s", window, t.Name)
	}
	st, err := a.Runtime.State(ctx, a.Runtime.SessionName(t.Name))
	if err != nil {
		return err
	}
	if _, open := st.Pane(a.Runtime.WindowName(window)); !open {
		return fmt.Errorf("%s has no open window %s; to open a window and save it: tspace window %s [REPO] --name %s --save -- COMMAND", t.Name, window, t.Name, name)
	}
	w, ok := workspace.KnownWindow(a.Store, t, name)
	if !ok {
		return fmt.Errorf("tspace does not know what %s runs; it remembers only unsaved windows it opened. Close it and open it saved: tspace stop %s %s, then tspace window %s [REPO] --name %s --save -- COMMAND", window, t.Name, window, t.Name, name)
	}
	t.Windows = append(t.Windows, w)
	if err := a.Store.Save(t); err != nil {
		return err
	}
	if err := a.Store.ForgetWindow(t.Name, name); err != nil {
		a.warnf("tspace: %v\n", err)
	}
	a.printf("saved %s of %s: start opens it again whenever it is not there\n", window, t.Name)
	return nil
}

// forgetWindow drops a saved window from the task. A window of that name
// already open is left alone.
func (a *App) forgetWindow(t *task.Task, name string) error {
	i, ok := t.SavedWindow(name)
	if !ok {
		return fmt.Errorf("%s has no saved window %s%s%s", t.Name, extraWindowPrefix, strings.TrimPrefix(name, extraWindowPrefix), savedWindowsHint(t))
	}
	w := t.Windows[i]
	t.Windows = append(t.Windows[:i:i], t.Windows[i+1:]...)
	if len(t.Windows) == 0 {
		t.Windows = nil
	}
	if err := a.Store.Save(t); err != nil {
		return err
	}
	a.printf("forgot saved window %s of %s; if it is open, it stays until it is closed or the session stops\n", w.SessionName(), t.Name)
	return nil
}

// renameWindow renames an extra window, in the running session if it is open
// there and in the record if it is saved.
func (a *App) renameWindow(ctx context.Context, t *task.Task, from, to string) error {
	from, to = strings.TrimPrefix(from, extraWindowPrefix), strings.TrimPrefix(to, extraWindowPrefix)
	oldName, newName := extraWindowPrefix+from, extraWindowPrefix+to
	if oldName == task.RootWindowName || newName == task.RootWindowName {
		return fmt.Errorf("only extra windows can be renamed; the root and repository windows are named by the task")
	}
	if err := task.ValidateName("window", to); err != nil {
		return err
	}
	i, saved := t.SavedWindow(from)
	session := a.Runtime.SessionName(t.Name)
	// Without the session's state, an open window could keep its old name
	// while the saved one changes, and start would open it twice.
	st, err := a.Runtime.State(ctx, session)
	if err != nil {
		return err
	}
	open := false
	if st.Exists {
		_, open = st.Pane(a.Runtime.WindowName(oldName))
		if _, taken := st.Pane(a.Runtime.WindowName(newName)); taken {
			return fmt.Errorf("%s's session already has a window %s", t.Name, newName)
		}
	}
	if _, taken := t.SavedWindow(to); taken {
		return fmt.Errorf("%s already has a saved window %s", t.Name, newName)
	}
	if !open && !saved {
		return fmt.Errorf("%s has no window %s, open or saved%s", t.Name, oldName, savedWindowsHint(t))
	}
	if open {
		if err := a.Runtime.RenameWindow(ctx, session, oldName, newName); err != nil {
			return err
		}
	}
	if saved {
		t.Windows[i].Name = to
		if err := a.Store.Save(t); err != nil {
			return err
		}
	} else if err := a.Store.RenameWindow(t.Name, from, to); err != nil {
		a.warnf("tspace: %v\n", err)
	}
	where := "open and saved"
	switch {
	case !saved:
		where = "open"
	case !open:
		where = "saved"
	}
	a.printf("renamed %s to %s in %s (%s)\n", oldName, newName, t.Name, where)
	return nil
}

func savedWindowsHint(t *task.Task) string {
	if len(t.Windows) == 0 {
		return ""
	}
	names := make([]string, len(t.Windows))
	for i, w := range t.Windows {
		names[i] = w.SessionName()
	}
	return " (saved: " + joinNames(names) + ")"
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
