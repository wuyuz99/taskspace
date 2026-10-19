package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
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

// resumeOptions are the --resume and --no-resume flags, for the command that
// brings an agent back with its conversation. tspace knows no agent's way of
// doing that, so it is given like the agent itself, one flag per word:
//
//	--agent claude --resume claude --resume=--continue
type resumeOptions struct {
	argv []string
	none bool
}

func (o *resumeOptions) flags(cmd *cobra.Command, withNone bool) {
	cmd.Flags().StringArrayVar(&o.argv, "resume", nil,
		"command that resumes the agent with its conversation, for restart --resume; one flag per word")
	if withNone {
		cmd.Flags().BoolVar(&o.none, "no-resume", false, "forget the resume command")
	}
}

func (o *resumeOptions) validate(cmd *cobra.Command) error {
	set := cmd.Flags().Changed("resume")
	if set && o.none {
		return fmt.Errorf("choose either --resume or --no-resume")
	}
	if set && (len(o.argv) == 0 || strings.TrimSpace(o.argv[0]) == "") {
		return fmt.Errorf("resume command is empty; use --no-resume to forget it")
	}
	return nil
}

func (o *resumeOptions) changed(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("resume") || o.none
}

// apply returns the resume command to record. A changed agent drops the old
// one, which resumed a different program, unless a new one is given; cleared
// reports that, so the caller can say so.
func (o *resumeOptions) apply(cmd *cobra.Command, current []string, agentChanged bool) (resume []string, cleared bool) {
	switch {
	case cmd.Flags().Changed("resume"):
		return o.argv, false
	case o.none:
		return nil, false
	case agentChanged:
		return nil, len(current) > 0
	}
	return current, false
}

// defaultsToRoot reports whether an omitted repository means the task root.
// With one repository the worktree is the obvious answer; with none or several,
// the root is the only directory that stands for the whole task.
func defaultsToRoot(t *task.Task, repoArg string) bool {
	return repoArg == "" && len(t.Repos) != 1
}

// rootWindow and expandArgv are the lifecycle service's; see package
// workspace.
func (a *App) rootWindow(t *task.Task) runtime.Window { return workspace.RootWindow(a.Store, t) }

func (a *App) expandArgv(t *task.Task, argv []string) []string {
	return workspace.ExpandArgv(a.Store, t, argv)
}
