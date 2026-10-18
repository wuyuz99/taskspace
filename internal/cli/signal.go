package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

// errNotInTask is signal's answer outside every task, which --quiet hides.
var errNotInTask = errors.New("not inside a task")

type notInTask struct{ dir string }

func (e notInTask) Error() string {
	return e.dir + " is not inside a task; run this in a task's worktree, or name one with --task"
}

func (e notInTask) Is(target error) bool { return target == errNotInTask }

// signalMessageMax keeps a message to what a status line can show.
const signalMessageMax = 200

// Every window tspace launches is told which task and window it is, so that
// signal reports for the window the agent runs in wherever it has cd'd to.
const (
	envTask   = "TSPACE_TASK"
	envWindow = "TSPACE_WINDOW"
)

// windowEnv is the environment a task's window is launched with. An empty
// window names none, which a window started from inside another one needs, so
// as not to inherit that one's.
func windowEnv(taskName, window string) []string {
	return []string{envTask + "=" + taskName, envWindow + "=" + window}
}

func (a *App) newSignalCmd() *cobra.Command {
	var taskName, repo string
	var root, clear, quiet bool
	cmd := &cobra.Command{
		Use:   "signal STATE [MESSAGE]",
		Short: "Report what an agent is doing, for status and dash",
		Long: "signal records what the agent in a window says about itself, so that\n" +
			"`tspace status` and `tspace dash` can show which agents need a person. tspace can see\n" +
			"whether a process is alive; only the agent knows whether it is busy,\n" +
			"waiting or finished. STATE is one of:\n\n" +
			"  working  busy, needs nothing\n" +
			"  blocked  waiting on a person: a question, a permission, a decision\n" +
			"  done     finished what it was asked\n" +
			"  failed   gave up\n\n" +
			"Run from inside a task, it knows where it is. tspace launches every window\n" +
			"with TSPACE_TASK and TSPACE_WINDOW set, so an agent reports for its own\n" +
			"window wherever it has cd'd to. Without them, the directory decides: in a\n" +
			"worktree it reports for that repository, and at the task root for the root\n" +
			"window.\n\n" +
			"  tspace signal blocked 'migration needs a decision: drop or keep the column?'\n" +
			"  tspace signal done\n" +
			"  tspace signal working --task PAY-142 --repo backend\n\n" +
			"It is meant to be called by the agent's own hooks rather than typed. For\n" +
			"Claude Code, for example: UserPromptSubmit runs `tspace signal working`,\n" +
			"Notification runs `tspace signal blocked`, and Stop runs `tspace signal done`;\n" +
			"`tspace config hooks claude` installs exactly these.\n\n" +
			"--quiet is for hooks that run in every session, inside a task or not: it\n" +
			"prints nothing, and outside a task it does nothing and succeeds. A hook's\n" +
			"output can end up in front of the agent, which has no use for it.\n\n" +
			"A signal is one JSON file, .context/signals/REPO.json beside the\n" +
			"worktrees (_root.json for the root window), holding\n" +
			`{"state": "blocked", "message": "..."}` + "; an agent without tspace on its\n" +
			"PATH can write it directly. Its age is the file's modification time.\n\n" +
			"Signals describe a running agent, so stop, clean and a fresh start\n" +
			"clear them, and restart clears the window's own.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if root && repo != "" {
				return fmt.Errorf("--root cannot be combined with --repo")
			}
			if clear && len(args) > 0 {
				return fmt.Errorf("--clear takes no state")
			}
			if !clear && len(args) == 0 {
				return fmt.Errorf("give a state: working, blocked, done or failed")
			}
			var state task.SignalState
			if !clear {
				var err error
				if state, err = task.ParseSignalState(args[0]); err != nil {
					return err
				}
			}

			t, window, err := a.signalTarget(taskName, repo, root)
			if err != nil {
				// Only "not in a task" is expected of a hook run in every
				// session; a typo'd task or a broken record still fails.
				if quiet && errors.Is(err, errNotInTask) {
					return nil
				}
				return err
			}
			label := t.Name + " (root)"
			if window != task.RootWindowName {
				label = t.Name + "/" + window
			}

			if clear {
				if err := a.Store.ClearSignal(t.Name, window); err != nil {
					return err
				}
				if !quiet {
					a.printf("%s: signal cleared\n", label)
				}
				return nil
			}
			message := ""
			if len(args) == 2 {
				message = oneLine(args[1])
				if r := []rune(message); len(r) > signalMessageMax {
					message = string(r[:signalMessageMax-3]) + "..."
				}
			}
			if err := a.Store.WriteSignal(t.Name, window, state, message); err != nil {
				return err
			}
			if !quiet {
				a.printf("%s: %s\n", label, state)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&taskName, "task", "", "task to report for (default: the one the current directory is in)")
	cmd.Flags().StringVar(&repo, "repo", "", "repository window to report for (default: the worktree the current directory is in)")
	cmd.Flags().BoolVar(&root, "root", false, "report for the task-root window")
	cmd.Flags().BoolVar(&clear, "clear", false, "forget the window's signal")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print nothing, and outside a task do nothing, for hooks")
	return cmd
}

