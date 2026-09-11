package cli

import (
	"fmt"
	"strings"

	"github.com/aw-tool/aw/internal/runtime"
	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

// agentOptions are the task-root agent flags shared by task create and update.
type agentOptions struct {
	argv  []string
	shell bool
}

func (o *agentOptions) flags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&o.argv, "agent", nil,
		"agent command for the task-root window, which sees every repository; repeat for arguments")
	cmd.Flags().BoolVar(&o.shell, "shell", false, "run the default shell in the task-root window")
}

func (o *agentOptions) validate(cmd *cobra.Command) error {
	setAgent := cmd.Flags().Changed("agent")
	if setAgent && o.shell {
		return fmt.Errorf("choose either --agent or --shell")
	}
	if setAgent && (len(o.argv) == 0 || strings.TrimSpace(o.argv[0]) == "") {
		return fmt.Errorf("agent command is empty; use --shell to restore the default shell")
	}
	return nil
}

// explicit reports whether the command line chose the root agent itself.
func (o *agentOptions) explicit(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("agent") || o.shell
}

// apply records the requested root agent and reports whether one was requested.
func (o *agentOptions) apply(cmd *cobra.Command, t *task.Task) bool {
	if !o.explicit(cmd) {
		return false
	}
	t.Agent = nil
	if cmd.Flags().Changed("agent") {
		t.Agent = o.argv
	}
	return true
}

// defaultsToRoot reports whether an omitted repository means the task root.
// With one repository the worktree is the obvious answer; with none or several,
// the root is the only directory that stands for the whole task.
func defaultsToRoot(t *task.Task, repoArg string) bool {
	return repoArg == "" && len(t.Repos) != 1
}

// rootWindow is the task's window at its workspace root.
func (a *App) rootWindow(t *task.Task) runtime.Window {
	return runtime.Window{Name: task.RootWindowName, Dir: a.Store.TaskDir(t.Name), Command: t.Agent}
}
