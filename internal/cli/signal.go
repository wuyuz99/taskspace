package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

// signalMessageMax keeps a message to what a status line can show.
const signalMessageMax = 200

func (a *App) newSignalCmd() *cobra.Command {
	var taskName, repo string
	var root, clear bool
	cmd := &cobra.Command{
		Use:   "signal STATE [MESSAGE]",
		Short: "Report what an agent is doing, for status and dash",
		Long: "signal records what the agent in a window says about itself, so that\n" +
			"`aw status` and `aw dash` can show which agents need a person. aw can see\n" +
			"whether a process is alive; only the agent knows whether it is busy,\n" +
			"waiting or finished. STATE is one of:\n\n" +
			"  working  busy, needs nothing\n" +
			"  blocked  waiting on a person: a question, a permission, a decision\n" +
			"  done     finished what it was asked\n" +
			"  failed   gave up\n\n" +
			"Run from inside a task, it knows where it is: in a worktree it reports for\n" +
			"that repository, and at the task root for the root window.\n\n" +
			"  aw signal blocked 'migration needs a decision: drop or keep the column?'\n" +
			"  aw signal done\n" +
			"  aw signal working --task PAY-142 --repo backend\n\n" +
			"It is meant to be called by the agent's own hooks rather than typed. For\n" +
			"Claude Code, for example: UserPromptSubmit runs `aw signal working`,\n" +
			"Notification runs `aw signal blocked`, and Stop runs `aw signal done`.\n\n" +
			"A signal is one JSON file, .context/signals/REPO.json beside the\n" +
			"worktrees (_root.json for the root window), holding\n" +
			`{"state": "blocked", "message": "..."}` + "; an agent without aw on its\n" +
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
				a.printf("%s: signal cleared\n", label)
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
			a.printf("%s: %s\n", label, state)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskName, "task", "", "task to report for (default: the one the current directory is in)")
	cmd.Flags().StringVar(&repo, "repo", "", "repository window to report for (default: the worktree the current directory is in)")
	cmd.Flags().BoolVar(&root, "root", false, "report for the task-root window")
	cmd.Flags().BoolVar(&clear, "clear", false, "forget the window's signal")
	return cmd
}

// signalTarget resolves which window a signal is about.
//
// The agent calling this is a hook with no arguments to spare, so the current
// directory answers by default: worktrees/TASK/REPO/... is that repository,
// and anywhere else under worktrees/TASK is the root window.
func (a *App) signalTarget(taskName, repo string, root bool) (*task.Task, string, error) {
	here := ""
	if taskName == "" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, "", err
		}
		var ok bool
		if taskName, here, ok = a.locate(dir); !ok {
			return nil, "", fmt.Errorf("%s is not inside a task; run this in a task's worktree, or name one with --task", dir)
		}
	}
	t, err := a.loadTask(taskName)
	if err != nil {
		return nil, "", err
	}
	switch {
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
	return t, task.RootWindowName, nil
}

// locate reads a task name, and the directory below it, out of a path inside
// the worktrees directory.
func (a *App) locate(dir string) (taskName, below string, ok bool) {
	base := a.Store.WorktreesDir()
	// Both sides resolved, so a symlinked AW_ROOT or temp directory still matches.
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