// signalTarget resolves which window a signal is about.
//
// The agent calling this is a hook with no arguments to spare. The window it
// was launched in answers first, from TSPACE_TASK and TSPACE_WINDOW: an agent
// may cd anywhere, so its directory says where it is working, not which
// window it is. Without them the current directory answers:
// worktrees/TASK/REPO/... is that repository, and anywhere else under
// worktrees/TASK is the root window.
func (a *App) signalTarget(taskName, repo string, root bool) (*task.Task, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	located, here, inTask := a.locate(dir)
	if !root && repo == "" && inTask {
		if t, window, ok := a.launchedWindow(taskName, located); ok {
			return t, window, nil
		}
	}
	if taskName == "" {
		if !inTask {
			return nil, "", notInTask{dir}
		}
		taskName = located
	} else if taskName != located {
		here = ""
	}
	t, err := a.loadTask(taskName)
	if err != nil {
		return nil, "", err
	}
	switch {
	case root && !t.HasRootWindow():
		return nil, "", fmt.Errorf("task %s has no root window; its agent runs in %s: use --repo %s", t.Name, t.AgentRepo, t.AgentRepo)
	case root:
		return t, task.RootWindowName, nil
	case repo != "":
		if _, ok := t.Repo(repo); !ok {
			return nil, "", fmt.Errorf("task %q has no repo named %q (has: %s)", t.Name, repo, joinNames(t.RepoNames()))
		}
		return t, repo, nil
	}
	if _, ok := t.Repo(here); ok {
		return t, here, nil
	}
	// Without a root window, the task directory stands for the agent's.
	if !t.HasRootWindow() {
		return t, t.AgentRepo, nil
	}
	return t, task.RootWindowName, nil
}

// launchedWindow is the window the environment says this process runs in, if
// it names one of taskName's (any task's, for an empty taskName) that still exists.
//
// The environment is trusted only for a process in that task's directory.
// It is inherited by everything a window starts, so an agent the user starts
// in that window after cd'ing elsewhere would otherwise report for it.
func (a *App) launchedWindow(taskName, located string) (*task.Task, string, bool) {
	envName, window := os.Getenv(envTask), os.Getenv(envWindow)
	if envName == "" || window == "" || envName != located || (taskName != "" && taskName != envName) {
		return nil, "", false
	}
	if task.ValidateName("task", envName) != nil {
		return nil, "", false
	}
	t, err := a.Store.Load(envName)
	if err != nil {
		return nil, "", false
	}
	if window == task.RootWindowName {
		return t, window, true
	}
	if _, ok := t.Repo(window); ok {
		return t, window, true
	}
	return nil, "", false
}

// locate reads a task name, and the directory below it, out of a path inside
// the worktrees directory.
func (a *App) locate(dir string) (taskName, below string, ok bool) {
	base := a.Store.WorktreesDir()
	// Both sides resolved, so a symlinked TSPACE_ROOT or temp directory still matches.
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 1 {
		below = parts[1]
	}
	return parts[0], below, true
}
