package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// agentOptions are the task-root agent flags shared by task create and update.
type agentOptions struct {
	argv  []string
	shell bool
}

func (o *agentOptions) flags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&o.argv, "agent", nil,
		"agent command for the task-root window, which sees every repository; one flag per word")
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
	return runtime.Window{Name: task.RootWindowName, Dir: a.Store.TaskDir(t.Name), Command: a.expandArgv(t, t.RootAgent()), Env: windowEnv(t.Name, task.RootWindowName)}
}

// repoWindow is a repository's window, in its worktree.
//
// An agent running there cannot see the task's notes, which sit beside the
// worktree in .context: clean refuses untracked files, so they cannot be
// copied in. It finds them at ../.context or through TSPACE_CONTEXT_DIR, or
// its command names them with {context_dir}; see expandArgv.
func (a *App) repoWindow(t *task.Task, name string) runtime.Window {
	env := append(windowEnv(t.Name, name), "TSPACE_CONTEXT_DIR="+a.Store.ContextDir(t.Name))
	return runtime.Window{Name: name, Dir: t.Repos[name].Worktree, Command: a.expandArgv(t, t.RepoAgent(name)), Env: env}
}

// expandArgv fills in the task's directories where an agent command names
// them, so any agent can be pointed at them with its own flag, such as
// `claude --add-dir {context_dir}`, without tspace knowing about that agent:
//
//	{context_dir}  the task's .context directory, holding TASK.md and HANDOFF.md
//	{task_dir}     the task's directory, holding every worktree
func (a *App) expandArgv(t *task.Task, argv []string) []string {
	r := strings.NewReplacer("{context_dir}", a.Store.ContextDir(t.Name), "{task_dir}", a.Store.TaskDir(t.Name))
	out := make([]string, len(argv))
	for i, s := range argv {
		out[i] = r.Replace(s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
