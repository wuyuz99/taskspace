// Package cli implements tspace's commands.
//
// This is the only package that formats anything for humans. git, runtime and
// status all return data; rendering happens here.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// version is set at build time via -ldflags.
var version = "dev"

// App carries everything the commands need.
type App struct {
	Store   *task.Store
	Git     *tsgit.Git
	Runtime runtime.Runtime
	Out     io.Writer
	Err     io.Writer

	noTitle bool
}

// NewApp wires the default implementations together.
func NewApp() (*App, error) {
	store, err := task.Open()
	if err != nil {
		return nil, err
	}
	runner := tsexec.Real()
	return &App{
		Store:   store,
		Git:     tsgit.New(runner),
		Runtime: runtime.NewTmux(runner),
		Out:     os.Stdout,
		Err:     os.Stderr,
	}, nil
}

func (a *App) printf(format string, args ...any) {
	fmt.Fprintf(a.Out, format, args...)
}

func (a *App) warnf(format string, args ...any) {
	fmt.Fprintf(a.Err, format, args...)
}

// loadTask is the common "resolve the task argument" step.
func (a *App) loadTask(name string) (*task.Task, error) { return a.Store.Load(name) }

// listed unwraps a listing error: entries the store had to skip are reported as
// warnings and the remaining rows are still shown, because one unreadable file
// should not make `tspace task list` refuse to tell you about the other nine.
func (a *App) listed(err error) error {
	var skipped *task.SkippedError
	if errors.As(err, &skipped) {
		for _, e := range skipped.Errs {
			a.warnf("tspace: %v\n", e)
		}
		return nil
	}
	return err
}

// Execute builds and runs the command tree.
func Execute(ctx context.Context) int {
	app, err := NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tspace: %v\n", err)
		return 1
	}

	root := &cobra.Command{
		Use:   "tspace",
		Short: "Manage multi-repo, multi-worktree coding-agent tasks",
		Long: "tspace manages multi-repo, multi-worktree coding-agent tasks.\n\n" +
			"Worktrees are per-repository. Features aren't. A task is the first-class\n" +
			"object: it spans repositories, each with its own branch, worktree and agent.\n\n" +
			"Requires git, and tmux for sessions (Linux, macOS or WSL). Without tmux\n" +
			"everything but the session still works: use `tspace start TASK --no-runtime`.\n\n" +
			"A typical task:\n\n" +
			"  tspace config repo backend ~/src/backend     register a repository, once\n" +
			"  tspace task create PAY-142 backend frontend  record the task; nothing on disk yet\n" +
			"  tspace start PAY-142                         create worktrees, start the session\n" +
			"  tspace status PAY-142                        what is running, what is dirty\n" +
			"  tspace attach PAY-142                        enter the session\n" +
			"  tspace stop PAY-142                          end the session, keep the worktrees\n" +
			"  tspace clean PAY-142                         remove the worktrees, if nothing is lost\n" +
			"  tspace task rm PAY-142                       delete the record and its notes\n\n" +
			"`tspace open TASK` is start and attach in one step, and `tspace dash` watches\n" +
			"every task at once.\n\n" +
			"State lives in $TSPACE_ROOT, by default ~/.tspace:\n\n" +
			"  tasks/TASK.json                     the task record\n" +
			"  worktrees/TASK/                     the task root, where the root agent runs\n" +
			"  worktrees/TASK/REPO/                one worktree per repository\n" +
			"  worktrees/TASK/.context/TASK.md     the brief: what the task is\n" +
			"  worktrees/TASK/.context/HANDOFF.md  the handoff: where the work stands\n\n" +
			"For scripts and agents: open, attach, shell, dash and `task brief --edit`\n" +
			"take over the terminal and need a TTY, and window switches to the new\n" +
			"window unless given --detach. Every other command runs to completion,\n" +
			"prints its result on stdout and warnings on stderr, and exits non-zero on\n" +
			"failure. status, task list and task show take --json. An agent working\n" +
			"in a worktree finds its instructions in ../.context/TASK.md, records\n" +
			"progress with `tspace task handoff TASK --file FILE`, and says when it is\n" +
			"blocked or done with `tspace signal`.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&app.noTitle, "no-title", false,
		"do not set the terminal tab title")

	root.AddCommand(
		app.newInitCmd(),
		app.newConfigCmd(),
		app.newTaskCmd(),
		app.newProjectCmd(),
		app.newRepoCmd(),
		app.newStartCmd(),
		app.newOpenCmd(),
		app.newRestartCmd(),
		app.newStopCmd(),
		app.newAttachCmd(),
		app.newShellCmd(),
		app.newWindowCmd(),
		app.newPathCmd(),
		app.newExecCmd(),
		app.newStatusCmd(),
		app.newDashCmd(),
		app.newSignalCmd(),
		app.newCleanCmd(),
	)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "tspace: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the tspace state directories and report where they live",
		Long: "init creates the state directories. It is never required — every command\n" +
			"creates what it needs — but it is a convenient way to see where tspace keeps\n" +
			"its files. They default to ~/.tspace; set TSPACE_ROOT to keep them elsewhere.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.Store.Init(); err != nil {
				return err
			}
			a.printf("state root:  %s\n", a.Store.Root())
			a.printf("tasks:       %s\n", a.Store.TasksDir())
			a.printf("worktrees:   %s\n", a.Store.WorktreesDir())
			a.printf("config:      %s\n", a.Store.ConfigPath())
			if os.Getenv("TSPACE_ROOT") == "" {
				a.printf("\nSet TSPACE_ROOT to keep workspaces somewhere else.\n")
			}
			return nil
		},
	}
}
